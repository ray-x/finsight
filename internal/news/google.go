package news

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

)

// googleNewsRSS is the public Google News RSS search endpoint. It
// requires no API key and returns up to ~100 items per query in RSS
// 2.0 format. See:
//
//	https://news.google.com/rss/search?q=<query>&hl=en-US&gl=US&ceid=US:en
//
// We query `"<SYMBOL>" stock OR shares` to bias toward equity-related
// coverage rather than unrelated uses of the ticker symbol (e.g. "AI"
// the band vs the sector).
const googleNewsRSS = "https://news.google.com/rss/search"

// GoogleProvider fetches news from Google News' public RSS feed.
type GoogleProvider struct {
	HTTP *http.Client
	// Hl / Gl / Ceid control language and locale. Defaults are
	// en-US / US / US:en if left blank.
	Hl, Gl, Ceid string
}

// NewGoogle returns a Google News RSS-backed provider.
func NewGoogle() *GoogleProvider {
	return &GoogleProvider{HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Name implements Provider.
func (g *GoogleProvider) Name() string { return "google" }

type googleRSS struct {
	Channel struct {
		Items []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			PubDate     string `xml:"pubDate"`
			Description string `xml:"description"`
			Source      struct {
				Value string `xml:",chardata"`
				URL   string `xml:"url,attr"`
			} `xml:"source"`
			GUID string `xml:"guid"`
		} `xml:"item"`
	} `xml:"channel"`
}

// GetCompanyNews implements Provider using Google News RSS search.
func (g *GoogleProvider) GetCompanyNews(symbol string, since time.Time, limit int) ([]Item, error) {
	if limit <= 0 {
		limit = 15
	}
	hl := g.Hl
	if hl == "" {
		hl = "en-US"
	}
	gl := g.Gl
	if gl == "" {
		gl = "US"
	}
	ceid := g.Ceid
	if ceid == "" {
		ceid = "US:en"
	}

	q := fmt.Sprintf(`"%s" stock OR shares`, strings.ToUpper(symbol))
	u := fmt.Sprintf("%s?q=%s&hl=%s&gl=%s&ceid=%s",
		googleNewsRSS, url.QueryEscape(q),
		url.QueryEscape(hl), url.QueryEscape(gl), url.QueryEscape(ceid))

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/xml;q=0.9, */*;q=0.8")

	client := g.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google news: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var feed googleRSS
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, fmt.Errorf("google news: parse: %w", err)
	}

	items := make([]Item, 0, len(feed.Channel.Items))
	for _, it := range feed.Channel.Items {
		if it.Title == "" {
			continue
		}
		// Google News titles are "Headline - Publisher". Split so we
		// can normalise the shape to Yahoo's output.
		title, pub := splitGoogleTitle(it.Title)
		if pub == "" {
			pub = it.Source.Value
		}
		ts, _ := parseRSSTime(it.PubDate)
		if !since.IsZero() && !ts.IsZero() && ts.Before(since) {
			continue
		}
		items = append(items, Item{
			Title:       title,
			Publisher:   pub,
			Link:        it.Link,
			PublishedAt: ts,
			UUID:        it.GUID,
			Type:        "news",
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

// splitGoogleTitle splits a Google News title of the form
// "Headline text - Publisher" into (headline, publisher). The
// publisher suffix uses " - " (hyphen with spaces) consistently; we
// split on the last occurrence so hyphens inside the headline survive.
func splitGoogleTitle(raw string) (title, publisher string) {
	idx := strings.LastIndex(raw, " - ")
	if idx < 0 {
		return strings.TrimSpace(raw), ""
	}
	return strings.TrimSpace(raw[:idx]), strings.TrimSpace(raw[idx+3:])
}

// parseRSSTime accepts the RFC1123Z and RFC1123 layouts used in RSS
// pubDate fields.
func parseRSSTime(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC822Z, time.RFC822} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unknown pubDate format: %q", s)
}

// MultiProvider fans a single GetCompanyNews call out to several
// providers in parallel, merges and de-duplicates the results by link
// (falling back to title), and returns the combined set newest-first.
type MultiProvider struct {
	Providers []Provider
}

// NewMulti returns a MultiProvider that queries all given providers.
func NewMulti(providers ...Provider) *MultiProvider {
	return &MultiProvider{Providers: providers}
}

// Name implements Provider.
func (m *MultiProvider) Name() string {
	names := make([]string, 0, len(m.Providers))
	for _, p := range m.Providers {
		names = append(names, p.Name())
	}
	return "multi[" + strings.Join(names, ",") + "]"
}

// GetCompanyNews implements Provider. Providers run concurrently; a
// single provider error is tolerated as long as at least one succeeds.
func (m *MultiProvider) GetCompanyNews(symbol string, since time.Time, limit int) ([]Item, error) {
	if len(m.Providers) == 0 {
		return nil, fmt.Errorf("news: no providers configured")
	}

	type providerResult struct {
		items []Item
		err   error
		name  string
	}

	results := make([]providerResult, len(m.Providers))
	var wg sync.WaitGroup
	var mu sync.Mutex

	for i, p := range m.Providers {
		i, p := i, p
		wg.Go(func() {
			items, err := p.GetCompanyNews(symbol, since, limit)
			mu.Lock()
			results[i] = providerResult{items, err, p.Name()}
			mu.Unlock()
		})
	}
	wg.Wait()

	var merged []Item
	seen := map[string]bool{}
	var lastErr error
	ok := 0
	for _, r := range results {
		if r.err != nil {
			lastErr = fmt.Errorf("%s: %w", r.name, r.err)
			continue
		}
		ok++
		for _, it := range r.items {
			key := dedupKey(it)
			if seen[key] {
				continue
			}
			seen[key] = true
			merged = append(merged, it)
		}
	}
	if ok == 0 {
		return nil, lastErr
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].PublishedAt.After(merged[j].PublishedAt)
	})
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged, nil
}

var wsRe = regexp.MustCompile(`\s+`)

// dedupKey normalises a headline for cross-provider de-duplication.
// Identical stories from Yahoo and Google often share the same URL,
// but when they differ (Google serves a news.google.com shim URL) we
// fall back to a whitespace-collapsed lowercase title.
func dedupKey(it Item) string {
	if it.Link != "" {
		return "l:" + it.Link
	}
	t := strings.ToLower(strings.TrimSpace(it.Title))
	t = wsRe.ReplaceAllString(t, " ")
	return "t:" + t
}
