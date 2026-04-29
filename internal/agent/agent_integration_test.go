//go:build integration
// +build integration

package agent

import (
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ray-x/finsight/internal/config"
	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/yahoo"
)

// TestIntegrationAgentToolsYahoo validates that key agent tools wired
// to Yahoo return parseable payloads against live data.
func TestIntegrationAgentToolsYahoo(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	y := yahoo.NewClient()
	tools := DefaultTools(Deps{Yahoo: y})
	byName := map[string]Tool{}
	for _, tool := range tools {
		byName[tool.Spec.Name] = tool
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	quote, ok := byName["get_quote"]
	if !ok {
		t.Fatal("get_quote not registered")
	}
	quoteOut, err := quote.Handler(ctx, map[string]any{"symbols": []any{"AAPL"}})
	if err != nil {
		t.Fatalf("get_quote failed: %v", err)
	}
	var quoteRows []map[string]any
	if err := json.Unmarshal([]byte(quoteOut), &quoteRows); err != nil {
		t.Fatalf("get_quote returned non-JSON payload: %v\npayload: %s", err, quoteOut)
	}
	if len(quoteRows) == 0 {
		t.Fatalf("get_quote returned zero rows: %s", quoteOut)
	}

	tech, ok := byName["get_technicals"]
	if !ok {
		t.Fatal("get_technicals not registered")
	}
	techOut, err := tech.Handler(ctx, map[string]any{"symbol": "AAPL", "range": "6mo", "interval": "1d"})
	if err != nil {
		t.Fatalf("get_technicals failed: %v", err)
	}
	var techDoc map[string]any
	if err := json.Unmarshal([]byte(techOut), &techDoc); err != nil {
		t.Fatalf("get_technicals returned non-JSON payload: %v\npayload: %s", err, techOut)
	}
	if _, ok := techDoc["symbol"]; !ok {
		t.Fatalf("get_technicals missing symbol field: %s", techOut)
	}

	earnings, ok := byName["get_earnings"]
	if !ok {
		t.Fatal("get_earnings not registered")
	}
	eOut, err := earnings.Handler(ctx, map[string]any{"symbol": "AAPL", "quarters": float64(4)})
	if err != nil {
		t.Fatalf("get_earnings failed: %v", err)
	}
	var eDoc map[string]any
	if err := json.Unmarshal([]byte(eOut), &eDoc); err != nil {
		t.Fatalf("get_earnings returned non-JSON payload: %v\npayload: %s", err, eOut)
	}
	if _, ok := eDoc["symbol"]; !ok {
		t.Fatalf("get_earnings missing symbol field: %s", eOut)
	}
}

// TestIntegrationAgentRunWithConfig validates the end-to-end agent
// loop using LLM settings loaded from config.yaml.
func TestIntegrationAgentRunWithConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := loadAgentIntegrationConfig(t)
	client := llm.NewClient(llm.Config{
		Provider:      llm.Provider(strings.ToLower(cfg.LLM.Provider)),
		Endpoint:      cfg.LLM.Endpoint,
		Model:         cfg.LLM.Model,
		APIKey:        cfg.LLM.APIKey,
		Project:       cfg.LLM.Project,
		Location:      cfg.LLM.Location,
		ContextTokens: cfg.LLM.ContextTokens,
	})
	if !client.Configured() {
		t.Skip("LLM is not configured from config.yaml (provider/model/endpoint missing)")
	}

	y := yahoo.NewClient()
	tools := []Tool{QuoteTool(y)}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	system := "You are an integration test assistant. You MUST call get_quote exactly once, then return one short sentence with the symbol and price."
	user := "What is the latest QQQ price?"
	text, trace, err := Run(ctx, client, system, user, tools, Options{MaxSteps: 4, PerToolCallCap: 2})
	if err != nil {
		if isLikelyMissingAuth(err) {
			t.Skipf("LLM auth not available in this environment: %v", err)
		}
		t.Fatalf("agent run failed: %v", err)
	}
	if strings.TrimSpace(text) == "" {
		t.Fatal("agent returned empty final answer")
	}
	if len(trace.Steps) == 0 {
		t.Fatalf("agent produced no tool calls; final answer: %q", text)
	}
	foundQuote := false
	for _, s := range trace.Steps {
		if s.ToolName == "get_quote" {
			foundQuote = true
			break
		}
	}
	if !foundQuote {
		t.Fatalf("expected trace to include get_quote; trace=%+v", trace.Steps)
	}
}

func loadAgentIntegrationConfig(t *testing.T) *config.Config {
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

func isLikelyMissingAuth(err error) bool {
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

// TestIntegrationMultiAgentOrchestration validates the multi-agent
// orchestration end-to-end using LLM settings from config.yaml.
// Each role agent runs concurrently, and the Portfolio Manager synthesizes results.
func TestIntegrationMultiAgentOrchestration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := loadAgentIntegrationConfig(t)
	client := llm.NewClient(llm.Config{
		Provider:      llm.Provider(strings.ToLower(cfg.LLM.Provider)),
		Endpoint:      cfg.LLM.Endpoint,
		Model:         cfg.LLM.Model,
		APIKey:        cfg.LLM.APIKey,
		Project:       cfg.LLM.Project,
		Location:      cfg.LLM.Location,
		ContextTokens: cfg.LLM.ContextTokens,
	})
	if !client.Configured() {
		t.Skip("LLM is not configured from config.yaml (provider/model/endpoint missing)")
	}

	y := yahoo.NewClient()
	tools := []Tool{QuoteTool(y), EarningsTool(y), TechnicalsTool(y)}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	question := "Analyse AAPL. Is now a good time to buy?"
	t.Logf("question: %q", question)

	var completedRoles []RoleType
	analyses, err := OrchestrateMultiAgentAnalysis(ctx, client, question, tools, MultiAgentConfig{
		MaxStepsPerRole: 4,
		PerToolCallCap:  2,
		ParallelRoles:   true,
		OnRoleComplete: func(analysis RoleAnalysis) {
			t.Logf("role %s complete: score=%.2f confidence=%.2f verdict=%q", analysis.Role, analysis.Score, analysis.Confidence, analysis.Verdict)
			completedRoles = append(completedRoles, analysis.Role)
		},
	})
	if err != nil {
		if isLikelyMissingAuth(err) {
			t.Skipf("LLM auth not available in this environment: %v", err)
		}
		t.Fatalf("multi-agent orchestration failed: %v", err)
	}
	if len(analyses) == 0 {
		t.Fatal("OrchestrateMultiAgentAnalysis returned no analyses")
	}

	// Validate we got results for expected roles
	roleMap := make(map[RoleType]RoleAnalysis)
	for _, a := range analyses {
		roleMap[a.Role] = a
	}
	t.Logf("total analyses returned: %d", len(analyses))
	for role, analysis := range roleMap {
		t.Logf("  role=%s score=%.2f confidence=%.2f verdict=%q chars=%d", role, analysis.Score, analysis.Confidence, analysis.Verdict, len(analysis.Analysis))
	}

	// Each role should have produced some analysis text
	for _, role := range []RoleType{RoleMarket, RoleFundamental, RoleTechnical, RoleRisk, RoleSentiment, RoleStrategy, RolePortfolio} {
		a, ok := roleMap[role]
		if !ok {
			t.Errorf("missing analysis for role %s", role)
			continue
		}
		if strings.TrimSpace(a.Analysis) == "" {
			t.Errorf("role %s produced empty analysis", role)
		}
	}

	// Portfolio synthesis should have a valid verdict
	portfolio, ok := roleMap[RolePortfolio]
	if !ok {
		t.Fatal("missing Portfolio Manager synthesis")
	}
	validVerdicts := map[string]bool{"Bullish": true, "Bearish": true, "Neutral": true}
	if !validVerdicts[portfolio.Verdict] {
		t.Errorf("Portfolio verdict %q is not one of Bullish/Bearish/Neutral", portfolio.Verdict)
	}
	t.Logf("final verdict: %s (score %.2f)", portfolio.Verdict, portfolio.Score)
}

// TestIntegrationMultiAgentVsSingleAgent compares timing of multi-agent
// (parallel) vs single-agent (sequential roles) for the same question.
// Logs the wall-clock times; does not assert timing constraints (LLM
// latency is too variable).
func TestIntegrationMultiAgentVsSingleAgent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := loadAgentIntegrationConfig(t)
	client := llm.NewClient(llm.Config{
		Provider:      llm.Provider(strings.ToLower(cfg.LLM.Provider)),
		Endpoint:      cfg.LLM.Endpoint,
		Model:         cfg.LLM.Model,
		APIKey:        cfg.LLM.APIKey,
		Project:       cfg.LLM.Project,
		Location:      cfg.LLM.Location,
		ContextTokens: cfg.LLM.ContextTokens,
	})
	if !client.Configured() {
		t.Skip("LLM is not configured from config.yaml (provider/model/endpoint missing)")
	}

	y := yahoo.NewClient()
	tools := []Tool{QuoteTool(y), TechnicalsTool(y)}
	question := "Brief technical analysis of NVDA."

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Sequential (ParallelRoles=false)
	seqStart := time.Now()
	seqAnalyses, seqErr := OrchestrateMultiAgentAnalysis(ctx, client, question, tools, MultiAgentConfig{
		MaxStepsPerRole: 3,
		PerToolCallCap:  1,
		ParallelRoles:   false,
	})
	seqDur := time.Since(seqStart)
	if seqErr != nil {
		if isLikelyMissingAuth(seqErr) {
			t.Skipf("LLM auth not available: %v", seqErr)
		}
		t.Fatalf("sequential multi-agent failed: %v", seqErr)
	}
	t.Logf("sequential: %d roles, duration=%s", len(seqAnalyses), seqDur)

	// Parallel (ParallelRoles=true)
	parStart := time.Now()
	parAnalyses, parErr := OrchestrateMultiAgentAnalysis(ctx, client, question, tools, MultiAgentConfig{
		MaxStepsPerRole: 3,
		PerToolCallCap:  1,
		ParallelRoles:   true,
	})
	parDur := time.Since(parStart)
	if parErr != nil {
		t.Fatalf("parallel multi-agent failed: %v", parErr)
	}
	t.Logf("parallel: %d roles, duration=%s", len(parAnalyses), parDur)

	if len(parAnalyses) == 0 {
		t.Fatal("parallel multi-agent returned no analyses")
	}
	t.Logf("speedup vs sequential: %.1fx", float64(seqDur)/float64(parDur))
}

// TestIntegrationMultiAgentRoleIsolation verifies that one role failing
// (due to a bad tool) does not prevent other roles from completing.
func TestIntegrationMultiAgentRoleIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	cfg := loadAgentIntegrationConfig(t)
	client := llm.NewClient(llm.Config{
		Provider:      llm.Provider(strings.ToLower(cfg.LLM.Provider)),
		Endpoint:      cfg.LLM.Endpoint,
		Model:         cfg.LLM.Model,
		APIKey:        cfg.LLM.APIKey,
		Project:       cfg.LLM.Project,
		Location:      cfg.LLM.Location,
		ContextTokens: cfg.LLM.ContextTokens,
	})
	if !client.Configured() {
		t.Skip("LLM is not configured from config.yaml (provider/model/endpoint missing)")
	}

	y := yahoo.NewClient()
	// Provide a broken tool that always errors, alongside a good one
	brokenTool := Tool{
		Spec: llm.ToolSpec{
			Name:        "always_fails",
			Description: "This tool always fails. Do not use it.",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "", context.DeadlineExceeded
		},
	}
	tools := []Tool{QuoteTool(y), brokenTool}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	analyses, err := OrchestrateMultiAgentAnalysis(ctx, client, "Quick AAPL sentiment check.", tools, MultiAgentConfig{
		MaxStepsPerRole: 3,
		PerToolCallCap:  1,
		ParallelRoles:   true,
	})
	if err != nil {
		if isLikelyMissingAuth(err) {
			t.Skipf("LLM auth not available: %v", err)
		}
		t.Fatalf("orchestration failed: %v", err)
	}
	// Should still get analyses from all roles that could complete
	if len(analyses) == 0 {
		t.Fatal("expected at least some analyses despite broken tool")
	}
	t.Logf("received %d analyses even with broken tool present", len(analyses))
}
