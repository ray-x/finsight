// Package portfolio manages user-owned stock positions separately from the
// watchlist. Positions can be stored in a dedicated portfolio.yaml file
// (recommended for privacy) or embedded in the main config.yaml under the
// `portfolio:` key.
package portfolio

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Position represents a single held position.
//
// Position is the quantity of shares/contracts (float to support fractional
// shares). OpenPrice is the user's entry cost basis; when zero at load time
// the UI will auto-fill it from the first fetched quote's Open and persist
// the update. BoughtAt is the acquisition date in YYYY-MM-DD form and is
// used for portfolio record-keeping (for example, tax workflows).
type Position struct {
	Symbol    string  `yaml:"symbol"`
	Position  float64 `yaml:"position"`
	OpenPrice float64 `yaml:"open_price,omitempty"`
	Note      string  `yaml:"note,omitempty"`
	BoughtAt  string  `yaml:"bought_at,omitempty"`
	AddedAt   string  `yaml:"added_at,omitempty"` // Deprecated legacy field; migrated to BoughtAt on load.
}

// File is the on-disk schema for portfolio.yaml.
type File struct {
	Positions []Position `yaml:"positions"`
}

// DefaultPath returns the default portfolio.yaml path
// (~/.config/finsight/portfolio.yaml).
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "finsight", "portfolio.yaml")
}

// LocalPath returns ./portfolio.yaml for local overrides.
func LocalPath() string {
	return "portfolio.yaml"
}

// Load reads positions from the given path. If the file does not exist,
// returns an empty File with no error so callers can treat "no file" as
// "no portfolio".
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, err
	}
	f := &File{}
	if err := yaml.Unmarshal(data, f); err != nil {
		return nil, err
	}
	f.normalize()
	return f, nil
}

// Exists reports whether a portfolio.yaml file is present at path.
func Exists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// Save writes the portfolio to disk with 0600 permission since holdings
// are private.
func Save(path string, f *File) error {
	if path == "" {
		return nil
	}
	f.normalize()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// Add appends a new position or replaces an existing one by Symbol.
func (f *File) Add(p Position) {
	normalizePosition(&p)
	for i, existing := range f.Positions {
		if existing.Symbol == p.Symbol {
			f.Positions[i] = p
			return
		}
	}
	f.Positions = append(f.Positions, p)
}

// Remove deletes the first position matching symbol. Returns true if a
// position was removed.
func (f *File) Remove(symbol string) bool {
	for i, p := range f.Positions {
		if p.Symbol == symbol {
			f.Positions = append(f.Positions[:i], f.Positions[i+1:]...)
			return true
		}
	}
	return false
}

// Find returns a pointer to the position matching symbol or nil.
func (f *File) Find(symbol string) *Position {
	for i := range f.Positions {
		if f.Positions[i].Symbol == symbol {
			return &f.Positions[i]
		}
	}
	return nil
}

// Update replaces the position matching p.Symbol. Returns true on success.
func (f *File) Update(p Position) bool {
	normalizePosition(&p)
	for i, existing := range f.Positions {
		if existing.Symbol == p.Symbol {
			f.Positions[i] = p
			return true
		}
	}
	return false
}

func (f *File) normalize() {
	for i := range f.Positions {
		normalizePosition(&f.Positions[i])
	}
}

func normalizePosition(p *Position) {
	p.BoughtAt = normalizeBoughtAt(p.BoughtAt, p.AddedAt)
	p.AddedAt = ""
}

func normalizeBoughtAt(boughtAt, legacyAddedAt string) string {
	if v := normalizeDateOnly(boughtAt); v != "" {
		return v
	}
	return normalizeDateOnly(legacyAddedAt)
}

func normalizeDateOnly(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.Format(time.DateOnly)
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.Format(time.DateOnly)
	}
	return s
}
