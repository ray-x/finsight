package llm

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ray-x/finsight/internal/logger"
)

var reThinkTags = regexp.MustCompile(`(?s)<think>.*?</think>\s*`)

// Provider identifies the LLM backend protocol.
type Provider string

const (
	ProviderOpenAI    Provider = "openai"    // OpenAI-compatible /chat/completions
	ProviderCopilot   Provider = "copilot"   // GitHub Copilot chat
	ProviderVertex    Provider = "vertex"    // Google Cloud Vertex AI Model Garden
	ProviderGemini    Provider = "gemini"    // Google AI Studio (generativelanguage.googleapis.com)
	ProviderAnthropic Provider = "anthropic" // Anthropic Claude (api.anthropic.com)
)

// Config bundles the parameters needed to instantiate a Client.
type Config struct {
	Provider      Provider
	Endpoint      string // OpenAI base URL (ignored for copilot/vertex)
	Model         string
	APIKey        string // Bearer / OAuth / access token (semantics vary per provider)
	Project       string // vertex: GCP project id
	Location      string // vertex: region, defaults to "global"
	ContextTokens int    // model window size; 0 → 32768
}

// Client talks to the configured LLM backend.
type Client struct {
	cfg        Config
	httpClient *http.Client

	// Copilot bearer token cache (the api_key is a long-lived OAuth
	// token; we exchange it for a short-lived chat token).
	copilotMu       sync.Mutex
	copilotToken    string
	copilotExpires  time.Time
	copilotEndpoint string // discovered chat host from token-exchange response
}

// NewClient returns a client configured per cfg.
func NewClient(cfg Config) *Client {
	if cfg.Provider == "" {
		cfg.Provider = ProviderOpenAI
	}
	if cfg.ContextTokens <= 0 {
		cfg.ContextTokens = 32768
	}
	if cfg.Provider == ProviderVertex && cfg.Location == "" {
		cfg.Location = "global"
	}
	cfg.Endpoint = strings.TrimRight(cfg.Endpoint, "/")
	return &Client{
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

// ContextTokens returns the configured context window size in tokens.
func (c *Client) ContextTokens() int { return c.cfg.ContextTokens }

// Provider returns the configured backend identifier (openai/copilot/vertex).
func (c *Client) Provider() Provider { return c.cfg.Provider }

// Model returns the configured model name.
func (c *Client) Model() string { return c.cfg.Model }

// Configured returns true if the client has the minimum settings for its provider.
func (c *Client) Configured() bool {
	if c.cfg.Model == "" {
		return false
	}
	switch c.cfg.Provider {
	case ProviderVertex:
		return c.cfg.Project != ""
	case ProviderCopilot:
		return true // OAuth token is resolved lazily
	case ProviderGemini:
		return c.cfg.APIKey != ""
	case ProviderAnthropic:
		return c.cfg.APIKey != ""
	default:
		return c.cfg.Endpoint != ""
	}
}

// Message represents a chat message (OpenAI/Copilot schema). When used
// in a tool-calling loop, `ToolCalls` carries the model's function
// requests on an assistant turn, and `ToolCallID`/`Name` identify the
// paired `role:tool` result messages sent back on the next turn.
type Message struct {
	Role             string     `json:"role"`
	Content          string     `json:"content"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	Name             string     `json:"name,omitempty"`
}

// ToolCall is the OpenAI/Copilot function-call envelope returned on an
// assistant turn when the model invokes a tool.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // always "function" today
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction carries the chosen tool name plus its raw JSON
// argument string (per the OpenAI spec; arguments are not pre-parsed).
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolSpec declares a tool to the model. `Parameters` must be a
// JSON-Schema object describing the function arguments.
type ToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatRequest struct {
	Model       string     `json:"model"`
	Messages    []Message  `json:"messages"`
	Temperature float64    `json:"temperature,omitempty"`
	MaxTokens   int        `json:"max_tokens,omitempty"`
	Tools       []toolDecl `json:"tools,omitempty"`
	ToolChoice  string     `json:"tool_choice,omitempty"`
}

// toolDecl is the `{"type":"function","function":{...}}` wrapper
// expected by the OpenAI chat-completions schema.
type toolDecl struct {
	Type     string   `json:"type"`
	Function ToolSpec `json:"function"`
}

type chatChoice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason,omitempty"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
	Error   *apiError    `json:"error,omitempty"`
}

type apiError struct {
	Message string `json:"message"`
}

// Chat sends a chat completion request and returns the assistant's reply.
// Dispatches to the configured provider.
func (c *Client) Chat(ctx context.Context, system, user string) (string, error) {
	logger.Log("LLM request: provider=%s model=%s system_len=%d user_len=%d",
		c.cfg.Provider, c.cfg.Model, len(system), len(user))
	if len(user) > 1000 {
		logger.Log("LLM prompt (user, first 1000 chars):\n%.1000s...", user)
	} else {
		logger.Log("LLM prompt (user):\n%s", user)
	}

	switch c.cfg.Provider {
	case ProviderVertex:
		return c.chatVertex(ctx, system, user)
	case ProviderCopilot:
		return c.chatCopilot(ctx, system, user)
	case ProviderGemini:
		return c.chatGemini(ctx, system, user)
	case ProviderAnthropic:
		return c.chatAnthropic(ctx, system, user)
	default:
		return c.chatOpenAI(ctx, system, user)
	}
}

// chatOpenAI calls an OpenAI-compatible /chat/completions endpoint.
func (c *Client) chatOpenAI(ctx context.Context, system, user string) (string, error) {
	body := chatRequest{
		Model:       c.cfg.Model,
		Messages:    []Message{{Role: "system", Content: system}, {Role: "user", Content: user}},
		Temperature: 0.3,
		MaxTokens:   8192,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := c.cfg.Endpoint + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	return c.doChatCompletions(req)
}

// doChatCompletions executes a request that returns OpenAI chat-completion JSON
// and applies the standard post-processing (reasoning fallback, <think> stripping).
func (c *Client) doChatCompletions(req *http.Request) (string, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(data))
	}

	var result chatResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("API error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in response")
	}

	choice := result.Choices[0]
	content := choice.Message.Content
	logger.Log("LLM response: finish_reason=%s content_len=%d reasoning_len=%d",
		choice.FinishReason, len(content), len(choice.Message.ReasoningContent))

	if content == "" && choice.Message.ReasoningContent != "" {
		content = choice.Message.ReasoningContent
	}
	content = cleanThinkTags(content)

	if content == "" && choice.FinishReason == "length" {
		return "", fmt.Errorf("response truncated (max_tokens reached)")
	}
	logger.Log("LLM cleaned content (%d chars):\n%s", len(content), content)
	return content, nil
}

// cleanThinkTags strips <think>...</think> blocks emitted by reasoning models.
func cleanThinkTags(content string) string {
	content = reThinkTags.ReplaceAllString(content, "")
	if idx := strings.Index(content, "<think>"); idx >= 0 {
		if after := strings.Index(content[idx:], "</think>"); after >= 0 {
			content = content[:idx] + content[idx+after+8:]
		} else {
			before := strings.TrimSpace(content[:idx])
			after := strings.TrimSpace(content[idx+7:])
			if before != "" {
				content = before
			} else {
				content = after
			}
		}
	}
	return strings.TrimSpace(content)
}
