package news

import (
	"testing"
	"time"
)

func TestFilterDropsStale(t *testing.T) {
	now := time.Now()
	items := []Item{
		{Title: "NVDA beats earnings", PublishedAt: now},
		{Title: "NVDA old story", PublishedAt: now.Add(-30 * 24 * time.Hour)},
	}
	got := Filter(items, FilterOpts{Symbol: "NVDA"})
	if len(got) != 1 || got[0].Title != "NVDA beats earnings" {
		t.Fatalf("expected only fresh item, got %+v", got)
	}
}

func TestFilterDropsBlocklistedPublisher(t *testing.T) {
	items := []Item{
		{Title: "NVDA rallies", Publisher: "Reuters", PublishedAt: time.Now()},
		{Title: "NVDA rallies again", Publisher: "Simplywall.st", PublishedAt: time.Now()},
	}
	got := Filter(items, FilterOpts{Symbol: "NVDA"})
	if len(got) != 1 || got[0].Publisher != "Reuters" {
		t.Fatalf("expected Reuters only, got %+v", got)
	}
}

func TestFilterRequiresSymbolOrNameInTitle(t *testing.T) {
	items := []Item{
		{Title: "Top 10 AI stocks to buy", PublishedAt: time.Now()},
		{Title: "NVIDIA unveils new GPU", PublishedAt: time.Now()},
		{Title: "Random market wrap", PublishedAt: time.Now()},
	}
	got := Filter(items, FilterOpts{Symbol: "NVDA", CompanyName: "NVIDIA Corporation"})
	if len(got) != 1 || got[0].Title != "NVIDIA unveils new GPU" {
		t.Fatalf("expected only NVIDIA match, got %+v", got)
	}
}

func TestFilterSymbolTokenBoundary(t *testing.T) {
	items := []Item{
		{Title: "AI leader NVDAX announces earnings", PublishedAt: time.Now()},
		{Title: "NVDA up on strong guidance", PublishedAt: time.Now()},
	}
	got := Filter(items, FilterOpts{Symbol: "NVDA"})
	if len(got) != 1 || got[0].Title != "NVDA up on strong guidance" {
		t.Fatalf("expected token-bounded NVDA match only, got %+v", got)
	}
}

func TestFilterDedupesAndCaps(t *testing.T) {
	items := []Item{
		{Title: "NVDA beats", PublishedAt: time.Now()},
		{Title: "nvda beats", PublishedAt: time.Now().Add(-time.Minute)},
		{Title: "NVDA surges", PublishedAt: time.Now().Add(-2 * time.Minute)},
	}
	got := Filter(items, FilterOpts{Symbol: "NVDA", Limit: 5})
	if len(got) != 2 {
		t.Fatalf("expected 2 unique items, got %d: %+v", len(got), got)
	}
}

func TestFilterNoSymbolKeepsAll(t *testing.T) {
	items := []Item{
		{Title: "Market wrap", PublishedAt: time.Now()},
		{Title: "Tech roundup", PublishedAt: time.Now()},
	}
	got := Filter(items, FilterOpts{})
	if len(got) != 2 {
		t.Fatalf("expected passthrough, got %d", len(got))
	}
}

func TestStripCorpSuffix(t *testing.T) {
	cases := map[string]string{
		"NVIDIA Corporation": "NVIDIA",
		"Apple Inc.":         "Apple",
		"Alphabet Inc":       "Alphabet",
		"Plain Name":         "Plain Name",
	}
	for in, want := range cases {
		if got := stripCorpSuffix(in); got != want {
			t.Errorf("stripCorpSuffix(%q) = %q, want %q", in, got, want)
		}
	}
}
