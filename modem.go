package tern

import (
	"fmt"
	"math"
	"time"
)

// SampleRate is the audio sample rate the modem works at, in Hz.
const SampleRate = 8000

// Profile is a symbol rate. The tones of a profile are spaced by its symbol rate, so
// a slower profile is also a narrower one. Each halving of the symbol rate doubles
// the airtime and lets the signal be 3 dB weaker.
type Profile struct {
	Name   string
	SymLen int // samples per symbol at SampleRate
}

// The profiles, fastest first.
var (
	T31 = Profile{"TERN31", 256} // 31.25 symbols a second, 2 kHz wide
	T16 = Profile{"TERN16", 512} // 15.6 symbols a second, 1 kHz wide
	T8  = Profile{"TERN8", 1024} // 7.8 symbols a second, 500 Hz wide
	T4  = Profile{"TERN4", 2048} // 3.9 symbols a second, 250 Hz wide
	All = []Profile{T31, T16, T8, T4}
)

// ProfileByName finds a profile.
func ProfileByName(name string) (Profile, error) {
	for _, p := range All {
		if p.Name == name {
			return p, nil
		}
	}
	return Profile{}, fmt.Errorf("tern: unknown profile %q", name)
}

// Spacing is the distance between adjacent tones in Hz, which equals the symbol rate.
func (p Profile) Spacing() float64 { return SampleRate / float64(p.SymLen) }

// Width is the span from the lowest tone to the highest plus one spacing, in Hz.
func (p Profile) Width() float64 { return Tones * p.Spacing() }

// base is the frequency of tone 0 when the signal is centred on centre.
func (p Profile) base(centre float64) float64 { return centre - (Tones-1)/2.0*p.Spacing() }

// leadIn is how long the first tone is held before the first symbol starts, so that
// the transmitter and the receiver's gain control settle before anything that matters.
const leadIn = 512 // samples: 64 ms

const ramp = 16 // samples over which the level rises at the start and falls at the end

// Modulate returns the audio (SampleRate, mono, peak 1) of a transmission of frame
// from call: one tone at a time at constant level, with the phase running on
// smoothly from each tone to the next.
func Modulate(p Profile, centre float64, call string, frame []byte, fast bool) ([]float32, error) {
	sym, err := Symbols(call, frame, fast)
	if err != nil {
		return nil, err
	}
	f0 := p.base(centre)
	out := make([]float32, 0, leadIn+len(sym)*p.SymLen)
	var phase float64
	emit := func(tone uint8, n int) {
		step := 2 * math.Pi * (f0 + float64(tone)*p.Spacing()) / SampleRate
		for i := 0; i < n; i++ {
			out = append(out, float32(math.Sin(phase)))
			phase += step
		}
		phase = math.Mod(phase, 2*math.Pi)
	}
	emit(sym[0], leadIn)
	for _, s := range sym {
		emit(s, p.SymLen)
	}
	for i := 0; i < ramp; i++ {
		g := float32(i) / ramp
		out[i] *= g
		out[len(out)-1-i] *= g
	}
	return out, nil
}

// Airtime returns how long a transmission of a frame of n bytes from a call sign of
// the given length lasts.
func Airtime(p Profile, callLen, n int, fast bool) time.Duration {
	k := (n*8 + 5) / 6
	m := (k + rings(fast) - 1) / rings(fast)
	if m < 4 {
		m = 4
	}
	symbols := syncLen + 1 + callLen + headerLen + k + m
	return time.Duration(float64(leadIn+symbols*p.SymLen) / SampleRate * float64(time.Second))
}
