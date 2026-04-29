//go:build integration
// +build integration

package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ray-x/finsight/internal/config"
)

// TestIntegrationChatFromConfig validates that the LLM settings from
// repo-root config.yaml can be loaded and used to complete a basic
// chat request.
func TestIntegrationChatFromConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := loadIntegrationConfig(t)
	c := NewClient(Config{
		Provider:      Provider(strings.ToLower(cfg.LLM.Provider)),
		Endpoint:      cfg.LLM.Endpoint,
		Model:         cfg.LLM.Model,
		APIKey:        cfg.LLM.APIKey,
		Project:       cfg.LLM.Project,
		Location:      cfg.LLM.Location,
		ContextTokens: cfg.LLM.ContextTokens,
	})

	if !c.Configured() {
		t.Skip("LLM is not configured from config.yaml (provider/model/endpoint missing)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	text, err := c.Chat(ctx, "You are a concise test assistant.", "Reply with exactly: finsight-ok")
	if err != nil {
		if isMissingAuth(err) {
			t.Skipf("LLM auth not available in this environment: %v", err)
		}
		t.Fatalf("chat failed: %v", err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("expected non-empty response")
	}
	if !strings.Contains(strings.ToLower(text), "finsight") {
		t.Fatalf("unexpected response %q; expected token containing 'finsight'", text)
	}
}

func loadIntegrationConfig(t *testing.T) *config.Config {
	t.Helper()
	rootCfg := filepath.Join("..", "..", "config.yaml")
	if _, err := os.Stat(rootCfg); err != nil {
		t.Skipf("config.yaml not found at %s: %v", rootCfg, err)
	}
	cfg, err := config.Load(rootCfg)
	if err != nil {
		t.Fatalf("load config %s: %v", rootCfg, err)
	}
	return cfg
}

func isMissingAuth(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	needles := []string{
		"no copilot oauth token",
		"token exchange http 401",
		"token exchange http 403",
		"unauthorized",
		"forbidden",
		"api key",
		"invalid authentication",
		"permission",
	}
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}
