package portfolio

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileReturnsEmpty(t *testing.T) {
	f, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(f.Positions) != 0 {
		t.Errorf("expected empty positions, got %d", len(f.Positions))
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portfolio.yaml")
	in := &File{Positions: []Position{
		{Symbol: "AAPL", Position: 10, OpenPrice: 150.5, Note: "core", BoughtAt: "2026-04-30"},
		{Symbol: "NVDA", Position: 2.5},
	}}
	if err := Save(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("expected 0600 perm, got %o", info.Mode().Perm())
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out.Positions) != 2 {
		t.Fatalf("expected 2 positions, got %d", len(out.Positions))
	}
	if out.Positions[0].Symbol != "AAPL" || out.Positions[0].Position != 10 || out.Positions[0].OpenPrice != 150.5 {
		t.Errorf("AAPL round-trip mismatch: %+v", out.Positions[0])
	}
	if out.Positions[0].BoughtAt != "2026-04-30" {
		t.Errorf("AAPL bought_at mismatch: %+v", out.Positions[0])
	}
	if out.Positions[1].Symbol != "NVDA" || out.Positions[1].OpenPrice != 0 {
		t.Errorf("NVDA round-trip mismatch: %+v", out.Positions[1])
	}
}

func TestLoadMigratesLegacyAddedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portfolio.yaml")
	content := []byte("positions:\n  - symbol: AAPL\n    position: 10\n    added_at: 2026-04-15T13:45:00Z\n")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}

	out, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(out.Positions) != 1 {
		t.Fatalf("expected 1 position, got %d", len(out.Positions))
	}
	if out.Positions[0].BoughtAt != "2026-04-15" {
		t.Fatalf("expected migrated bought_at, got %+v", out.Positions[0])
	}
	if out.Positions[0].AddedAt != "" {
		t.Fatalf("expected legacy added_at cleared, got %+v", out.Positions[0])
	}
}

func TestSaveOmitsLegacyAddedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portfolio.yaml")
	in := &File{Positions: []Position{{
		Symbol:   "AAPL",
		Position: 10,
		AddedAt:  "2026-04-15T13:45:00Z",
	}}}
	if err := Save(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "added_at:") {
		t.Fatalf("expected legacy added_at to be omitted, got:\n%s", text)
	}
	if !strings.Contains(text, "bought_at: \"2026-04-15\"") {
		t.Fatalf("expected bought_at to be written, got:\n%s", text)
	}
}

func TestAddReplacesExisting(t *testing.T) {
	f := &File{}
	f.Add(Position{Symbol: "AAPL", Position: 10})
	f.Add(Position{Symbol: "AAPL", Position: 15, OpenPrice: 180})
	if len(f.Positions) != 1 {
		t.Fatalf("expected dedupe, got %d", len(f.Positions))
	}
	if f.Positions[0].Position != 15 || f.Positions[0].OpenPrice != 180 {
		t.Errorf("expected replacement, got %+v", f.Positions[0])
	}
}

func TestRemove(t *testing.T) {
	f := &File{Positions: []Position{
		{Symbol: "AAPL"}, {Symbol: "NVDA"}, {Symbol: "MSFT"},
	}}
	if !f.Remove("NVDA") {
		t.Fatal("expected remove to succeed")
	}
	if len(f.Positions) != 2 || f.Positions[0].Symbol != "AAPL" || f.Positions[1].Symbol != "MSFT" {
		t.Errorf("unexpected state: %+v", f.Positions)
	}
	if f.Remove("ZZZ") {
		t.Error("expected remove on missing symbol to return false")
	}
}

func TestFindAndUpdate(t *testing.T) {
	f := &File{Positions: []Position{{Symbol: "AAPL", Position: 10}}}
	p := f.Find("AAPL")
	if p == nil || p.Position != 10 {
		t.Fatalf("find failed: %+v", p)
	}
	if !f.Update(Position{Symbol: "AAPL", Position: 20, OpenPrice: 200}) {
		t.Fatal("update failed")
	}
	if f.Positions[0].Position != 20 || f.Positions[0].OpenPrice != 200 {
		t.Errorf("update not applied: %+v", f.Positions[0])
	}
	if f.Update(Position{Symbol: "ZZZ"}) {
		t.Error("expected update on missing symbol to return false")
	}
}
