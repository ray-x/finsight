# Multi-Agent Orchestration Layer

This document describes the concurrent multi-agent analysis system for finsight's `/ask` command. Each of the seven specialist roles (Market, Fundamental, Technical, Risk, Sentiment, Strategy, Portfolio Manager) runs as an independent concurrent agent with parallel tool execution.

## Architecture

### Components

#### 1. **RoleAgent**
- Represents one specialist analyst in the system
- Runs independently with its own LLM client and tool set
- Executes the agent loop (LLM → tool calls → LLM → final answer)
- Sends results via a result channel

#### 2. **MultiAgentOrchestrator**
- Manages registration and lifecycle of all role agents
- Spawns role agents as goroutines (optionally in parallel)
- Collects results from all channels
- Coordinates synthesis step

#### 3. **SynthesisAgent**
- Portfolio Manager role: synthesizes all 6 analyst outputs
- Applies role weighting: Fundamentals 35%, Technicals 25%, Sentiment 15%, Strategy 15%, Market 5%, Risk 5%
- Produces final verdict and weighted score

### Execution Flow

```
User Question
    ↓
[Orchestrator.RunAllRoles()]
    │
    ├─→ [RoleAgent: Market] → Tools → LLM → Score + Analysis
    ├─→ [RoleAgent: Fundamental] → Tools → LLM → Score + Analysis
    ├─→ [RoleAgent: Technical] → Tools → LLM → Score + Analysis
    ├─→ [RoleAgent: Risk] → Tools → LLM → Score + Analysis
    ├─→ [RoleAgent: Sentiment] → Tools → LLM → Score + Analysis
    └─→ [RoleAgent: Strategy] → Tools → LLM → Score + Analysis
           (all run concurrently via sync.WaitGroup.Go)
    ↓
[Collect results from channels]
    ↓
[SynthesisAgent.RunSynthesis()]
    ↓
[Final Report: All 7 analyses + weighted verdict]
```

## Key Design Patterns

### 1. Goroutine Management with `sync.WaitGroup.Go` (Go 1.25)

All roles spawn concurrently using the Go 1.25 `sync.WaitGroup.Go` method:

```go
var wg sync.WaitGroup
for _, agent := range orchestrator.Agents {
    agent := agent
    wg.Go(func() {
        orchestrator.RunRoleAgent(ctx, agent)
    })
}
wg.Wait() // Wait for all roles to complete
```

**Benefits:**
- Single method handles `Add(1)`, goroutine launch, and `Done()` automatically
- No external dependencies (stdlib only)
- Automatic synchronization (no manual channel draining)
- Cleaner than manual `Add/Done` WaitGroup pattern

### 2. Channel-Based Result Aggregation

Each role writes its `RoleAnalysis` to a buffered result channel:

```go
type RoleAnalysis struct {
    Role       RoleType  `json:"role"`
    Analysis   string    `json:"analysis"`
    Score      float64   `json:"score"`
    Confidence float64   `json:"confidence"`
    Verdict    string    `json:"verdict,omitempty"`
    RawOutput  string    `json:"raw_output,omitempty"`
    Error      error     `json:"-"`
}

// Each role sends once:
agent.ResultChan <- analysis

// Orchestrator collects:
for _, agent := range orchestrator.Agents {
    result := <-agent.ResultChan
    results = append(results, result)
}
```

**Benefits:**
- Non-blocking communication between roles and orchestrator
- Type-safe result passing
- Natural pipeline semantics

### 3. Parallel Tool Dispatch (Already in Place)

Within each role agent, the existing `dispatchAll()` function dispatches multiple tool calls in parallel using `sync.WaitGroup.Go`:

```go
var wg sync.WaitGroup
for i, call := range resp.ToolCalls {
    i, call := i, call
    wg.Go(func() {
        res, err := tool.Handler(ctx, args)
        out[i] = dispatchResult{
            callID:  call.ID,
            step:    step,
            content: string_result,
        }
    })
}
wg.Wait()
```

This means **nested concurrency**:
- L1: 6 role agents run in parallel
- L2: Within each role, tool calls run in parallel

## Usage

### Basic Integration

```go
package ui

import (
    "context"
    "github.com/ray-x/finsight/internal/agent"
    "github.com/ray-x/finsight/internal/llm"
)

// In your /ask handler:
func askMultiAgent(ctx context.Context, client *llm.Client, question string, tools []agent.Tool) ([]agent.RoleAnalysis, error) {
    cfg := agent.MultiAgentConfig{
        MaxStepsPerRole: 6,
        PerToolCallCap:  4,
        ParallelRoles:   true,  // Run all roles concurrently
        OnRoleStep: func(role agent.RoleType, step agent.Step) {
            logger.Log("  [%s] tool: %s", role, step.ToolName)
        },
        OnRoleComplete: func(analysis agent.RoleAnalysis) {
            logger.Log("  [%s] complete: score=%.2f, verdict=%s", 
                analysis.Role, analysis.Score, analysis.Verdict)
        },
    }

    return agent.OrchestrateMultiAgentAnalysis(ctx, client, question, tools, cfg)
}
```

### Handling Results

```go
analyses, err := askMultiAgent(ctx, client, question, tools)
if err != nil {
    return fmt.Errorf("multi-agent analysis failed: %w", err)
}

// All 7 analyses (6 roles + 1 synthesis)
for _, analysis := range analyses {
    fmt.Printf("## %s\n", analysis.Role)
    fmt.Printf("Score: %.2f | Confidence: %.2f\n", analysis.Score, analysis.Confidence)
    fmt.Printf("%s\n\n", analysis.Analysis)
}
```

### Sequential Mode (for Testing/Debugging)

```go
cfg := agent.MultiAgentConfig{
    ParallelRoles: false,  // Run roles one at a time
}
analyses, _ := agent.OrchestrateMultiAgentAnalysis(ctx, client, question, tools, cfg)
```

Useful for:
- Debugging role interactions
- Reducing resource contention in test environments
- Deterministic ordering for test assertions

## Concurrency Safety

### Shared Mutable State

The orchestrator uses `sync.Mutex` to protect agent map:

```go
type MultiAgentOrchestrator struct {
    Agents map[RoleType]*RoleAgent
    mu     sync.Mutex
}

func (m *MultiAgentOrchestrator) RegisterRole(...) {
    m.mu.Lock()
    defer m.Unlock()
    m.Agents[role] = agent
}
```

### Per-Role Tool Dispatch

Each role's `dispatchAll()` uses local `sync.Mutex` to track per-tool call counts:

```go
var mu sync.Mutex
var wg sync.WaitGroup

for i, call := range calls {
    wg.Go(func() {
        // Track tool usage (protected by mutex)
        mu.Lock()
        callCount[call.Function.Name]++
        over := callCount[call.Function.Name] > opts.PerToolCallCap
        mu.Unlock()
        
        if over {
            return // tool cap reached; error recorded via out[i]
        }
        // Execute tool
    })
}
wg.Wait()
```

### Result Channels (No Locks Needed)

Result channels use buffering, so `wg.Wait()` ensures all writers close before we read:

```go
wg.Wait()  // All role goroutines complete here
for _, agent := range orchestrator.Agents {
    result := <-agent.ResultChan  // Safe to read; writer is done
}
```

## Performance Characteristics

### Parallelism Levels

| Level | Description | Impl |
|-------|-------------|------|
| **L1: Roles** | 6 roles run concurrently | `sync.WaitGroup.Go` + goroutines |
| **L2: Tools per Role** | Tool calls within a role run in parallel | `sync.WaitGroup.Go` + `dispatchAll()` |
| **L3: Sequential** | LLM iterations within a role are sequential | Each role's agent loop |

### Latency Improvements

**Sequential (Single LLM Pass):**
```
1 LLM call for all 7 roles simultaneously: ~T_llm
Total: ~T_llm (but less control per role, limited by slowest role)
```

**Parallel (Multi-Agent):**
```
6 role agents in parallel: max(T_role1, T_role2, ..., T_role6)
+ 1 synthesis: T_synthesis
Total: ~max(T_role) + T_synthesis
```

Typically **2-4x faster** than sequential per-role execution (if roles were serial), since LLM latency dominates and overlaps.

### Memory Overhead

- **6 RoleAgent structs** (1 client, 1 tool set, 1 result channel each)
- **Buffered result channels** (6 channels, 1 message each = ~1KB)
- **Goroutines** (6 role + N tool dispatch = ~12 lightweight goroutines)

Negligible compared to LLM API calls.

## Configuration Tuning

### Parallel vs. Sequential Roles

```go
// Fast: Parallel (default)
ParallelRoles: true

// Slow: Sequential (debug/test only)
ParallelRoles: false
```

### Per-Tool Call Limits

Prevent infinite tool loops:

```go
PerToolCallCap: 4  // Any single tool can be called max 4 times across LLM iterations
```

### Max Steps Per Role

```go
MaxStepsPerRole: 6  // Each role's LLM loop runs max 6 iterations
```

## Testing

### Unit Tests

```bash
GOEXPERIMENT=jsonv2 go test ./internal/agent -v -run "MultiAgent|RoleAnalysis|Weighted" --count=1
```

Tests cover:
- Orchestrator initialization
- Role registration
- Result channel behavior
- Score weighting
- Verdict extraction
- Parallel vs. sequential execution

### Benchmark

```bash
GOEXPERIMENT=jsonv2 go test ./internal/agent -bench=Weighting -benchmem
```

## Transitioning from Single-Agent to Multi-Agent

### Current State (Existing)

Single LLM call with 7 roles in prompt:

```go
systemPrompt := llm.SevenRoleInstruction()  // All roles in one prompt
text, _, _ := agent.Run(ctx, client, systemPrompt, question, tools, opts)
```

### New State (Multi-Agent)

7 concurrent role agents + synthesis:

```go
analyses, _ := agent.OrchestrateMultiAgentAnalysis(
    ctx, client, question, tools, 
    agent.MultiAgentConfig{ParallelRoles: true},
)
```

Both approaches produce identical output (12-section markdown + Factor Vote JSON), but multi-agent provides:
- **Concurrency** (faster with multiple roles)
- **Isolation** (role failures don't break others)
- **Flexibility** (customize role prompts, tools per role)
- **Traceability** (separate analysis per role)

## Future Enhancements

1. **Role-Specific Tools**
   ```go
   orchestrator.RegisterRole(RoleMarket, client, marketTools, ...)
   orchestrator.RegisterRole(RoleTechnical, client, technicalTools, ...)
   ```

2. **Role Inter-Communication**
   ```go
   // Sentiment agent could query technical analysis result before finalizing
   sentiment.Dependencies = []RoleType{RoleTechnical}
   ```

3. **Adaptive Weighting**
   ```go
   // Adjust weights dynamically based on confidence/volatility
   weights := computeAdaptiveWeights(ctx, analyses)
   ```

4. **Budget Management**
   ```go
   cfg.TokenBudget = 100_000  // Fail-safe LLM token limit per role
   ```

5. **Fallback Strategies**
   ```go
   if roleResult.Error != nil {
       fallback := runSimplifiedAnalysis(role)
       results = append(results, fallback)
   }
   ```

## References

- `internal/agent/agent.go` — Single-agent loop (used within each role)
- `internal/agent/multi_agent_orchestration.go` — Orchestrator + synthesis
- `sync.WaitGroup.Go` (Go 1.25 stdlib) — Goroutine group synchronization
