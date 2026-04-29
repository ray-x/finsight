package earnings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ray-x/finsight/internal/db"
)

func TestPollerRSSInsertAndConditionalFetch(t *testing.T) {
	etag := "\"v1\""
	lastModified := time.Now().UTC().Format(http.TimeFormat)
	rss := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Example IR</title>
    <item>
      <title>Q1 Results Released</title>
      <link>https://example.com/earnings/q1</link>
      <pubDate>Mon, 20 Apr 2026 20:00:00 GMT</pubDate>
      <description>Revenue and EPS details</description>
      <guid>q1-2026</guid>
    </item>
  </channel>
</rss>`

	hit304 := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			hit304 = true
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Last-Modified", lastModified)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rss))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	database, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	registry := NewRegistry(map[string]string{"AAPL": srv.URL})
	poller := NewPoller(database, registry)

	ctx := context.Background()
	inserted, err := poller.PollSymbol(ctx, "AAPL")
	if err != nil {
		t.Fatalf("poll first: %v", err)
	}
	if inserted != 1 {
		t.Fatalf("expected 1 new event, got %d", inserted)
	}

	events, err := database.GetRecentEarningsEvents(ctx, "AAPL", 10)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 stored event, got %d", len(events))
	}

	inserted, err = poller.PollSymbol(ctx, "AAPL")
	if err != nil {
		t.Fatalf("poll second: %v", err)
	}
	if inserted != 0 {
		t.Fatalf("expected no new event on conditional fetch, got %d", inserted)
	}
	if !hit304 {
		t.Fatal("expected conditional request to trigger 304")
	}
}

func TestRegistryLookup(t *testing.T) {
	r := NewRegistry(map[string]string{" nvda ": " https://example.com/rss "})
	u, ok := r.FeedURL("NVDA")
	if !ok {
		t.Fatal("expected registry hit")
	}
	if u != "https://example.com/rss" {
		t.Fatalf("unexpected url: %q", u)
	}
}

func TestPollerSkipsNonFeedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!doctype html><html><body><a href="/earnings.pdf">earnings</a></body></html>`))
	}))
	defer srv.Close()

	tmp := t.TempDir()
	database, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer database.Close()

	registry := NewRegistry(map[string]string{"AAPL": srv.URL})
	poller := NewPoller(database, registry)

	inserted, err := poller.PollSymbol(context.Background(), "AAPL")
	if err != nil {
		t.Fatalf("poll should not fail for non-feed page: %v", err)
	}
	if inserted != 0 {
		t.Fatalf("expected 0 inserted events, got %d", inserted)
	}

	events, err := database.GetRecentEarningsEvents(context.Background(), "AAPL", 10)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("expected no stored events, got %d", len(events))
	}
}
