package yahoo

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ray-x/finsight/internal/logger"
)

const (
	baseURL   = "https://query1.finance.yahoo.com"
	userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

type Client struct {
	http          *http.Client
	crumb         string
	mu            sync.Mutex
	backoffUntil  time.Time // negative cache: skip crumb fetch until this time
	lastErr       error     // most recent auth error (returned during backoff window)
	sessionFile   string    // persisted crumb+cookie path
	sessionLoaded bool      // avoid rewriting unchanged session
}

// persistedSession is the on-disk representation of a warm Yahoo session.
type persistedSession struct {
	Crumb   string            `json:"crumb"`
	Cookies []persistedCookie `json:"cookies"`
	Saved   int64             `json:"saved"`
}

type persistedCookie struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Domain  string `json:"domain"`
	Path    string `json:"path"`
	Expires int64  `json:"expires"`
}

func NewClient() *Client {
	jar, _ := cookiejar.New(nil)
	c := &Client{
		http: &http.Client{
			Timeout: 15 * time.Second,
			Jar:     jar,
		},
	}
	if cacheDir, err := os.UserCacheDir(); err == nil {
		c.sessionFile = filepath.Join(cacheDir, "finsight", "yahoo_session.json")
		c.loadSession()
	}
	return c
}

// loadSession hydrates the crumb + cookie jar from disk so cold starts
// don't have to hit the getcrumb endpoint (a frequent 429 source).
func (c *Client) loadSession() {
	if c.sessionFile == "" {
		return
	}
	data, err := os.ReadFile(c.sessionFile)
	if err != nil {
		return
	}
	var s persistedSession
	if err := json.Unmarshal(data, &s); err != nil {
		return
	}
	// Expire sessions older than 6 hours — crumbs are session tokens
	// and can go stale silently.
	if s.Saved > 0 && time.Since(time.Unix(s.Saved, 0)) > 6*time.Hour {
		return
	}
	if s.Crumb == "" {
		return
	}
	// Rebuild cookies by domain.
	byHost := map[string][]*http.Cookie{}
	for _, pc := range s.Cookies {
		if pc.Expires > 0 && time.Unix(pc.Expires, 0).Before(time.Now()) {
			continue
		}
		ck := &http.Cookie{
			Name:   pc.Name,
			Value:  pc.Value,
			Domain: pc.Domain,
			Path:   pc.Path,
		}
		if pc.Expires > 0 {
			ck.Expires = time.Unix(pc.Expires, 0)
		}
		host := strings.TrimPrefix(pc.Domain, ".")
		byHost[host] = append(byHost[host], ck)
	}
	for host, cookies := range byHost {
		u := &url.URL{Scheme: "https", Host: host}
		c.http.Jar.SetCookies(u, cookies)
	}
	c.mu.Lock()
	c.crumb = s.Crumb
	c.sessionLoaded = true
	c.mu.Unlock()
	logger.Log("yahoo: loaded persisted session (crumb cached, %d cookies)", len(s.Cookies))
}

// saveSession persists the current crumb+cookies so the next cold
// start can skip the auth dance entirely.
func (c *Client) saveSession() {
	if c.sessionFile == "" || c.crumb == "" {
		return
	}
	hosts := []string{"yahoo.com", "finance.yahoo.com", "query1.finance.yahoo.com", "query2.finance.yahoo.com", "fc.yahoo.com"}
	var all []persistedCookie
	seen := map[string]bool{}
	for _, h := range hosts {
		u := &url.URL{Scheme: "https", Host: h}
		for _, ck := range c.http.Jar.Cookies(u) {
			key := ck.Domain + "|" + ck.Path + "|" + ck.Name
			if seen[key] {
				continue
			}
			seen[key] = true
			pc := persistedCookie{
				Name:   ck.Name,
				Value:  ck.Value,
				Domain: ck.Domain,
				Path:   ck.Path,
			}
			if !ck.Expires.IsZero() {
				pc.Expires = ck.Expires.Unix()
			}
			if pc.Domain == "" {
				pc.Domain = h
			}
			if pc.Path == "" {
				pc.Path = "/"
			}
			all = append(all, pc)
		}
	}
	s := persistedSession{
		Crumb:   c.crumb,
		Cookies: all,
		Saved:   time.Now().Unix(),
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(c.sessionFile), 0o755); err != nil {
		return
	}
	tmp := c.sessionFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, c.sessionFile)
}

// retryAfter parses a Retry-After header (seconds or HTTP-date). Returns
// 0 if absent or unparseable.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// ensureCrumb fetches a crumb+cookie pair if not already cached.
// Retries on 429 with exponential backoff and honours Retry-After.
// Uses a negative cache (backoffUntil) so a flood of concurrent
// callers don't each hammer the getcrumb endpoint after a rate-limit.
func (c *Client) ensureCrumb() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.crumb != "" {
		return nil
	}
	if now := time.Now(); now.Before(c.backoffUntil) {
		if c.lastErr != nil {
			return c.lastErr
		}
		return fmt.Errorf("yahoo: auth backoff, retry in %s", c.backoffUntil.Sub(now).Round(time.Second))
	}

	const maxAttempts = 4
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 500ms, 1s, 2s
			wait := time.Duration(250*(1<<attempt)) * time.Millisecond
			time.Sleep(wait)
		}

		// Step 1: cookie endpoint
		req, err := http.NewRequest("GET", "https://fc.yahoo.com/", nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := c.http.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		cookieStatus := resp.StatusCode
		resp.Body.Close()
		if cookieStatus == 429 {
			wait := retryAfter(resp)
			if wait == 0 {
				wait = time.Duration(500*(1<<attempt)) * time.Millisecond
			}
			lastErr = fmt.Errorf("yahoo: cookie endpoint 429 (attempt %d, waited %s)", attempt+1, wait)
			logger.Log("yahoo: cookie fetch 429, attempt %d, sleeping %s", attempt+1, wait)
			time.Sleep(wait)
			continue
		}

		// Step 2: crumb endpoint
		req, err = http.NewRequest("GET", "https://query2.finance.yahoo.com/v1/test/getcrumb", nil)
		if err != nil {
			lastErr = err
			continue
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err = c.http.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("failed to get crumb: %w", err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 429 {
			wait := retryAfter(resp)
			if wait == 0 {
				wait = time.Duration(500*(1<<attempt)) * time.Millisecond
			}
			lastErr = fmt.Errorf("failed to obtain crumb (status 429, attempt %d)", attempt+1)
			logger.Log("yahoo: crumb fetch 429, attempt %d, sleeping %s", attempt+1, wait)
			time.Sleep(wait)
			continue
		}

		crumb := strings.TrimSpace(string(body))
		if crumb == "" || resp.StatusCode != 200 {
			lastErr = fmt.Errorf("failed to obtain crumb (status %d)", resp.StatusCode)
			continue
		}

		c.crumb = crumb
		c.backoffUntil = time.Time{}
		c.lastErr = nil
		go c.saveSession() // fire-and-forget: don't block callers on disk I/O
		return nil
	}

	// All attempts failed. Set a short negative-cache window so
	// concurrent callers don't each re-trigger the same 429 storm.
	c.lastErr = lastErr
	c.backoffUntil = time.Now().Add(30 * time.Second)
	return lastErr
}

// Quote holds real-time quote data for a symbol.
type Quote struct {
	Symbol            string
	Name              string
	Price             float64
	Change            float64
	ChangePercent     float64
	Currency          string
	MarketState       string
	MarketCap         int64
	RegularMarketTime int64
	Open              float64
	DayLow            float64
	DayHigh           float64
	Volume            int64
	AvgVolume         int64
	FiftyTwoWeekLow   float64
	FiftyTwoWeekHigh  float64
	PE                float64
	DividendYield     float64
	Bid               float64
	BidSize           int64
	Ask               float64
	AskSize           int64
	PreviousClose     float64
	EPS               float64
	ForwardPE         float64
	BookValue         float64
	PriceToBook       float64
	PEG               float64
	Beta              float64

	// Pre/Post market data
	PreMarketPrice          float64
	PreMarketChange         float64
	PreMarketChangePercent  float64
	PreMarketTime           int64
	PostMarketPrice         float64
	PostMarketChange        float64
	PostMarketChangePercent float64
	PostMarketTime          int64
	ExchangeTimezone        string
}

// ChartData holds historical price data for chart rendering.
type ChartData struct {
	Timestamps []int64
	Closes     []float64
	Opens      []float64
	Highs      []float64
	Lows       []float64
	Volumes    []int64
}

// SearchResult from Yahoo symbol search.
type SearchResult struct {
	Symbol   string
	Name     string
	Type     string
	Exchange string
}

// resetCrumb forces re-authentication on next request.
func (c *Client) resetCrumb() {
	c.mu.Lock()
	c.crumb = ""
	c.mu.Unlock()
}

// doGet performs an authenticated GET request with crumb.
// Retries once on 401/403 (after re-authentication) and up to 3
// times on 429 with exponential backoff + Retry-After support.
func (c *Client) doGet(rawURL string) ([]byte, error) {
	if err := c.ensureCrumb(); err != nil {
		return nil, err
	}

	sep := "?"
	if strings.Contains(rawURL, "?") {
		sep = "&"
	}

	execute := func() (*http.Response, []byte, error) {
		fullURL := rawURL + sep + "crumb=" + url.QueryEscape(c.crumb)
		req, err := http.NewRequest("GET", fullURL, nil)
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("User-Agent", userAgent)
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, body, err
	}

	const maxAttempts = 3
	var lastBody []byte
	var lastStatus int
	for attempt := 0; attempt < maxAttempts; attempt++ {
		resp, body, err := execute()
		if err != nil {
			return nil, err
		}
		lastBody = body
		lastStatus = resp.StatusCode

		switch resp.StatusCode {
		case 200:
			return body, nil
		case 401, 403:
			// Crumb expired — re-auth and retry once immediately.
			c.resetCrumb()
			if err := c.ensureCrumb(); err != nil {
				return nil, err
			}
			resp2, body2, err := execute()
			if err != nil {
				return nil, err
			}
			if resp2.StatusCode == 200 {
				return body2, nil
			}
			return nil, fmt.Errorf("HTTP %d after re-auth", resp2.StatusCode)
		case 429:
			wait := retryAfter(resp)
			if wait == 0 {
				// 500ms, 1s, 2s
				wait = time.Duration(500*(1<<attempt)) * time.Millisecond
			}
			if attempt == maxAttempts-1 {
				return nil, fmt.Errorf("HTTP 429 after %d attempts", maxAttempts)
			}
			logger.Log("yahoo: doGet 429, attempt %d, sleeping %s", attempt+1, wait)
			time.Sleep(wait)
			continue
		default:
			preview := string(body)
			if len(preview) > 200 {
				preview = preview[:200]
			}
			return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, preview)
		}
	}
	preview := string(lastBody)
	if len(preview) > 200 {
		preview = preview[:200]
	}
	return nil, fmt.Errorf("HTTP %d: %s", lastStatus, preview)
}

// GetQuotes fetches real-time quotes for multiple symbols.
func (c *Client) GetQuotes(symbols []string) ([]Quote, error) {
	symbolList := strings.Join(symbols, ",")
	u := fmt.Sprintf("%s/v7/finance/quote?symbols=%s", baseURL, url.QueryEscape(symbolList))

	body, err := c.doGet(u)
	if err != nil {
		return nil, err
	}

	var result struct {
		QuoteResponse struct {
			Result []struct {
				Symbol                      string  `json:"symbol"`
				ShortName                   string  `json:"shortName"`
				LongName                    string  `json:"longName"`
				RegularMarketPrice          float64 `json:"regularMarketPrice"`
				RegularMarketChange         float64 `json:"regularMarketChange"`
				RegularMarketChangePercent  float64 `json:"regularMarketChangePercent"`
				Currency                    string  `json:"currency"`
				MarketState                 string  `json:"marketState"`
				MarketCap                   int64   `json:"marketCap"`
				RegularMarketTime           int64   `json:"regularMarketTime"`
				RegularMarketOpen           float64 `json:"regularMarketOpen"`
				RegularMarketDayLow         float64 `json:"regularMarketDayLow"`
				RegularMarketDayHigh        float64 `json:"regularMarketDayHigh"`
				RegularMarketVolume         int64   `json:"regularMarketVolume"`
				AverageDailyVolume3Month    int64   `json:"averageDailyVolume3Month"`
				FiftyTwoWeekLow             float64 `json:"fiftyTwoWeekLow"`
				FiftyTwoWeekHigh            float64 `json:"fiftyTwoWeekHigh"`
				TrailingPE                  float64 `json:"trailingPE"`
				TrailingAnnualDividendYield float64 `json:"trailingAnnualDividendYield"`
				Bid                         float64 `json:"bid"`
				BidSize                     int64   `json:"bidSize"`
				Ask                         float64 `json:"ask"`
				AskSize                     int64   `json:"askSize"`
				RegularMarketPreviousClose  float64 `json:"regularMarketPreviousClose"`
				EpsTrailingTwelveMonths     float64 `json:"epsTrailingTwelveMonths"`
				ForwardPE                   float64 `json:"forwardPE"`
				BookValue                   float64 `json:"bookValue"`
				PriceToBook                 float64 `json:"priceToBook"`
				PegRatio                    float64 `json:"pegRatio"`
				PreMarketPrice              float64 `json:"preMarketPrice"`
				PreMarketChange             float64 `json:"preMarketChange"`
				PreMarketChangePercent      float64 `json:"preMarketChangePercent"`
				PreMarketTime               int64   `json:"preMarketTime"`
				PostMarketPrice             float64 `json:"postMarketPrice"`
				PostMarketChange            float64 `json:"postMarketChange"`
				PostMarketChangePercent     float64 `json:"postMarketChangePercent"`
				PostMarketTime              int64   `json:"postMarketTime"`
				ExchangeTimezoneShortName   string  `json:"exchangeTimezoneShortName"`
			} `json:"result"`
			Error interface{} `json:"error"`
		} `json:"quoteResponse"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse quote response: %w", err)
	}

	quotes := make([]Quote, 0, len(result.QuoteResponse.Result))
	for _, r := range result.QuoteResponse.Result {
		name := r.ShortName
		if name == "" {
			name = r.LongName
		}
		quotes = append(quotes, Quote{
			Symbol:                  r.Symbol,
			Name:                    name,
			Price:                   r.RegularMarketPrice,
			Change:                  r.RegularMarketChange,
			ChangePercent:           r.RegularMarketChangePercent,
			Currency:                r.Currency,
			MarketState:             r.MarketState,
			MarketCap:               r.MarketCap,
			RegularMarketTime:       r.RegularMarketTime,
			Open:                    r.RegularMarketOpen,
			DayLow:                  r.RegularMarketDayLow,
			DayHigh:                 r.RegularMarketDayHigh,
			Volume:                  r.RegularMarketVolume,
			AvgVolume:               r.AverageDailyVolume3Month,
			FiftyTwoWeekLow:         r.FiftyTwoWeekLow,
			FiftyTwoWeekHigh:        r.FiftyTwoWeekHigh,
			PE:                      r.TrailingPE,
			DividendYield:           r.TrailingAnnualDividendYield,
			Bid:                     r.Bid,
			BidSize:                 r.BidSize,
			Ask:                     r.Ask,
			AskSize:                 r.AskSize,
			PreviousClose:           r.RegularMarketPreviousClose,
			EPS:                     r.EpsTrailingTwelveMonths,
			ForwardPE:               r.ForwardPE,
			BookValue:               r.BookValue,
			PriceToBook:             r.PriceToBook,
			PEG:                     r.PegRatio,
			PreMarketPrice:          r.PreMarketPrice,
			PreMarketChange:         r.PreMarketChange,
			PreMarketChangePercent:  r.PreMarketChangePercent,
			PreMarketTime:           r.PreMarketTime,
			PostMarketPrice:         r.PostMarketPrice,
			PostMarketChange:        r.PostMarketChange,
			PostMarketChangePercent: r.PostMarketChangePercent,
			PostMarketTime:          r.PostMarketTime,
			ExchangeTimezone:        r.ExchangeTimezoneShortName,
		})
	}
	return quotes, nil
}

// GetChart fetches historical chart data for a symbol.
func (c *Client) GetChart(symbol, chartRange, interval string) (*ChartData, error) {
	return c.GetChartWithOpts(symbol, chartRange, interval, false)
}

// GetChartWithOpts fetches historical chart data with optional pre/post market data.
func (c *Client) GetChartWithOpts(symbol, chartRange, interval string, includePrePost bool) (*ChartData, error) {
	u := fmt.Sprintf("%s/v8/finance/chart/%s?range=%s&interval=%s",
		baseURL, url.PathEscape(symbol), url.QueryEscape(chartRange), url.QueryEscape(interval))
	if includePrePost {
		u += "&includePrePost=true"
	}

	body, err := c.doGet(u)
	if err != nil {
		return nil, err
	}

	var result struct {
		Chart struct {
			Result []struct {
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close  []interface{} `json:"close"`
						Open   []interface{} `json:"open"`
						High   []interface{} `json:"high"`
						Low    []interface{} `json:"low"`
						Volume []interface{} `json:"volume"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
		} `json:"chart"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse chart response: %w", err)
	}

	if len(result.Chart.Result) == 0 {
		return nil, fmt.Errorf("no chart data for %s", symbol)
	}

	r := result.Chart.Result[0]
	cd := &ChartData{
		Timestamps: r.Timestamp,
	}

	if len(r.Indicators.Quote) > 0 {
		q := r.Indicators.Quote[0]
		cd.Closes = toFloat64Slice(q.Close)
		cd.Opens = toFloat64Slice(q.Open)
		cd.Highs = toFloat64Slice(q.High)
		cd.Lows = toFloat64Slice(q.Low)
		cd.Volumes = toInt64Slice(q.Volume)
	}

	return cd, nil
}

// Search searches for symbols matching a query.
func (c *Client) Search(query string) ([]SearchResult, error) {
	u := fmt.Sprintf("%s/v1/finance/search?q=%s&quotesCount=8&newsCount=0",
		baseURL, url.QueryEscape(query))

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Quotes []struct {
			Symbol    string `json:"symbol"`
			ShortName string `json:"shortname"`
			LongName  string `json:"longname"`
			TypeDisp  string `json:"typeDisp"`
			Exchange  string `json:"exchDisp"`
		} `json:"quotes"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse search response: %w", err)
	}

	results := make([]SearchResult, 0, len(result.Quotes))
	for _, q := range result.Quotes {
		name := q.ShortName
		if name == "" {
			name = q.LongName
		}
		results = append(results, SearchResult{
			Symbol:   q.Symbol,
			Name:     name,
			Type:     q.TypeDisp,
			Exchange: q.Exchange,
		})
	}
	return results, nil
}

// GetMostActiveSymbols fetches symbols from Yahoo's "most_actives" predefined screener.
func (c *Client) GetMostActiveSymbols(count int) ([]string, error) {
	if count <= 0 {
		count = 50
	}
	u := fmt.Sprintf("%s/v1/finance/screener/predefined/saved?count=%d&scrIds=most_actives", baseURL, count)
	body, err := c.doGet(u)
	if err != nil {
		return nil, err
	}

	var result struct {
		Finance struct {
			Result []struct {
				Quotes []struct {
					Symbol string `json:"symbol"`
				} `json:"quotes"`
			} `json:"result"`
		} `json:"finance"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse most active response: %w", err)
	}
	if len(result.Finance.Result) == 0 {
		return nil, fmt.Errorf("no most active symbols returned")
	}

	syms := make([]string, 0, len(result.Finance.Result[0].Quotes))
	for _, q := range result.Finance.Result[0].Quotes {
		s := strings.TrimSpace(q.Symbol)
		if s == "" {
			continue
		}
		syms = append(syms, strings.ToUpper(s))
	}
	return uniqueSymbols(syms, count), nil
}

// GetTrendingSymbols fetches symbols from Yahoo trending feed for a region (e.g. "US").
func (c *Client) GetTrendingSymbols(region string, count int) ([]string, error) {
	if region == "" {
		region = "US"
	}
	if count <= 0 {
		count = 50
	}
	u := fmt.Sprintf("%s/v1/finance/trending/%s", baseURL, url.PathEscape(strings.ToUpper(region)))
	body, err := c.doGet(u)
	if err != nil {
		return nil, err
	}

	var result struct {
		Finance struct {
			Result []struct {
				Quotes []struct {
					Symbol string `json:"symbol"`
				} `json:"quotes"`
			} `json:"result"`
		} `json:"finance"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse trending response: %w", err)
	}
	if len(result.Finance.Result) == 0 {
		return nil, fmt.Errorf("no trending symbols returned")
	}

	syms := make([]string, 0, len(result.Finance.Result[0].Quotes))
	for _, q := range result.Finance.Result[0].Quotes {
		s := strings.TrimSpace(q.Symbol)
		if s == "" {
			continue
		}
		syms = append(syms, strings.ToUpper(s))
	}
	return uniqueSymbols(syms, count), nil
}

func uniqueSymbols(in []string, limit int) []string {
	if limit <= 0 {
		limit = len(in)
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) >= limit {
			break
		}
	}
	return out
}

// KeyStats holds additional stats from the quoteSummary endpoint.
type KeyStats struct {
	PEGRatio     float64
	AnnualGrowth float64 // current year EPS growth rate (0y period)
	Beta         float64
}

// FinancialPeriod holds revenue and earnings for one period.
type FinancialPeriod struct {
	Date     string // e.g. "2Q2025" or "2025"
	Revenue  int64
	Earnings int64
}

// EPSQuarter holds EPS data for one quarter.
type EPSQuarter struct {
	Period          string // e.g. "-1q"
	Quarter         string // date string e.g. "2026-01-31"
	EPSActual       float64
	EPSEstimate     float64
	SurprisePercent float64
}

// RecommendationPeriod holds analyst recommendation counts for a single period.
type RecommendationPeriod struct {
	Period     string // e.g. "0m", "-1m", "-2m", "-3m"
	StrongBuy  int
	Buy        int
	Hold       int
	Sell       int
	StrongSell int
}

// FinancialData holds quarterly and annual financial data.
type FinancialData struct {
	Quarterly []FinancialPeriod
	Yearly    []FinancialPeriod

	// EPS history (last 4 quarters)
	EPSHistory []EPSQuarter

	// Key financial metrics from financialData module
	EBITDA            int64
	RevenueTTM        int64
	ProfitMargin      float64 // decimal, e.g. 0.55 = 55%
	EarningsGrowth    float64 // quarterly YoY, decimal
	RevenueGrowth     float64 // quarterly YoY, decimal
	DebtToEquity      float64 // percentage, e.g. 17.08
	CurrentRatio      float64
	FreeCashflow      int64
	OperatingCashflow int64
	GrossMargins      float64
	OperatingMargins  float64
	ReturnOnEquity    float64
	RecommendationKey string // e.g. "strong_buy"
	TargetMeanPrice   float64
	TargetHighPrice   float64
	TargetLowPrice    float64
	NumberOfAnalysts  int64

	// Balance sheet
	TotalCash int64
	TotalDebt int64

	// Valuation (from defaultKeyStatistics)
	PriceToSales        float64
	EnterpriseValue     int64
	EnterpriseToRevenue float64
	EnterpriseToEbitda  float64

	// Analyst recommendation trend
	RecommendationTrend []RecommendationPeriod

	// Forward guidance (analyst consensus from earningsTrend module).
	// Periods covered: current quarter (0q), next quarter (+1q),
	// current year (0y), next year (+1y).
	Guidance []GuidancePeriod

	// Next scheduled earnings report date (UTC, "2006-01-02"),
	// from calendarEvents.earnings. Empty when unknown.
	NextEarningsDate string
}

// GuidancePeriod captures forward-looking analyst consensus for a single
// horizon (e.g. "+1q" = next quarter). All numeric fields are zero when
// Yahoo did not report them.
type GuidancePeriod struct {
	Period              string  // raw Yahoo period: "0q", "+1q", "0y", "+1y"
	Label               string  // human label: "Current Quarter", "Next Quarter", …
	EndDate             string  // period end date, e.g. "2026-06-30"
	Growth              float64 // YoY growth implied by consensus, decimal
	EPSAvg              float64
	EPSLow              float64
	EPSHigh             float64
	EPSAnalysts         int64
	EPSYearAgo          float64
	RevenueAvg          int64
	RevenueLow          int64
	RevenueHigh         int64
	RevenueAnalysts     int64
	RevenueYearAgo      int64
	RevenueGrowth       float64 // YoY rev growth implied, decimal
	EPSRevisionsUp7d    int64
	EPSRevisionsDown7d  int64
	EPSRevisionsUp30d   int64
	EPSRevisionsDown30d int64
}

// HolderEntry represents a single institutional or insider holder.
type HolderEntry struct {
	Name       string
	Shares     int64
	PctHeld    float64
	ReportDate string
}

// HolderData holds ownership breakdown and top holders.
type HolderData struct {
	InsiderPctHeld     float64
	InstitutionPctHeld float64
	InstitutionCount   int64
	TopInstitutions    []HolderEntry
	Insiders           []HolderEntry
}

// GetKeyStats fetches key statistics (PEG ratio, etc.) from the v10 quoteSummary endpoint.
func (c *Client) GetKeyStats(symbol string) (*KeyStats, error) {
	if err := c.ensureCrumb(); err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/v10/finance/quoteSummary/%s?modules=defaultKeyStatistics,earningsTrend&crumb=%s",
		baseURL, url.QueryEscape(symbol), url.QueryEscape(c.crumb))

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		QuoteSummary struct {
			Result []struct {
				DefaultKeyStatistics struct {
					PegRatio struct {
						Raw float64 `json:"raw"`
					} `json:"pegRatio"`
					Beta struct {
						Raw float64 `json:"raw"`
					} `json:"beta"`
				} `json:"defaultKeyStatistics"`
				EarningsTrend struct {
					Trend []struct {
						Period string `json:"period"`
						Growth struct {
							Raw float64 `json:"raw"`
						} `json:"growth"`
					} `json:"trend"`
				} `json:"earningsTrend"`
			} `json:"result"`
		} `json:"quoteSummary"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse key stats: %w", err)
	}

	if len(result.QuoteSummary.Result) == 0 {
		return &KeyStats{}, nil
	}

	r := result.QuoteSummary.Result[0]
	peg := r.DefaultKeyStatistics.PegRatio.Raw
	beta := r.DefaultKeyStatistics.Beta.Raw

	// Find current year annual growth ("0y" period) from earningsTrend
	var annualGrowth float64
	for _, t := range r.EarningsTrend.Trend {
		if t.Period == "0y" {
			annualGrowth = t.Growth.Raw
			break
		}
	}

	return &KeyStats{
		PEGRatio:     peg,
		AnnualGrowth: annualGrowth,
		Beta:         beta,
	}, nil
}

// GetFinancials fetches quarterly and annual revenue/earnings from the earnings module.
func (c *Client) GetFinancials(symbol string) (*FinancialData, error) {
	if err := c.ensureCrumb(); err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/v10/finance/quoteSummary/%s?modules=earnings,earningsHistory,earningsTrend,financialData,defaultKeyStatistics,recommendationTrend,calendarEvents&crumb=%s",
		baseURL, url.QueryEscape(symbol), url.QueryEscape(c.crumb))

	body, err := c.doGet(u)
	if err != nil {
		return nil, err
	}

	type rawVal struct {
		Raw float64 `json:"raw"`
	}
	type rawValInt struct {
		Raw int64 `json:"raw"`
	}
	type rawValStr struct {
		Fmt string `json:"fmt"`
	}

	var result struct {
		QuoteSummary struct {
			Result []struct {
				Earnings struct {
					FinancialsChart struct {
						Quarterly []struct {
							Date    string `json:"date"`
							Revenue struct {
								Raw int64 `json:"raw"`
							} `json:"revenue"`
							Earnings struct {
								Raw int64 `json:"raw"`
							} `json:"earnings"`
						} `json:"quarterly"`
						Yearly []struct {
							Date    int `json:"date"`
							Revenue struct {
								Raw int64 `json:"raw"`
							} `json:"revenue"`
							Earnings struct {
								Raw int64 `json:"raw"`
							} `json:"earnings"`
						} `json:"yearly"`
					} `json:"financialsChart"`
				} `json:"earnings"`
				EarningsHistory struct {
					History []struct {
						Period          string    `json:"period"`
						Quarter         rawValStr `json:"quarter"`
						EPSActual       rawVal    `json:"epsActual"`
						EPSEstimate     rawVal    `json:"epsEstimate"`
						SurprisePercent rawVal    `json:"surprisePercent"`
					} `json:"history"`
				} `json:"earningsHistory"`
				FinData struct {
					EBITDA            rawValInt `json:"ebitda"`
					TotalRevenue      rawValInt `json:"totalRevenue"`
					ProfitMargins     rawVal    `json:"profitMargins"`
					EarningsGrowth    rawVal    `json:"earningsGrowth"`
					RevenueGrowth     rawVal    `json:"revenueGrowth"`
					DebtToEquity      rawVal    `json:"debtToEquity"`
					CurrentRatio      rawVal    `json:"currentRatio"`
					FreeCashflow      rawValInt `json:"freeCashflow"`
					OperatingCashflow rawValInt `json:"operatingCashflow"`
					GrossMargins      rawVal    `json:"grossMargins"`
					OperatingMargins  rawVal    `json:"operatingMargins"`
					ReturnOnEquity    rawVal    `json:"returnOnEquity"`
					RecommendationKey string    `json:"recommendationKey"`
					TargetMeanPrice   rawVal    `json:"targetMeanPrice"`
					TargetHighPrice   rawVal    `json:"targetHighPrice"`
					TargetLowPrice    rawVal    `json:"targetLowPrice"`
					NumberOfAnalysts  rawValInt `json:"numberOfAnalystOpinions"`
					TotalCash         rawValInt `json:"totalCash"`
					TotalDebt         rawValInt `json:"totalDebt"`
				} `json:"financialData"`
				DefKeyStats struct {
					PriceToSales        rawVal    `json:"priceToSalesTrailing12Months"`
					EnterpriseValue     rawValInt `json:"enterpriseValue"`
					EnterpriseToRevenue rawVal    `json:"enterpriseToRevenue"`
					EnterpriseToEbitda  rawVal    `json:"enterpriseToEbitda"`
				} `json:"defaultKeyStatistics"`
				RecTrend struct {
					Trend []struct {
						Period     string `json:"period"`
						StrongBuy  int    `json:"strongBuy"`
						Buy        int    `json:"buy"`
						Hold       int    `json:"hold"`
						Sell       int    `json:"sell"`
						StrongSell int    `json:"strongSell"`
					} `json:"trend"`
				} `json:"recommendationTrend"`
				EarningsTrend struct {
					Trend []struct {
						Period      string `json:"period"`
						EndDate     string `json:"endDate"`
						Growth      rawVal `json:"growth"`
						EarningsEst struct {
							Avg            rawVal    `json:"avg"`
							Low            rawVal    `json:"low"`
							High           rawVal    `json:"high"`
							YearAgoEPS     rawVal    `json:"yearAgoEps"`
							NumberAnalysts rawValInt `json:"numberOfAnalysts"`
							Growth         rawVal    `json:"growth"`
						} `json:"earningsEstimate"`
						RevenueEst struct {
							Avg            rawValInt `json:"avg"`
							Low            rawValInt `json:"low"`
							High           rawValInt `json:"high"`
							NumberAnalysts rawValInt `json:"numberOfAnalysts"`
							YearAgoRevenue rawValInt `json:"yearAgoRevenue"`
							Growth         rawVal    `json:"growth"`
						} `json:"revenueEstimate"`
						EPSRevisions struct {
							UpLast7Days    rawValInt `json:"upLast7days"`
							UpLast30Days   rawValInt `json:"upLast30days"`
							DownLast7Days  rawValInt `json:"downLast7days"`
							DownLast30Days rawValInt `json:"downLast30days"`
						} `json:"epsRevisions"`
					} `json:"trend"`
				} `json:"earningsTrend"`
				CalendarEvents struct {
					Earnings struct {
						EarningsDate []struct {
							Raw int64 `json:"raw"`
						} `json:"earningsDate"`
					} `json:"earnings"`
				} `json:"calendarEvents"`
			} `json:"result"`
		} `json:"quoteSummary"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse financials: %w", err)
	}

	fd := &FinancialData{}
	if len(result.QuoteSummary.Result) == 0 {
		return fd, nil
	}

	r := result.QuoteSummary.Result[0]
	fc := r.Earnings.FinancialsChart

	for _, q := range fc.Quarterly {
		fd.Quarterly = append(fd.Quarterly, FinancialPeriod{
			Date:     q.Date,
			Revenue:  q.Revenue.Raw,
			Earnings: q.Earnings.Raw,
		})
	}
	for _, y := range fc.Yearly {
		fd.Yearly = append(fd.Yearly, FinancialPeriod{
			Date:     fmt.Sprintf("%d", y.Date),
			Revenue:  y.Revenue.Raw,
			Earnings: y.Earnings.Raw,
		})
	}

	// EPS history
	for _, h := range r.EarningsHistory.History {
		fd.EPSHistory = append(fd.EPSHistory, EPSQuarter{
			Period:          h.Period,
			Quarter:         h.Quarter.Fmt,
			EPSActual:       h.EPSActual.Raw,
			EPSEstimate:     h.EPSEstimate.Raw,
			SurprisePercent: h.SurprisePercent.Raw,
		})
	}

	// Financial metrics
	fin := r.FinData
	fd.EBITDA = fin.EBITDA.Raw
	fd.RevenueTTM = fin.TotalRevenue.Raw
	fd.ProfitMargin = fin.ProfitMargins.Raw
	fd.EarningsGrowth = fin.EarningsGrowth.Raw
	fd.RevenueGrowth = fin.RevenueGrowth.Raw
	fd.DebtToEquity = fin.DebtToEquity.Raw
	fd.CurrentRatio = fin.CurrentRatio.Raw
	fd.FreeCashflow = fin.FreeCashflow.Raw
	fd.OperatingCashflow = fin.OperatingCashflow.Raw
	fd.GrossMargins = fin.GrossMargins.Raw
	fd.OperatingMargins = fin.OperatingMargins.Raw
	fd.ReturnOnEquity = fin.ReturnOnEquity.Raw
	fd.RecommendationKey = fin.RecommendationKey
	fd.TargetMeanPrice = fin.TargetMeanPrice.Raw
	fd.TargetHighPrice = fin.TargetHighPrice.Raw
	fd.TargetLowPrice = fin.TargetLowPrice.Raw
	fd.NumberOfAnalysts = fin.NumberOfAnalysts.Raw
	fd.TotalCash = fin.TotalCash.Raw
	fd.TotalDebt = fin.TotalDebt.Raw

	// Valuation from defaultKeyStatistics
	dks := r.DefKeyStats
	fd.PriceToSales = dks.PriceToSales.Raw
	fd.EnterpriseValue = dks.EnterpriseValue.Raw
	fd.EnterpriseToRevenue = dks.EnterpriseToRevenue.Raw
	fd.EnterpriseToEbitda = dks.EnterpriseToEbitda.Raw

	// Recommendation trend
	for _, t := range r.RecTrend.Trend {
		fd.RecommendationTrend = append(fd.RecommendationTrend, RecommendationPeriod{
			Period:     t.Period,
			StrongBuy:  t.StrongBuy,
			Buy:        t.Buy,
			Hold:       t.Hold,
			Sell:       t.Sell,
			StrongSell: t.StrongSell,
		})
	}

	// Forward guidance (analyst consensus). Yahoo returns up to 6 periods
	// (-2q, -1q, 0q, +1q, 0y, +1y); we surface the four forward-looking
	// ones and label them so the LLM doesn't have to decode the codes.
	for _, t := range r.EarningsTrend.Trend {
		label, keep := guidanceLabel(t.Period)
		if !keep {
			continue
		}
		fd.Guidance = append(fd.Guidance, GuidancePeriod{
			Period:              t.Period,
			Label:               label,
			EndDate:             t.EndDate,
			Growth:              t.Growth.Raw,
			EPSAvg:              t.EarningsEst.Avg.Raw,
			EPSLow:              t.EarningsEst.Low.Raw,
			EPSHigh:             t.EarningsEst.High.Raw,
			EPSAnalysts:         t.EarningsEst.NumberAnalysts.Raw,
			EPSYearAgo:          t.EarningsEst.YearAgoEPS.Raw,
			RevenueAvg:          t.RevenueEst.Avg.Raw,
			RevenueLow:          t.RevenueEst.Low.Raw,
			RevenueHigh:         t.RevenueEst.High.Raw,
			RevenueAnalysts:     t.RevenueEst.NumberAnalysts.Raw,
			RevenueYearAgo:      t.RevenueEst.YearAgoRevenue.Raw,
			RevenueGrowth:       t.RevenueEst.Growth.Raw,
			EPSRevisionsUp7d:    t.EPSRevisions.UpLast7Days.Raw,
			EPSRevisionsDown7d:  t.EPSRevisions.DownLast7Days.Raw,
			EPSRevisionsUp30d:   t.EPSRevisions.UpLast30Days.Raw,
			EPSRevisionsDown30d: t.EPSRevisions.DownLast30Days.Raw,
		})
	}

	// Next earnings date (first entry in calendarEvents.earnings.earningsDate).
	if dates := r.CalendarEvents.Earnings.EarningsDate; len(dates) > 0 && dates[0].Raw > 0 {
		fd.NextEarningsDate = time.Unix(dates[0].Raw, 0).UTC().Format("2006-01-02")
	}

	return fd, nil
}

// guidanceLabel maps Yahoo's earningsTrend period codes to human labels
// and filters out historical periods (already covered by EPSHistory).
func guidanceLabel(period string) (string, bool) {
	switch period {
	case "0q":
		return "Current Quarter", true
	case "+1q":
		return "Next Quarter", true
	case "0y":
		return "Current Year", true
	case "+1y":
		return "Next Year", true
	default:
		return "", false
	}
}

// GetHolders fetches ownership breakdown and top holders.
func (c *Client) GetHolders(symbol string) (*HolderData, error) {
	if err := c.ensureCrumb(); err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/v10/finance/quoteSummary/%s?modules=majorHoldersBreakdown,institutionOwnership,insiderHolders&crumb=%s",
		baseURL, url.QueryEscape(symbol), url.QueryEscape(c.crumb))

	body, err := c.doGet(u)
	if err != nil {
		return nil, err
	}

	var result struct {
		QuoteSummary struct {
			Result []struct {
				MajorHoldersBreakdown struct {
					InsidersPercentHeld struct {
						Raw float64 `json:"raw"`
					} `json:"insidersPercentHeld"`
					InstitutionsPercentHeld struct {
						Raw float64 `json:"raw"`
					} `json:"institutionsPercentHeld"`
					InstitutionsCount struct {
						Raw int64 `json:"raw"`
					} `json:"institutionsCount"`
				} `json:"majorHoldersBreakdown"`
				InstitutionOwnership struct {
					OwnershipList []struct {
						Organization string `json:"organization"`
						PctHeld      struct {
							Raw float64 `json:"raw"`
						} `json:"pctHeld"`
						Position struct {
							Raw int64 `json:"raw"`
						} `json:"position"`
						ReportDate struct {
							Fmt string `json:"fmt"`
						} `json:"reportDate"`
					} `json:"ownershipList"`
				} `json:"institutionOwnership"`
				InsiderHolders struct {
					Holders []struct {
						Name            string `json:"name"`
						Relation        string `json:"relation"`
						LatestTransDate struct {
							Fmt string `json:"fmt"`
						} `json:"latestTransDate"`
						PositionDirect struct {
							Raw int64 `json:"raw"`
						} `json:"positionDirect"`
					} `json:"holders"`
				} `json:"insiderHolders"`
			} `json:"result"`
		} `json:"quoteSummary"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse holders: %w", err)
	}

	hd := &HolderData{}
	if len(result.QuoteSummary.Result) == 0 {
		return hd, nil
	}

	r := result.QuoteSummary.Result[0]

	hd.InsiderPctHeld = r.MajorHoldersBreakdown.InsidersPercentHeld.Raw
	hd.InstitutionPctHeld = r.MajorHoldersBreakdown.InstitutionsPercentHeld.Raw
	hd.InstitutionCount = r.MajorHoldersBreakdown.InstitutionsCount.Raw

	for _, inst := range r.InstitutionOwnership.OwnershipList {
		hd.TopInstitutions = append(hd.TopInstitutions, HolderEntry{
			Name:       inst.Organization,
			Shares:     inst.Position.Raw,
			PctHeld:    inst.PctHeld.Raw,
			ReportDate: inst.ReportDate.Fmt,
		})
		if len(hd.TopInstitutions) >= 10 {
			break
		}
	}

	for _, ins := range r.InsiderHolders.Holders {
		name := ins.Name
		if ins.Relation != "" {
			name += " (" + ins.Relation + ")"
		}
		hd.Insiders = append(hd.Insiders, HolderEntry{
			Name:       name,
			Shares:     ins.PositionDirect.Raw,
			ReportDate: ins.LatestTransDate.Fmt,
		})
		if len(hd.Insiders) >= 10 {
			break
		}
	}

	return hd, nil
}

func toFloat64Slice(raw []interface{}) []float64 {
	out := make([]float64, len(raw))
	for i, v := range raw {
		if v == nil {
			if i > 0 {
				out[i] = out[i-1]
			}
			continue
		}
		if f, ok := v.(float64); ok {
			out[i] = f
		}
	}
	return out
}

func toInt64Slice(raw []interface{}) []int64 {
	out := make([]int64, len(raw))
	for i, v := range raw {
		if v == nil {
			continue
		}
		if f, ok := v.(float64); ok {
			out[i] = int64(f)
		}
	}
	return out
}

// RecommendedSymbol represents a related stock recommendation.
type RecommendedSymbol struct {
	Symbol string
	Score  float64
}

// GetRecommendedSymbols fetches related/similar symbols for a given stock.
func (c *Client) GetRecommendedSymbols(symbol string) ([]RecommendedSymbol, error) {
	u := fmt.Sprintf("https://query2.finance.yahoo.com/v6/finance/recommendationsbysymbol/%s",
		url.QueryEscape(symbol))

	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Finance struct {
			Result []struct {
				Symbol             string `json:"symbol"`
				RecommendedSymbols []struct {
					Symbol string  `json:"symbol"`
					Score  float64 `json:"score"`
				} `json:"recommendedSymbols"`
			} `json:"result"`
		} `json:"finance"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse recommendations: %w", err)
	}

	if len(result.Finance.Result) == 0 {
		return nil, nil
	}

	var out []RecommendedSymbol
	for _, r := range result.Finance.Result[0].RecommendedSymbols {
		out = append(out, RecommendedSymbol{Symbol: r.Symbol, Score: r.Score})
	}
	return out, nil
}
