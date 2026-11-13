package tern

import (
	"math/rand"
	"testing"
)

// BenchmarkReceiverIdle measures the cost of listening: one op is one second of
// noise through a receiver listening at every speed.
func BenchmarkReceiverIdle(b *testing.B) {
	rx, _ := NewReceiver(Config{Calls: []string{"N0CALL"}})
	rng := rand.New(rand.NewSource(1))
	noise := make([]float32, SampleRate)
	for i := range noise {
		noise[i] = float32(rng.NormFloat64() * 0.1)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rx.Feed(noise)
	}
}
