package agent

import (
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/ray-x/finsight/internal/llm"
)

// TestMultiAgentOrchestratorInitialization validates orchestrator setup.
func TestMultiAgentOrchestratorInitialization(t *testing.T) {
	cfg := MultiAgentConfig{
		MaxStepsPerRole: 3,
		PerToolCallCap:  2,
		ParallelRoles:   true,
	}
	orch := NewMultiAgentOrchestrator(cfg)
	if orch == nil {
		t.Fatal("orchestrator is nil")
	}
	if len(orch.Agents) != 0 {
		t.Fatalf("expected 0 agents initially, got %d", len(orch.Agents))
	}
}

// TestRegisterRole validates role registration.
func TestRegisterRole(t *testing.T) {
	orch := NewMultiAgentOrchestrator(MultiAgentConfig{})
	client := &llm.Client{}
	tools := []Tool{}
	prompt := "test prompt"
	question := "What changed for NVDA?"

	orch.RegisterRole(RoleMarket, client, tools, prompt, question)

	if agent, ok := orch.Agents[RoleMarket]; !ok {
		t.Fatalf("role %s not registered", RoleMarket)
	} else if agent.Role != RoleMarket {
		t.Fatalf("expected role %s, got %s", RoleMarket, agent.Role)
	} else if agent.Prompt != prompt {
		t.Fatalf("prompt mismatch")
	} else if agent.Question != question {
		t.Fatalf("question mismatch")
	}
}

// TestRoleTypeConstants validates role type definitions.
func TestRoleTypeConstants(t *testing.T) {
	roles := []RoleType{
		RoleMarket,
		RoleFundamental,
		RoleTechnical,
		RoleRisk,
		RoleSentiment,
		RoleStrategy,
		RolePortfolio,
	}
	if len(roles) != 7 {
		t.Fatalf("expected 7 roles, got %d", len(roles))
	}
	for _, r := range roles {
		if r == "" {
			t.Fatal("role type is empty")
		}
	}
}

// TestRoleAnalysisJSONMarshaling validates serialization.
func TestRoleAnalysisJSONMarshaling(t *testing.T) {
	analysis := RoleAnalysis{
		Role:       RoleMarket,
		Analysis:   "Market is bullish",
		Score:      1.5,
		Confidence: 0.85,
		Verdict:    "Bullish",
		RawOutput:  "full response",
	}

	// Should marshal without error (error field not serialized)
	_, err := json.Marshal(analysis)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
}

// TestExtractVerdict validates verdict extraction from text.
func TestExtractVerdict(t *testing.T) {
	cases := []struct {
		text     string
		expected string
	}{
		{"This stock is bullish", "Bullish"},
		{"I recommend a strong buy", "Bullish"},
		{"Very bearish sentiment", "Bearish"},
		{"This is a strong sell", "Bearish"},
		{"Neutral on this one", "Neutral"},
		{"No clear signal", "Neutral"},
	}
	for _, tc := range cases {
		got := extractVerdict(tc.text)
		if got != tc.expected {
			t.Fatalf("text=%q: expected %q, got %q", tc.text, tc.expected, got)
		}
	}
}

func TestExtractScoreAndConfidence(t *testing.T) {
	score, confidence := extractScoreAndConfidence("Analysis...\nScore: 1.25\nConfidence: 80%")
	if score != 1.25 {
		t.Fatalf("expected score 1.25, got %.2f", score)
	}
	if confidence != 0.8 {
		t.Fatalf("expected confidence 0.8, got %.2f", confidence)
	}

	score, confidence = extractScoreAndConfidence("No structured fields here")
	if score != 0 || confidence != 0 {
		t.Fatalf("expected zero defaults, got %.2f / %.2f", score, confidence)
	}
}

func TestExtractScoreAndConfidenceClampsAndSupportsAliases(t *testing.T) {
	score, confidence := extractScoreAndConfidence("technical_score: 9\nConfidence: 125")
	if score != 2 {
		t.Fatalf("expected clamped score 2, got %.2f", score)
	}
	if confidence != 1 {
		t.Fatalf("expected clamped confidence 1, got %.2f", confidence)
	}

	score, confidence = extractScoreAndConfidence("risk_score: -9\nConfidence: -5")
	if score != -2 {
		t.Fatalf("expected clamped score -2, got %.2f", score)
	}
	if confidence != 0 {
		t.Fatalf("expected clamped confidence 0, got %.2f", confidence)
	}
}

func TestBuildRoleMessagesIncludesQuestion(t *testing.T) {
	msgs := buildRoleMessages("system prompt", "user question")
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[1].Role != "user" {
		t.Fatalf("unexpected roles: %+v", msgs)
	}
	if msgs[1].Content != "user question" {
		t.Fatalf("unexpected user message: %+v", msgs[1])
	}
}

func TestBuildRoleMessagesOmitsBlankQuestion(t *testing.T) {
	msgs := buildRoleMessages("system prompt", "   ")
	if len(msgs) != 1 {
		t.Fatalf("expected only system message, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("unexpected role: %+v", msgs[0])
	}
}

// TestCalculateWeightedScore validates score weighting across roles.
func TestCalculateWeightedScore(t *testing.T) {
	roles := []RoleAnalysis{
		{Role: RoleFundamental, Score: 2.0}, // 35% weight → 0.7
		{Role: RoleTechnical, Score: 1.0},   // 25% weight → 0.25
		{Role: RoleSentiment, Score: 0.0},   // 15% weight → 0.0
		{Role: RoleStrategy, Score: 0.5},    // 15% weight → 0.075
		{Role: RoleMarket, Score: -1.0},     // 5% weight → -0.05
		{Role: RoleRisk, Score: -0.5},       // 5% weight → -0.025
	}
	// Expected: 0.7 + 0.25 + 0.0 + 0.075 - 0.05 - 0.025 = 0.95
	expected := 0.95
	got := calculateWeightedScore(roles)

	// Use approximate equality due to floating-point precision
	epsilon := 0.001
	if got < expected-epsilon || got > expected+epsilon {
		t.Fatalf("expected ~%.2f, got %.2f", expected, got)
	}
}

// TestWeightedScoreWithMissingRoles validates handling of incomplete role set.
func TestWeightedScoreWithMissingRoles(t *testing.T) {
	roles := []RoleAnalysis{
		{Role: RoleFundamental, Score: 1.0}, // 35% → 0.35
		{Role: RoleTechnical, Score: 2.0},   // 25% → 0.5
		{Role: RoleSentiment, Score: -0.5},  // 15% → -0.075
		// Missing: Strategy, Market, Risk
	}
	// Should still compute weighted avg of available roles.
	got := calculateWeightedScore(roles)
	if got == 0.0 {
		t.Fatalf("expected non-zero weighted score")
	}
}

// TestMultiAgentConfigDefaults validates default configuration.
func TestMultiAgentConfigDefaults(t *testing.T) {
	cfg := MultiAgentConfig{}
	orch := NewMultiAgentOrchestrator(cfg)
	if orch.Config.MaxStepsPerRole != 6 {
		t.Fatalf("expected MaxStepsPerRole=6, got %d", orch.Config.MaxStepsPerRole)
	}
	if orch.Config.PerToolCallCap != 4 {
		t.Fatalf("expected PerToolCallCap=4, got %d", orch.Config.PerToolCallCap)
	}
}

// TestRoleAgentChannelBehavior validates result channel communication.
func TestRoleAgentChannelBehavior(t *testing.T) {
	agent := &RoleAgent{
		Role:       RoleMarket,
		ResultChan: make(chan RoleAnalysis, 1),
	}

	result := RoleAnalysis{
		Role:    RoleMarket,
		Score:   0.5,
		Verdict: "Neutral",
	}

	// Non-blocking send to buffered channel.
	agent.ResultChan <- result

	// Receive and verify.
	received := <-agent.ResultChan
	if received.Role != RoleMarket {
		t.Fatalf("role mismatch")
	}
	if received.Score != 0.5 {
		t.Fatalf("score mismatch")
	}
}

// BenchmarkWeightedScoring benchmarks the weighting calculation.
func BenchmarkWeightedScoring(b *testing.B) {
	roles := []RoleAnalysis{
		{Role: RoleFundamental, Score: 1.5},
		{Role: RoleTechnical, Score: 0.8},
		{Role: RoleSentiment, Score: -0.2},
		{Role: RoleStrategy, Score: 0.6},
		{Role: RoleMarket, Score: 0.3},
		{Role: RoleRisk, Score: -0.4},
	}
	for i := 0; i < b.N; i++ {
		_ = calculateWeightedScore(roles)
	}
}

// TestOrchestrateMultiAgentAnalysisValidation tests input validation.
func TestOrchestrateMultiAgentAnalysisValidation(t *testing.T) {
	ctx := context.Background()

	// No client should fail.
	_, err := OrchestrateMultiAgentAnalysis(ctx, nil, "test", []Tool{}, MultiAgentConfig{})
	if err == nil {
		t.Fatal("expected error for nil client")
	}
}
