// Package dsp holds the signal processing the modem and its tools share: a Fourier
// transform, and conversion between the modem's 8 kHz and a sound card's 48 kHz.
package dsp

import "math"

// Factor is the ratio between the sound card rate (48 kHz) and the modem rate (8 kHz).
const Factor = 6

// The same low-pass filter serves both directions. At 48 kHz it passes everything
// the modem uses (up to 3 kHz) and is 70 dB down from 4.5 kHz, which removes the
// images created by upsampling and the aliases that decimation would fold back.
//
// The image rejection matters for transmitters that work out the transmit frequency
// by timing zero crossings of the audio: the tone they are given must be clean.
var taps = design(145, 3750.0/48000.0, 6.76)

// design returns a Kaiser-windowed sinc low-pass filter with unity gain at DC.
// cutoff is in cycles per sample.
func design(n int, cutoff, beta float64) []float64 {
	h := make([]float64, n)
	mid := float64(n-1) / 2
	var sum float64
	for i := range h {
		x := float64(i) - mid
		s := 2 * cutoff
		if x != 0 {
			s = math.Sin(2*math.Pi*cutoff*x) / (math.Pi * x)
		}
		r := x / mid
		h[i] = s * bessel0(beta*math.Sqrt(1-r*r)) / bessel0(beta)
		sum += h[i]
	}
	for i := range h {
		h[i] /= sum
	}
	return h
}

// bessel0 is the zeroth-order modified Bessel function of the first kind.
func bessel0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1.0; k < 40; k++ {
		term *= (x / (2 * k)) * (x / (2 * k))
		sum += term
	}
	return sum
}

// Upsample converts a complete 8 kHz signal to 48 kHz. The output is delayed by the
// filter's 1.5 ms group delay and is that much longer than six times the input.
func Upsample(in []float32) []float32 {
	out := make([]float32, len(in)*Factor+len(taps)-1)
	// Zero-stuffing puts input sample i at output position i*Factor; each output
	// sample is the sum over the input samples the filter overlaps there.
	for n := range out {
		var acc float64
		// k runs over filter taps with (n-k) a multiple of Factor.
		for k := n % Factor; k < len(taps); k += Factor {
			i := (n - k) / Factor
			if i < 0 {
				break
			}
			if i < len(in) {
				acc += taps[k] * float64(in[i])
			}
		}
		out[n] = float32(acc * Factor)
	}
	return out
}

// Decimator converts a 48 kHz stream to 8 kHz. It keeps state between calls, so a
// stream may be fed in chunks of any size.
type Decimator struct {
	buf   []float32 // samples not yet fully consumed
	phase int       // input samples to skip before the next output
}

// Process consumes 48 kHz samples and returns the 8 kHz samples now available.
func (d *Decimator) Process(in []float32) []float32 {
	d.buf = append(d.buf, in...)
	n := len(taps)
	var out []float32
	pos := d.phase
	for ; pos+n <= len(d.buf); pos += Factor {
		var acc float64
		w := d.buf[pos : pos+n]
		for k, t := range taps {
			acc += t * float64(w[k])
		}
		out = append(out, float32(acc))
	}
	// Keep the samples the next window still needs.
	if pos > len(d.buf) {
		d.phase = pos - len(d.buf)
		d.buf = d.buf[:0]
	} else {
		d.phase = 0
		d.buf = append(d.buf[:0], d.buf[pos:]...)
	}
	return out
}
