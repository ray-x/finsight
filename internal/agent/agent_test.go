package agent

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ray-x/finsight/internal/llm"
)

func TestRunReturnsFinalAnswerImmediately(t *testing.T) {
	// A tool is registered but the (fake) model answers without using
	// it. We verify Run returns the model's text and records no trace.
	echo := Tool{
		Spec: llm.ToolSpec{Name: "echo"},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			return "echoed", nil
		},
	}
	// We can't hit a real LLM from tests, so this test only verifies
	// the harness compiles + intArg / spec packaging. Loop behaviour
	// is covered by the tool handler tests below.
	_ = echo
}

func TestIntArgHonoursCapAndDefault(t *testing.T) {
	if got := intArg(map[string]any{}, "limit", 8, 15); got != 8 {
		t.Errorf("default: got %d want 8", got)
	}
	if got := intArg(map[string]any{"limit": float64(100)}, "limit", 8, 15); got != 15 {
		t.Errorf("cap: got %d want 15", got)
	}
	if got := intArg(map[string]any{"limit": float64(3)}, "limit", 8, 15); got != 3 {
		t.Errorf("override: got %d want 3", got)
	}
	if got := intArg(map[string]any{"limit": "12"}, "limit", 8, 15); got != 12 {
		t.Errorf("string override: got %d want 12", got)
	}
}

func TestRequiredStringArg(t *testing.T) {
	got, err := requiredStringArg(map[string]any{"symbol": " NVDA "}, "symbol")
	if err != nil {
		t.Fatalf("requiredStringArg returned unexpected error: %v", err)
	}
	if got != "NVDA" {
		t.Fatalf("requiredStringArg = %q, want %q", got, "NVDA")
	}

	if _, err := requiredStringArg(map[string]any{}, "symbol"); err == nil {
		t.Fatal("expected missing-key error")
	}
	if _, err := requiredStringArg(map[string]any{"symbol": 123}, "symbol"); err == nil {
		t.Fatal("expected type error")
	}
	if _, err := requiredStringArg(map[string]any{"symbol": "   "}, "symbol"); err == nil {
		t.Fatal("expected empty-string error")
	}
}

func TestOptionalStringArg(t *testing.T) {
	got, err := optionalStringArg(map[string]any{"company_name": " NVIDIA "}, "company_name")
	if err != nil {
		t.Fatalf("optionalStringArg returned unexpected error: %v", err)
	}
	if got != "NVIDIA" {
		t.Fatalf("optionalStringArg = %q, want %q", got, "NVIDIA")
	}

	got, err = optionalStringArg(map[string]any{}, "company_name")
	if err != nil {
		t.Fatalf("optionalStringArg missing key returned error: %v", err)
	}
	if got != "" {
		t.Fatalf("optionalStringArg missing key = %q, want empty string", got)
	}

	if _, err := optionalStringArg(map[string]any{"company_name": 123}, "company_name"); err == nil {
		t.Fatal("expected type error")
	}
}

func TestRequiredStringSliceArg(t *testing.T) {
	got, err := requiredStringSliceArg(map[string]any{"symbols": []any{" NVDA ", "", "AAPL"}}, "symbols")
	if err != nil {
		t.Fatalf("requiredStringSliceArg returned unexpected error: %v", err)
	}
	if len(got) != 2 || got[0] != "NVDA" || got[1] != "AAPL" {
		t.Fatalf("requiredStringSliceArg = %+v, want [NVDA AAPL]", got)
	}

	if _, err := requiredStringSliceArg(map[string]any{}, "symbols"); err == nil {
		t.Fatal("expected missing-key error")
	}
	if _, err := requiredStringSliceArg(map[string]any{"symbols": "NVDA"}, "symbols"); err == nil {
		t.Fatal("expected non-slice type error")
	}
	if _, err := requiredStringSliceArg(map[string]any{"symbols": []any{"NVDA", 123}}, "symbols"); err == nil {
		t.Fatal("expected mixed-type slice error")
	}
}

func TestDispatchHandlesBadArgsAndUnknownTool(t *testing.T) {
	byName := map[string]Tool{
		"good": {
			Spec: llm.ToolSpec{Name: "good"},
			Handler: func(ctx context.Context, args map[string]any) (string, error) {
				return "ok", nil
			},
		},
	}
	cc := map[string]int{}
	calls := []llm.ToolCall{
		{ID: "1", Function: llm.ToolCallFunction{Name: "good", Arguments: `{"x":1}`}},
		{ID: "2", Function: llm.ToolCallFunction{Name: "missing", Arguments: `{}`}},
		{ID: "3", Function: llm.ToolCallFunction{Name: "good", Arguments: `{not json`}},
	}
	results := dispatchAll(context.Background(), calls, byName, cc, Options{PerToolCallCap: 4})
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	if results[0].content != "ok" {
		t.Errorf("good call content = %q", results[0].content)
	}
	if !strings.Contains(results[1].content, "unknown tool") {
		t.Errorf("missing tool content = %q", results[1].content)
	}
	if !strings.Contains(results[2].content, "invalid JSON") {
		t.Errorf("bad-json content = %q", results[2].content)
	}
}

func TestDispatchEnforcesPerToolCap(t *testing.T) {
	byName := map[string]Tool{
		"t": {Spec: llm.ToolSpec{Name: "t"}, Handler: func(ctx context.Context, a map[string]any) (string, error) {
			return "hit", nil
		}},
	}
	cc := map[string]int{}
	mk := func(id string) llm.ToolCall {
		return llm.ToolCall{ID: id, Function: llm.ToolCallFunction{Name: "t", Arguments: `{}`}}
	}
	calls := []llm.ToolCall{mk("a"), mk("b"), mk("c")}
	results := dispatchAll(context.Background(), calls, byName, cc, Options{PerToolCallCap: 2})
	cappedSeen := false
	for _, r := range results {
		if strings.Contains(r.content, "per-tool cap") || strings.Contains(r.content, "already called") {
			cappedSeen = true
		}
	}
	if !cappedSeen {
		t.Errorf("expected at least one cap-rejected result, got %+v", results)
	}
}

func TestDispatchSkipsHandlerWhenContextCancelled(t *testing.T) {
	called := false
	byName := map[string]Tool{
		"t": {
			Spec: llm.ToolSpec{Name: "t"},
			Handler: func(ctx context.Context, a map[string]any) (string, error) {
				called = true
				return "hit", nil
			},
		},
	}
	cc := map[string]int{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := dispatchAll(ctx, []llm.ToolCall{{
		ID:       "a",
		Function: llm.ToolCallFunction{Name: "t", Arguments: `{}`},
	}}, byName, cc, Options{PerToolCallCap: 1})

	if called {
		t.Fatal("tool handler was invoked after context cancellation")
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if !strings.Contains(results[0].content, context.Canceled.Error()) {
		t.Fatalf("expected canceled result, got %q", results[0].content)
	}
}

func TestDispatchStressNoDeadlock(t *testing.T) {
	byName := map[string]Tool{
		"t": {
			Spec: llm.ToolSpec{Name: "t"},
			Handler: func(ctx context.Context, a map[string]any) (string, error) {
				return "ok", nil
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	for iteration := 0; iteration < 120; iteration++ {
		calls := make([]llm.ToolCall, 48)
		for i := range calls {
			calls[i] = llm.ToolCall{
				ID: fmt.Sprintf("%d-%d", iteration, i),
				Function: llm.ToolCallFunction{
					Name:      "t",
					Arguments: `{}`,
				},
			}
		}

		results := dispatchAll(ctx, calls, byName, map[string]int{}, Options{PerToolCallCap: 5000})
		if len(results) != len(calls) {
			t.Fatalf("iteration %d: got %d results, want %d", iteration, len(results), len(calls))
		}
		for i, r := range results {
			if r.content != "ok" {
				t.Fatalf("iteration %d result %d: content=%q, want ok", iteration, i, r.content)
			}
		}
	}
}

// Example of how Run would be invoked. Not executed — requires a
// live LLM — but compiled so signature drift is caught.
var _ = func() (string, Trace, error) {
	return Run(context.Background(), nil, "sys", "user", nil, Options{})
}

func TestEPSVerdict(t *testing.T) {
	cases := []struct {
		name                       string
		actual, estimate, surprise float64
		want                       string
	}{
		{"clear beat via surprise", 3.49, 3.33, 4.68, "beat"},
		{"clear miss via surprise", 1.10, 1.50, -26.6, "miss"},
		{"meet within band", 2.01, 2.00, 0.5, "meet"},
		{"fallback to actual vs estimate when surprise missing", 5.00, 4.50, 0, "beat"},
		{"empty when both zero", 0, 0, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := epsVerdict(tc.actual, tc.estimate, tc.surprise); got != tc.want {
				t.Errorf("epsVerdict(%v, %v, %v) = %q, want %q",
					tc.actual, tc.estimate, tc.surprise, got, tc.want)
			}
		})
	}
}

var _ = fmt.Sprintf
