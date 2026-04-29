package llm

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ray-x/finsight/internal/logger"
)

// ChatWithTools sends a tool-capable chat completion request. The
// caller supplies the full message history (including prior
// tool-call/tool-result pairs) plus the tool catalogue; the model
// either returns final assistant text or a set of tool calls to
// execute. Supported providers: OpenAI-compatible and GitHub Copilot.
// Vertex is not wired yet — callers should detect that via
// ErrToolsUnsupported and fall back to plain Chat.
func (c *Client) ChatWithTools(ctx context.Context, messages []Message, tools []ToolSpec) (Message, error) {
	logger.Log("LLM tool request: provider=%s model=%s messages=%d tools=%d",
		c.cfg.Provider, c.cfg.Model, len(messages), len(tools))
	switch c.cfg.Provider {
	case ProviderOpenAI, "":
		return c.chatToolsOpenAI(ctx, messages, tools, "")
	case ProviderCopilot:
		return c.chatToolsCopilot(ctx, messages, tools)
	case ProviderGemini:
		return c.chatToolsGemini(ctx, messages, tools)
	case ProviderAnthropic:
		return c.chatToolsAnthropic(ctx, messages, tools)
	default:
		return Message{}, ErrToolsUnsupported
	}
}

// ErrToolsUnsupported signals that the configured backend does not yet
// support the tool-calling flow.
var ErrToolsUnsupported = fmt.Errorf("tool calling not supported for this provider")

func (c *Client) chatToolsOpenAI(ctx context.Context, messages []Message, tools []ToolSpec, authOverride string) (Message, error) {
	body := chatRequest{
		Model:       c.cfg.Model,
		Messages:    messages,
		Temperature: 0.3,
		MaxTokens:   8192,
		Tools:       wrapTools(tools),
	}
	if len(tools) > 0 {
		body.ToolChoice = "auto"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Message{}, fmt.Errorf("marshal request: %w", err)
	}
	url := c.cfg.Endpoint + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Message{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	auth := authOverride
	if auth == "" {
		auth = c.cfg.APIKey
	}
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	return c.doToolChatCompletions(req)
}

func (c *Client) chatToolsCopilot(ctx context.Context, messages []Message, tools []ToolSpec) (Message, error) {
	token, err := c.copilotChatToken(ctx)
	if err != nil {
		return Message{}, fmt.Errorf("copilot auth: %w", err)
	}
	body := struct {
		Model               string     `json:"model"`
		Messages            []Message  `json:"messages"`
		Temperature         float64    `json:"temperature"`
		MaxCompletionTokens int        `json:"max_completion_tokens"`
		N                   int        `json:"n"`
		Stream              bool       `json:"stream"`
		Tools               []toolDecl `json:"tools,omitempty"`
		ToolChoice          string     `json:"tool_choice,omitempty"`
	}{
		Model:               c.cfg.Model,
		Messages:            messages,
		Temperature:         0.3,
		MaxCompletionTokens: 8192,
		N:                   1,
		Stream:              false,
		Tools:               wrapTools(tools),
	}
	if len(tools) > 0 {
		body.ToolChoice = "auto"
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return Message{}, fmt.Errorf("marshal request: %w", err)
	}
	endpoint := c.cfg.Endpoint
	if endpoint == "" {
		endpoint = c.copilotEndpoint
	}
	if endpoint == "" {
		endpoint = "https://api.githubcopilot.com"
	}
	url := strings.TrimRight(endpoint, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Message{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Editor-Version", "vscode/1.95.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.20.0")
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.20.0")
	return c.doToolChatCompletions(req)
}

func (c *Client) doToolChatCompletions(req *http.Request) (Message, error) {
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return Message{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Message{}, fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, string(data))
	}
	var result chatResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return Message{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return Message{}, fmt.Errorf("API error: %s", result.Error.Message)
	}
	if len(result.Choices) == 0 {
		return Message{}, fmt.Errorf("no choices in response")
	}
	msg := result.Choices[0].Message
	msg.Content = cleanThinkTags(msg.Content)
	logger.Log("LLM tool response: tool_calls=%d content_len=%d", len(msg.ToolCalls), len(msg.Content))
	return msg, nil
}

func wrapTools(tools []ToolSpec) []toolDecl {
	if len(tools) == 0 {
		return nil
	}
	out := make([]toolDecl, len(tools))
	for i, t := range tools {
		out[i] = toolDecl{Type: "function", Function: t}
	}
	return out
}
