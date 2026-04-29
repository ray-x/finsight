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

// chatVertex calls Google Cloud Vertex AI (Model Garden) using the
// Gemini generateContent schema. We use the non-streaming endpoint to
// keep parsing simple — the response shape is the same as
// streamGenerateContent's final aggregated message.
//
// Endpoint:
//
//	https://{aiplatform-host}/v1/projects/{project}/locations/{location}/publishers/google/models/{model}:generateContent
//
// The api_key field is treated as a Google access token (the kind
// produced by `gcloud auth print-access-token`). For "global" location
// the host is aiplatform.googleapis.com; regional locations use
// {location}-aiplatform.googleapis.com.
func (c *Client) chatVertex(ctx context.Context, system, user string) (string, error) {
	if c.cfg.Project == "" {
		return "", fmt.Errorf("vertex: llm.project is required")
	}
	if c.cfg.APIKey == "" {
		return "", fmt.Errorf("vertex: llm.api_key (gcloud access token) is required")
	}

	location := c.cfg.Location
	if location == "" {
		location = "global"
	}
	host := "aiplatform.googleapis.com"
	if location != "global" {
		host = location + "-aiplatform.googleapis.com"
	}
	base := c.cfg.Endpoint
	if base == "" {
		base = "https://" + host
	}
	url := fmt.Sprintf("%s/v1/projects/%s/locations/%s/publishers/google/models/%s:generateContent",
		strings.TrimRight(base, "/"), c.cfg.Project, location, c.cfg.Model)

	body := vertexRequest{
		Contents: []vertexContent{{
			Role:  "user",
			Parts: []vertexPart{{Text: user}},
		}},
		GenerationConfig: vertexGenConfig{
			Temperature:     0.3,
			MaxOutputTokens: 8192,
		},
	}
	if system != "" {
		body.SystemInstruction = &vertexContent{Parts: []vertexPart{{Text: system}}}
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
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)

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
		return "", fmt.Errorf("vertex API error (HTTP %d): %s", resp.StatusCode, string(data))
	}

	var result vertexResponse
	if err := json.Unmarshal(data, &result); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if result.Error != nil {
		return "", fmt.Errorf("vertex API error: %s", result.Error.Message)
	}
	if len(result.Candidates) == 0 {
		// Sometimes errors arrive as PromptFeedback.BlockReason
		if result.PromptFeedback != nil && result.PromptFeedback.BlockReason != "" {
			return "", fmt.Errorf("vertex blocked: %s", result.PromptFeedback.BlockReason)
		}
		return "", fmt.Errorf("vertex: no candidates in response")
	}

	cand := result.Candidates[0]
	var sb strings.Builder
	for _, p := range cand.Content.Parts {
		sb.WriteString(p.Text)
	}
	content := cleanThinkTags(sb.String())
	logger.Log("Vertex response: finish_reason=%s content_len=%d", cand.FinishReason, len(content))

	if content == "" && cand.FinishReason == "MAX_TOKENS" {
		return "", fmt.Errorf("response truncated (max_output_tokens reached)")
	}
	return content, nil
}

type vertexRequest struct {
	Contents          []vertexContent `json:"contents"`
	SystemInstruction *vertexContent  `json:"systemInstruction,omitempty"`
	GenerationConfig  vertexGenConfig `json:"generationConfig"`
}

type vertexContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []vertexPart `json:"parts"`
}

type vertexPart struct {
	Text string `json:"text"`
}

type vertexGenConfig struct {
	Temperature     float64 `json:"temperature,omitempty"`
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
}

type vertexResponse struct {
	Candidates     []vertexCandidate `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback,omitempty"`
	Error *apiError `json:"error,omitempty"`
}

type vertexCandidate struct {
	Content      vertexContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}
