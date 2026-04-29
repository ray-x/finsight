// Package agent implements a minimal tool-calling loop around
// internal/llm. The model is given a catalogue of tools and asked to
// answer a user question; it may reply with tool calls that the loop
// dispatches (in parallel, when multiple are returned) and feeds back
// as role:tool messages until the model produces a final answer or the
// step budget is exhausted.
//
// This is a prototype: OpenAI + Copilot providers only, JSON-argument
// validation is minimal, and there is no streaming. It exists so we
// can dogfood agentic retrieval for finsight's AI command window
// without rewriting the macro path.
package agent

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"sync"


	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/logger"
)

// Tool bundles a declaration (what the model sees) with a handler
// (what runs when the model invokes it). Handlers receive already-
// parsed JSON arguments and return a string result that goes straight
// back into the model's context — so handlers should summarise or
// truncate before returning.
type Tool struct {
	Spec    llm.ToolSpec
	Handler func(ctx context.Context, args map[string]any) (string, error)
}

// Step records one turn of the agent loop. Useful for logging and
// surfacing progress in the TUI spinner.
type Step struct {
	ToolName string
	Args     map[string]any
	Result   string
	Err      error
}

// Trace is the complete history of tool calls the loop made before
// arriving at the final answer.
type Trace struct {
	Steps []Step
}

// Options tunes the loop. Zero values are safe defaults.
type Options struct {
	MaxSteps        int // 0 → 6
	OnStep          func(Step)
	PerToolCallCap  int // max invocations of the same tool; 0 → 4
}

// Run executes the agent loop. Returns final assistant text and a
// trace of tool calls. Callers typically build the tool set, then call
// Run with a system prompt that tells the model which tools to prefer.
func Run(
	ctx context.Context,
	client *llm.Client,
	system, user string,
	tools []Tool,
	opts Options,
) (string, Trace, error) {
	if opts.MaxSteps <= 0 {
		opts.MaxSteps = 6
	}
	if opts.PerToolCallCap <= 0 {
		opts.PerToolCallCap = 4
	}

	byName := make(map[string]Tool, len(tools))
	specs := make([]llm.ToolSpec, 0, len(tools))
	for _, t := range tools {
		byName[t.Spec.Name] = t
		specs = append(specs, t.Spec)
	}

	messages := []llm.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user},
	}
	callCount := make(map[string]int)
	var trace Trace

	for step := 0; step < opts.MaxSteps; step++ {
		resp, err := client.ChatWithTools(ctx, messages, specs)
		if err != nil {
			return "", trace, fmt.Errorf("chat step %d: %w", step, err)
		}
		// No tool calls → model is done.
		if len(resp.ToolCalls) == 0 {
			logger.Log("agent: final answer on step %d (%d chars)", step, len(resp.Content))
			return resp.Content, trace, nil
		}
		// Append assistant turn so the model can see its own calls.
		messages = append(messages, resp)
		// Dispatch calls in parallel.
		results := dispatchAll(ctx, resp.ToolCalls, byName, callCount, opts)
		for _, r := range results {
			trace.Steps = append(trace.Steps, r.step)
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
	return "", trace, fmt.Errorf("agent exceeded max steps (%d)", opts.MaxSteps)
}

type dispatchResult struct {
	callID  string
	step    Step
	content string
}

func dispatchAll(
	ctx context.Context,
	calls []llm.ToolCall,
	byName map[string]Tool,
	callCount map[string]int,
	opts Options,
) []dispatchResult {
	out := make([]dispatchResult, len(calls))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i, call := range calls {
		i, call := i, call
		tool, ok := byName[call.Function.Name]
		step := Step{ToolName: call.Function.Name}
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			step.Err = fmt.Errorf("invalid JSON args: %w", err)
			step.Result = fmt.Sprintf("error: %v", step.Err)
			out[i] = dispatchResult{callID: call.ID, step: step, content: step.Result}
			continue
		}
		step.Args = args
		if !ok {
			step.Err = fmt.Errorf("unknown tool")
			step.Result = fmt.Sprintf("error: unknown tool %q", call.Function.Name)
			out[i] = dispatchResult{callID: call.ID, step: step, content: step.Result}
			continue
		}
		mu.Lock()
		callCount[call.Function.Name]++
		over := callCount[call.Function.Name] > opts.PerToolCallCap
		mu.Unlock()
		if over {
			step.Err = fmt.Errorf("per-tool cap reached")
			step.Result = fmt.Sprintf("error: tool %q already called %d times; refusing further calls",
				call.Function.Name, opts.PerToolCallCap)
			out[i] = dispatchResult{callID: call.ID, step: step, content: step.Result}
			continue
		}
		wg.Go(func() {
			if err := ctx.Err(); err != nil {
				s := step
				s.Err = err
				s.Result = fmt.Sprintf("error: %v", err)
				out[i] = dispatchResult{callID: call.ID, step: s, content: s.Result}
				return
			}
			res, err := tool.Handler(ctx, args)
			s := step
			s.Err = err
			if err != nil {
				s.Result = fmt.Sprintf("error: %v", err)
			} else {
				s.Result = res
			}
			out[i] = dispatchResult{callID: call.ID, step: s, content: s.Result}
		})
	}
	wg.Wait()
	return out
}
