package ui

import (
	"testing"

	"github.com/ray-x/finsight/internal/chart"
)

func TestApplyThemeChangesColors(t *testing.T) {
	ApplyTheme("default")
	defaultGreen := string(colorGreen)
	defaultChartGreen := chart.GreenFg

	ApplyTheme("solarized")
	solarizedGreen := string(colorGreen)
	solarizedChartGreen := chart.GreenFg

	if defaultGreen == solarizedGreen {
		t.Errorf("colorGreen should differ: default=%s, solarized=%s", defaultGreen, solarizedGreen)
	}

	if defaultChartGreen == solarizedChartGreen {
		t.Errorf("chart.GreenFg should differ: default=%q, solarized=%q", defaultChartGreen, solarizedChartGreen)
	}

	t.Logf("default colorGreen:   %s", defaultGreen)
	t.Logf("solarized colorGreen: %s", solarizedGreen)
	t.Logf("default chart green:  %q", defaultChartGreen)
	t.Logf("solarized chart green: %q", solarizedChartGreen)

	// Verify ActiveTheme updated
	if string(ActiveTheme.Green) != "#859900" {
		t.Errorf("ActiveTheme.Green = %s, want #859900", string(ActiveTheme.Green))
	}

	// Restore default
	ApplyTheme("default")
}
