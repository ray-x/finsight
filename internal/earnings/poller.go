package earnings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/ray-x/finsight/internal/db"
)

const defaultUserAgent = "finsight-ir-poller/1.0 (+https://github.com/ray-x/finsight)"

var errNotFeed = errors.New("not rss/atom feed")

// Poller fetches IR feeds with conditional headers and stores normalized events.
type Poller struct {
	DB       *db.DB
	Registry *Registry
	HTTP     *http.Client
	MaxItems int
}

// NewPoller creates a poller with sane defaults.
func NewPoller(database *db.DB, registry *Registry) *Poller {
	return &Poller{
		DB:       database,
		Registry: registry,
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		MaxItems: 20,
	}
}

// PollSymbol fetches one symbol's IR feed and stores newly discovered events.
func (p *Poller) PollSymbol(ctx context.Context, symbol string) (int, error) {
	if p == nil || p.DB == nil || p.Registry == nil {
		return 0, nil
	}
	feedURL, ok := p.Registry.FeedURL(symbol)
	if !ok || feedURL == "" {
		return 0, nil
	}

	state, err := p.DB.GetIRFeedState(ctx, feedURL)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feedURL, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, */*;q=0.8")
	if state != nil {
		if state.ETag != "" {
			req.Header.Set("If-None-Match", state.ETag)
		}
		if state.LastModified != "" {
			req.Header.Set("If-Modified-Since", state.LastModified)
		}
	}

	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		_ = p.DB.UpsertIRFeedState(ctx, &db.IRFeedState{
			FeedURL:       feedURL,
			LastCheckedAt: time.Now().UTC(),
			LastStatus:    0,
			LastError:     err.Error(),
		})
		return 0, err
	}
	defer resp.Body.Close()

	now := time.Now().UTC()
	if resp.StatusCode == http.StatusNotModified {
		_ = p.DB.UpsertIRFeedState(ctx, &db.IRFeedState{
			FeedURL:       feedURL,
			ETag:          headerOr(stateValue(state, func(s *db.IRFeedState) string { return s.ETag }), resp.Header.Get("ETag")),
			LastModified:  headerOr(stateValue(state, func(s *db.IRFeedState) string { return s.LastModified }), resp.Header.Get("Last-Modified")),
			LastCheckedAt: now,
			LastStatus:    resp.StatusCode,
		})
		return 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("ir poller: status %d", resp.StatusCode)
		_ = p.DB.UpsertIRFeedState(ctx, &db.IRFeedState{
			FeedURL:       feedURL,
			LastCheckedAt: now,
			LastStatus:    resp.StatusCode,
			LastError:     err.Error(),
		})
		return 0, err
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, err
	}
	if !looksLikeFeed(resp.Header.Get("Content-Type"), body) {
		_ = p.DB.UpsertIRFeedState(ctx, &db.IRFeedState{
			FeedURL:       feedURL,
			ETag:          resp.Header.Get("ETag"),
			LastModified:  resp.Header.Get("Last-Modified"),
			LastCheckedAt: now,
			LastStatus:    resp.StatusCode,
			LastError:     errNotFeed.Error(),
		})
		return 0, nil
	}
	items, err := parseFeedItems(body)
	if err != nil {
		_ = p.DB.UpsertIRFeedState(ctx, &db.IRFeedState{
			FeedURL:       feedURL,
			ETag:          resp.Header.Get("ETag"),
			LastModified:  resp.Header.Get("Last-Modified"),
			LastCheckedAt: now,
			LastStatus:    resp.StatusCode,
			LastError:     err.Error(),
		})
		return 0, err
	}

	maxItems := p.MaxItems
	if maxItems <= 0 {
		maxItems = 20
	}
	if len(items) > maxItems {
		items = items[:maxItems]
	}

	newCount := 0
	for _, item := range items {
		if item.Link == "" {
			continue
		}
		fingerprint := eventFingerprint(symbol, item.Title, item.Link, item.PublishedAt)
		meta := map[string]any{
			"feed_url": feedURL,
			"guid":     item.GUID,
			"author":   item.Author,
		}
		metaJSON, _ := json.Marshal(meta)

		ev := &db.EarningsEvent{
			Symbol:       strings.ToUpper(symbol),
			SourceType:   "company_ir_release",
			SourceName:   item.SourceName,
			Title:        item.Title,
			URL:          item.Link,
			ReleasedAt:   item.PublishedAt,
			DetectedAt:   now,
			IngestedAt:   now,
			Fingerprint:  fingerprint,
			Confidence:   "high",
			Status:       "new",
			MetadataJSON: string(metaJSON),
			Content:      item.Summary,
		}
		inserted, err := p.DB.InsertEarningsEventIfNew(ctx, ev)
		if err != nil {
			return newCount, err
		}
		if inserted {
			newCount++
		}
	}

	_ = p.DB.UpsertIRFeedState(ctx, &db.IRFeedState{
		FeedURL:       feedURL,
		ETag:          resp.Header.Get("ETag"),
		LastModified:  resp.Header.Get("Last-Modified"),
		LastCheckedAt: now,
		LastStatus:    resp.StatusCode,
	})
	return newCount, nil
}

type feedItem struct {
	Title       string
	Link        string
	PublishedAt time.Time
	Summary     string
	GUID        string
	Author      string
	SourceName  string
}

func parseFeedItems(body []byte) ([]feedItem, error) {
	items, err := parseRSSItems(body)
	if err == nil && len(items) > 0 {
		return items, nil
	}
	atomItems, atomErr := parseAtomItems(body)
	if atomErr == nil {
		return atomItems, nil
	}
	if err != nil {
		return nil, err
	}
	return nil, atomErr
}

func parseRSSItems(body []byte) ([]feedItem, error) {
	var rss struct {
		Channel struct {
			Title string `xml:"title"`
			Items []struct {
				Title       string `xml:"title"`
				Link        string `xml:"link"`
				PubDate     string `xml:"pubDate"`
				Description string `xml:"description"`
				GUID        string `xml:"guid"`
				Author      string `xml:"author"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &rss); err != nil {
		return nil, err
	}
	out := make([]feedItem, 0, len(rss.Channel.Items))
	for _, it := range rss.Channel.Items {
		t := parseFeedTime(it.PubDate)
		out = append(out, feedItem{
			Title:       strings.TrimSpace(it.Title),
			Link:        normalizeURL(it.Link),
			PublishedAt: t,
			Summary:     strings.TrimSpace(it.Description),
			GUID:        strings.TrimSpace(it.GUID),
			Author:      strings.TrimSpace(it.Author),
			SourceName:  strings.TrimSpace(rss.Channel.Title),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishedAt.After(out[j].PublishedAt) })
	return out, nil
}

func parseAtomItems(body []byte) ([]feedItem, error) {
	var atom struct {
		Title   string `xml:"title"`
		Entries []struct {
			Title     string `xml:"title"`
			Updated   string `xml:"updated"`
			Published string `xml:"published"`
			Summary   string `xml:"summary"`
			Content   string `xml:"content"`
			ID        string `xml:"id"`
			Author    struct {
				Name string `xml:"name"`
			} `xml:"author"`
			Links []struct {
				Rel  string `xml:"rel,attr"`
				Href string `xml:"href,attr"`
			} `xml:"link"`
		} `xml:"entry"`
	}
	if err := xml.Unmarshal(body, &atom); err != nil {
		return nil, err
	}
	out := make([]feedItem, 0, len(atom.Entries))
	for _, e := range atom.Entries {
		link := ""
		for _, l := range e.Links {
			if l.Rel == "" || l.Rel == "alternate" {
				link = l.Href
				break
			}
		}
		ts := e.Published
		if ts == "" {
			ts = e.Updated
		}
		summary := strings.TrimSpace(e.Summary)
		if summary == "" {
			summary = strings.TrimSpace(e.Content)
		}
		out = append(out, feedItem{
			Title:       strings.TrimSpace(e.Title),
			Link:        normalizeURL(link),
			PublishedAt: parseFeedTime(ts),
			Summary:     summary,
			GUID:        strings.TrimSpace(e.ID),
			Author:      strings.TrimSpace(e.Author.Name),
			SourceName:  strings.TrimSpace(atom.Title),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishedAt.After(out[j].PublishedAt) })
	return out, nil
}

func parseFeedTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339,
		time.RFC3339Nano,
		time.RFC1123Z,
		time.RFC1123,
		time.RFC822Z,
		time.RFC822,
		"2006-01-02 15:04:05 MST",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC()
		}
	}
	return time.Time{}
}

func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	return u.String()
}

func eventFingerprint(symbol, title, link string, releasedAt time.Time) string {
	stamp := ""
	if !releasedAt.IsZero() {
		stamp = releasedAt.UTC().Format(time.RFC3339)
	}
	payload := strings.ToUpper(strings.TrimSpace(symbol)) + "|" + strings.TrimSpace(title) + "|" + normalizeURL(link) + "|" + stamp
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:16])
}

func stateValue(state *db.IRFeedState, f func(*db.IRFeedState) string) string {
	if state == nil {
		return ""
	}
	return f(state)
}

func headerOr(a, b string) string {
	if strings.TrimSpace(b) != "" {
		return b
	}
	return a
}

func looksLikeFeed(contentType string, body []byte) bool {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if strings.Contains(ct, "rss") || strings.Contains(ct, "atom") || strings.Contains(ct, "xml") {
		return true
	}
	probe := strings.ToLower(strings.TrimSpace(string(body)))
	if strings.HasPrefix(probe, "<?xml") || strings.Contains(probe, "<rss") || strings.Contains(probe, "<feed") {
		return true
	}
	return false
}
