// Package channel simulates an HF radio path for modem audio: white noise, and
// optionally the two-path fading model that HF modems are conventionally tested
// against (Watterson; ITU-R F.1487).
//
// In that model the signal arrives by two paths of equal average strength, the second
// delayed by a fixed time. Each path's gain is an independent complex Gaussian process,
// so its strength follows a Rayleigh distribution, and it varies at a rate set by the
// Doppler spread. The result is fading that is both time-varying and, because of the
// delay, frequency-selective.
package channel

import (
	"fmt"
	"math"
	"math/rand"

	"github.com/edinnen/tern/internal/dsp"
)

// RefBandwidth is the noise bandwidth SNR figures refer to, in Hz. 2.5 kHz is the
// amateur convention for comparing digital modes.
const RefBandwidth = 2500.0

// Model describes a path.
type Model struct {
	Name string
	// DelayMS is the delay of the second path in milliseconds. Zero with a zero
	// Doppler spread means no fading at all.
	DelayMS float64
	// DopplerHz is the Doppler spread: twice the standard deviation of each path's
	// Gaussian Doppler spectrum, which is how ITU-R F.1487 states it.
	DopplerHz float64
	// Paths is 0 for no fading, 1 for a single fading path (flat fading) or 2.
	Paths int
}

// The standard conditions. AWGN is white noise only.
var (
	AWGN     = Model{Name: "awgn"}
	Good     = Model{Name: "good", Paths: 2, DelayMS: 0.5, DopplerHz: 0.1}
	Moderate = Model{Name: "moderate", Paths: 2, DelayMS: 1, DopplerHz: 0.5}
	Poor     = Model{Name: "poor", Paths: 2, DelayMS: 2, DopplerHz: 1}
	// NVIS is the disturbed near-vertical-incidence condition of ITU-R F.1487: a long
	// delay between the paths, as when one-hop and two-hop echoes both arrive.
	NVIS = Model{Name: "nvis", Paths: 2, DelayMS: 7, DopplerHz: 1}
	Flat = Model{Name: "flat", Paths: 1, DopplerHz: 0.5}
)

// Models lists the conditions by name.
var Models = []Model{AWGN, Good, Moderate, Poor, NVIS, Flat}

// Lookup finds a condition by name.
func Lookup(name string) (Model, error) {
	for _, m := range Models {
		if m.Name == name {
			return m, nil
		}
	}
	return Model{}, fmt.Errorf("channel: unknown condition %q (awgn, good, moderate, poor, nvis, flat)", name)
}

func (m Model) String() string {
	if m.Paths == 0 {
		return m.Name + " (white noise only)"
	}
	if m.Paths == 1 {
		return fmt.Sprintf("%s (one fading path, %.1f Hz Doppler spread)", m.Name, m.DopplerHz)
	}
	return fmt.Sprintf("%s (two fading paths %.1f ms apart, %.1f Hz Doppler spread)", m.Name, m.DelayMS, m.DopplerHz)
}

// Apply passes a signal through the path and returns it with lead seconds of noise in
// front and tail seconds behind. rate is the sample rate in Hz. snrDB is the ratio of
// the signal's average power to the noise power in RefBandwidth; with fading it is the
// long-term average, so individual transmissions are stronger or weaker than it says.
// An snrDB of +Inf adds no noise.
func (m Model) Apply(pcm []float32, rate float64, snrDB float64, lead, tail float64, rng *rand.Rand) []float32 {
	x := make([]float64, len(pcm))
	var power float64
	for i, s := range pcm {
		x[i] = float64(s)
		power += x[i] * x[i]
	}
	if len(pcm) > 0 {
		power /= float64(len(pcm))
	}
	if m.Paths > 0 {
		x = m.fade(x, rate, rng)
	}
	before := int(lead * rate)
	out := make([]float32, before+len(x)+int(tail*rate))
	for i, s := range x {
		out[before+i] = float32(s)
	}
	if math.IsInf(snrDB, 1) || power == 0 {
		return out
	}
	// Noise power inside the reference bandwidth is signal power over the SNR; white
	// noise at this sample rate spreads over rate/2, so scale up to the full band.
	sigma := math.Sqrt(power / math.Pow(10, snrDB/10) * (rate / 2 / RefBandwidth))
	for i := range out {
		out[i] += float32(rng.NormFloat64() * sigma)
	}
	return out
}

// fade applies the fading paths. The average output power equals the input power.
func (m Model) fade(x []float64, rate float64, rng *rand.Rand) []float64 {
	re, im := dsp.Analytic(x)
	out := make([]float64, len(x))
	paths := m.Paths
	delay := int(math.Round(m.DelayMS / 1000 * rate))
	scale := 1 / math.Sqrt(float64(paths))
	for p := 0; p < paths; p++ {
		gr, gi := gain(len(x), rate, m.DopplerHz, rng)
		d := p * delay
		for i := d; i < len(x); i++ {
			// Real part of gain times the delayed analytic signal.
			out[i] += scale * (gr[i]*re[i-d] - gi[i]*im[i-d])
		}
	}
	return out
}

// gain returns a complex Gaussian process of unit average power whose Doppler spectrum
// is Gaussian with the given spread (two standard deviations). It is generated at a
// low rate, by filtering white noise, and interpolated up to the sample rate.
func gain(n int, rate, spread float64, rng *rand.Rand) (re, im []float64) {
	re = make([]float64, n)
	im = make([]float64, n)
	if spread <= 0 {
		// A fixed random gain: fading too slow to change during the signal.
		a, b := rng.NormFloat64()/math.Sqrt2, rng.NormFloat64()/math.Sqrt2
		for i := range re {
			re[i], im[i] = a, b
		}
		return re, im
	}
	sigmaF := spread / 2
	// Low rate: 32 samples per hertz of spread keeps the spectrum well inside Nyquist.
	low := math.Max(32*spread, 8)
	// A Gaussian power spectrum exp(-f^2 / 2 sigmaF^2) comes from a Gaussian impulse
	// response with standard deviation 1 / (2 sqrt2 pi sigmaF) seconds.
	sigmaT := 1 / (2 * math.Sqrt2 * math.Pi * sigmaF)
	half := int(math.Ceil(4 * sigmaT * low))
	taps := make([]float64, 2*half+1)
	var norm float64
	for i := range taps {
		t := float64(i-half) / low
		taps[i] = math.Exp(-t * t / (2 * sigmaT * sigmaT))
		norm += taps[i] * taps[i]
	}
	norm = math.Sqrt(norm * 2) // unit power across the real and imaginary parts together
	m := int(float64(n)/rate*low) + 2
	lr := make([]float64, m)
	li := make([]float64, m)
	wr := make([]float64, m+len(taps))
	wi := make([]float64, m+len(taps))
	for i := range wr {
		wr[i], wi[i] = rng.NormFloat64(), rng.NormFloat64()
	}
	for i := 0; i < m; i++ {
		var ar, ai float64
		for k, t := range taps {
			ar += t * wr[i+k]
			ai += t * wi[i+k]
		}
		lr[i], li[i] = ar/norm, ai/norm
	}
	step := low / rate
	for i := range re {
		pos := float64(i) * step
		k := int(pos)
		f := pos - float64(k)
		re[i] = lr[k]*(1-f) + lr[k+1]*f
		im[i] = li[k]*(1-f) + li[k+1]*f
	}
	return re, im
}
