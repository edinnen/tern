package tern

import (
	"bytes"
	"math"
	"math/rand"
	"testing"

	"github.com/edinnen/tern/channel"
)

func TestPackUnpack(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	for n := 6; n < 200; n++ {
		b := make([]byte, n)
		r.Read(b)
		if got := unpack(pack(b)); !bytes.Equal(got, b) {
			t.Fatalf("%d bytes did not survive", n)
		}
	}
}

func TestHeader(t *testing.T) {
	for _, k := range []int{8, 23, 64, 98, 1463, 2048} {
		for _, fast := range []bool{false, true} {
			h := headerSymbols(k, fast)
			var ll [headerLen][Tones]float64
			for j := range ll {
				for x := range ll[j] {
					ll[j][x] = -5
				}
				ll[j][h[j]^white[7+j]] = 0
			}
			// Two of the eight symbols wiped out.
			ll[2], ll[5] = [Tones]float64{}, [Tones]float64{}
			gk, gf, _ := decodeHeader(&ll, 7)
			if gk != k || gf != fast {
				t.Fatalf("header %d/%v read as %d/%v", k, fast, gk, gf)
			}
		}
	}
}

func receive(t *testing.T, pcm []float32, calls ...string) []Frame {
	t.Helper()
	rx, err := NewReceiver(Config{Calls: calls})
	if err != nil {
		t.Fatal(err)
	}
	var out []Frame
	// Feed in uneven pieces, as a sound card would.
	for len(pcm) > 0 {
		n := min(len(pcm), 777)
		out = append(out, rx.Feed(pcm[:n])...)
		pcm = pcm[n:]
	}
	return out
}

func TestCleanRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for _, p := range All {
		for _, fast := range []bool{false, true} {
			for _, n := range []int{17, 57, 73} {
				if p.SymLen > 512 && n > 17 {
					continue // keep the test quick
				}
				frame := make([]byte, n)
				r.Read(frame)
				pcm, err := Modulate(p, 1500, "N0CALL", frame, fast)
				if err != nil {
					t.Fatal(err)
				}
				if want := Airtime(p, 6, n, fast); math.Abs(float64(len(pcm))/SampleRate-want.Seconds()) > 0.001 {
					t.Fatalf("airtime %v, signal is %.3f s", want, float64(len(pcm))/SampleRate)
				}
				rng := rand.New(rand.NewSource(int64(n)))
				got := receive(t, channel.AWGN.Apply(pcm, SampleRate, 20, 0.7, 1.5, rng), "N0CALL")
				if len(got) != 1 || !bytes.Equal(got[0].Data, frame) || got[0].Profile != p || got[0].Fast != fast || got[0].Call != "N0CALL" {
					t.Fatalf("%s fast=%v %d bytes: got %d frames %+v", p.Name, fast, n, len(got), got)
				}
				if math.Abs(got[0].SNR-20) > 3 || math.Abs(got[0].Offset) > 1 {
					t.Errorf("%s: reported %.1f dB and %+.1f Hz for a 20 dB signal on frequency", p.Name, got[0].SNR, got[0].Offset)
				}
			}
		}
	}
}

func TestOffFrequencyAndOtherCalls(t *testing.T) {
	frame := []byte("a frame of twenty-four b")
	pcm, _ := Modulate(T31, 1500+37, "N0CALL/P", frame, false)
	rng := rand.New(rand.NewSource(9))
	noisy := channel.AWGN.Apply(pcm, SampleRate, 0, 1, 1.5, rng)
	got := receive(t, noisy, "N0CALL", "N0CALL/P")
	if len(got) != 1 || !bytes.Equal(got[0].Data, frame) || got[0].Call != "N0CALL/P" {
		t.Fatalf("got %+v", got)
	}
	if math.Abs(got[0].Offset-37) > 2 {
		t.Errorf("offset reported as %+.1f Hz, want +37", got[0].Offset)
	}
	// A station not listening for that call hears nothing.
	if got := receive(t, noisy, "N0CALL"); len(got) != 0 {
		t.Fatalf("heard a call it was not listening for: %+v", got)
	}
}

func TestNoiseAloneYieldsNothing(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	noise := make([]float32, 120*SampleRate)
	for i := range noise {
		noise[i] = float32(rng.NormFloat64() * 0.1)
	}
	if got := receive(t, noise, "N0CALL"); len(got) != 0 {
		t.Fatalf("decoded %d frames from noise", len(got))
	}
}

func TestBackToBack(t *testing.T) {
	a, _ := Modulate(T31, 1500, "N0CALL", []byte("first frame, seventeen"), false)
	b, _ := Modulate(T16, 1500, "N0CALL", []byte("second frame, slower one"), true)
	gap := make([]float32, SampleRate/2)
	pcm := append(append(append([]float32{}, a...), gap...), b...)
	rng := rand.New(rand.NewSource(13))
	got := receive(t, channel.AWGN.Apply(pcm, SampleRate, 3, 1, 2, rng), "N0CALL")
	if len(got) != 2 || string(got[0].Data) != "first frame, seventeen" || string(got[1].Data) != "second frame, slower one" {
		t.Fatalf("got %+v", got)
	}
}

func TestAnyCallSign(t *testing.T) {
	frame := []byte("heard without knowing who")
	pcm, _ := Modulate(T16, 1500-12, "N0CALL/P", frame, false)
	noisy := channel.AWGN.Apply(pcm, SampleRate, -8, 1, 2, rand.New(rand.NewSource(17)))
	got := receive(t, noisy) // no call signs listed: listen for anyone
	if len(got) != 1 || !bytes.Equal(got[0].Data, frame) || got[0].Call != "N0CALL/P" {
		t.Fatalf("got %+v", got)
	}
	// Listed and unlisted together: the transmission is still reported once.
	rx, _ := NewReceiver(Config{Calls: []string{"N0CALL/P"}, AnyCall: true})
	if got := rx.Feed(noisy); len(got) != 1 {
		t.Fatalf("%d frames with the call sign both listed and unlisted", len(got))
	}
}

func TestNoiseAloneYieldsNothingForAnyCall(t *testing.T) {
	rng := rand.New(rand.NewSource(19))
	noise := make([]float32, 120*SampleRate)
	for i := range noise {
		noise[i] = float32(rng.NormFloat64() * 0.1)
	}
	if got := receive(t, noise); len(got) != 0 {
		t.Fatalf("decoded %d frames from noise", len(got))
	}
}
