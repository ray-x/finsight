// Package mcp implements a client for the Model Context Protocol (MCP).
//
// MCP allows finsight to connect to external MCP servers that provide
// tools, resources, and prompts. This enables integration with any
// MCP-compatible tool server (e.g. filesystem, database, web search,
// custom data sources).
//
// Specification: https://modelcontextprotocol.io/specification
//
// The client communicates via JSON-RPC 2.0 over stdio (subprocess) or
// SSE (HTTP). Tool definitions discovered from MCP servers are converted
// to llm.ToolSpec so the agent loop can invoke them seamlessly.
package mcp

import (
	"bufio"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/logger"
)

// ServerConfig describes an MCP server to connect to.
type ServerConfig struct {
	// Name is a human-readable identifier for this server.
	Name string `yaml:"name"`
	// Command is the executable to spawn (stdio transport).
	Command string `yaml:"command"`
	// Args are the command-line arguments.
	Args []string `yaml:"args,omitempty"`
	// Env are extra environment variables for the subprocess.
	Env []string `yaml:"env,omitempty"`
	// URL is the SSE endpoint (HTTP transport). If set, Command is ignored.
	URL string `yaml:"url,omitempty"`
}

// Client manages connections to one or more MCP servers and exposes
// their tools as llm.ToolSpec for the agent loop.
type Client struct {
	servers []serverConn
	mu      sync.RWMutex
}

// NewClient creates an MCP client. Call Connect() to start servers.
func NewClient() *Client {
	return &Client{}
}

// Connect starts the configured MCP servers and performs capability
// negotiation (initialize → initialized handshake).
func (c *Client) Connect(ctx context.Context, configs []ServerConfig) error {
	for _, cfg := range configs {
		if cfg.Command == "" && cfg.URL == "" {
			continue
		}
		if cfg.URL != "" {
			return fmt.Errorf("mcp: SSE/URL transport is not implemented for server %q", cfg.Name)
		}
		conn, err := c.connectStdio(ctx, cfg)
		if err != nil {
			logger.Log("mcp: failed to connect to %s: %v", cfg.Name, err)
			continue
		}
		c.mu.Lock()
		c.servers = append(c.servers, *conn)
		c.mu.Unlock()
		logger.Log("mcp: connected to %s (%d tools)", cfg.Name, len(conn.tools))
	}
	return nil
}

// Tools returns all discovered tools from connected MCP servers,
// converted to llm.ToolSpec for use in the agent loop.
func (c *Client) Tools() []llm.ToolSpec {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var specs []llm.ToolSpec
	for _, s := range c.servers {
		for _, t := range s.tools {
			specs = append(specs, llm.ToolSpec{
				Name:        s.name + "_" + t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			})
		}
	}
	return specs
}

// CallTool invokes a tool on the appropriate MCP server and returns
// the result as a string.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for i := range c.servers {
		s := &c.servers[i]
		for _, t := range s.tools {
			if s.name+"_"+t.Name == name {
				return s.callTool(ctx, t.Name, args)
			}
		}
	}
	return "", fmt.Errorf("mcp: tool %q not found", name)
}

// Close shuts down all MCP server connections.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.servers {
		c.servers[i].close()
	}
	c.servers = nil
}

// serverConn tracks a single MCP server connection.
type serverConn struct {
	name   string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	tools  []mcpTool
	nextID atomic.Int64
	mu     sync.Mutex // serializes writes to stdin
}

// MCP protocol types (JSON-RPC 2.0)

type jsonrpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonrpcResponse struct {
	JSONRPC string        `json:"jsonrpc"`
	ID      int64         `json:"id"`
	Result  any           `json:"result,omitempty"`
	Error   *jsonrpcError `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type initializeResult struct {
	Capabilities struct {
		Tools *struct{} `json:"tools,omitempty"`
	} `json:"capabilities"`
	ServerInfo struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo"`
}

type toolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type toolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

func (c *Client) connectStdio(ctx context.Context, cfg ServerConfig) (*serverConn, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	cmd.Env = append(cmd.Environ(), cfg.Env...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", cfg.Command, err)
	}

	conn := &serverConn{
		name:   cfg.Name,
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}

	// Initialize handshake
	initResp, err := conn.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "finsight",
			"version": "1.0.0",
		},
	})
	if err != nil {
		conn.close()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	var initResult initializeResult
	if err := json.Unmarshal(initResp, &initResult); err != nil {
		conn.close()
		return nil, fmt.Errorf("parse initialize result: %w", err)
	}
	logger.Log("mcp: server %s v%s", initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	// Send initialized notification
	conn.notify("notifications/initialized", nil)

	// List tools
	if initResult.Capabilities.Tools != nil {
		toolsResp, err := conn.call(ctx, "tools/list", nil)
		if err != nil {
			logger.Log("mcp: tools/list failed for %s: %v", cfg.Name, err)
		} else {
			var toolsList toolsListResult
			if err := json.Unmarshal(toolsResp, &toolsList); err != nil {
				logger.Log("mcp: parse tools/list: %v", err)
			} else {
				conn.tools = toolsList.Tools
			}
		}
	}

	return conn, nil
}

func (s *serverConn) call(ctx context.Context, method string, params any) ([]byte, error) {
	id := s.nextID.Add(1)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	_, err = fmt.Fprintf(s.stdin, "%s\n", data)
	s.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("write request: %w", err)
	}

	// Read response (simple synchronous line-delimited JSON-RPC)
	line, err := s.stdout.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var resp jsonrpcResponse
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	resultBytes, err := json.Marshal(resp.Result)
	if err != nil {
		return nil, fmt.Errorf("re-marshal result: %w", err)
	}
	return resultBytes, nil
}

func (s *serverConn) notify(method string, params any) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, _ := json.Marshal(req)
	s.mu.Lock()
	fmt.Fprintf(s.stdin, "%s\n", data)
	s.mu.Unlock()
}

func (s *serverConn) callTool(ctx context.Context, name string, args map[string]any) (string, error) {
	resp, err := s.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}
	var result toolCallResult
	if err := json.Unmarshal(resp, &result); err != nil {
		return "", fmt.Errorf("parse tool result: %w", err)
	}
	if result.IsError {
		if len(result.Content) > 0 {
			return "", fmt.Errorf("tool error: %s", result.Content[0].Text)
		}
		return "", fmt.Errorf("tool error (no details)")
	}
	var sb []string
	for _, c := range result.Content {
		if c.Type == "text" {
			sb = append(sb, c.Text)
		}
	}
	return fmt.Sprintf("%s", sb), nil
}

func (s *serverConn) close() {
	if s.stdin != nil {
		s.stdin.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
}
