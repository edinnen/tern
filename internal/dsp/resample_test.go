package dsp

import (
	"math"
	"math/rand"
	"testing"
)

func tone(freq, rate float64, n int) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = float32(0.5 * math.Sin(2*math.Pi*freq*float64(i)/rate))
	}
	return out
}

// level returns the amplitude of the component at freq, measured over the middle of
// the signal so filter start-up and tail are excluded.
func level(x []float32, freq, rate float64) float64 {
	x = x[len(x)/4 : len(x)*3/4]
	var re, im float64
	for i, v := range x {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(len(x)-1)) // Hann window
		p := 2 * math.Pi * freq * float64(i) / rate
		re += w * float64(v) * math.Cos(p)
		im += w * float64(v) * math.Sin(p)
	}
	return 4 * math.Hypot(re, im) / float64(len(x)) // x2 for one-sided, x2 for the window
}

func dB(ratio float64) float64 { return 20 * math.Log10(ratio) }

func TestUpsampleKeepsToneAndRemovesImages(t *testing.T) {
	for _, f := range []float64{500, 1500, 2500} {
		up := Upsample(tone(f, 8000, 8000))
		if got := level(up, f, 48000); math.Abs(got-0.5) > 0.005 {
			t.Errorf("%v Hz: amplitude %.4f after upsampling, want 0.5", f, got)
		}
		// Zero-stuffing creates images around every multiple of 8 kHz.
		for _, img := range []float64{8000 - f, 8000 + f, 16000 - f, 16000 + f, 24000 - f} {
			if a := dB(level(up, img, 48000) / 0.5); a > -60 {
				t.Errorf("%v Hz: image at %v Hz is only %.0f dB down", f, img, a)
			}
		}
	}
}

func TestDecimateKeepsToneAndRejectsAliases(t *testing.T) {
	for _, f := range []float64{500, 1500, 2500} {
		var d Decimator
		down := d.Process(tone(f, 48000, 48000))
		if got := level(down, f, 8000); math.Abs(got-0.5) > 0.005 {
			t.Errorf("%v Hz: amplitude %.4f after decimation, want 0.5", f, got)
		}
	}
	// Tones above the modem's range would alias into it if they were not filtered.
	for _, f := range []float64{5500, 6500, 9500, 14500, 22000} {
		var d Decimator
		down := d.Process(tone(f, 48000, 48000))
		alias := math.Abs(f - 8000*math.Round(f/8000))
		if a := dB(level(down, alias, 8000) / 0.5); a > -60 {
			t.Errorf("%v Hz aliases to %v Hz only %.0f dB down", f, alias, a)
		}
	}
}

func TestRoundTripPreservesSignal(t *testing.T) {
	in := tone(1500, 8000, 8000)
	var d Decimator
	out := d.Process(Upsample(in))
	// Upsampling delays by the filter's group delay (72 samples at 48 kHz) and the
	// decimator's first output is centred exactly that far in, so the two cancel.
	var worst float64
	for i := 200; i < 7000; i++ {
		worst = math.Max(worst, math.Abs(float64(out[i]-in[i])))
	}
	if worst > 0.002 {
		t.Fatalf("round trip error %.5f", worst)
	}
}

func TestDecimatorIsChunkInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	in := make([]float32, 20000)
	for i := range in {
		in[i] = float32(rng.NormFloat64())
	}
	var whole Decimator
	want := whole.Process(in)
	for _, chunk := range []int{1, 5, 6, 7, 144, 145, 480, 4096} {
		var d Decimator
		var got []float32
		for i := 0; i < len(in); i += chunk {
			got = append(got, d.Process(in[i:min(i+chunk, len(in))])...)
		}
		if len(got) != len(want) {
			t.Fatalf("chunk %d: %d samples, want %d", chunk, len(got), len(want))
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("chunk %d: sample %d differs", chunk, i)
			}
		}
	}
}
