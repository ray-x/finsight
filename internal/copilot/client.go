// Package copilot provides native GitHub Copilot CLI integration using the
// official copilot-sdk Go SDK (github.com/github/copilot-sdk/go).
//
// Unlike the generic ACP client, this package speaks the Copilot-specific
// protocol and exposes richer features such as model selection, system
// message customisation, and permission control.
package copilot

import (
	"context"
	"fmt"
	"sync"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/ray-x/finsight/internal/logger"
)

// Config describes how to connect to the Copilot CLI agent.
type Config struct {
	// CLIPath overrides the copilot executable path.
	// Default: "copilot" (or COPILOT_CLI_PATH env).
	CLIPath string `yaml:"cli_path,omitempty"`
	// Model to use for sessions (e.g. "gpt-5", "claude-sonnet-4.5").
	Model string `yaml:"model,omitempty"`
	// SystemMessage is an optional instruction appended to the system prompt.
	SystemMessage string `yaml:"system_message,omitempty"`
	// GitHubToken overrides the token for authentication.
	// By default the SDK uses the logged-in user's credentials.
	GitHubToken string `yaml:"-"`
	// Env holds extra environment variables for the CLI process.
	Env []string `yaml:"env,omitempty"`
}

// Client wraps the Copilot SDK client and manages a single session.
type Client struct {
	cfg     Config
	client  *copilot.Client
	session *copilot.Session

	mu      sync.Mutex
	started bool
}

// NewClient creates a Copilot SDK client from the given config.
func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg}
}

// Start launches the Copilot CLI subprocess and creates an initial session.
func (c *Client) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return nil
	}

	opts := &copilot.ClientOptions{
		LogLevel: "error",
	}
	if c.cfg.CLIPath != "" {
		opts.CLIPath = c.cfg.CLIPath
	}
	if c.cfg.GitHubToken != "" {
		opts.GitHubToken = c.cfg.GitHubToken
	}
	if len(c.cfg.Env) > 0 {
		opts.Env = c.cfg.Env
	}

	c.client = copilot.NewClient(opts)

	if err := c.client.Start(ctx); err != nil {
		return fmt.Errorf("copilot: start CLI: %w", err)
	}

	sessCfg := &copilot.SessionConfig{
		OnPermissionRequest: copilot.PermissionHandler.ApproveAll,
	}
	if c.cfg.Model != "" {
		sessCfg.Model = c.cfg.Model
	}
	if c.cfg.SystemMessage != "" {
		sessCfg.SystemMessage = &copilot.SystemMessageConfig{
			Content: c.cfg.SystemMessage,
		}
	}

	session, err := c.client.CreateSession(ctx, sessCfg)
	if err != nil {
		_ = c.client.Stop()
		return fmt.Errorf("copilot: create session: %w", err)
	}

	c.session = session
	c.started = true
	logger.Log("copilot: started (model=%s)", c.cfg.Model)
	return nil
}

// Prompt sends a message to the Copilot session and blocks until the
// assistant replies (session goes idle). Returns the assistant's response text.
func (c *Client) Prompt(ctx context.Context, message string) (string, error) {
	c.mu.Lock()
	sess := c.session
	c.mu.Unlock()

	if sess == nil {
		return "", fmt.Errorf("copilot: not started")
	}

	event, err := sess.SendAndWait(ctx, copilot.MessageOptions{
		Prompt: message,
	})
	if err != nil {
		return "", fmt.Errorf("copilot: send: %w", err)
	}

	if event == nil {
		return "", nil
	}
	if msg, ok := event.Data.(*copilot.AssistantMessageData); ok {
		return msg.Content, nil
	}
	return "", nil
}

// Close disconnects the session and stops the CLI subprocess.
func (c *Client) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.started {
		return
	}
	if c.session != nil {
		if err := c.session.Disconnect(); err != nil {
			logger.Log("copilot: disconnect session: %v", err)
		}
	}
	if c.client != nil {
		if err := c.client.Stop(); err != nil {
			logger.Log("copilot: stop client: %v", err)
		}
	}
	c.started = false
}
