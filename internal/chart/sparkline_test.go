package chart

import (
	"strings"
	"testing"
)

func TestRenderSparklineEmpty(t *testing.T) {
	result := RenderSparkline(nil, 10, 3, "")
	if result != "" {
		t.Errorf("expected empty string for nil data, got %q", result)
	}

	result = RenderSparkline([]float64{}, 10, 3, "")
	if result != "" {
		t.Errorf("expected empty string for empty data, got %q", result)
	}

	result = RenderSparkline([]float64{1, 2, 3}, 0, 3, "")
	if result != "" {
		t.Errorf("expected empty string for zero width, got %q", result)
	}
}

func TestRenderSparklineOutput(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	result := RenderSparkline(data, 10, 3, "")

	if result == "" {
		t.Fatal("expected non-empty sparkline output")
	}

	lines := strings.Split(result, "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines for height=3, got %d", len(lines))
	}

	// All lines should contain braille characters (U+2800 range)
	for i, line := range lines {
		for _, r := range line {
			if r < 0x2800 || r > 0x28FF {
				t.Errorf("line %d: unexpected non-braille char %U", i, r)
			}
		}
	}
}

func TestRenderSparklineFlatData(t *testing.T) {
	data := []float64{5, 5, 5, 5, 5}
	result := RenderSparkline(data, 5, 2, "")
	if result == "" {
		t.Fatal("expected output for flat data")
	}
}

func TestSparklineBarsEmpty(t *testing.T) {
	bars := SparklineBars(nil, 10)
	if bars != nil {
		t.Errorf("expected nil for nil data, got %v", bars)
	}

	bars = SparklineBars([]float64{1, 2}, 0)
	if bars != nil {
		t.Errorf("expected nil for zero width, got %v", bars)
	}
}

func TestSparklineBarsOutput(t *testing.T) {
	data := []float64{1, 3, 2, 5, 4, 6, 3, 7}
	bars := SparklineBars(data, 8)

	if len(bars) != 8 {
		t.Fatalf("expected 8 bars, got %d", len(bars))
	}

	// Verify bar characters are valid sparkline chars
	validChars := map[rune]bool{
		'▁': true, '▂': true, '▃': true, '▄': true,
		'▅': true, '▆': true, '▇': true, '█': true,
	}
	for i, b := range bars {
		if !validChars[b.Char] {
			t.Errorf("bar %d: unexpected char %c", i, b.Char)
		}
	}

	// First bar has no previous, should be Up
	if !bars[0].Up {
		t.Error("first bar should be Up")
	}

	// Bar at index 2 (value 2) follows index 1 (value 3), should be down
	if bars[2].Up {
		t.Error("bar[2] should be down (2 < 3)")
	}
}

func TestSparklineBarsDirection(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5} // strictly increasing
	bars := SparklineBars(data, 5)

	for i := 1; i < len(bars); i++ {
		if !bars[i].Up {
			t.Errorf("bar[%d] should be Up in increasing data", i)
		}
	}

	data = []float64{5, 4, 3, 2, 1} // strictly decreasing
	bars = SparklineBars(data, 5)
	for i := 1; i < len(bars); i++ {
		if bars[i].Up {
			t.Errorf("bar[%d] should be Down in decreasing data", i)
		}
	}
}

func TestResample(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}

	// Same length
	result := resample(data, 10)
	if len(result) != 10 {
		t.Errorf("expected 10 items, got %d", len(result))
	}

	// Downsample
	result = resample(data, 5)
	if len(result) != 5 {
		t.Errorf("expected 5 items, got %d", len(result))
	}

	// Upsample
	result = resample(data, 20)
	if len(result) != 20 {
		t.Errorf("expected 20 items, got %d", len(result))
	}

	// Empty
	result = resample(nil, 5)
	if result != nil {
		t.Errorf("expected nil for nil data, got %v", result)
	}
}

func TestRenderSparklineWithColor(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5}
	result := RenderSparkline(data, 5, 2, "\033[32m")
	if result == "" {
		t.Fatal("expected output with color")
	}
	// Should contain ANSI color codes
	if !strings.Contains(result, "\033[32m") {
		t.Error("expected ANSI color escape in output")
	}
	if !strings.Contains(result, "\033[0m") {
		t.Error("expected ANSI reset escape in output")
	}
}
