package edgar

import (
	"encoding/json/v2"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ray-x/finsight/internal/logger"
)

const (
	submissionsURL = "https://data.sec.gov/submissions/CIK%s.json"
	archiveURL     = "https://www.sec.gov/Archives/edgar/data/%s/%s"
	tickersURL     = "https://www.sec.gov/files/company_tickers.json"
)

// Client fetches SEC EDGAR filings.
type Client struct {
	http      *http.Client
	userAgent string
	tickers   map[string]string // ticker -> CIK (zero-padded 10 digits)
	mu        sync.Mutex
}

// NewClient creates an EDGAR client. email is included in the User-Agent
// header as required by SEC (e.g. "user@example.com").
// NewClient creates an EDGAR client. Returns nil if email is empty,
// since SEC requires a contact email in the User-Agent header.
func NewClient(email string) *Client {
	if email == "" {
		logger.Log("EDGAR: client disabled (no email configured)")
		return nil
	}
	logger.Log("EDGAR: client initialized with email=%s", email)
	return &Client{
		http:      &http.Client{Timeout: 30 * time.Second},
		userAgent: fmt.Sprintf("Finsight/1.0 (%s)", email),
	}
}

// Filing represents a single SEC filing.
type Filing struct {
	Form        string // e.g. "10-Q", "10-K"
	FilingDate  string // e.g. "2026-01-31"
	AccessionNo string // e.g. "0001234567-26-000001"
	PrimaryDoc  string // e.g. "nvda-20251231.htm"
	Description string
}

// FilingText holds the extracted text from a filing.
type FilingText struct {
	Filing Filing
	Text   string // Cleaned text content (truncated to fit LLM context)
}

// submissionsResp is the shape of the EDGAR submissions JSON.
type submissionsResp struct {
	CIK        any      `json:"cik"` // SEC returns this as either a number or a string
	EntityName string   `json:"name"`
	Tickers    []string `json:"tickers"`
	Filings    struct {
		Recent recentFilings `json:"recent"`
	} `json:"filings"`
}

type recentFilings struct {
	AccessionNumber []string `json:"accessionNumber"`
	FilingDate      []string `json:"filingDate"`
	Form            []string `json:"form"`
	PrimaryDocument []string `json:"primaryDocument"`
	PrimaryDocDesc  []string `json:"primaryDocDescription"`
}

// tickerEntry is one entry in company_tickers.json.
type tickerEntry struct {
	CIK    int    `json:"cik_str"`
	Ticker string `json:"ticker"`
	Title  string `json:"title"`
}

func (c *Client) edgarGet(url string) ([]byte, error) {
	logger.Log("EDGAR GET %s", url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		logger.Log("EDGAR: request build error: %v", err)
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		logger.Log("EDGAR: request failed: %v", err)
		return nil, fmt.Errorf("EDGAR request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log("EDGAR: HTTP %d for %s", resp.StatusCode, url)
		return nil, fmt.Errorf("EDGAR HTTP %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Log("EDGAR: read body error: %v", err)
		return nil, err
	}
	logger.Log("EDGAR: response %d bytes from %s", len(body), url)
	return body, nil
}

// loadTickers fetches the ticker→CIK mapping from SEC.
func (c *Client) loadTickers() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.tickers != nil {
		return nil
	}

	logger.Log("EDGAR: loading ticker→CIK mapping from SEC")
	data, err := c.edgarGet(tickersURL)
	if err != nil {
		logger.Log("EDGAR: failed to load tickers: %v", err)
		return err
	}

	var entries map[string]tickerEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		logger.Log("EDGAR: failed to parse tickers JSON: %v", err)
		return fmt.Errorf("parse tickers: %w", err)
	}

	c.tickers = make(map[string]string, len(entries))
	for _, e := range entries {
		cik := fmt.Sprintf("%010d", e.CIK)
		c.tickers[strings.ToUpper(e.Ticker)] = cik
	}

	logger.Log("EDGAR: loaded %d tickers", len(c.tickers))
	return nil
}

// LookupCIK returns the 10-digit zero-padded CIK for a ticker symbol.
func (c *Client) LookupCIK(ticker string) (string, error) {
	if err := c.loadTickers(); err != nil {
		return "", err
	}

	clean := strings.ToUpper(ticker)
	clean = strings.Split(clean, ".")[0]
	clean = strings.TrimPrefix(clean, "^")

	c.mu.Lock()
	cik, ok := c.tickers[clean]
	c.mu.Unlock()

	if !ok {
		logger.Log("EDGAR: no CIK found for ticker %q (cleaned=%q)", ticker, clean)
		return "", fmt.Errorf("no CIK found for ticker %q", ticker)
	}
	logger.Log("EDGAR: CIK for %s = %s", ticker, cik)
	return cik, nil
}

// GetRecentFilings returns recent 10-Q and 10-K filings for a ticker.
func (c *Client) GetRecentFilings(ticker string) ([]Filing, error) {
	return c.GetRecentFilingsByForm(ticker, []string{"10-Q", "10-K"}, 8)
}

// GetRecentFilingsByForm returns the most recent filings whose form code
// is in `forms` (case-sensitive, e.g. "10-Q", "10-K", "8-K", "6-K",
// "20-F", "40-F"). Pass nil/empty `forms` to return all filings. The
// result is capped at `limit` (or 8 when limit <= 0) and ordered
// newest-first as EDGAR returns them.
func (c *Client) GetRecentFilingsByForm(ticker string, forms []string, limit int) ([]Filing, error) {
	if limit <= 0 {
		limit = 8
	}
	logger.Log("EDGAR: GetRecentFilingsByForm for %s forms=%v limit=%d", ticker, forms, limit)
	cik, err := c.LookupCIK(ticker)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf(submissionsURL, cik)
	data, err := c.edgarGet(url)
	if err != nil {
		return nil, err
	}

	var sub submissionsResp
	if err := json.Unmarshal(data, &sub); err != nil {
		return nil, fmt.Errorf("parse submissions: %w", err)
	}

	wanted := map[string]bool{}
	for _, f := range forms {
		wanted[f] = true
	}

	var filings []Filing
	recent := sub.Filings.Recent
	for i := range recent.Form {
		form := recent.Form[i]
		if len(wanted) > 0 && !wanted[form] {
			continue
		}
		filings = append(filings, Filing{
			Form:        form,
			FilingDate:  recent.FilingDate[i],
			AccessionNo: recent.AccessionNumber[i],
			PrimaryDoc:  recent.PrimaryDocument[i],
			Description: safeIndex(recent.PrimaryDocDesc, i),
		})
		if len(filings) >= limit {
			break
		}
	}

	logger.Log("EDGAR: found %d filings for %s (entity=%s)", len(filings), ticker, sub.EntityName)
	return filings, nil
}

// GetLatestPressRelease returns the most recent 8-K (US filers) or
// 6-K (foreign private issuers) filing — these typically carry
// earnings press releases with company-issued forward guidance.
// Returns an error if none is found within the recent window.
func (c *Client) GetLatestPressRelease(ticker string) (*Filing, error) {
	logger.Log("EDGAR: GetLatestPressRelease for %s", ticker)
	// Pull a wider window since 8-K/6-K are filed frequently for many
	// non-earnings reasons (M&A, board changes, etc.); the agent tool
	// will scan descriptions / text to confirm relevance.
	filings, err := c.GetRecentFilingsByForm(ticker, []string{"8-K", "6-K"}, 25)
	if err != nil {
		return nil, err
	}
	if len(filings) == 0 {
		return nil, fmt.Errorf("no 8-K or 6-K filings found for %s", ticker)
	}
	f := &filings[0]
	logger.Log("EDGAR: latest press release for %s: %s filed %s", ticker, f.Form, f.FilingDate)
	return f, nil
}

// GetLatestQuarterlyFiling returns the most recent 10-Q or 10-K filing,
// whichever was filed most recently.
func (c *Client) GetLatestQuarterlyFiling(ticker string) (*Filing, error) {
	logger.Log("EDGAR: GetLatestQuarterlyFiling for %s", ticker)
	filings, err := c.GetRecentFilings(ticker)
	if err != nil {
		return nil, err
	}

	// Filings are sorted by date (most recent first) from EDGAR.
	// Pick the first one — it's the most recent 10-Q or 10-K.
	if len(filings) > 0 {
		f := &filings[0]
		logger.Log("EDGAR: latest filing for %s: %s filed %s", ticker, f.Form, f.FilingDate)
		return f, nil
	}

	logger.Log("EDGAR: no 10-Q or 10-K found for %s", ticker)
	return nil, fmt.Errorf("no quarterly or annual filing found for %s", ticker)
}

// FetchFilingText downloads a filing document and extracts text.
// The text is truncated to maxChars to fit LLM context windows.
func (c *Client) FetchFilingText(ticker string, filing Filing, maxChars int) (*FilingText, error) {
	logger.Log("EDGAR: FetchFilingText for %s: %s %s (doc=%s)", ticker, filing.Form, filing.FilingDate, filing.PrimaryDoc)
	cik, err := c.LookupCIK(ticker)
	if err != nil {
		return nil, err
	}

	// Build the document URL: accession number without dashes for the path
	accNoClean := strings.ReplaceAll(filing.AccessionNo, "-", "")
	docURL := fmt.Sprintf(archiveURL, cik, accNoClean+"/"+filing.PrimaryDoc)

	req, err := http.NewRequest("GET", docURL, nil)
	if err != nil {
		logger.Log("EDGAR: request build error for filing: %v", err)
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)

	logger.Log("EDGAR: fetching filing doc from %s", docURL)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch filing: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Log("EDGAR: filing HTTP %d for %s", resp.StatusCode, docURL)
		return nil, fmt.Errorf("filing HTTP %d for %s", resp.StatusCode, docURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Log("EDGAR: read filing body error: %v", err)
		return nil, fmt.Errorf("read filing: %w", err)
	}

	logger.Log("EDGAR: filing doc %d bytes, stripping HTML", len(body))
	text := stripHTML(string(body))

	// Decode HTML entities (&#160; &#8217; &amp; etc.)
	text = html.UnescapeString(text)

	// Try to extract the most valuable sections for financial analysis.
	// For 10-K/10-Q, skip the cover page and legal boilerplate.
	text = extractFinancialSections(text, filing.Form)

	if maxChars > 0 && len(text) > maxChars {
		logger.Log("EDGAR: truncating text from %d to %d chars", len(text), maxChars)
		text = text[:maxChars]
	}

	logger.Log("EDGAR: FetchFilingText complete for %s: %d chars", ticker, len(text))
	return &FilingText{
		Filing: filing,
		Text:   text,
	}, nil
}

// stripHTML removes HTML/XML tags and collapses whitespace.
// It skips content inside iXBRL metadata sections (ix:header, xbrli:context, etc.)
// which contain machine-readable data, not human-readable financial text.
func stripHTML(s string) string {
	// First, remove iXBRL header sections that contain XBRL metadata.
	// These appear as <ix:header>...</ix:header> and contain context definitions,
	// unit declarations, and hidden facts that are not useful as text.
	s = removeTagBlock(s, "ix:header")
	s = removeTagBlock(s, "ix:hidden")

	var out strings.Builder
	inTag := false
	prevSpace := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			if !prevSpace {
				out.WriteRune(' ')
				prevSpace = true
			}
		case inTag:
			// skip
		case r == '\n' || r == '\r' || r == '\t' || r == ' ':
			if !prevSpace {
				out.WriteRune(' ')
				prevSpace = true
			}
		default:
			out.WriteRune(r)
			prevSpace = false
		}
	}
	return strings.TrimSpace(out.String())
}

// removeTagBlock removes all content between <tag...> and </tag> (case-insensitive).
func removeTagBlock(s string, tag string) string {
	lower := strings.ToLower(s)
	openTag := "<" + strings.ToLower(tag)
	closeTag := "</" + strings.ToLower(tag) + ">"

	for {
		start := strings.Index(lower, openTag)
		if start < 0 {
			break
		}
		end := strings.Index(lower[start:], closeTag)
		if end < 0 {
			// No closing tag found — remove from start to end of string
			s = s[:start]
			break
		}
		end = start + end + len(closeTag)
		s = s[:start] + s[end:]
		lower = strings.ToLower(s)
	}
	return s
}

func safeIndex(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return ""
}

// sectionPattern matches Item headings like "ITEM 7." or "ITEM 8." (case-insensitive).
var sectionPattern = regexp.MustCompile(`(?i)\bITEM\s+(\d+[A-Za-z]?)\.?\s`)

// extractFinancialSections tries to extract the most relevant financial sections
// from a 10-K or 10-Q filing text, skipping the cover page and legal boilerplate.
// For 10-K, extracts both Item 7 (MD&A) and Item 8 (Financial Statements).
// For 10-Q, extracts both Item 2 (MD&A) and Item 1 (Financial Statements).
func extractFinancialSections(text, form string) string {
	var targets []string
	if form == "10-K" {
		targets = []string{"7", "8"}
	} else {
		targets = []string{"2", "1"}
	}

	// Find all section positions
	matches := sectionPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		logger.Log("EDGAR: no ITEM headings found, using full text")
		return text
	}

	// Build a map of item number -> list of positions in text.
	itemPositions := make(map[string][]int)
	for _, m := range matches {
		itemNum := strings.ToUpper(strings.TrimSpace(text[m[2]:m[3]]))
		itemPositions[itemNum] = append(itemPositions[itemNum], m[0])
	}

	// Extract all target sections and concatenate them.
	var sections []string
	for _, target := range targets {
		positions, ok := itemPositions[target]
		if !ok {
			continue
		}

		// Use the last occurrence (actual section, not TOC)
		start := positions[len(positions)-1]

		// Find the start of the next different Item section
		end := len(text)
		for _, m := range matches {
			if m[0] > start+10 {
				itemNum := strings.ToUpper(strings.TrimSpace(text[m[2]:m[3]]))
				if itemNum != target {
					end = m[0]
					break
				}
			}
		}

		section := strings.TrimSpace(text[start:end])
		if len(section) > 500 {
			logger.Log("EDGAR: extracted Item %s section (%d chars) from %s", target, len(section), form)
			sections = append(sections, section)
		}
	}

	if len(sections) > 0 {
		combined := strings.Join(sections, "\n\n")
		logger.Log("EDGAR: total extracted %d sections (%d chars) from %s", len(sections), len(combined), form)
		return combined
	}

	// Fallback: skip to the first real Item section (past TOC)
	// Use the last occurrence of Item 1 as a starting point
	if positions, ok := itemPositions["1"]; ok && len(positions) > 1 {
		start := positions[len(positions)-1]
		logger.Log("EDGAR: fallback to Item 1 onwards (%d chars)", len(text)-start)
		return strings.TrimSpace(text[start:])
	}

	logger.Log("EDGAR: no target sections found, using full text")
	return text
}
