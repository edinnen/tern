package channel

import (
	"math"
	"math/rand"
	"testing"
)

func tone(freq, seconds, rate float64) []float32 {
	out := make([]float32, int(seconds*rate))
	for i := range out {
		out[i] = float32(math.Sin(2 * math.Pi * freq * float64(i) / rate))
	}
	return out
}

func power(x []float32) float64 {
	var p float64
	for _, s := range x {
		p += float64(s) * float64(s)
	}
	return p / float64(len(x))
}

func TestNoiseLevel(t *testing.T) {
	const rate = 8000
	sig := tone(1500, 20, rate)
	rng := rand.New(rand.NewSource(1))
	out := AWGN.Apply(sig, rate, 0, 0, 0, rng)
	// At 0 dB in 2.5 kHz the noise over the whole 4 kHz band has 1.6 times the signal
	// power, so the total is 2.6 times the signal.
	got := power(out) / power(sig)
	if math.Abs(got-2.6) > 0.05 {
		t.Fatalf("total power is %.3f times the signal, want 2.6", got)
	}
	clean := AWGN.Apply(sig, rate, math.Inf(1), 0.5, 1, rng)
	if len(clean) != len(sig)+int(1.5*rate) {
		t.Fatalf("length %d", len(clean))
	}
	if clean[0] != 0 || clean[len(clean)-1] != 0 {
		t.Fatal("lead and tail should be silent without noise")
	}
}

func TestFadingKeepsAveragePower(t *testing.T) {
	const rate = 8000
	sig := tone(1500, 30, rate)
	for _, m := range []Model{Good, Moderate, Poor, NVIS, Flat} {
		var sum float64
		const runs = 40
		for seed := int64(0); seed < runs; seed++ {
			out := m.Apply(sig, rate, math.Inf(1), 0, 0, rand.New(rand.NewSource(seed)))
			sum += power(out) / power(sig)
		}
		if avg := sum / runs; avg < 0.8 || avg > 1.25 {
			t.Errorf("%s: average power ratio %.2f, want about 1", m.Name, avg)
		}
	}
}

// A fading path must actually fade: over a long tone the envelope should spend about
// 10% of the time more than 10 dB below its average (Rayleigh: 1 - exp(-0.1) = 9.5%).
func TestFlatFadingDepth(t *testing.T) {
	const rate = 8000
	sig := tone(1500, 600, rate)
	out := Flat.Apply(sig, rate, math.Inf(1), 0, 0, rand.New(rand.NewSource(7)))
	const block = 400 // 50 ms
	var deep, blocks int
	avg := power(out)
	for i := 0; i+block <= len(out); i += block {
		blocks++
		if power(out[i:i+block]) < avg/10 {
			deep++
		}
	}
	frac := float64(deep) / float64(blocks)
	if frac < 0.06 || frac > 0.14 {
		t.Fatalf("%.1f%% of the time is more than 10 dB down, want about 9.5%%", 100*frac)
	}
}

func TestLookup(t *testing.T) {
	if m, err := Lookup("poor"); err != nil || m.DelayMS != 2 {
		t.Fatalf("poor: %v %v", m, err)
	}
	if _, err := Lookup("nope"); err == nil {
		t.Fatal("unknown name accepted")
	}
}
