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

// chatAnthropic calls the Anthropic Messages API (api.anthropic.com).
//
// Endpoint:
//
//	POST https://api.anthropic.com/v1/messages
//
// Auth is via the x-api-key header (not Bearer).
func (c *Client) chatAnthropic(ctx context.Context, system, user string) (string, error) {
	if c.cfg.APIKey == "" {
		return "", fmt.Errorf("anthropic: API key is required (set ANTHROPIC_API_KEY)")
	}

	base := c.cfg.Endpoint
	if base == "" {
		base = "https://api.anthropic.com"
	}
	url := strings.TrimRight(base, "/") + "/v1/messages"

	body := anthropicRequest{
		Model:     c.cfg.Model,
		MaxTokens: 8192,
		Messages: []anthropicMessage{{
			Role:    "user",
			Content: user,
		}},
	}
	if system != "" {
		body.System = system
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

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
		return "", fmt.Errorf("anthropic API error (HTTP %d): %s", resp.StatusCode, string(data))
	}

	var result anthropicResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("anthropic API error: %s (type: %s)", result.Error.Message, result.Error.Type)
	}

	var sb strings.Builder
	for _, block := range result.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	content := cleanThinkTags(sb.String())
	logger.Log("Anthropic response: stop_reason=%s content_len=%d", result.StopReason, len(content))

	if content == "" && result.StopReason == "max_tokens" {
		return "", fmt.Errorf("response truncated (max_tokens reached)")
	}
	return content, nil
}

// chatToolsAnthropic sends a tool-capable request to the Anthropic Messages API.
func (c *Client) chatToolsAnthropic(ctx context.Context, messages []Message, tools []ToolSpec) (Message, error) {
	if c.cfg.APIKey == "" {
		return Message{}, fmt.Errorf("anthropic: API key is required")
	}

	base := c.cfg.Endpoint
	if base == "" {
		base = "https://api.anthropic.com"
	}
	url := strings.TrimRight(base, "/") + "/v1/messages"

	// Convert messages from OpenAI format to Anthropic format
	var anthropicMsgs []anthropicMessage
	var systemText string
	for _, m := range messages {
		switch m.Role {
		case "system":
			systemText = m.Content
		case "user":
			anthropicMsgs = append(anthropicMsgs, anthropicMessage{
				Role:    "user",
				Content: m.Content,
			})
		case "assistant":
			if len(m.ToolCalls) > 0 {
				// Assistant with tool use
				var blocks []anthropicContentBlock
				if m.Content != "" {
					blocks = append(blocks, anthropicContentBlock{
						Type: "text",
						Text: m.Content,
					})
				}
				for _, tc := range m.ToolCalls {
					var input map[string]any
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
					blocks = append(blocks, anthropicContentBlock{
						Type:  "tool_use",
						ID:    tc.ID,
						Name:  tc.Function.Name,
						Input: input,
					})
				}
				anthropicMsgs = append(anthropicMsgs, anthropicMessage{
					Role:          "assistant",
					ContentBlocks: blocks,
				})
			} else {
				anthropicMsgs = append(anthropicMsgs, anthropicMessage{
					Role:    "assistant",
					Content: m.Content,
				})
			}
		case "tool":
			anthropicMsgs = append(anthropicMsgs, anthropicMessage{
				Role: "user",
				ContentBlocks: []anthropicContentBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				}},
			})
		}
	}

	// Convert tools
	var anthropicTools []anthropicToolDecl
	for _, t := range tools {
		anthropicTools = append(anthropicTools, anthropicToolDecl{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: t.Parameters,
		})
	}

	body := anthropicToolRequest{
		Model:     c.cfg.Model,
		MaxTokens: 8192,
		Messages:  anthropicMsgs,
		Tools:     anthropicTools,
	}
	if systemText != "" {
		body.System = systemText
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Message{}, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Message{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.cfg.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")

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
		return Message{}, fmt.Errorf("anthropic API error (HTTP %d): %s", resp.StatusCode, string(data))
	}

	var result anthropicResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return Message{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return Message{}, fmt.Errorf("anthropic API error: %s", result.Error.Message)
	}

	// Convert Anthropic response back to OpenAI Message format
	msg := Message{Role: "assistant"}
	for _, block := range result.Content {
		switch block.Type {
		case "text":
			msg.Content += block.Text
		case "tool_use":
			argsJSON, _ := json.Marshal(block.Input)
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   block.ID,
				Type: "function",
				Function: ToolCallFunction{
					Name:      block.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}
	msg.Content = cleanThinkTags(msg.Content)
	logger.Log("Anthropic tool response: tool_calls=%d content_len=%d", len(msg.ToolCalls), len(msg.Content))
	return msg, nil
}

// Anthropic API types

type anthropicRequest struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicToolRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
	Tools     []anthropicToolDecl `json:"tools,omitempty"`
}

type anthropicMessage struct {
	Role          string                  `json:"role"`
	Content       string                  `json:"content,omitempty"`
	ContentBlocks []anthropicContentBlock `json:"content_blocks,omitempty"`
}

// MarshalJSON handles the dual content field: if ContentBlocks is set,
// serialize "content" as the block array; otherwise use the string.
func (m anthropicMessage) MarshalJSON() ([]byte, error) {
	if len(m.ContentBlocks) > 0 {
		type alias struct {
			Role    string                  `json:"role"`
			Content []anthropicContentBlock `json:"content"`
		}
		return json.Marshal(alias{Role: m.Role, Content: m.ContentBlocks})
	}
	type alias struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	return json.Marshal(alias{Role: m.Role, Content: m.Content})
}

type anthropicContentBlock struct {
	Type      string         `json:"type"`
	Text      string         `json:"text,omitempty"`
	ID        string         `json:"id,omitempty"`
	Name      string         `json:"name,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
	ToolUseID string         `json:"tool_use_id,omitempty"`
	Content   string         `json:"content,omitempty"`
}

type anthropicToolDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicResponse struct {
	Content    []anthropicContentBlock `json:"content"`
	StopReason string                  `json:"stop_reason"`
	Error      *anthropicError         `json:"error,omitempty"`
}

type anthropicError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
