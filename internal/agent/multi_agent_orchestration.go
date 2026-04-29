// Package agent implements multi-agent orchestration for the seven-role analysis workflow.
// Each role (Market, Fundamental, Technical, Risk, Sentiment, Strategy) runs as an
// independent concurrent agent, with a final Portfolio Manager agent synthesizing results.
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/logger"
)

// RoleType identifies an analyst role in the multi-agent system.
type RoleType string

const (
	RoleMarket      RoleType = "market"
	RoleFundamental RoleType = "fundamental"
	RoleTechnical   RoleType = "technical"
	RoleRisk        RoleType = "risk"
	RoleSentiment   RoleType = "sentiment"
	RoleStrategy    RoleType = "strategy"
	RolePortfolio   RoleType = "portfolio"
)

// RoleAnalysis holds the result of one analyst's evaluation.
type RoleAnalysis struct {
	Role       RoleType              `json:"role"`
	Analysis   string                `json:"analysis"`
	Score      float64               `json:"score"`
	Confidence float64               `json:"confidence"`
	Verdict    string                `json:"verdict,omitempty"`     // for synthesis roles
	RawOutput  string                `json:"raw_output,omitempty"`  // full LLM response for audit
	Error      error                 `json:"-"`                     // errors don't serialize to JSON
}

// MultiAgentConfig tunes the orchestration layer.
type MultiAgentConfig struct {
	MaxStepsPerRole int           // 0 → 6; max LLM iterations per role
	PerToolCallCap  int           // 0 → 4; max times same tool can be called
	ParallelRoles   bool          // if true, run all roles concurrently; if false, sequential (for testing/debugging)
	OnRoleStep      func(RoleType, Step) // callback when a role completes a tool call
	OnRoleComplete  func(RoleAnalysis)   // callback when a role completes analysis
}

// RoleAgent represents one analyst in the system.
type RoleAgent struct {
	Role       RoleType
	Prompt     string
	Tools      []Tool
	Client     *llm.Client
	ResultChan chan RoleAnalysis
}

// MultiAgentOrchestrator manages the lifecycle of all role agents and synthesis.
type MultiAgentOrchestrator struct {
	Agents map[RoleType]*RoleAgent
	Config MultiAgentConfig
	mu     sync.Mutex
}

// NewMultiAgentOrchestrator creates a new orchestration coordinator.
func NewMultiAgentOrchestrator(cfg MultiAgentConfig) *MultiAgentOrchestrator {
	if cfg.MaxStepsPerRole <= 0 {
		cfg.MaxStepsPerRole = 6
	}
	if cfg.PerToolCallCap <= 0 {
		cfg.PerToolCallCap = 4
	}
	return &MultiAgentOrchestrator{
		Agents: make(map[RoleType]*RoleAgent),
		Config: cfg,
	}
}

// RegisterRole adds a role agent to the orchestrator.
func (m *MultiAgentOrchestrator) RegisterRole(role RoleType, client *llm.Client, tools []Tool, prompt string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Agents[role] = &RoleAgent{
		Role:       role,
		Client:     client,
		Tools:      tools,
		Prompt:     prompt,
		ResultChan: make(chan RoleAnalysis, 1),
	}
}

// RunRoleAgent executes a single role's analysis in a goroutine.
// It calls the LLM iteratively, dispatches tool calls in parallel,
// and returns the final analysis via the result channel.
func (m *MultiAgentOrchestrator) RunRoleAgent(ctx context.Context, role *RoleAgent) {
	defer close(role.ResultChan)

	opts := Options{
		MaxSteps:       m.Config.MaxStepsPerRole,
		PerToolCallCap: m.Config.PerToolCallCap,
		OnStep: func(s Step) {
			if m.Config.OnRoleStep != nil {
				m.Config.OnRoleStep(role.Role, s)
			}
		},
	}

	// Build tool specs for this role.
	specs := make([]llm.ToolSpec, 0, len(role.Tools))
	byName := make(map[string]Tool, len(role.Tools))
	for _, t := range role.Tools {
		byName[t.Spec.Name] = t
		specs = append(specs, t.Spec)
	}

	// Execute the agent loop.
	systemPrompt := role.Prompt
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
	}
	callCount := make(map[string]int)

	var finalAnalysis string
	var stepErr error

	for step := 0; step < opts.MaxSteps; step++ {
		resp, err := role.Client.ChatWithTools(ctx, messages, specs)
		if err != nil {
			stepErr = fmt.Errorf("role %s step %d: %w", role.Role, step, err)
			break
		}

		// No tool calls → model produced final answer.
		if len(resp.ToolCalls) == 0 {
			logger.Log("agent: role %s finished on step %d", role.Role, step)
			finalAnalysis = resp.Content
			break
		}

		// Append assistant turn.
		messages = append(messages, resp)

		// Dispatch tool calls in parallel.
		results := dispatchAll(ctx, resp.ToolCalls, byName, callCount, opts)
		for _, r := range results {
			if opts.OnStep != nil {
				opts.OnStep(r.step)
			}
			messages = append(messages, llm.Message{
				Role:       "tool",
				ToolCallID: r.callID,
				Name:       r.step.ToolName,
				Content:    r.content,
			})
		}
	}

	// Parse the final analysis to extract score and confidence.
	score, confidence := extractScoreAndConfidence(finalAnalysis)
	verdict := extractVerdict(finalAnalysis)

	analysis := RoleAnalysis{
		Role:       role.Role,
		Analysis:   finalAnalysis,
		Score:      score,
		Confidence: confidence,
		Verdict:    verdict,
		RawOutput:  finalAnalysis,
		Error:      stepErr,
	}

	if m.Config.OnRoleComplete != nil {
		m.Config.OnRoleComplete(analysis)
	}

	role.ResultChan <- analysis
}

// RunAllRoles executes all registered roles either in parallel or sequentially,
// depending on Config.ParallelRoles. Returns all analyses once complete.
func (m *MultiAgentOrchestrator) RunAllRoles(ctx context.Context) ([]RoleAnalysis, error) {
	// Copy agent list while holding lock, then release before spawning goroutines
	m.mu.Lock()
	agents := make([]*RoleAgent, 0, len(m.Agents))
	for _, agent := range m.Agents {
		agents = append(agents, agent)
	}
	m.mu.Unlock()

	if m.Config.ParallelRoles {
		// Spawn all roles concurrently using sync.WaitGroup.Go (Go 1.25).
		var wg sync.WaitGroup
		for _, agent := range agents {
			agent := agent
			wg.Go(func() {
				m.RunRoleAgent(ctx, agent)
			})
		}
		wg.Wait()
	} else {
		// Run roles sequentially (useful for debugging/testing).
		for _, agent := range agents {
			m.RunRoleAgent(ctx, agent)
		}
	}

	// Collect results from all channels (safe to do without locks since all goroutines are done)
	results := make([]RoleAnalysis, 0, len(agents))
	for _, agent := range agents {
		result := <-agent.ResultChan
		results = append(results, result)
	}

	return results, nil
}

// SynthesisAgent represents the Portfolio Manager who merges all role outputs.
type SynthesisAgent struct {
	Client    *llm.Client
	Tools     []Tool
	RoleInputs []RoleAnalysis
}

// RunSynthesis executes the Portfolio Manager synthesis step.
// It receives all role analyses, packages them as input, and produces
// a final integrated verdict with weighted scoring.
func (s *SynthesisAgent) RunSynthesis(ctx context.Context, roles []RoleAnalysis) (RoleAnalysis, error) {
	// Build a summary of all role outputs to feed to the Portfolio Manager.
	rolesSummary := ""
	for _, r := range roles {
		rolesSummary += fmt.Sprintf(
			"## %s\nScore: %.2f/2 | Confidence: %.2f\n%s\n\n",
			titleCase(string(r.Role)),
			r.Score,
			r.Confidence,
			r.Analysis,
		)
	}

	systemPrompt := fmt.Sprintf(`You are a portfolio manager synthesizing analysis from six specialist roles.
Each role has provided their assessment with a score [-2..2] and confidence [0..1].

Role Analyses:
%s

Your job:
1. Weigh the scores: Fundamentals 35%%, Technicals 25%%, Sentiment 15%%, Strategy 15%%, Market 5%%, Risk 5%%.
2. Output a final verdict (Bullish/Neutral/Bearish) and weighted total score.
3. Briefly explain key drivers and downside triggers.
4. Recommend actions.
`, rolesSummary)

	// For synthesis, use a simpler prompt; no tools needed (already have all data).
	specs := make([]llm.ToolSpec, 0)
	messages := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Synthesize the above analyses into a final verdict and recommendation."},
	}

	resp, err := s.Client.ChatWithTools(ctx, messages, specs)
	if err != nil {
		return RoleAnalysis{}, fmt.Errorf("synthesis: %w", err)
	}

	weightedTotal := calculateWeightedScore(roles)
	verdict := extractVerdict(resp.Content)

	return RoleAnalysis{
		Role:       RolePortfolio,
		Analysis:   resp.Content,
		Score:      weightedTotal,
		Confidence: 1.0, // synthesis combines all roles
		Verdict:    verdict,
		RawOutput:  resp.Content,
	}, nil
}

// OrchestrateMutiAgentAnalysis is the main entry point. It:
// 1. Spawns all role agents concurrently
// 2. Waits for all role results
// 3. Runs synthesis to produce final verdict
// 4. Returns complete analysis from all 7 roles
func OrchestrateMultiAgentAnalysis(
	ctx context.Context,
	client *llm.Client,
	question string,
	tools []Tool,
	cfg MultiAgentConfig,
) ([]RoleAnalysis, error) {
	if client == nil {
		return nil, fmt.Errorf("orchestration: client required")
	}

	orchestrator := NewMultiAgentOrchestrator(cfg)

	// Define role-specific prompts (simplified; in production, much more detailed).
	rolePrompts := map[RoleType]string{
		RoleMarket: `You are a Market Analyst. Analyze the broader market context, sector trends, 
and macroeconomic factors relevant to this stock. Provide a market_score [-2..2] and confidence.`,

		RoleFundamental: `You are a Fundamental Analyst. Evaluate growth, profitability, balance sheet, 
valuation, and earnings quality. Provide a fundamental_score [-2..2] and confidence.`,

		RoleTechnical: `You are a Technical Analyst. Analyze price trends, momentum, support/resistance, 
and volatility. Provide a technical_score [-2..2] and confidence.`,

		RoleRisk: `You are a Risk Analyst. Identify key risks: concentration, liquidity, event risk, 
downside triggers. Provide a risk_score [-2..2] (negative = higher risk) and confidence.`,

		RoleSentiment: `You are a Sentiment & News Analyst. Evaluate recent headlines, sentiment tone, 
and novelty. Provide a sentiment_news_score [-2..2] and confidence.`,

		RoleStrategy: `You are a Strategy Analyst. Evaluate alignment with investment strategies 
(value, growth, dividend, momentum, etc.), position sizing, and portfolio fit. Provide a strategy_score [-2..2] and confidence.`,
	}

	// Register each role.
	for role, prompt := range rolePrompts {
		// In production, you'd pass role-specific subsets of tools.
		// For now, all roles get all tools.
		orchestrator.RegisterRole(role, client, tools, prompt)
	}

	// Run all role agents in parallel.
	roleResults, err := orchestrator.RunAllRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("orchestration: run roles failed: %w", err)
	}

	// Run synthesis (Portfolio Manager).
	synthesis := &SynthesisAgent{Client: client, Tools: tools}
	synthesisResult, err := synthesis.RunSynthesis(ctx, roleResults)
	if err != nil {
		logger.Log("orchestration: synthesis failed: %v", err)
		// Don't fail the whole analysis; synthesis is optional.
	} else {
		roleResults = append(roleResults, synthesisResult)
	}

	return roleResults, nil
}

// Helper functions for score extraction and weighting.

func extractScoreAndConfidence(text string) (float64, float64) {
	// Simplified extraction; in production, use regex to find "Score: X/2, Confidence: Y" pattern.
	// For now, return defaults.
	return 0.0, 0.8
}

func extractVerdict(text string) string {
	lowerText := strings.ToLower(text)
	if strings.Contains(lowerText, "bullish") || strings.Contains(lowerText, "strong buy") || strings.Contains(lowerText, "buy") {
		return "Bullish"
	}
	if strings.Contains(lowerText, "bearish") || strings.Contains(lowerText, "strong sell") || strings.Contains(lowerText, "sell") {
		return "Bearish"
	}
	return "Neutral"
}

func containsAny(s string, substrs ...string) bool {
	lowerS := strings.ToLower(s)
	for _, sub := range substrs {
		if strings.Contains(lowerS, strings.ToLower(sub)) {
			return true
		}
	}
	return false
}

func calculateWeightedScore(roles []RoleAnalysis) float64 {
	weights := map[RoleType]float64{
		RoleFundamental: 0.35,
		RoleTechnical:   0.25,
		RoleSentiment:   0.15,
		RoleStrategy:    0.15,
		RoleMarket:      0.05,
		RoleRisk:        0.05,
	}

	total := 0.0
	weightSum := 0.0

	for _, r := range roles {
		if w, ok := weights[r.Role]; ok {
			total += r.Score * w
			weightSum += w
		}
	}

	if weightSum > 0 {
		return total / weightSum
	}
	return 0.0
}

func titleCase(s string) string {
	if len(s) == 0 {
		return s
	}
	return string(s[0]-32) + s[1:] // Simple uppercase first letter
}
