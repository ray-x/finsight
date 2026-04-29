package news

import (
	"sort"
	"strings"
	"time"
)

// FilterOpts tunes deterministic relevance filtering applied before
// news items are fed to the LLM. All fields are optional.
type FilterOpts struct {
	Symbol      string        // ticker, e.g. "NVDA"
	CompanyName string        // e.g. "NVIDIA Corporation"
	Limit       int           // 0 = 10
	MaxAge      time.Duration // 0 = 14 days
	// PublisherBlocklist contains lowercased substrings; any item whose
	// publisher matches is dropped. nil uses the default blocklist.
	PublisherBlocklist []string
}

// defaultBlocklist drops a few chronically noisy or low-signal
// sources. Users can override via FilterOpts.PublisherBlocklist.
var defaultBlocklist = []string{
	"simplywall.st",
	"zacks investment ideas",
	"insider monkey",
}

// Filter applies deterministic relevance rules:
//   - drops items older than MaxAge
//   - drops items from blocklisted publishers
//   - requires Symbol or CompanyName to appear in the title (if either
//     is provided)
//   - dedupes by (lowercased) title
//   - sorts newest-first and caps at Limit
//
// It never calls out to an LLM, so it is cheap and deterministic.
func Filter(items []Item, opts FilterOpts) []Item {
	if len(items) == 0 {
		return items
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 10
	}
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = 14 * 24 * time.Hour
	}
	cutoff := time.Now().Add(-maxAge)
	block := opts.PublisherBlocklist
	if block == nil {
		block = defaultBlocklist
	}
	sym := strings.ToLower(strings.TrimSpace(opts.Symbol))
	name := strings.ToLower(strings.TrimSpace(stripCorpSuffix(opts.CompanyName)))

	seen := make(map[string]struct{}, len(items))
	out := make([]Item, 0, len(items))
	for _, it := range items {
		if !it.PublishedAt.IsZero() && it.PublishedAt.Before(cutoff) {
			continue
		}
		pubLower := strings.ToLower(it.Publisher)
		if containsAny(pubLower, block) {
			continue
		}
		title := strings.ToLower(it.Title)
		if sym != "" || name != "" {
			if !titleMatchesSymbol(title, sym) && (name == "" || !strings.Contains(title, name)) {
				continue
			}
		}
		key := strings.TrimSpace(title)
		if key == "" {
			continue
		}
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, it)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].PublishedAt.After(out[j].PublishedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// titleMatchesSymbol returns true if the ticker appears in the title as
// a standalone token (so "NVDA" matches but "NVDAX" does not). Empty
// sym returns false.
func titleMatchesSymbol(titleLower, symLower string) bool {
	if symLower == "" {
		return false
	}
	idx := 0
	for {
		found := strings.Index(titleLower[idx:], symLower)
		if found < 0 {
			return false
		}
		start := idx + found
		end := start + len(symLower)
		leftOK := start == 0 || !isTokenChar(titleLower[start-1])
		rightOK := end == len(titleLower) || !isTokenChar(titleLower[end])
		if leftOK && rightOK {
			return true
		}
		idx = end
	}
}

func isTokenChar(b byte) bool {
	switch {
	case b >= 'a' && b <= 'z':
		return true
	case b >= '0' && b <= '9':
		return true
	}
	return false
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if n != "" && strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// stripCorpSuffix trims common company-suffix noise so "NVIDIA
// Corporation" matches "NVIDIA" in a headline.
func stripCorpSuffix(name string) string {
	n := strings.TrimSpace(name)
	lowers := strings.ToLower(n)
	for _, suf := range []string{
		" corporation", " corp.", " corp", " incorporated", " inc.", " inc",
		" limited", " ltd.", " ltd", " plc", " sa", " ag", " nv", " co.", " company",
	} {
		if strings.HasSuffix(lowers, suf) {
			return strings.TrimSpace(n[:len(n)-len(suf)])
		}
	}
	return n
}
