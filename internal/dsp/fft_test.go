package dsp

import (
	"math"
	"testing"
)

func TestFFTFindsATone(t *testing.T) {
	const n = 256
	re := make([]float64, n)
	im := make([]float64, n)
	for i := range re {
		re[i] = math.Cos(2 * math.Pi * 10 * float64(i) / n)
	}
	f := NewFFT(n)
	f.Transform(re, im, false)
	for k := 0; k < n; k++ {
		mag := math.Hypot(re[k], im[k])
		want := 0.0
		if k == 10 || k == n-10 {
			want = n / 2
		}
		if math.Abs(mag-want) > 1e-9 {
			t.Fatalf("bin %d: %g, want %g", k, mag, want)
		}
	}
	f.Transform(re, im, true)
	for i := range re {
		if want := math.Cos(2*math.Pi*10*float64(i)/n) * n; math.Abs(re[i]-want) > 1e-9 || math.Abs(im[i]) > 1e-9 {
			t.Fatalf("inverse at %d: %g%+gi, want %g", i, re[i], im[i], want)
		}
	}
}

func TestAnalytic(t *testing.T) {
	x := make([]float64, 1000)
	for i := range x {
		x[i] = math.Cos(2 * math.Pi * 0.05 * float64(i))
	}
	re, im := Analytic(x)
	for i := 100; i < 900; i++ {
		if math.Abs(re[i]-x[i]) > 1e-6 {
			t.Fatalf("real part changed at %d", i)
		}
		if want := math.Sin(2 * math.Pi * 0.05 * float64(i)); math.Abs(im[i]-want) > 0.02 {
			t.Fatalf("imaginary part at %d: %g, want %g", i, im[i], want)
		}
	}
}
