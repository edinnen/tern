package tern

import (
	"math"
	"math/rand"
	"os"
	"strconv"
	"testing"
)

func TestFieldArithmetic(t *testing.T) {
	for a := 1; a < Tones; a++ {
		if mul(uint8(a), inv(uint8(a))) != 1 {
			t.Fatalf("%d has no inverse", a)
		}
		for b := 1; b < Tones; b++ {
			for c := 1; c < Tones; c += 7 {
				l := mul(uint8(a), uint8(b)^uint8(c))
				r := mul(uint8(a), uint8(b)) ^ mul(uint8(a), uint8(c))
				if l != r {
					t.Fatalf("not distributive at %d %d %d", a, b, c)
				}
			}
		}
	}
	var v [Tones]float64
	for i := range v {
		v[i] = float64(i * i % 17)
	}
	w := v
	wht(&w)
	wht(&w)
	for i := range v {
		if math.Abs(w[i]-64*v[i]) > 1e-9 {
			t.Fatal("transform is not its own inverse")
		}
	}
}

func TestEncodeMakesCodewords(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for _, rings := range []int{1, 3} {
		for _, k := range []int{8, 9, 19, 23, 24, 76, 98, 200, 601, 1463} {
			c, err := CodeFor(k, rings)
			if err != nil {
				t.Fatal(err)
			}
			info := make([]uint8, k)
			for i := range info {
				info[i] = uint8(r.Intn(Tones))
			}
			sent := c.Encode(info)
			if len(sent) != c.Sent() {
				t.Fatalf("k=%d: %d symbols sent, want %d", k, len(sent), c.Sent())
			}
			val := make([]uint8, c.nvar)
			for i, v := range c.order {
				val[v] = sent[i]
			}
			if !c.check(val) {
				t.Fatalf("k=%d rings=%d: the encoder's output fails the checks", k, rings)
			}
			for j := 0; j < k; j++ {
				if val[j] != info[j] {
					t.Fatalf("k=%d: information symbol %d is not sent as is", k, j)
				}
			}
			// Noiseless decoding.
			like := make([][Tones]float64, len(sent))
			for i, s := range sent {
				like[i][s] = 1
			}
			got, ok, _ := c.Decode(like, nil, 5)
			if !ok || string(got) != string(info) {
				t.Fatalf("k=%d rings=%d: clean decode failed", k, rings)
			}
		}
	}
	if _, err := CodeFor(4, 1); err == nil {
		t.Fatal("a code for 4 symbols was built")
	}
}

// The construction is part of the format: a change that alters any code breaks
// compatibility with every station already using it.
func TestCodesAreStable(t *testing.T) {
	sum := func(k, rings int) uint32 {
		c, _ := CodeFor(k, rings)
		h := uint32(2166136261)
		mix := func(x int) { h = (h ^ uint32(x)) * 16777619 }
		for i := range c.chkVar {
			for s, v := range c.chkVar[i] {
				mix(int(v))
				mix(int(c.chkCoef[i][s]))
			}
		}
		for _, v := range c.order {
			mix(int(v))
		}
		return h
	}
	got := []uint32{sum(23, 1), sum(98, 1), sum(98, 3)}
	t.Logf("fingerprints: %#x", got)
	if want := os.Getenv("TERN_CODE_FINGERPRINTS"); want == "print" {
		return
	}
	want := codeFingerprints
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("code %d changed: %#x, was %#x", i, got[i], want[i])
		}
	}
}

// channelSymbol simulates one symbol of 64-tone signalling in white noise as a
// receiver that does not know the carrier phase sees it: the energy in each of the 64
// tone filters, in units of the noise power.
func channelSymbol(r *rand.Rand, sent uint8, snr float64) [Tones]float64 {
	var e [Tones]float64
	for k := range e {
		re, im := r.NormFloat64()/math.Sqrt2, r.NormFloat64()/math.Sqrt2
		if k == int(sent) {
			re += math.Sqrt(snr)
		}
		e[k] = re*re + im*im
	}
	return e
}

func frameErrors(k, rings int, snrDB float64, trials int, seed int64) int {
	c, _ := CodeFor(k, rings)
	r := rand.New(rand.NewSource(seed))
	snr := math.Pow(10, snrDB/10)
	bad := 0
	for n := 0; n < trials; n++ {
		info := make([]uint8, k)
		for i := range info {
			info[i] = uint8(r.Intn(Tones))
		}
		sent := c.Encode(info)
		like := make([][Tones]float64, len(sent))
		for i, s := range sent {
			e := channelSymbol(r, s, snr)
			like[i] = likelihoods(&e, snr)
		}
		got, ok, _ := c.Decode(like, nil, 60)
		if !ok || string(got) != string(info) {
			bad++
		}
	}
	return bad
}

func TestDecodesThroughNoise(t *testing.T) {
	// 23 information symbols is a 17-byte frame. At 10 dB per symbol nearly every
	// frame should decode; at 3 dB hardly any should.
	if bad := frameErrors(23, 1, 10, 200, 1); bad > 6 {
		t.Errorf("%d of 200 frames lost at 10 dB per symbol", bad)
	}
	if bad := frameErrors(23, 1, 3, 100, 2); bad < 80 {
		t.Errorf("only %d of 100 frames lost at 3 dB per symbol; the test channel is too kind", bad)
	}
}

// TestCodeSweep prints the frame loss against signal level. Run it with
// TERN_SWEEP=trials go test -run CodeSweep -v ./internal/tern/
func TestCodeSweep(t *testing.T) {
	trials, _ := strconv.Atoi(os.Getenv("TERN_SWEEP"))
	if trials == 0 {
		t.Skip("set TERN_SWEEP to a number of trials")
	}
	for _, rings := range []int{1, 3} {
		for _, k := range []int{23, 76, 98, 400} {
			line := ""
			for snr := 5.0; snr <= 12; snr += 1 {
				bad := frameErrors(k, rings, snr, trials, int64(k*100)+int64(snr))
				line += " " + strconv.FormatFloat(snr, 'f', 0, 64) + "dB:" + strconv.FormatFloat(100*float64(bad)/float64(trials), 'f', 1, 64) + "%"
			}
			t.Logf("rings=%d k=%4d lost:%s", rings, k, line)
		}
	}
}
