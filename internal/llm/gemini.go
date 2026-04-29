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

// chatGemini calls the Google AI Studio (generativelanguage.googleapis.com)
// REST API directly using an API key. This is simpler than Vertex as it
// does not require a GCP project or gcloud access token.
//
// Endpoint:
//
//	https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent?key={api_key}
func (c *Client) chatGemini(ctx context.Context, system, user string) (string, error) {
	if c.cfg.APIKey == "" {
		return "", fmt.Errorf("gemini: API key is required (set GEMINI_API_KEY)")
	}

	base := c.cfg.Endpoint
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		strings.TrimRight(base, "/"), c.cfg.Model, c.cfg.APIKey)

	body := geminiRequest{
		Contents: []geminiContent{{
			Role:  "user",
			Parts: []geminiPart{{Text: user}},
		}},
		GenerationConfig: geminiGenConfig{
			Temperature:     0.3,
			MaxOutputTokens: 8192,
		},
	}
	if system != "" {
		body.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
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
		return "", fmt.Errorf("gemini API error (HTTP %d): %s", resp.StatusCode, string(data))
	}

	var result geminiResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("gemini API error: %s", result.Error.Message)
	}
	if len(result.Candidates) == 0 {
		if result.PromptFeedback != nil && result.PromptFeedback.BlockReason != "" {
			return "", fmt.Errorf("gemini blocked: %s", result.PromptFeedback.BlockReason)
		}
		return "", fmt.Errorf("gemini: no candidates in response")
	}

	cand := result.Candidates[0]
	var sb strings.Builder
	for _, p := range cand.Content.Parts {
		if p.Text != "" {
			sb.WriteString(p.Text)
		}
	}
	content := cleanThinkTags(sb.String())
	logger.Log("Gemini response: finish_reason=%s content_len=%d", cand.FinishReason, len(content))

	if content == "" && cand.FinishReason == "MAX_TOKENS" {
		return "", fmt.Errorf("response truncated (max_output_tokens reached)")
	}
	return content, nil
}

// chatToolsGemini sends a tool-capable request to the Gemini API.
// The Gemini REST API uses its own tool schema; we convert from the
// OpenAI-style ToolSpec and message history.
func (c *Client) chatToolsGemini(ctx context.Context, messages []Message, tools []ToolSpec) (Message, error) {
	if c.cfg.APIKey == "" {
		return Message{}, fmt.Errorf("gemini: API key is required")
	}

	base := c.cfg.Endpoint
	if base == "" {
		base = "https://generativelanguage.googleapis.com"
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s",
		strings.TrimRight(base, "/"), c.cfg.Model, c.cfg.APIKey)

	// Convert messages to Gemini format
	var contents []geminiContent
	var systemText string
	for _, m := range messages {
		switch m.Role {
		case "system":
			systemText = m.Content
		case "user":
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			})
		case "assistant":
			parts := []geminiPart{}
			if m.Content != "" {
				parts = append(parts, geminiPart{Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				var args map[string]any
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			contents = append(contents, geminiContent{Role: "model", Parts: parts})
		case "tool":
			var result any
			_ = json.Unmarshal([]byte(m.Content), &result)
			if result == nil {
				result = m.Content
			}
			contents = append(contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFunctionResponse{
						Name:     m.Name,
						Response: map[string]any{"result": result},
					},
				}},
			})
		}
	}

	body := geminiRequest{
		Contents: contents,
		GenerationConfig: geminiGenConfig{
			Temperature:     0.3,
			MaxOutputTokens: 8192,
		},
	}
	if systemText != "" {
		body.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: systemText}}}
	}

	// Convert tools
	if len(tools) > 0 {
		var decls []geminiToolDecl
		for _, t := range tools {
			decls = append(decls, geminiToolDecl{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			})
		}
		body.Tools = []geminiToolSet{{FunctionDeclarations: decls}}
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
		return Message{}, fmt.Errorf("gemini API error (HTTP %d): %s", resp.StatusCode, string(data))
	}

	var result geminiResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return Message{}, fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return Message{}, fmt.Errorf("gemini API error: %s", result.Error.Message)
	}
	if len(result.Candidates) == 0 {
		return Message{}, fmt.Errorf("gemini: no candidates in response")
	}

	// Convert Gemini response back to OpenAI Message format
	cand := result.Candidates[0]
	msg := Message{Role: "assistant"}
	for _, p := range cand.Content.Parts {
		if p.Text != "" {
			msg.Content += p.Text
		}
		if p.FunctionCall != nil {
			argsJSON, _ := json.Marshal(p.FunctionCall.Args)
			msg.ToolCalls = append(msg.ToolCalls, ToolCall{
				ID:   fmt.Sprintf("call_%s_%d", p.FunctionCall.Name, len(msg.ToolCalls)),
				Type: "function",
				Function: ToolCallFunction{
					Name:      p.FunctionCall.Name,
					Arguments: string(argsJSON),
				},
			})
		}
	}
	msg.Content = cleanThinkTags(msg.Content)
	logger.Log("Gemini tool response: tool_calls=%d content_len=%d", len(msg.ToolCalls), len(msg.Content))
	return msg, nil
}

// Gemini API types

type geminiRequest struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  geminiGenConfig `json:"generationConfig"`
	Tools             []geminiToolSet `json:"tools,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiGenConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type geminiToolSet struct {
	FunctionDeclarations []geminiToolDecl `json:"functionDeclarations"`
}

type geminiToolDecl struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiResponse struct {
	Candidates     []geminiCandidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback,omitempty"`
	Error *apiError `json:"error,omitempty"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}
