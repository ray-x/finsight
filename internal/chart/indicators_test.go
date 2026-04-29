package chart

import (
	"math"
	"testing"
)

func approx(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

func TestSMA(t *testing.T) {
	in := []float64{1, 2, 3, 4, 5}
	got := SMA(in, 3)
	want := []float64{math.NaN(), math.NaN(), 2, 3, 4}
	for i, v := range want {
		if math.IsNaN(v) {
			if !math.IsNaN(got[i]) {
				t.Fatalf("SMA[%d]=%v want NaN", i, got[i])
			}
			continue
		}
		if !approx(got[i], v, 1e-9) {
			t.Fatalf("SMA[%d]=%v want %v", i, got[i], v)
		}
	}
}

func TestEMASeedIsSMA(t *testing.T) {
	in := []float64{1, 2, 3, 4, 5, 6, 7, 8}
	got := EMA(in, 3)
	// Seed at index 2 = avg(1,2,3) = 2.
	if !approx(got[2], 2, 1e-9) {
		t.Fatalf("EMA seed = %v want 2", got[2])
	}
	// k=0.5; next value = 4*0.5 + 2*0.5 = 3.
	if !approx(got[3], 3, 1e-9) {
		t.Fatalf("EMA[3] = %v want 3", got[3])
	}
}

func TestRSIFlatZero(t *testing.T) {
	// Flat prices → no gain, no loss. Should produce neutral 50.
	in := make([]float64, 30)
	for i := range in {
		in[i] = 100
	}
	got := RSI(in, 14)
	v, idx := LastValid(got)
	if idx < 0 || !approx(v, 50, 1e-6) {
		t.Fatalf("flat RSI last = %v idx=%d want 50", v, idx)
	}
}

func TestRSIMonotonicUp(t *testing.T) {
	// Strictly increasing prices → RSI near 100.
	in := make([]float64, 30)
	for i := range in {
		in[i] = float64(i + 1)
	}
	got := RSI(in, 14)
	v, idx := LastValid(got)
	if idx < 0 || v < 99 {
		t.Fatalf("monotonic-up RSI last = %v idx=%d want near 100", v, idx)
	}
}

func TestBollingerMean(t *testing.T) {
	in := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	u, m, l := BollingerBands(in, 5, 2)
	// Last mid = avg(6..10) = 8.
	if !approx(m[9], 8, 1e-9) {
		t.Fatalf("bb mid[9]=%v want 8", m[9])
	}
	if !(u[9] > m[9] && l[9] < m[9]) {
		t.Fatalf("bb bands not ordered: u=%v m=%v l=%v", u[9], m[9], l[9])
	}
}

func TestMACDShape(t *testing.T) {
	in := make([]float64, 60)
	for i := range in {
		in[i] = float64(i)
	}
	macd, sig, hist := MACD(in, 12, 26, 9)
	if len(macd) != 60 || len(sig) != 60 || len(hist) != 60 {
		t.Fatalf("length mismatch")
	}
	if _, idx := LastValid(sig); idx < 0 {
		t.Fatalf("no valid signal value")
	}
}

func TestStochasticRange(t *testing.T) {
	h := make([]float64, 30)
	l := make([]float64, 30)
	c := make([]float64, 30)
	for i := range c {
		c[i] = float64(i + 1)
		h[i] = c[i] + 0.5
		l[i] = c[i] - 0.5
	}
	k, d := Stochastic(h, l, c, 14, 1, 3)
	kv, _ := LastValid(k)
	dv, _ := LastValid(d)
	if kv < 0 || kv > 100 || dv < 0 || dv > 100 {
		t.Fatalf("stoch out of range: k=%v d=%v", kv, dv)
	}
}

func TestPivotPoints(t *testing.T) {
	p := PivotPoints(110, 90, 100)
	if !approx(p.P, 100, 1e-9) {
		t.Fatalf("pivot P=%v want 100", p.P)
	}
	if !approx(p.R1, 110, 1e-9) || !approx(p.S1, 90, 1e-9) {
		t.Fatalf("pivot R1/S1 wrong: %+v", p)
	}
}

func TestCrossStateBullish(t *testing.T) {
	// fast crosses above slow at the last bar.
	fast := []float64{1, 1, 1, 1, 2}
	slow := []float64{1.5, 1.5, 1.5, 1.5, 1.5}
	dir, ago := CrossState(fast, slow)
	if dir != 1 || ago != 0 {
		t.Fatalf("want bullish cross at 0, got dir=%d ago=%d", dir, ago)
	}
}
