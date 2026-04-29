package llm

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// chatCopilot calls the GitHub Copilot chat endpoint. It uses an
// OpenAI-style schema but on api.githubcopilot.com with a short-lived
// bearer token derived from the user's long-lived GitHub OAuth token.
//
// Endpoint resolution order:
//  1. cfg.Endpoint  — explicit override from config.yaml
//  2. endpoints.api returned by the token-exchange call (per-account;
//     this is how editors auto-discover business/enterprise hosts)
//  3. https://api.githubcopilot.com  — last-resort default for personal
func (c *Client) chatCopilot(ctx context.Context, system, user string) (string, error) {
	token, err := c.copilotChatToken(ctx)
	if err != nil {
		return "", fmt.Errorf("copilot auth: %w", err)
	}

	// GitHub Copilot rejects the legacy `max_tokens` field and requires
	// `max_completion_tokens` (matching OpenAI's newer reasoning-model
	// schema). It also expects `n` and `stream` to be explicit.
	body := struct {
		Model               string    `json:"model"`
		Messages            []Message `json:"messages"`
		Temperature         float64   `json:"temperature"`
		MaxCompletionTokens int       `json:"max_completion_tokens"`
		N                   int       `json:"n"`
		Stream              bool      `json:"stream"`
	}{
		Model:               c.cfg.Model,
		Messages:            []Message{{Role: "system", Content: system}, {Role: "user", Content: user}},
		Temperature:         0.3,
		MaxCompletionTokens: 8192,
		N:                   1,
		Stream:              false,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
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
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Editor-Version", "vscode/1.95.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.20.0")
	req.Header.Set("Copilot-Integration-Id", "vscode-chat")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.20.0")

	return c.doChatCompletions(req)
}

// copilotChatToken returns a valid short-lived chat token, exchanging
// the long-lived OAuth token if needed. Tokens are cached until a
// minute before expiry.
func (c *Client) copilotChatToken(ctx context.Context) (string, error) {
	c.copilotMu.Lock()
	defer c.copilotMu.Unlock()

	if c.copilotToken != "" && time.Until(c.copilotExpires) > time.Minute {
		return c.copilotToken, nil
	}

	oauth := c.cfg.APIKey
	if oauth == "" {
		var err error
		oauth, err = readCopilotOAuthToken()
		if err != nil {
			return "", err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.github.com/copilot_internal/v2/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "token "+oauth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Editor-Version", "vscode/1.95.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.20.0")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.20.0")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token exchange: %w", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token exchange HTTP %d: %s", resp.StatusCode, string(data))
	}

	var tr struct {
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expires_at"`
		Endpoints struct {
			API string `json:"api"`
		} `json:"endpoints"`
	}
	if err := json.Unmarshal(data, &tr); err != nil {
		return "", fmt.Errorf("token exchange parse: %w", err)
	}
	if tr.Token == "" {
		return "", fmt.Errorf("token exchange returned empty token")
	}

	c.copilotToken = tr.Token
	c.copilotEndpoint = strings.TrimRight(tr.Endpoints.API, "/")
	if tr.ExpiresAt > 0 {
		c.copilotExpires = time.Unix(tr.ExpiresAt, 0)
	} else {
		c.copilotExpires = time.Now().Add(25 * time.Minute)
	}
	return c.copilotToken, nil
}

// readCopilotOAuthToken locates the GitHub OAuth token saved by the
// official Copilot extensions. Searches the standard config locations.
func readCopilotOAuthToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	candidates := []string{
		filepath.Join(home, ".config", "github-copilot", "apps.json"),
		filepath.Join(home, ".config", "github-copilot", "hosts.json"),
		filepath.Join(home, "AppData", "Local", "github-copilot", "apps.json"),
		filepath.Join(home, "AppData", "Local", "github-copilot", "hosts.json"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		for _, v := range m {
			if tok, ok := v["oauth_token"].(string); ok && tok != "" {
				return tok, nil
			}
		}
	}
	return "", fmt.Errorf("no Copilot OAuth token found (looked in %s); set llm.api_key to a GitHub OAuth token, or sign in with the official Copilot extension first",
		strings.Join(candidates, ", "))
}
