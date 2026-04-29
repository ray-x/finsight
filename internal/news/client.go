// Package news provides recent company news headlines used by the AI
// command window to explain short-term price action. The Provider
// interface keeps room for future sources (Finnhub, AlphaVantage,
// Marketaux) while the default implementation hits Yahoo Finance's
// free search endpoint so no API key is required out of the box.
package news

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"time"
)

const (
	yahooSearchURL = "https://query1.finance.yahoo.com/v1/finance/search"
	userAgent      = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

// Item is a single news headline tied to a ticker.
type Item struct {
	Title       string    `json:"title"`
	Publisher   string    `json:"publisher"`
	Link        string    `json:"link"`
	PublishedAt time.Time `json:"published_at"`
	UUID        string    `json:"uuid,omitempty"`
	Type        string    `json:"type,omitempty"`
}

// Provider fetches recent news for a symbol. Implementations should be
// safe for concurrent use.
type Provider interface {
	// GetCompanyNews returns headlines for the symbol, newest first.
	// `since` is the earliest acceptable PublishedAt; items older than
	// `since` are filtered out. `limit` caps the returned slice (0 =
	// provider default).
	GetCompanyNews(symbol string, since time.Time, limit int) ([]Item, error)
	Name() string
}

// YahooProvider is the zero-config default. It uses Yahoo Finance's
// public search endpoint.
type YahooProvider struct {
	HTTP *http.Client
}

// NewYahoo returns a Yahoo-backed provider with a sensible default
// HTTP client (15s timeout).
func NewYahoo() *YahooProvider {
	return &YahooProvider{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Name implements Provider.
func (y *YahooProvider) Name() string { return "yahoo" }

// GetCompanyNews implements Provider using Yahoo's search endpoint.
func (y *YahooProvider) GetCompanyNews(symbol string, since time.Time, limit int) ([]Item, error) {
	if limit <= 0 {
		limit = 15
	}
	u := fmt.Sprintf("%s?q=%s&quotesCount=0&newsCount=%d",
		yahooSearchURL, url.QueryEscape(symbol), limit)

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	client := y.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("yahoo news: status %d", resp.StatusCode)
	}

	var parsed struct {
		News []struct {
			UUID                string `json:"uuid"`
			Title               string `json:"title"`
			Publisher           string `json:"publisher"`
			Link                string `json:"link"`
			ProviderPublishTime int64  `json:"providerPublishTime"`
			Type                string `json:"type"`
		} `json:"news"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("yahoo news: parse: %w", err)
	}

	items := make([]Item, 0, len(parsed.News))
	for _, n := range parsed.News {
		if n.Title == "" {
			continue
		}
		ts := time.Unix(n.ProviderPublishTime, 0)
		if !since.IsZero() && ts.Before(since) {
			continue
		}
		items = append(items, Item{
			Title:       n.Title,
			Publisher:   n.Publisher,
			Link:        n.Link,
			PublishedAt: ts,
			UUID:        n.UUID,
			Type:        n.Type,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
