// Package acp implements an ACP (Agent Client Protocol) client that
// communicates with AI coding agents (Gemini CLI, Copilot CLI, Claude Code)
// over stdio using the coder/acp-go-sdk.
//
// Each configured agent is launched as a subprocess and spoken to via the
// ACP JSON-RPC protocol. Agent capabilities are exposed as llm.ToolSpec
// so the local agent loop can delegate tasks transparently.
//
// Specification: https://agentclientprotocol.com/
package acp

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	acpsdk "github.com/coder/acp-go-sdk"
	"github.com/ray-x/finsight/internal/logger"
)

// AgentConfig describes an ACP agent subprocess to launch.
type AgentConfig struct {
	// Name is a human-readable identifier for this agent.
	Name string `yaml:"name"`
	// Command is the executable path (e.g. "gemini", "gh").
	Command string `yaml:"command"`
	// Args are command-line arguments (e.g. ["--acp"] or ["copilot", "--acp"]).
	Args []string `yaml:"args,omitempty"`
	// Env are extra environment variables in KEY=VALUE form.
	Env []string `yaml:"env,omitempty"`
}

// Client manages ACP agent subprocess connections.
type Client struct {
	mu     sync.Mutex
	agents []*agentConn
}

// agentConn holds a running agent subprocess and its ACP connection.
type agentConn struct {
	name      string
	conn      *acpsdk.ClientSideConnection
	handler   *clientHandler
	sessionID acpsdk.SessionId
	cmd       *exec.Cmd
}

// NewClient creates an ACP client.
func NewClient() *Client {
	return &Client{}
}

// clientHandler implements the acp.Client interface — it receives
// callbacks from the agent subprocess. We collect streamed text
// in a thread-safe buffer.
type clientHandler struct {
	mu   sync.Mutex
	text strings.Builder
}

func (h *clientHandler) SessionUpdate(_ context.Context, params acpsdk.SessionNotification) error {
	u := params.Update
	if u.AgentMessageChunk != nil {
		if txt := u.AgentMessageChunk.Content.Text; txt != nil {
			h.mu.Lock()
			h.text.WriteString(txt.Text)
			h.mu.Unlock()
		}
	}
	return nil
}

func (h *clientHandler) RequestPermission(_ context.Context, params acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, error) {
	// Auto-select the first available permission option.
	if len(params.Options) > 0 {
		return acpsdk.RequestPermissionResponse{
			Outcome: acpsdk.RequestPermissionOutcome{
				Selected: &acpsdk.RequestPermissionOutcomeSelected{
					OptionId: params.Options[0].OptionId,
					Outcome:  "selected",
				},
			},
		}, nil
	}
	return acpsdk.RequestPermissionResponse{
		Outcome: acpsdk.RequestPermissionOutcome{
			Cancelled: &acpsdk.RequestPermissionOutcomeCancelled{
				Outcome: "cancelled",
			},
		},
	}, nil
}

func (h *clientHandler) CreateTerminal(_ context.Context, _ acpsdk.CreateTerminalRequest) (acpsdk.CreateTerminalResponse, error) {
	return acpsdk.CreateTerminalResponse{}, fmt.Errorf("terminals not supported")
}

func (h *clientHandler) KillTerminal(_ context.Context, _ acpsdk.KillTerminalRequest) (acpsdk.KillTerminalResponse, error) {
	return acpsdk.KillTerminalResponse{}, fmt.Errorf("terminals not supported")
}

func (h *clientHandler) TerminalOutput(_ context.Context, _ acpsdk.TerminalOutputRequest) (acpsdk.TerminalOutputResponse, error) {
	return acpsdk.TerminalOutputResponse{}, fmt.Errorf("terminals not supported")
}

func (h *clientHandler) ReleaseTerminal(_ context.Context, _ acpsdk.ReleaseTerminalRequest) (acpsdk.ReleaseTerminalResponse, error) {
	return acpsdk.ReleaseTerminalResponse{}, fmt.Errorf("terminals not supported")
}

func (h *clientHandler) WaitForTerminalExit(_ context.Context, _ acpsdk.WaitForTerminalExitRequest) (acpsdk.WaitForTerminalExitResponse, error) {
	return acpsdk.WaitForTerminalExitResponse{}, fmt.Errorf("terminals not supported")
}

func (h *clientHandler) ReadTextFile(_ context.Context, _ acpsdk.ReadTextFileRequest) (acpsdk.ReadTextFileResponse, error) {
	return acpsdk.ReadTextFileResponse{}, fmt.Errorf("file system not supported")
}

func (h *clientHandler) WriteTextFile(_ context.Context, _ acpsdk.WriteTextFileRequest) (acpsdk.WriteTextFileResponse, error) {
	return acpsdk.WriteTextFileResponse{}, fmt.Errorf("file system not supported")
}

func (h *clientHandler) getText() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.text.String()
}

func (h *clientHandler) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.text.Reset()
}

// Connect launches each configured agent subprocess, performs the ACP
// handshake, and creates a session. Agents that fail to start are
// logged and skipped.
func (c *Client) Connect(ctx context.Context, configs []AgentConfig) error {
	for _, cfg := range configs {
		if cfg.Command == "" {
			continue
		}
		ac, err := c.startAgent(ctx, cfg)
		if err != nil {
			logger.Log("acp: failed to start %s: %v", cfg.Name, err)
			continue
		}
		c.mu.Lock()
		c.agents = append(c.agents, ac)
		c.mu.Unlock()
		logger.Log("acp: connected to %s (session %s)", ac.name, ac.sessionID)
	}
	return nil
}

func (c *Client) startAgent(ctx context.Context, cfg AgentConfig) (*agentConn, error) {
	cmd := exec.CommandContext(ctx, cfg.Command, cfg.Args...)
	if len(cfg.Env) > 0 {
		cmd.Env = append(cmd.Environ(), cfg.Env...)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start process: %w", err)
	}

	handler := &clientHandler{}
	conn := acpsdk.NewClientSideConnection(handler, stdin, stdout)

	// Initialize the ACP connection.
	initResp, err := conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersion(acpsdk.ProtocolVersionNumber),
		ClientInfo: &acpsdk.Implementation{
			Name:    "finsight",
			Version: "1.0.0",
		},
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("initialize: %w", err)
	}

	agentName := cfg.Name
	if agentName == "" && initResp.AgentInfo != nil {
		agentName = initResp.AgentInfo.Name
	}

	// Create a session.
	sessResp, err := conn.NewSession(ctx, acpsdk.NewSessionRequest{})
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, fmt.Errorf("new session: %w", err)
	}

	return &agentConn{
		name:      agentName,
		conn:      conn,
		handler:   handler,
		sessionID: sessResp.SessionId,
		cmd:       cmd,
	}, nil
}

// Prompt sends a text prompt to the named agent and returns the response text.
func (c *Client) Prompt(ctx context.Context, agentName string, message string) (string, error) {
	c.mu.Lock()
	var ac *agentConn
	for _, a := range c.agents {
		if a.name == agentName {
			ac = a
			break
		}
	}
	c.mu.Unlock()

	if ac == nil {
		return "", fmt.Errorf("acp: agent %q not connected", agentName)
	}

	// Reset the handler buffer so we only capture this turn's output.
	ac.handler.reset()

	_, err := ac.conn.Prompt(ctx, acpsdk.PromptRequest{
		SessionId: ac.sessionID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(message)},
	})
	if err != nil {
		return "", fmt.Errorf("prompt: %w", err)
	}

	// The streamed text was collected by the clientHandler via SessionUpdate
	// callbacks during the Prompt RPC.
	return ac.handler.getText(), nil
}

// AgentNames returns the names of all connected agents.
func (c *Client) AgentNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, len(c.agents))
	for i, a := range c.agents {
		names[i] = a.name
	}
	return names
}

// Close terminates all agent subprocesses.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.agents {
		_ = a.cmd.Process.Kill()
		_ = a.cmd.Wait()
	}
	c.agents = nil
}
