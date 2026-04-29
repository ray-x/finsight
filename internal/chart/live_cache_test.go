//go:build livecache

package chart

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestLiveCacheSessionBreaks(t *testing.T) {
	home, _ := os.UserHomeDir()
	granularities := []string{"5d_5m", "5d_15m", "1mo_5m", "1mo_60m", "2d_5m"}
	for _, sym := range []string{"SPY", "QQQ"} {
		for _, g := range granularities {
			label := fmt.Sprintf("%s_%s", sym, g)
			path := fmt.Sprintf("%s/.cache/finsight/%s.json", home, label)
			b, err := os.ReadFile(path)
			if err != nil {
				t.Logf("skip %s: %v", label, err)
				continue
			}
			var raw struct {
				Data struct {
					Timestamps []int64   `json:"Timestamps"`
					Closes     []float64 `json:"Closes"`
				} `json:"data"`
			}
			if err := json.Unmarshal(b, &raw); err != nil {
				t.Fatalf("%s: %v", label, err)
			}
			ts := raw.Data.Timestamps
			closes := raw.Data.Closes
			t.Logf("%s: n=%d tsLen=%d closesLen=%d", label, len(ts), len(ts), len(closes))
			if len(ts) < 2 {
				continue
			}
			iv := inferIntervalSec(ts)
			gaps := detectGaps(ts, iv, 2.5)
			t.Logf("%s: interval=%ds gaps=%d", label, iv, len(gaps))
			for _, gi := range gaps {
				t.Logf("  gap at idx=%d delta=%ds", gi, ts[gi]-ts[gi-1])
			}
			bigDeltas := 0
			for i := 1; i < len(ts); i++ {
				d := ts[i] - ts[i-1]
				if d > iv {
					bigDeltas++
					if bigDeltas <= 5 {
						t.Logf("  non-standard delta idx=%d d=%ds", i, d)
					}
				}
			}
			t.Logf("%s: bigDeltas total=%d", label, bigDeltas)
			out := WithLineSessionBreaks(ts, closes)
			t.Logf("%s: WithLineSessionBreaks: in=%d out=%d (inserted=%d)", label, len(closes), len(out), len(out)-len(closes))
		}
	}
}
