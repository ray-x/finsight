package config

import (
	"strings"
	"testing"
)

func TestInvestorProfileEmpty(t *testing.T) {
	var p InvestorProfile
	if got := p.SystemPromptBlock(); got != "" {
		t.Errorf("expected empty string for empty profile, got %q", got)
	}
}

func TestInvestorProfileFull(t *testing.T) {
	p := InvestorProfile{
		Strategies: []string{"buy-and-hold", "dca"},
		Risk:       "balanced",
		Horizon:    "10+ years",
		Goals:      []string{"long-term growth"},
		Notes:      "prefer ETFs",
	}
	out := p.SystemPromptBlock()
	for _, want := range []string{
		"## Investor Profile",
		"buy-and-hold, dca",
		"balanced",
		"10+ years",
		"long-term growth",
		"prefer ETFs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("SystemPromptBlock missing %q in:\n%s", want, out)
		}
	}
}

func TestInvestorProfilePartial(t *testing.T) {
	p := InvestorProfile{Risk: "aggressive"}
	out := p.SystemPromptBlock()
	if !strings.Contains(out, "aggressive") {
		t.Errorf("expected risk in output: %s", out)
	}
	if strings.Contains(out, "Strategies:") {
		t.Errorf("did not expect strategies section: %s", out)
	}
}
