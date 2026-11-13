package tern

import (
	"math/rand"
	"testing"
	"time"

	"github.com/edinnen/tern/channel"
)

// After a transmission has ended, a receiver must not go on reporting that one is
// arriving. A very strong signal used to leave it doing so for up to a minute: other
// alignments of the same signal passed for the start of a second, longer transmission.
func TestNothingArrivingAfterTheEnd(t *testing.T) {
	rng := rand.New(rand.NewSource(23))
	stuck, worst := 0, time.Duration(0)
	const trials = 240
	for i := 0; i < trials; i++ {
		frame := make([]byte, 17+rng.Intn(60))
		rng.Read(frame)
		p := All[i%2] // the two fastest speeds
		pcm, _ := Modulate(p, 1500, "N0CALL", frame, i%3 == 0)
		snr := []float64{40, 25, 10}[i%3]
		audio := channel.AWGN.Apply(pcm, SampleRate, snr, 0.5, 1.0, rng)
		rx, _ := NewReceiver(Config{Calls: []string{"N0CALL"}})
		got := rx.Feed(audio)
		if len(got) != 1 {
			t.Fatalf("trial %d: %d frames decoded", i, len(got))
		}
		if left := rx.Receiving(); left > 0 {
			stuck++
			worst = max(worst, left)
		}
	}
	if stuck > 0 {
		t.Fatalf("%d of %d receivers still report a transmission arriving a second after it ended; the longest for another %v", stuck, trials, worst.Round(time.Second))
	}
}

// Noise alone must not hold a transmitter off, and a real transmission must: it is
// reported as arriving from soon after its header until it ends.
func TestArrivingFollowsTheSignal(t *testing.T) {
	rng := rand.New(rand.NewSource(29))
	rx, _ := NewReceiver(Config{Calls: []string{"N0CALL"}})
	noise := make([]float32, SampleRate/4)
	busy := 0
	const quarters = 4 * 600 // ten minutes
	for i := 0; i < quarters; i++ {
		for j := range noise {
			noise[j] = float32(rng.NormFloat64() * 0.1)
		}
		rx.Feed(noise)
		if rx.Receiving() > 0 {
			busy++
		}
	}
	if busy > quarters/200 {
		t.Errorf("noise alone looked like an arriving transmission %.1f%% of the time", 100*float64(busy)/quarters)
	}

	// A weak, long transmission: -11 dB at the fastest speed.
	frame := make([]byte, 120)
	rng.Read(frame)
	pcm, _ := Modulate(T31, 1500, "N0CALL", frame, false)
	audio := channel.AWGN.Apply(pcm, SampleRate, -11, 0.5, 2, rng)
	rx, _ = NewReceiver(Config{Calls: []string{"N0CALL"}})
	seen, during := 0, 0
	end := SampleRate/2 + len(pcm)
	for at := 0; at < len(audio); at += SampleRate / 4 {
		rx.Feed(audio[at:min(len(audio), at+SampleRate/4)])
		if mid := at + SampleRate/4; mid > 2*SampleRate && mid < end-SampleRate/2 {
			during++
			if rx.Receiving() > 0 {
				seen++
			}
		}
	}
	if seen*10 < during*8 {
		t.Errorf("a weak transmission was reported as arriving in only %d of %d checks made while it was", seen, during)
	}
}
