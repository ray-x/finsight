package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := defaultConfig()
	if cfg.RefreshInterval != 900 {
		t.Errorf("expected RefreshInterval=900, got %d", cfg.RefreshInterval)
	}
	if cfg.ChartStyle != "candlestick_dotted" {
		t.Errorf("expected ChartStyle=candlestick_dotted, got %s", cfg.ChartStyle)
	}
	if len(cfg.Watchlists) != 1 {
		t.Fatalf("expected 1 default watchlist group, got %d", len(cfg.Watchlists))
	}
	if len(cfg.Watchlists[0].Symbols) != 2 {
		t.Errorf("expected 2 default watchlist items, got %d", len(cfg.Watchlists[0].Symbols))
	}
}

func TestLoadMissing(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if cfg == nil {
		t.Fatal("expected default config, got nil")
	}
	if cfg.RefreshInterval != 900 {
		t.Errorf("expected default RefreshInterval=900, got %d", cfg.RefreshInterval)
	}
}

func TestLoadAndSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-config.yaml")

	cfg := &Config{
		RefreshInterval: 60,
		ChartRange:      "5d",
		ChartInterval:   "15m",
		ChartStyle:      "candlestick",
		Watchlists: []WatchlistGroup{
			{Name: "Test", Symbols: []WatchItem{
				{Symbol: "AAPL", Name: "Apple Inc."},
			}},
		},
	}

	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("config file was not created")
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if loaded.RefreshInterval != 60 {
		t.Errorf("expected RefreshInterval=60, got %d", loaded.RefreshInterval)
	}
	if loaded.ChartStyle != "candlestick" {
		t.Errorf("expected ChartStyle=candlestick, got %s", loaded.ChartStyle)
	}
	if len(loaded.Watchlists) != 1 || loaded.Watchlists[0].Symbols[0].Symbol != "AAPL" {
		t.Errorf("watchlist mismatch: %+v", loaded.Watchlists)
	}
}

func TestLoadExpandsLogFileHome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-config.yaml")
	if err := os.WriteFile(path, []byte("log_file: ~/tmp/finsight.log\n"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir failed: %v", err)
	}
	want := filepath.Join(home, "tmp", "finsight.log")
	if loaded.LogFile != want {
		t.Fatalf("expected expanded log file %q, got %q", want, loaded.LogFile)
	}
}

func TestAddSymbol(t *testing.T) {
	cfg := &Config{
		Watchlists: []WatchlistGroup{
			{Name: "Test", Symbols: nil},
		},
	}
	cfg.AddSymbol(0, "AAPL", "Apple Inc.")
	if len(cfg.Watchlists[0].Symbols) != 1 {
		t.Fatalf("expected 1 item, got %d", len(cfg.Watchlists[0].Symbols))
	}

	// Duplicate should be ignored
	cfg.AddSymbol(0, "AAPL", "Apple Inc.")
	if len(cfg.Watchlists[0].Symbols) != 1 {
		t.Errorf("duplicate add should be ignored, got %d items", len(cfg.Watchlists[0].Symbols))
	}

	cfg.AddSymbol(0, "MSFT", "Microsoft")
	if len(cfg.Watchlists[0].Symbols) != 2 {
		t.Errorf("expected 2 items, got %d", len(cfg.Watchlists[0].Symbols))
	}
}

func TestRemoveSymbol(t *testing.T) {
	cfg := &Config{
		Watchlists: []WatchlistGroup{
			{Name: "Test", Symbols: []WatchItem{
				{Symbol: "AAPL", Name: "Apple"},
				{Symbol: "MSFT", Name: "Microsoft"},
				{Symbol: "GOOG", Name: "Alphabet"},
			}},
		},
	}

	cfg.RemoveSymbol(0, "MSFT")
	if len(cfg.Watchlists[0].Symbols) != 2 {
		t.Fatalf("expected 2 items after remove, got %d", len(cfg.Watchlists[0].Symbols))
	}
	for _, item := range cfg.Watchlists[0].Symbols {
		if item.Symbol == "MSFT" {
			t.Error("MSFT should have been removed")
		}
	}

	// Removing non-existent should be no-op
	cfg.RemoveSymbol(0, "TSLA")
	if len(cfg.Watchlists[0].Symbols) != 2 {
		t.Error("removing non-existent symbol should be no-op")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	path := DefaultConfigPath()
	if path == "" {
		t.Error("DefaultConfigPath should not be empty")
	}
	if filepath.Base(path) != "config.yaml" {
		t.Errorf("expected config.yaml, got %s", filepath.Base(path))
	}
}
