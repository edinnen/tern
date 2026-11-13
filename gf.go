// Package tern is a sound modem for short binary messages over HF radio. It sends one
// tone at a time at constant level, so a transmitter with no linear amplifier can
// send it, and it is built for weak and fading signals.
//
// A transmission is a run of symbols, each one of 64 tones. One symbol carries six
// bits, which is also one element of the 64-element field the error-correcting code
// works in, so the decoder is handed exactly what the demodulator measured: how
// strongly each of the 64 tones was heard. SPEC.md is the public description of the
// format.
//
// Modulate turns a payload into audio; a Receiver finds and decodes transmissions in
// a stream of audio. Audio is 8000 samples a second, mono.
package tern

// Tones is the number of tones, and the size of the code's symbol alphabet.
const Tones = 64

// Arithmetic in GF(64), built on the primitive polynomial x^6 + x + 1. Addition is
// exclusive-or.
var (
	gfExp [126]uint8
	gfLog [Tones]uint8
	// gfMul[a][b] is the product a*b.
	gfMul [Tones][Tones]uint8
)

func init() {
	x := 1
	for i := 0; i < 63; i++ {
		gfExp[i] = uint8(x)
		gfExp[i+63] = uint8(x)
		gfLog[x] = uint8(i)
		x <<= 1
		if x&64 != 0 {
			x ^= 0x43
		}
	}
	for a := 1; a < Tones; a++ {
		for b := 1; b < Tones; b++ {
			gfMul[a][b] = gfExp[int(gfLog[a])+int(gfLog[b])]
		}
	}
}

func mul(a, b uint8) uint8 { return gfMul[a][b] }

// inv returns the multiplicative inverse of a non-zero element.
func inv(a uint8) uint8 { return gfExp[63-int(gfLog[a])] }

func div(a, b uint8) uint8 { return gfMul[a][inv(b)] }

// wht is the Walsh-Hadamard transform of 64 values, in place. Applying it twice gives
// the input multiplied by 64. It turns the convolution that a parity check performs
// on probability vectors over GF(64) into a pointwise product.
func wht(v *[Tones]float64) {
	for h := 1; h < Tones; h <<= 1 {
		for i := 0; i < Tones; i += h << 1 {
			for j := i; j < i+h; j++ {
				a, b := v[j], v[j+h]
				v[j], v[j+h] = a+b, a-b
			}
		}
	}
}

// rng is the generator every pseudo-random choice in the format is drawn from
// (xorshift32), so that two implementations build identical codes and sequences.
type rng uint32

func newRNG(seed uint32) *rng {
	r := rng(seed*2654435761 + 0x9E3779B9)
	if r == 0 {
		r = 1
	}
	for i := 0; i < 8; i++ {
		r.next()
	}
	return &r
}

func (r *rng) next() uint32 {
	x := uint32(*r)
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*r = rng(x)
	return x
}

// intn returns a value in 0..n-1.
func (r *rng) intn(n int) int { return int(uint64(r.next()) * uint64(n) >> 32) }

// shuffle returns a pseudo-random ordering of 0..n-1.
func (r *rng) shuffle(n int) []int32 {
	p := make([]int32, n)
	for i := range p {
		p[i] = int32(i)
	}
	for i := n - 1; i > 0; i-- {
		j := r.intn(i + 1)
		p[i], p[j] = p[j], p[i]
	}
	return p
}
