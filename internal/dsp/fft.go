package dsp

import (
	"math"
	"math/bits"
	"sync"
)

// FFT is a radix-2 fast Fourier transform of one fixed size.
type FFT struct {
	n   int
	rev []int
	cos []float64
	sin []float64
}

var (
	fftMu    sync.Mutex
	fftPlans = map[int]*FFT{}
)

// NewFFT returns a transform of n points; n must be a power of two. Plans are shared
// and safe for concurrent use.
func NewFFT(n int) *FFT {
	if n < 2 || n&(n-1) != 0 {
		panic("dsp: FFT size must be a power of two")
	}
	fftMu.Lock()
	defer fftMu.Unlock()
	if f := fftPlans[n]; f != nil {
		return f
	}
	f := &FFT{n: n, rev: make([]int, n), cos: make([]float64, n/2), sin: make([]float64, n/2)}
	shift := bits.UintSize - bits.Len(uint(n-1))
	for i := range f.rev {
		f.rev[i] = int(bits.Reverse(uint(i)) >> shift)
	}
	for i := range f.cos {
		a := 2 * math.Pi * float64(i) / float64(n)
		f.cos[i], f.sin[i] = math.Cos(a), math.Sin(a)
	}
	fftPlans[n] = f
	return f
}

// Size returns the number of points.
func (f *FFT) Size() int { return f.n }

// Transform replaces re and im, each of length Size, with their discrete Fourier
// transform: X[k] = sum x[t] exp(-2 pi i k t / n). With inverse set it computes the
// unnormalised inverse (the caller divides by n).
func (f *FFT) Transform(re, im []float64, inverse bool) {
	n := f.n
	for i, j := range f.rev {
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := n / size
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				wr, wi := f.cos[k*step], -f.sin[k*step]
				if inverse {
					wi = -wi
				}
				a, b := start+k, start+k+half
				tr := re[b]*wr - im[b]*wi
				ti := re[b]*wi + im[b]*wr
				re[b], im[b] = re[a]-tr, im[a]-ti
				re[a], im[a] = re[a]+tr, im[a]+ti
			}
		}
	}
}

// Analytic returns the analytic signal of x: a complex signal whose real part is x and
// whose spectrum has no negative frequencies. Multiplying it by a complex gain scales
// and phase-shifts every frequency in x alike, which is how a fading path acts on a
// radio signal.
func Analytic(x []float64) (re, im []float64) {
	n := 1
	for n < len(x) {
		n <<= 1
	}
	re = make([]float64, n)
	im = make([]float64, n)
	copy(re, x)
	f := NewFFT(n)
	f.Transform(re, im, false)
	for k := 1; k < n/2; k++ {
		re[k] *= 2
		im[k] *= 2
	}
	for k := n/2 + 1; k < n; k++ {
		re[k], im[k] = 0, 0
	}
	f.Transform(re, im, true)
	inv := 1 / float64(n)
	for i := range re {
		re[i] *= inv
		im[i] *= inv
	}
	return re[:len(x)], im[:len(x)]
}
