// Package chart: indicator computations (pure math, no rendering).
//
// All functions return slices the same length as their primary input.
// Warmup values (before enough data is available) are filled with NaN
// so callers can `math.IsNaN(v)` to skip them. Inputs are never
// mutated; slices are copied where necessary.
package chart

import "math"

// SMA returns the simple moving average of values over the given period.
// Result[i] = avg(values[i-period+1 .. i]). First (period-1) entries are NaN.
func SMA(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	if period <= 0 || len(values) == 0 {
		for i := range out {
			out[i] = math.NaN()
		}
		return out
	}
	var sum float64
	for i, v := range values {
		sum += v
		if i >= period {
			sum -= values[i-period]
		}
		if i+1 < period {
			out[i] = math.NaN()
		} else {
			out[i] = sum / float64(period)
		}
	}
	return out
}

// EMA returns the exponential moving average of values over the given period.
// Uses a Wilder-style seed: the first EMA value at index period-1 is the
// SMA of the first `period` values; subsequent values apply the standard
// 2/(period+1) smoothing factor. Warmup entries are NaN.
func EMA(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	if period <= 0 || len(values) < period {
		for i := range out {
			out[i] = math.NaN()
		}
		return out
	}
	k := 2.0 / float64(period+1)
	// Seed: SMA of first `period` values.
	var seed float64
	for i := 0; i < period; i++ {
		seed += values[i]
		out[i] = math.NaN()
	}
	seed /= float64(period)
	out[period-1] = seed
	prev := seed
	for i := period; i < len(values); i++ {
		prev = values[i]*k + prev*(1-k)
		out[i] = prev
	}
	return out
}

// RSI returns the Relative Strength Index using Wilder's smoothing.
// period is typically 14. Result is in [0,100]. Warmup entries are NaN.
func RSI(closes []float64, period int) []float64 {
	out := make([]float64, len(closes))
	if period <= 0 || len(closes) <= period {
		for i := range out {
			out[i] = math.NaN()
		}
		return out
	}
	out[0] = math.NaN()
	var gainSum, lossSum float64
	for i := 1; i <= period; i++ {
		diff := closes[i] - closes[i-1]
		if diff >= 0 {
			gainSum += diff
		} else {
			lossSum -= diff
		}
		out[i] = math.NaN()
	}
	avgGain := gainSum / float64(period)
	avgLoss := lossSum / float64(period)
	out[period] = rsiFromAvg(avgGain, avgLoss)
	for i := period + 1; i < len(closes); i++ {
		diff := closes[i] - closes[i-1]
		gain, loss := 0.0, 0.0
		if diff >= 0 {
			gain = diff
		} else {
			loss = -diff
		}
		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
		out[i] = rsiFromAvg(avgGain, avgLoss)
	}
	return out
}

func rsiFromAvg(avgGain, avgLoss float64) float64 {
	if avgLoss == 0 {
		if avgGain == 0 {
			return 50
		}
		return 100
	}
	rs := avgGain / avgLoss
	return 100 - 100/(1+rs)
}

// BollingerBands returns (upper, middle, lower) bands.
// middle = SMA(period). upper/lower = middle ± stdDev * σ.
func BollingerBands(closes []float64, period int, stdDev float64) (upper, middle, lower []float64) {
	middle = SMA(closes, period)
	upper = make([]float64, len(closes))
	lower = make([]float64, len(closes))
	for i := range closes {
		if i+1 < period {
			upper[i] = math.NaN()
			lower[i] = math.NaN()
			continue
		}
		mean := middle[i]
		var sq float64
		for j := i - period + 1; j <= i; j++ {
			d := closes[j] - mean
			sq += d * d
		}
		sd := math.Sqrt(sq / float64(period))
		upper[i] = mean + stdDev*sd
		lower[i] = mean - stdDev*sd
	}
	return upper, middle, lower
}

// MACD returns (macdLine, signalLine, histogram).
// Defaults: fast=12, slow=26, signal=9. histogram = macd - signal.
func MACD(closes []float64, fast, slow, signal int) (macd, sig, hist []float64) {
	if fast <= 0 {
		fast = 12
	}
	if slow <= 0 {
		slow = 26
	}
	if signal <= 0 {
		signal = 9
	}
	emaFast := EMA(closes, fast)
	emaSlow := EMA(closes, slow)
	macd = make([]float64, len(closes))
	for i := range closes {
		if math.IsNaN(emaFast[i]) || math.IsNaN(emaSlow[i]) {
			macd[i] = math.NaN()
		} else {
			macd[i] = emaFast[i] - emaSlow[i]
		}
	}
	// Signal = EMA of macd, skipping NaN warmup.
	firstValid := -1
	for i, v := range macd {
		if !math.IsNaN(v) {
			firstValid = i
			break
		}
	}
	sig = make([]float64, len(closes))
	hist = make([]float64, len(closes))
	for i := range closes {
		sig[i] = math.NaN()
		hist[i] = math.NaN()
	}
	if firstValid < 0 || firstValid+signal > len(closes) {
		return macd, sig, hist
	}
	sub := macd[firstValid:]
	sigSub := EMA(sub, signal)
	for i, v := range sigSub {
		idx := firstValid + i
		sig[idx] = v
		if !math.IsNaN(v) && !math.IsNaN(macd[idx]) {
			hist[idx] = macd[idx] - v
		}
	}
	return macd, sig, hist
}

// Stochastic returns (%K, %D).
// %K raw = 100 * (close - lowest(kPeriod)) / (highest(kPeriod) - lowest(kPeriod))
// %K smoothed = SMA of raw %K over kSmooth
// %D = SMA of smoothed %K over dSmooth
// Defaults: kPeriod=14, kSmooth=1 (fast), dSmooth=3.
func Stochastic(highs, lows, closes []float64, kPeriod, kSmooth, dSmooth int) (k, d []float64) {
	n := len(closes)
	if n == 0 || kPeriod <= 0 {
		return nil, nil
	}
	if kSmooth <= 0 {
		kSmooth = 1
	}
	if dSmooth <= 0 {
		dSmooth = 3
	}
	raw := make([]float64, n)
	for i := 0; i < n; i++ {
		if i+1 < kPeriod {
			raw[i] = math.NaN()
			continue
		}
		hi, lo := highs[i], lows[i]
		for j := i - kPeriod + 1; j <= i; j++ {
			if highs[j] > hi {
				hi = highs[j]
			}
			if lows[j] < lo {
				lo = lows[j]
			}
		}
		rng := hi - lo
		if rng == 0 {
			raw[i] = 50
		} else {
			raw[i] = 100 * (closes[i] - lo) / rng
		}
	}
	if kSmooth == 1 {
		k = raw
	} else {
		k = smaSkipNaN(raw, kSmooth)
	}
	d = smaSkipNaN(k, dSmooth)
	return k, d
}

// smaSkipNaN is an SMA that treats NaN as "not yet available".
// Output is NaN until there are `period` consecutive valid inputs.
func smaSkipNaN(values []float64, period int) []float64 {
	out := make([]float64, len(values))
	buf := make([]float64, 0, period)
	var sum float64
	for i, v := range values {
		if math.IsNaN(v) {
			buf = buf[:0]
			sum = 0
			out[i] = math.NaN()
			continue
		}
		buf = append(buf, v)
		sum += v
		if len(buf) > period {
			sum -= buf[0]
			buf = buf[1:]
		}
		if len(buf) < period {
			out[i] = math.NaN()
		} else {
			out[i] = sum / float64(period)
		}
	}
	return out
}

// Pivot represents classic pivot point levels derived from the previous
// period's high/low/close.
type Pivot struct {
	P  float64
	R1 float64
	R2 float64
	R3 float64
	S1 float64
	S2 float64
	S3 float64
}

// PivotPoints returns classic pivot levels from the given prior-period HLC.
func PivotPoints(prevHigh, prevLow, prevClose float64) Pivot {
	p := (prevHigh + prevLow + prevClose) / 3
	return Pivot{
		P:  p,
		R1: 2*p - prevLow,
		S1: 2*p - prevHigh,
		R2: p + (prevHigh - prevLow),
		S2: p - (prevHigh - prevLow),
		R3: prevHigh + 2*(p-prevLow),
		S3: prevLow - 2*(prevHigh-p),
	}
}

// LastValid returns the last non-NaN value in s and its index, or
// (NaN, -1) if none exists.
func LastValid(s []float64) (float64, int) {
	for i := len(s) - 1; i >= 0; i-- {
		if !math.IsNaN(s[i]) {
			return s[i], i
		}
	}
	return math.NaN(), -1
}

// CrossState reports the most recent crossover between two series.
// Returns 1 if `fast` most recently crossed above `slow` (bullish),
// -1 if fast crossed below slow (bearish), and 0 if no crossover is
// visible in the common valid range. barsAgo is how many bars back the
// crossover occurred (0 = on the most recent bar).
func CrossState(fast, slow []float64) (dir int, barsAgo int) {
	n := len(fast)
	if n == 0 || n != len(slow) {
		return 0, -1
	}
	for i := n - 1; i > 0; i-- {
		if math.IsNaN(fast[i]) || math.IsNaN(slow[i]) ||
			math.IsNaN(fast[i-1]) || math.IsNaN(slow[i-1]) {
			continue
		}
		prev := fast[i-1] - slow[i-1]
		cur := fast[i] - slow[i]
		if prev <= 0 && cur > 0 {
			return 1, n - 1 - i
		}
		if prev >= 0 && cur < 0 {
			return -1, n - 1 - i
		}
	}
	return 0, -1
}
