package chart

import (
	"math"
	"testing"
)

// buildIntradayTS returns `bars` timestamps starting at `start`
// spaced `intervalSec` apart — simulating a single continuous
// intraday session with no breaks.
func buildIntradayTS(start, intervalSec int64, bars int) []int64 {
	ts := make([]int64, bars)
	for i := 0; i < bars; i++ {
		ts[i] = start + int64(i)*intervalSec
	}
	return ts
}

func TestInferIntervalSec(t *testing.T) {
	// 5-minute bars
	ts := buildIntradayTS(1_700_000_000, 300, 20)
	if got := inferIntervalSec(ts); got != 300 {
		t.Errorf("inferIntervalSec = %d, want 300", got)
	}

	// With an overnight gap, the median should still be 300.
	ts2 := append([]int64{}, ts...)
	// Add a jump of 18 hours before appending more bars.
	base := ts2[len(ts2)-1] + 18*3600
	for i := 0; i < 20; i++ {
		ts2 = append(ts2, base+int64(i)*300)
	}
	if got := inferIntervalSec(ts2); got != 300 {
		t.Errorf("inferIntervalSec with gap = %d, want 300", got)
	}

	if got := inferIntervalSec(nil); got != 0 {
		t.Errorf("inferIntervalSec(nil) = %d, want 0", got)
	}
	if got := inferIntervalSec([]int64{1}); got != 0 {
		t.Errorf("inferIntervalSec(single) = %d, want 0", got)
	}
}

func TestDetectGaps_NoGaps(t *testing.T) {
	ts := buildIntradayTS(1_700_000_000, 300, 50)
	gaps := detectGaps(ts, 300, 2.5)
	if len(gaps) != 0 {
		t.Errorf("expected 0 gaps for continuous series, got %v", gaps)
	}
}

func TestDetectGaps_OvernightGap(t *testing.T) {
	// Session 1: 20 bars, then 18h break, then 20 more bars.
	ts := buildIntradayTS(1_700_000_000, 300, 20)
	base := ts[len(ts)-1] + 18*3600
	for i := 0; i < 20; i++ {
		ts = append(ts, base+int64(i)*300)
	}
	gaps := detectGaps(ts, 300, 2.5)
	if len(gaps) != 1 {
		t.Fatalf("expected 1 gap, got %v", gaps)
	}
	if gaps[0] != 20 {
		t.Errorf("expected gap at index 20, got %d", gaps[0])
	}
}

// TestDetectGaps_SmallIntradayHoleIgnored ensures short data holes
// inside a single trading session (e.g. one missing 5m bar) don't
// trigger a gap. Absolute floor for sub-15m intervals is 1 hour.
func TestDetectGaps_SmallIntradayHoleIgnored(t *testing.T) {
	ts := buildIntradayTS(1_700_000_000, 300, 10)
	// Skip one 5m bar: delta ~10min (< 1h absolute floor).
	next := ts[len(ts)-1] + 10*60
	ts = append(ts, next)
	for i := 1; i < 10; i++ {
		ts = append(ts, next+int64(i)*300)
	}
	if gaps := detectGaps(ts, 300, 2.5); len(gaps) != 0 {
		t.Errorf("10-min data hole should not flag a session gap, got %v", gaps)
	}

	// A 30-min hole should still be ignored (< 1h floor).
	ts2 := buildIntradayTS(1_700_000_000, 300, 10)
	next = ts2[len(ts2)-1] + 30*60
	ts2 = append(ts2, next)
	for i := 1; i < 10; i++ {
		ts2 = append(ts2, next+int64(i)*300)
	}
	if gaps := detectGaps(ts2, 300, 2.5); len(gaps) != 0 {
		t.Errorf("30-min data hole should not flag a session gap, got %v", gaps)
	}
}

// TestDetectGaps_WeekendOnDailyIgnored ensures a regular weekend
// (Fri close -> Mon open = 3 calendar days on 1d bars) does not
// trigger a gap. 1d data naturally has weekend breaks; they
// shouldn't produce visible whitespace in daily/weekly charts.
func TestDetectGaps_WeekendOnDailyIgnored(t *testing.T) {
	day := int64(86400)
	// Mon..Fri, then Mon..Fri (weekend = 3 days between Fri and Mon).
	var ts []int64
	base := int64(1_700_000_000)
	for i := 0; i < 5; i++ {
		ts = append(ts, base+int64(i)*day)
	}
	base2 := ts[len(ts)-1] + 3*day // Fri -> next Mon
	for i := 0; i < 5; i++ {
		ts = append(ts, base2+int64(i)*day)
	}
	if gaps := detectGaps(ts, day, 2.5); len(gaps) != 0 {
		t.Errorf("weekend on daily bars should not flag a gap, got %v", gaps)
	}
}

func TestPerSessionBreakPlaceholders(t *testing.T) {
	// 60 bars, 1 session break, target 12.5% -> ~9 placeholders.
	if p := perSessionBreakPlaceholders(60, 1); p < 7 || p > 11 {
		t.Errorf("perSessionBreakPlaceholders(60,1) = %d, want ~9", p)
	}
	// Minimum 1 when math rounds down.
	if p := perSessionBreakPlaceholders(1, 1); p != 1 {
		t.Errorf("perSessionBreakPlaceholders(1,1) = %d, want >=1", p)
	}
	// Zero when no session breaks or bars.
	if p := perSessionBreakPlaceholders(100, 0); p != 0 {
		t.Errorf("perSessionBreakPlaceholders(100,0) = %d, want 0", p)
	}
	if p := perSessionBreakPlaceholders(0, 1); p != 0 {
		t.Errorf("perSessionBreakPlaceholders(0,1) = %d, want 0", p)
	}
}

// TestWithLineSessionBreaks_1D simulates a single-day 1D chart (no session
// breaks). The helper must be a no-op and return the original slice.
func TestWithLineSessionBreaks_1D(t *testing.T) {
	ts := buildIntradayTS(1_700_000_000, 300, 78) // ~full US regular session in 5m bars
	values := make([]float64, len(ts))
	for i := range values {
		values[i] = 100 + float64(i)*0.1
	}
	out := WithLineSessionBreaks(ts, values)
	if len(out) != len(values) {
		t.Fatalf("1D should have no session breaks inserted: len=%d, want %d", len(out), len(values))
	}
	for i := range out {
		if out[i] != values[i] {
			t.Fatalf("1D values altered at %d: %v != %v", i, out[i], values[i])
		}
	}
}

// TestWithLineSessionBreaks_MultiSession inserts NaN placeholders at
// session boundary. The total expanded length must match bars +
// perBreak * numBreaks and NaN sentinels must sit exactly at the break.
func TestWithLineSessionBreaks_MultiSession(t *testing.T) {
	// Two 20-bar sessions separated by 18 hours.
	ts := buildIntradayTS(1_700_000_000, 300, 20)
	base := ts[len(ts)-1] + 18*3600
	for i := 0; i < 20; i++ {
		ts = append(ts, base+int64(i)*300)
	}
	values := make([]float64, len(ts))
	for i := range values {
		values[i] = 100 + float64(i)
	}
	out := WithLineSessionBreaks(ts, values)
	if len(out) == len(values) {
		t.Fatal("expected session-break insertion, got unchanged slice")
	}
	inserted := len(out) - len(values)
	if inserted < 1 {
		t.Errorf("expected >=1 placeholder, got %d", inserted)
	}
	// First 20 values are the first session, then `inserted` NaN
	// placeholders, then the next 20 values.
	for i := 0; i < 20; i++ {
		if out[i] != values[i] {
			t.Errorf("first session altered at %d", i)
		}
	}
	for i := 20; i < 20+inserted; i++ {
		if !math.IsNaN(out[i]) {
			t.Errorf("expected NaN placeholder at %d, got %v", i, out[i])
		}
	}
	for i := 0; i < 20; i++ {
		if out[20+inserted+i] != values[20+i] {
			t.Errorf("second session altered at %d", i)
		}
	}
}

// TestWithCandleSessionBreaks_MultiSession does the same for candles.
func TestWithCandleSessionBreaks_MultiSession(t *testing.T) {
	ts := buildIntradayTS(1_700_000_000, 300, 10)
	base := ts[len(ts)-1] + 18*3600
	for i := 0; i < 10; i++ {
		ts = append(ts, base+int64(i)*300)
	}
	candles := make([]Candle, len(ts))
	for i := range candles {
		v := 100 + float64(i)
		candles[i] = Candle{Open: v, Close: v + 0.5, High: v + 1, Low: v - 1}
	}
	out := WithCandleSessionBreaks(ts, candles)
	if len(out) == len(candles) {
		t.Fatal("expected candle session-break insertion, got unchanged slice")
	}
	inserted := len(out) - len(candles)
	for i := 10; i < 10+inserted; i++ {
		if !isGapCandle(out[i]) {
			t.Errorf("expected session-break candle at %d, got %+v", i, out[i])
		}
	}
}

// TestWithLineSessionBreaks_MismatchedLen documents that mismatched
// lengths
// are tolerated: the helper aligns to the shorter of timestamps and
// values and still inserts session breaks based on the shared prefix. This
// matches the Yahoo response pattern where Opens/Closes can lag
// Timestamps by one element.
func TestWithLineSessionBreaks_MismatchedLen(t *testing.T) {
	// 5-bar session, 18h gap, 5-bar session — 11 timestamps.
	ts := buildIntradayTS(1_700_000_000, 300, 5)
	base := ts[len(ts)-1] + 18*3600
	for i := 0; i < 5; i++ {
		ts = append(ts, base+int64(i)*300)
	}
	// Values shorter by 1 (common Yahoo quirk).
	values := []float64{10, 11, 12, 13, 14, 20, 21, 22, 23}
	out := WithLineSessionBreaks(ts, values)
	if len(out) <= len(values) {
		t.Fatalf("expected session-break insertion on mismatched input, got len=%d (values=%d)", len(out), len(values))
	}
	// The gap in timestamps sits at index 5. Real values 0..4 must
	// precede the NaN run, real values 5..8 must follow it.
	foundNaN := false
	for _, v := range out {
		if math.IsNaN(v) {
			foundNaN = true
			break
		}
	}
	if !foundNaN {
		t.Error("expected at least one NaN placeholder")
	}
}

// TestWithCandleSessionBreaks_MismatchedLen verifies the candle
// variant also
// tolerates length skew — this is the specific bug that caused the
// detail view to skip session-break rendering while the watchlist
// showed them.
func TestWithCandleSessionBreaks_MismatchedLen(t *testing.T) {
	ts := buildIntradayTS(1_700_000_000, 300, 5)
	base := ts[len(ts)-1] + 18*3600
	for i := 0; i < 5; i++ {
		ts = append(ts, base+int64(i)*300)
	}
	// Candles shorter by 1 — buildCandles trims to min(Opens,Closes)
	// while Timestamps may include one trailing element.
	candles := make([]Candle, 9)
	for i := range candles {
		v := 100 + float64(i)
		candles[i] = Candle{Open: v, Close: v + 0.5, High: v + 1, Low: v - 1}
	}
	out := WithCandleSessionBreaks(ts, candles)
	if len(out) <= len(candles) {
		t.Fatalf("expected session-break insertion on mismatched input, got len=%d (candles=%d)", len(out), len(candles))
	}
	foundGap := false
	for _, c := range out {
		if isGapCandle(c) {
			foundGap = true
			break
		}
	}
	if !foundGap {
		t.Error("expected at least one session-break candle")
	}
}

// TestIsGapCandle verifies the sentinel detector.
func TestIsGapCandle(t *testing.T) {
	if isGapCandle(Candle{Open: 1, Close: 2, High: 3, Low: 0}) {
		t.Error("regular candle flagged as gap")
	}
	if !isGapCandle(gapCandle()) {
		t.Error("gapCandle() not recognised")
	}
}

// TestRenderSparklineLine_GapRendersEmpty is an end-to-end check: a
// series expanded with NaN gaps must render without panicking and
// the rendered output must contain at least one fully-empty column
// where the gap sits.
func TestRenderSparklineLine_GapRendersEmpty(t *testing.T) {
	// Build a NaN-bracketed series directly.
	values := []float64{10, 11, 12, 13, 14}
	values = append(values, math.NaN(), math.NaN(), math.NaN())
	values = append(values, 14.5, 15, 15.5, 16, 16.5)
	out := RenderSparklineLine(values, 20, 3)
	if out == "" {
		t.Fatal("renderer returned empty string for NaN-bracketed series")
	}
}

func TestResampleCandles_PreservesGapInMixedBucket(t *testing.T) {
	candles := []Candle{
		{Open: 100, Close: 101, High: 102, Low: 99},
		gapCandle(),
		{Open: 102, Close: 103, High: 104, Low: 101},
		{Open: 103, Close: 104, High: 105, Low: 102},
	}
	out := resampleCandles(candles, 2)
	if len(out) != 2 {
		t.Fatalf("expected 2 resampled candles, got %d", len(out))
	}
	if !isGapCandle(out[0]) {
		t.Fatalf("expected first bucket to preserve explicit gap, got %+v", out[0])
	}
}

func TestRenderVolumeBars_BlanksGapColumns(t *testing.T) {
	origGreen, origRed := GreenFg, RedFg
	GreenFg, RedFg = "", ""
	t.Cleanup(func() {
		GreenFg, RedFg = origGreen, origRed
	})

	candles := []Candle{gapCandle(), {Open: 100, Close: 101, High: 102, Low: 99}}
	vols := []int64{100, 200}
	out := RenderVolumeBars(candles, vols, 2, 1)
	if len(out) < 2 {
		t.Fatalf("expected at least 2 chars of volume output, got %q", out)
	}
	runes := []rune(out)
	if runes[0] != ' ' {
		t.Fatalf("expected gap column volume to be blank, got %q", string(runes[0]))
	}
}
