package earnings

import "strings"

// Registry stores symbol -> IR feed URL mappings.
type Registry struct {
	feeds map[string]string
}

// NewRegistry creates a normalized symbol registry.
func NewRegistry(in map[string]string) *Registry {
	feeds := make(map[string]string, len(in))
	for k, v := range in {
		s := strings.ToUpper(strings.TrimSpace(k))
		u := strings.TrimSpace(v)
		if s == "" || u == "" {
			continue
		}
		feeds[s] = u
	}
	return &Registry{feeds: feeds}
}

// FeedURL returns the feed URL for a symbol.
func (r *Registry) FeedURL(symbol string) (string, bool) {
	if r == nil {
		return "", false
	}
	u, ok := r.feeds[strings.ToUpper(strings.TrimSpace(symbol))]
	return u, ok
}

// Empty reports whether the registry has no mappings.
func (r *Registry) Empty() bool {
	return r == nil || len(r.feeds) == 0
}
