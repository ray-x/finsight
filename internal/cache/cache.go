package cache

import (
	"encoding/json/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/ray-x/finsight/internal/news"
	"github.com/ray-x/finsight/internal/yahoo"
)

const cacheTTL = 15 * time.Minute
const dailyCacheTTL = 24 * time.Hour
const newsCacheTTL = 1 * time.Hour

// Cacher is the interface for cache operations.
type Cacher interface {
	GetQuotes(symbols []string) []yahoo.Quote
	GetQuotesStale(symbols []string) []yahoo.Quote
	PutQuotes(quotes []yahoo.Quote)
	GetChart(symbol, chartRange, interval string) *yahoo.ChartData
	GetChartStale(symbol, chartRange, interval string) *yahoo.ChartData
	PutChart(symbol, chartRange, interval string, data *yahoo.ChartData)
	GetFinancials(symbol string) *yahoo.FinancialData
	GetFinancialsStale(symbol string) *yahoo.FinancialData
	PutFinancials(symbol string, data *yahoo.FinancialData)
	GetHolders(symbol string) *yahoo.HolderData
	GetHoldersStale(symbol string) *yahoo.HolderData
	PutHolders(symbol string, data *yahoo.HolderData)
	GetNews(symbol string) []news.Item
	GetNewsStale(symbol string) []news.Item
	PutNews(symbol string, items []news.Item)
	GetText(symbol, kind string) string
	GetTextFresh(symbol, kind string, maxAge time.Duration) string
	PutText(symbol, kind, text string)
	DeleteText(symbol, kind string)
	Invalidate(symbol string)
}

// Cache stores fetched data locally to avoid excessive API calls.
type Cache struct {
	dir string
}

type quoteCacheEntry struct {
	Quote     yahoo.Quote `json:"quote"`
	FetchedAt time.Time   `json:"fetched_at"`
}

type chartCacheEntry struct {
	Data      *yahoo.ChartData `json:"data"`
	FetchedAt time.Time        `json:"fetched_at"`
}

type financialsCacheEntry struct {
	Data      *yahoo.FinancialData `json:"data"`
	FetchedAt time.Time            `json:"fetched_at"`
}

type holdersCacheEntry struct {
	Data      *yahoo.HolderData `json:"data"`
	FetchedAt time.Time         `json:"fetched_at"`
}

func New(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Cache{dir: dir}, nil
}

func DefaultDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "finsight")
}

// GetQuotes returns cached quotes if fresh, nil otherwise.
func (c *Cache) GetQuotes(symbols []string) []yahoo.Quote {
	result := make([]yahoo.Quote, 0, len(symbols))
	for _, symbol := range symbols {
		entry, ok := c.readQuoteEntry(symbol)
		if !ok || time.Since(entry.FetchedAt) > cacheTTL {
			return nil
		}
		result = append(result, entry.Quote)
	}
	return result
}

// GetQuotesStale returns cached quotes regardless of age, nil if no cache exists.
func (c *Cache) GetQuotesStale(symbols []string) []yahoo.Quote {
	result := make([]yahoo.Quote, 0, len(symbols))
	for _, symbol := range symbols {
		entry, ok := c.readQuoteEntry(symbol)
		if !ok {
			return nil
		}
		result = append(result, entry.Quote)
	}
	return result
}

// PutQuotes caches quotes.
func (c *Cache) PutQuotes(quotes []yahoo.Quote) {
	for _, quote := range quotes {
		entry := quoteCacheEntry{
			Quote:     quote,
			FetchedAt: time.Now(),
		}
		data, err := json.Marshal(entry)
		if err != nil {
			continue
		}
		_ = os.WriteFile(c.quoteCachePath(quote.Symbol), data, 0644)
	}
}

// GetChart returns cached chart data if fresh, nil otherwise.
func (c *Cache) GetChart(symbol, chartRange, interval string) *yahoo.ChartData {
	key := sanitizeKey(symbol) + "_" + chartRange + "_" + interval + ".json"
	path := filepath.Join(c.dir, key)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var entry chartCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}

	if time.Since(entry.FetchedAt) > cacheTTL {
		return nil
	}

	return entry.Data
}

// GetChartStale returns cached chart data regardless of age, nil if no cache exists.
func (c *Cache) GetChartStale(symbol, chartRange, interval string) *yahoo.ChartData {
	key := sanitizeKey(symbol) + "_" + chartRange + "_" + interval + ".json"
	path := filepath.Join(c.dir, key)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var entry chartCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}

	return entry.Data
}

// PutChart caches chart data.
func (c *Cache) PutChart(symbol, chartRange, interval string, data *yahoo.ChartData) {
	entry := chartCacheEntry{
		Data:      data,
		FetchedAt: time.Now(),
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	key := sanitizeKey(symbol) + "_" + chartRange + "_" + interval + ".json"
	path := filepath.Join(c.dir, key)
	_ = os.WriteFile(path, b, 0644)
}

// Invalidate removes all cached data for a symbol.
func (c *Cache) Invalidate(symbol string) {
	prefix := sanitizeKey(symbol) + "_"
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if len(e.Name()) > len(prefix) && e.Name()[:len(prefix)] == prefix {
			_ = os.Remove(filepath.Join(c.dir, e.Name()))
		}
	}
	// Remove the legacy global quotes cache if present.
	_ = os.Remove(filepath.Join(c.dir, "quotes.json"))
}

func (c *Cache) quoteCachePath(symbol string) string {
	return filepath.Join(c.dir, sanitizeKey(symbol)+"_quote.json")
}

func (c *Cache) readQuoteEntry(symbol string) (quoteCacheEntry, bool) {
	data, err := os.ReadFile(c.quoteCachePath(symbol))
	if err != nil {
		return quoteCacheEntry{}, false
	}

	var entry quoteCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return quoteCacheEntry{}, false
	}
	if entry.Quote.Symbol == "" {
		return quoteCacheEntry{}, false
	}
	return entry, true
}

func sanitizeKey(s string) string {
	out := make([]byte, 0, len(s))
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '.':
			out = append(out, byte(c))
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}

// GetFinancials returns cached financials if fresh, nil otherwise.
func (c *Cache) GetFinancials(symbol string) *yahoo.FinancialData {
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_financials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry financialsCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	if time.Since(entry.FetchedAt) > dailyCacheTTL {
		return nil
	}
	return entry.Data
}

// GetFinancialsStale returns cached financials regardless of age.
func (c *Cache) GetFinancialsStale(symbol string) *yahoo.FinancialData {
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_financials.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry financialsCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	return entry.Data
}

// PutFinancials caches financial data.
func (c *Cache) PutFinancials(symbol string, data *yahoo.FinancialData) {
	entry := financialsCacheEntry{Data: data, FetchedAt: time.Now()}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_financials.json")
	_ = os.WriteFile(path, b, 0644)
}

// GetHolders returns cached holders if fresh, nil otherwise.
func (c *Cache) GetHolders(symbol string) *yahoo.HolderData {
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_holders.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry holdersCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	if time.Since(entry.FetchedAt) > dailyCacheTTL {
		return nil
	}
	return entry.Data
}

// GetHoldersStale returns cached holders regardless of age.
func (c *Cache) GetHoldersStale(symbol string) *yahoo.HolderData {
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_holders.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry holdersCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	return entry.Data
}

// PutHolders caches holder data.
func (c *Cache) PutHolders(symbol string, data *yahoo.HolderData) {
	entry := holdersCacheEntry{Data: data, FetchedAt: time.Now()}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_holders.json")
	_ = os.WriteFile(path, b, 0644)
}

// ── News cache ──

type newsCacheEntry struct {
	Items     []news.Item `json:"items"`
	FetchedAt time.Time   `json:"fetched_at"`
}

// GetNews returns cached news for the symbol if fresh, nil otherwise.
func (c *Cache) GetNews(symbol string) []news.Item {
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_news.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry newsCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	if time.Since(entry.FetchedAt) > newsCacheTTL {
		return nil
	}
	return entry.Items
}

// GetNewsStale returns cached news regardless of age.
func (c *Cache) GetNewsStale(symbol string) []news.Item {
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_news.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var entry newsCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil
	}
	return entry.Items
}

// PutNews caches news items for a symbol.
func (c *Cache) PutNews(symbol string, items []news.Item) {
	entry := newsCacheEntry{Items: items, FetchedAt: time.Now()}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_news.json")
	_ = os.WriteFile(path, b, 0644)
}

// ── LLM / Text cache ──

type textCacheEntry struct {
	Text      string    `json:"text"`
	FetchedAt time.Time `json:"fetched_at"`
}

// GetText returns cached text for a symbol+kind key if fresh, empty string otherwise.
func (c *Cache) GetText(symbol, kind string) string {
	return c.GetTextFresh(symbol, kind, dailyCacheTTL)
}

// GetTextFresh returns cached text for a symbol+kind key if it is newer than maxAge.
// Empty string is returned when missing, unreadable, or stale.
func (c *Cache) GetTextFresh(symbol, kind string, maxAge time.Duration) string {
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_"+kind+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var entry textCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return ""
	}
	if maxAge > 0 && time.Since(entry.FetchedAt) > maxAge {
		return ""
	}
	return entry.Text
}

// PutText caches text for a symbol+kind key.
func (c *Cache) PutText(symbol, kind, text string) {
	entry := textCacheEntry{Text: text, FetchedAt: time.Now()}
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_"+kind+".json")
	_ = os.WriteFile(path, b, 0644)
}

// DeleteText removes cached text for a symbol+kind key.
func (c *Cache) DeleteText(symbol, kind string) {
	path := filepath.Join(c.dir, sanitizeKey(symbol)+"_"+kind+".json")
	os.Remove(path)
}
