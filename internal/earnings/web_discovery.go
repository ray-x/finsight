package earnings

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

var googleNewsRSSURL = "https://news.google.com/rss/search"
var ddgSearchURL = "https://html.duckduckgo.com/html/"

var anchorRe = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
var stripTagsRe = regexp.MustCompile(`(?is)<[^>]+>`)
var markdownLinkRe = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)\s]+)\)`)

// ddgResultRe extracts result links from DuckDuckGo HTML output.
var ddgResultRe = regexp.MustCompile(`(?is)class=["']result__a["'][^>]+href=["']([^"']+)["']`)

// DiscoveryOptions controls web-based earnings link discovery.
type DiscoveryOptions struct {
	PageURL       string
	SearchHint    string
	CompanyName   string // optional; improves web search query quality
	MaxCandidates int
	HTTP          *http.Client
	CrawlerCmd    string   // optional external crawler command (e.g. crawl4ai)
	CrawlerArgs   []string // args; use {url} placeholder, or URL is appended when missing
}

// LinkCandidate is a potentially relevant earnings report page/PDF.
type LinkCandidate struct {
	URL    string
	Title  string
	Source string // ir_page | news_search
	Kind   string // pdf | page
	Score  int
}

// DiscoveryResult aggregates web hints that can be fed into LLM analysis.
type DiscoveryResult struct {
	Symbol     string
	PageURL    string
	Candidates []LinkCandidate
}

// PromptBlock renders a concise markdown block suitable for LLM prompt context.
func (r *DiscoveryResult) PromptBlock() string {
	if r == nil || len(r.Candidates) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Web Discovery Hints\n")
	if r.PageURL != "" {
		b.WriteString("Investor page: ")
		b.WriteString(r.PageURL)
		b.WriteString("\n")
	}
	b.WriteString("Potential release/report links (verify date and ticker before use):\n")
	for _, c := range r.Candidates {
		line := "- [page] "
		if c.Kind == "pdf" {
			line = "- [pdf] "
		}
		b.WriteString(line)
		if c.Title != "" {
			b.WriteString(c.Title)
			b.WriteString(" -> ")
		}
		b.WriteString(c.URL)
		b.WriteString("\n")
	}
	return b.String()
}

// DiscoverEarningsWebLinks gathers candidate earnings/report links from
// configured IR pages and an optional Google News RSS search hint.
func DiscoverEarningsWebLinks(ctx context.Context, symbol string, opt DiscoveryOptions) (*DiscoveryResult, error) {
	maxCandidates := opt.MaxCandidates
	if maxCandidates <= 0 {
		maxCandidates = 6
	}
	client := opt.HTTP
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}

	out := &DiscoveryResult{Symbol: strings.ToUpper(strings.TrimSpace(symbol)), PageURL: strings.TrimSpace(opt.PageURL)}
	seen := map[string]int{}

	if out.PageURL != "" {
		links, err := discoverFromPage(ctx, client, out.PageURL, opt.CrawlerCmd, opt.CrawlerArgs)
		if err == nil {
			for _, c := range links {
				mergeCandidate(out, seen, c)
			}
		}
	}

	if hint := strings.TrimSpace(opt.SearchHint); hint != "" {
		// DuckDuckGo web search: finds the actual earnings page/PDF URL
		if webLinks, err := discoverFromWebSearch(ctx, client, out.Symbol, opt.CompanyName, hint); err == nil {
			for _, c := range webLinks {
				mergeCandidate(out, seen, c)
			}
			// Crawl the top-scored web search result page for deeper link extraction
			if len(webLinks) > 0 {
				if crawled, err := discoverFromPage(ctx, client, webLinks[0].URL, opt.CrawlerCmd, opt.CrawlerArgs); err == nil {
					for _, c := range crawled {
						mergeCandidate(out, seen, c)
					}
				}
			}
		}
		// Google News RSS: finds recent press-release articles
		if newsLinks, err := discoverFromGoogleNewsRSS(ctx, client, hint); err == nil {
			for _, c := range newsLinks {
				mergeCandidate(out, seen, c)
			}
		}
	}

	sort.Slice(out.Candidates, func(i, j int) bool {
		if out.Candidates[i].Score == out.Candidates[j].Score {
			return out.Candidates[i].URL < out.Candidates[j].URL
		}
		return out.Candidates[i].Score > out.Candidates[j].Score
	})
	if len(out.Candidates) > maxCandidates {
		out.Candidates = out.Candidates[:maxCandidates]
	}
	return out, nil
}

func discoverFromPage(ctx context.Context, client *http.Client, pageURL, crawlerCmd string, crawlerArgs []string) ([]LinkCandidate, error) {
	if strings.TrimSpace(crawlerCmd) != "" {
		if links, err := discoverFromExternalCrawler(ctx, pageURL, crawlerCmd, crawlerArgs); err == nil && len(links) > 0 {
			return links, nil
		}
	}
	return discoverFromPageHTTP(ctx, client, pageURL)
}

func discoverFromPageHTTP(ctx context.Context, client *http.Client, pageURL string) ([]LinkCandidate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ir page status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}
	base, _ := url.Parse(pageURL)

	anchors := anchorRe.FindAllStringSubmatch(string(body), -1)
	out := make([]LinkCandidate, 0, len(anchors))
	for _, a := range anchors {
		if len(a) < 3 {
			continue
		}
		href := strings.TrimSpace(a[1])
		if href == "" {
			continue
		}
		u, err := url.Parse(href)
		if err != nil {
			continue
		}
		if base != nil {
			u = base.ResolveReference(u)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			continue
		}
		u.Fragment = ""
		title := cleanAnchorText(a[2])
		kind, score := scoreLink(title, u.String(), hostOrEmpty(base))
		if score <= 0 {
			continue
		}
		out = append(out, LinkCandidate{
			URL:    u.String(),
			Title:  title,
			Source: "ir_page",
			Kind:   kind,
			Score:  score,
		})
	}
	return out, nil
}

func discoverFromExternalCrawler(ctx context.Context, pageURL, crawlerCmd string, crawlerArgs []string) ([]LinkCandidate, error) {
	args := expandCrawlerArgs(crawlerArgs, pageURL)
	cmd := exec.CommandContext(ctx, crawlerCmd, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("external crawler failed: %w", err)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("external crawler returned empty output")
	}
	base, _ := url.Parse(pageURL)
	return parseCrawlerOutput(string(out), base), nil
}

func expandCrawlerArgs(args []string, pageURL string) []string {
	if len(args) == 0 {
		return []string{pageURL}
	}
	out := make([]string, 0, len(args)+1)
	replaced := false
	for _, arg := range args {
		if strings.Contains(arg, "{url}") {
			out = append(out, strings.ReplaceAll(arg, "{url}", pageURL))
			replaced = true
			continue
		}
		out = append(out, arg)
	}
	if !replaced {
		out = append(out, pageURL)
	}
	return out
}

func parseCrawlerOutput(raw string, base *url.URL) []LinkCandidate {
	anchors := anchorRe.FindAllStringSubmatch(raw, -1)
	out := make([]LinkCandidate, 0, len(anchors)+8)
	seen := make(map[string]struct{})
	baseHost := hostOrEmpty(base)

	for _, a := range anchors {
		if len(a) < 3 {
			continue
		}
		href := strings.TrimSpace(a[1])
		title := cleanAnchorText(a[2])
		candidate, ok := buildCrawlerCandidate(href, title, base, baseHost)
		if !ok {
			continue
		}
		if _, exists := seen[candidate.URL]; exists {
			continue
		}
		seen[candidate.URL] = struct{}{}
		out = append(out, candidate)
	}

	for _, m := range markdownLinkRe.FindAllStringSubmatch(raw, -1) {
		if len(m) < 3 {
			continue
		}
		href := strings.TrimSpace(m[2])
		title := strings.TrimSpace(m[1])
		candidate, ok := buildCrawlerCandidate(href, title, base, baseHost)
		if !ok {
			continue
		}
		if _, exists := seen[candidate.URL]; exists {
			continue
		}
		seen[candidate.URL] = struct{}{}
		out = append(out, candidate)
	}
	return out
}

func buildCrawlerCandidate(href, title string, base *url.URL, baseHost string) (LinkCandidate, bool) {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return LinkCandidate{}, false
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return LinkCandidate{}, false
	}
	u.Fragment = ""
	kind, score := scoreLink(title, u.String(), baseHost)
	if score <= 0 {
		return LinkCandidate{}, false
	}
	return LinkCandidate{
		URL:    u.String(),
		Title:  title,
		Source: "ir_page_crawler",
		Kind:   kind,
		Score:  score,
	}, true
}

func discoverFromGoogleNewsRSS(ctx context.Context, client *http.Client, hint string) ([]LinkCandidate, error) {
	q := hint + " earnings report pdf investor relations"
	u := googleNewsRSSURL + "?q=" + url.QueryEscape(q) + "&hl=en-US&gl=US&ceid=US:en"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", defaultUserAgent)
	req.Header.Set("Accept", "application/rss+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("news search status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return nil, err
	}

	var rss struct {
		Channel struct {
			Items []struct {
				Title string `xml:"title"`
				Link  string `xml:"link"`
			} `xml:"item"`
		} `xml:"channel"`
	}
	if err := xml.Unmarshal(body, &rss); err != nil {
		return nil, err
	}

	out := make([]LinkCandidate, 0, len(rss.Channel.Items))
	for _, it := range rss.Channel.Items {
		link := strings.TrimSpace(it.Link)
		if link == "" {
			continue
		}
		kind, score := scoreLink(it.Title, link, "")
		if score <= 0 {
			continue
		}
		out = append(out, LinkCandidate{
			URL:    link,
			Title:  strings.TrimSpace(it.Title),
			Source: "news_search",
			Kind:   kind,
			Score:  score,
		})
	}
	return out, nil
}

// discoverFromWebSearch queries DuckDuckGo (no API key required) to find
// the company's earnings/IR page URL, returning top scored candidates.
func discoverFromWebSearch(ctx context.Context, client *http.Client, symbol, companyName, hint string) ([]LinkCandidate, error) {
	q := hint + " " + symbol + " earnings press release investor relations site"
	if companyName != "" {
		q = companyName + " " + symbol + " earnings press release investor relations"
	}
	params := url.Values{}
	params.Set("q", q)
	params.Set("kl", "us-en")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ddgSearchURL, strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,*/*;q=0.8")
	req.Header.Set("Referer", "https://duckduckgo.com/")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ddg search status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}

	// DuckDuckGo HTML results wrap each link in an anchor with class="result__a"
	// with the real URL as href (no redirect shim).
	// Also extract all anchors and score them.
	allAnchors := anchorRe.FindAllStringSubmatch(string(body), -1)
	seen := map[string]bool{}
	out := make([]LinkCandidate, 0)

	for _, a := range allAnchors {
		if len(a) < 3 {
			continue
		}
		href := strings.TrimSpace(a[1])
		if href == "" || strings.HasPrefix(href, "/") || strings.HasPrefix(href, "javascript") {
			continue
		}
		u, err := url.Parse(href)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		// Skip DuckDuckGo's own pages
		host := strings.ToLower(u.Hostname())
		if strings.Contains(host, "duckduckgo.com") {
			continue
		}
		u.Fragment = ""
		clean := u.String()
		if seen[clean] {
			continue
		}
		seen[clean] = true
		title := cleanAnchorText(a[2])
		kind, score := scoreWebSearchLink(title, clean, companyName)
		if score <= 0 {
			continue
		}
		out = append(out, LinkCandidate{
			URL:    clean,
			Title:  title,
			Source: "web_search",
			Kind:   kind,
			Score:  score,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}

func mergeCandidate(r *DiscoveryResult, seen map[string]int, c LinkCandidate) {
	key := strings.TrimSpace(c.URL)
	if key == "" {
		return
	}
	if idx, ok := seen[key]; ok {
		if c.Score > r.Candidates[idx].Score {
			r.Candidates[idx] = c
		}
		return
	}
	seen[key] = len(r.Candidates)
	r.Candidates = append(r.Candidates, c)
}

func cleanAnchorText(s string) string {
	s = stripTagsRe.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	return s
}

func scoreLink(title, rawURL, pageHost string) (kind string, score int) {
	text := strings.ToLower(strings.TrimSpace(title + " " + rawURL))
	u, _ := url.Parse(rawURL)
	kind = "page"
	if strings.Contains(strings.ToLower(rawURL), ".pdf") {
		kind = "pdf"
		score += 10
	}

	for _, kw := range []string{"earnings", "result", "quarter", "q1", "q2", "q3", "q4", "financial", "investor", "release", "10-q", "10-k", "shareholder", "press"} {
		if strings.Contains(text, kw) {
			score += 2
		}
	}
	if strings.Contains(text, "nasdaq") || strings.Contains(text, "nyse") {
		score++
	}
	if pageHost != "" && u != nil && strings.EqualFold(u.Hostname(), pageHost) {
		score++
	}
	return kind, score
}

func scoreWebSearchLink(title, rawURL, companyName string) (kind string, score int) {
	kind, score = scoreLink(title, rawURL, "")
	u, _ := url.Parse(rawURL)
	if u == nil {
		return kind, score
	}

	host := strings.ToLower(u.Hostname())
	path := strings.ToLower(u.Path)
	if host == "" {
		return kind, score
	}

	hasCompanyHost := false
	for _, token := range companyTokens(companyName) {
		if strings.Contains(host, token) {
			hasCompanyHost = true
			score += 4
			break
		}
	}

	hasInvestorHost := strings.Contains(host, "investor") || strings.Contains(path, "/investor") || strings.Contains(path, "investor-relations")
	if hasInvestorHost {
		score += 6
	}
	if hasCompanyHost && hasInvestorHost {
		// Prefer the official investor-relations host as the crawl seed over
		// third-party articles or mirrored PDFs.
		score += 10
	}
	return kind, score
}

func companyTokens(companyName string) []string {
	fields := strings.Fields(strings.ToLower(companyName))
	tokens := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		token := strings.Trim(field, " .,-_()[]{}&/\\\t\n\r\"")
		if len(token) < 4 || isIgnoredCompanyToken(token) {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func isIgnoredCompanyToken(token string) bool {
	switch token {
	case "inc", "corp", "co", "company", "corporation", "group", "holdings", "holding", "ltd", "limited", "llc", "plc", "the":
		return true
	default:
		return false
	}
}

func hostOrEmpty(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Hostname()
}
