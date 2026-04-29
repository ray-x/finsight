package ui

import (
	"math"
	"testing"

	"github.com/ray-x/finsight/internal/yahoo"
)

// TestComputeTechnicalsWithContext verifies that prepending a daily
// context gives short intraday primary series enough warmup for the
// long MA / MACD lines to produce non-NaN readings, while outputs
// still align 1:1 with the primary series.
func TestComputeTechnicalsWithContext(t *testing.T) {
	// Context: 250 synthetic daily closes.
	ctx := &yahoo.ChartData{
		Timestamps: make([]int64, 250),
		Closes:     make([]float64, 250),
		Highs:      make([]float64, 250),
		Lows:       make([]float64, 250),
	}
	for i := 0; i < 250; i++ {
		ctx.Timestamps[i] = int64(i * 86400) // 1 day apart starting from epoch
		v := 100 + math.Sin(float64(i)/10)*5
		ctx.Closes[i] = v
		ctx.Highs[i] = v + 1
		ctx.Lows[i] = v - 1
	}

	// Primary: 10 intraday bars starting right after the last context bar.
	primary := &yahoo.ChartData{
		Timestamps: make([]int64, 10),
		Closes:     make([]float64, 10),
		Highs:      make([]float64, 10),
		Lows:       make([]float64, 10),
	}
	base := int64(250 * 86400)
	for i := 0; i < 10; i++ {
		primary.Timestamps[i] = base + int64(i*300) // 5-minute bars
		primary.Closes[i] = 110 + float64(i)*0.1
		primary.Highs[i] = primary.Closes[i] + 0.5
		primary.Lows[i] = primary.Closes[i] - 0.5
	}

	// Without context: 10 bars is far too few for EMA200 or even EMA26.
	bare := ComputeTechnicals(primary)
	if !math.IsNaN(bare.EMA26[len(bare.EMA26)-1]) {
		t.Fatalf("expected bare EMA26 to be NaN with 10 bars, got %v", bare.EMA26[len(bare.EMA26)-1])
	}

	// With context: all long-window indicators should have warmed up by
	// the final primary bar.
	warm := ComputeTechnicalsWithContext(primary, ctx)
	if len(warm.EMA26) != len(primary.Closes) {
		t.Fatalf("output length mismatch: got %d want %d", len(warm.EMA26), len(primary.Closes))
	}
	if math.IsNaN(warm.EMA26[len(warm.EMA26)-1]) {
		t.Fatal("EMA26 should be warmed up with 250-bar context prefix")
	}
	if math.IsNaN(warm.SMA200[len(warm.SMA200)-1]) {
		t.Fatal("SMA200 should be warmed up with 250-bar context prefix")
	}
	if math.IsNaN(warm.MACDSignal[len(warm.MACDSignal)-1]) {
		t.Fatal("MACD signal should be warmed up with 250-bar context prefix")
	}
	// Every entry must map to a primary close.
	if len(warm.Closes) != len(primary.Closes) {
		t.Fatalf("Closes slice length: got %d want %d", len(warm.Closes), len(primary.Closes))
	}
}

// TestComputeTechnicalsContextOverlapIgnored verifies that a context
// which overlaps the primary window is ignored (no duplicate bars).
func TestComputeTechnicalsContextOverlapIgnored(t *testing.T) {
	primary := &yahoo.ChartData{
		Timestamps: []int64{100, 200, 300},
		Closes:     []float64{10, 11, 12},
	}
	// Context fully overlaps primary → no bars are strictly older.
	ctx := &yahoo.ChartData{
		Timestamps: []int64{150, 250},
		Closes:     []float64{10.5, 11.5},
	}
	out := ComputeTechnicalsWithContext(primary, ctx)
	if len(out.SMA20) != 3 {
		t.Fatalf("output length = %d want 3", len(out.SMA20))
	}
}
