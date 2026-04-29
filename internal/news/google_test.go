package news

import (
	"testing"
	"time"
)

func TestSplitGoogleTitle(t *testing.T) {
	cases := []struct {
		in, title, pub string
	}{
		{"NVDA beats earnings - Reuters", "NVDA beats earnings", "Reuters"},
		{"Apple - AI strategy detailed - Bloomberg", "Apple - AI strategy detailed", "Bloomberg"},
		{"No publisher here", "No publisher here", ""},
	}
	for _, c := range cases {
		gotT, gotP := splitGoogleTitle(c.in)
		if gotT != c.title || gotP != c.pub {
			t.Errorf("splitGoogleTitle(%q) = (%q, %q), want (%q, %q)", c.in, gotT, gotP, c.title, c.pub)
		}
	}
}

func TestParseRSSTime(t *testing.T) {
	cases := []string{
		"Wed, 02 Oct 2024 13:45:00 +0000",
		"Wed, 02 Oct 2024 13:45:00 GMT",
		"",
	}
	for _, c := range cases {
		if _, err := parseRSSTime(c); err != nil {
			t.Errorf("parseRSSTime(%q) error: %v", c, err)
		}
	}
}

type stubProvider struct {
	name  string
	items []Item
	err   error
}

func (s stubProvider) Name() string { return s.name }
func (s stubProvider) GetCompanyNews(symbol string, since time.Time, limit int) ([]Item, error) {
	return s.items, s.err
}

func TestMultiProviderMergesAndDedupes(t *testing.T) {
	now := time.Now()
	y := stubProvider{name: "yahoo", items: []Item{
		{Title: "A", Link: "https://x.com/a", PublishedAt: now},
		{Title: "B", Link: "https://x.com/b", PublishedAt: now.Add(-time.Hour)},
	}}
	g := stubProvider{name: "google", items: []Item{
		{Title: "A", Link: "https://x.com/a", PublishedAt: now},             // dup by link
		{Title: "C", Link: "https://news.google.com/c", PublishedAt: now.Add(-30 * time.Minute)},
	}}
	m := NewMulti(y, g)
	items, err := m.GetCompanyNews("X", time.Time{}, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 merged items, got %d: %+v", len(items), items)
	}
	// Newest first.
	if items[0].Title != "A" {
		t.Errorf("expected A first, got %s", items[0].Title)
	}
}

func TestMultiProviderToleratesOneError(t *testing.T) {
	y := stubProvider{name: "yahoo", items: []Item{{Title: "A", Link: "a"}}}
	g := stubProvider{name: "google", err: errBoom}
	m := NewMulti(y, g)
	items, err := m.GetCompanyNews("X", time.Time{}, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}

func TestMultiProviderAllFailed(t *testing.T) {
	m := NewMulti(stubProvider{name: "a", err: errBoom}, stubProvider{name: "b", err: errBoom})
	if _, err := m.GetCompanyNews("X", time.Time{}, 0); err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

type stringErr string

func (s stringErr) Error() string { return string(s) }

var errBoom = stringErr("boom")
