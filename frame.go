package tern

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// A transmission is, in order:
//
//	sync      4 symbols   fixed tones, the same in every transmission
//	call      1 + n       the length of the call sign, then one symbol per character
//	header    8 symbols   the frame's size and code rate, heavily protected
//	payload   the frame's bytes, six bits per symbol, with the code's parity symbols
//
// Everything after the sync is whitened: each symbol is exclusive-ored with the next
// value of a fixed sequence, so that no content can produce a long run of one tone.

const (
	syncLen   = 4
	headerLen = 8

	// MinCall and MaxCall bound the length of a call sign.
	MinCall = 3
	MaxCall = 12

	// MinFrame and MaxFrame are the shortest and longest payloads, in bytes. The
	// shortest is what fills the smallest code; the longest is what the header's
	// size field can describe.
	MinFrame = 6
	MaxFrame = MaxInfo * 6 / 8
)

// syncTones are the four tones that open every transmission.
var syncTones = [syncLen]uint8{9, 54, 23, 40}

// callAlphabet maps a call sign character to a symbol value by position.
const callAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ/"

// white is the whitening sequence, indexed from the first call symbol.
var white = func() []uint8 {
	r := newRNG(0x7E12)
	w := make([]uint8, 1+MaxCall+headerLen+2*MaxInfo+8)
	for i := range w {
		w[i] = uint8(r.next() >> 13 & 63)
	}
	return w
}()

// callSymbols returns the unwhitened call field: its length, then its characters.
func callSymbols(call string) ([]uint8, error) {
	call = strings.ToUpper(call)
	if len(call) < MinCall || len(call) > MaxCall {
		return nil, fmt.Errorf("tern: call sign %q must be %d to %d characters", call, MinCall, MaxCall)
	}
	out := []uint8{uint8(len(call))}
	for _, ch := range call {
		i := strings.IndexRune(callAlphabet, ch)
		if i < 0 {
			return nil, fmt.Errorf("tern: call sign %q has a character other than A-Z, 0-9 and /", call)
		}
		out = append(out, uint8(i))
	}
	return out, nil
}

// preamble returns the tones of the sync and call field of a transmission from call:
// what a receiver that knows the call sign can search for.
func preamble(call string) ([]uint8, error) {
	cs, err := callSymbols(call)
	if err != nil {
		return nil, err
	}
	out := append([]uint8(nil), syncTones[:]...)
	for i, s := range cs {
		out = append(out, s^white[i])
	}
	return out, nil
}

// The header carries twelve bits as two field elements a and b: the number of
// information symbols less one in the low eleven bits and the rate in the top bit. It
// is sent as the eight values a + b*x for x = 1, alpha, ..., alpha^7. Any two
// different headers differ in at least seven of the eight symbols.
func headerSymbols(k int, fast bool) [headerLen]uint8 {
	v := k - 1
	if fast {
		v |= 1 << 11
	}
	a, b := uint8(v&63), uint8(v>>6)
	var out [headerLen]uint8
	for j := range out {
		out[j] = a ^ mul(b, gfExp[j])
	}
	return out
}

// decodeHeader finds the most likely header given the log-likelihood of each tone in
// each of the eight header symbols. pos is the index of the first header symbol in the
// whitening sequence. It considers only sizes a frame can have.
func decodeHeader(logLike *[headerLen][Tones]float64, pos int) (k int, fast bool, tones [headerLen]uint8) {
	best := math.Inf(-1)
	for v := 0; v < 4096; v++ {
		kk := v&0x7FF + 1
		if kk < MinInfo {
			continue
		}
		a, b := uint8(v&63), uint8(v>>6)
		var s float64
		for j := 0; j < headerLen; j++ {
			s += logLike[j][a^mul(b, gfExp[j])^white[pos+j]]
		}
		if s > best {
			best, k, fast = s, kk, v>>11 == 1
		}
	}
	h := headerSymbols(k, fast)
	for j := range tones {
		tones[j] = h[j] ^ white[pos+j]
	}
	return k, fast, tones
}

// pack turns bytes into six-bit symbols, most significant bit first, padding the last
// symbol with zero bits.
func pack(b []byte) []uint8 {
	out := make([]uint8, (len(b)*8+5)/6)
	for i := range out {
		var v uint8
		for bit := 0; bit < 6; bit++ {
			p := i*6 + bit
			v <<= 1
			if p/8 < len(b) && b[p/8]>>(7-p%8)&1 == 1 {
				v |= 1
			}
		}
		out[i] = v
	}
	return out
}

// unpack is the reverse of pack: n symbols hold floor(6n/8) whole bytes.
func unpack(sym []uint8) []byte {
	out := make([]byte, len(sym)*6/8)
	for i := range out {
		var v byte
		for bit := 0; bit < 8; bit++ {
			p := i*8 + bit
			v = v<<1 | sym[p/6]>>(5-p%6)&1
		}
		out[i] = v
	}
	return out
}

func rings(fast bool) int {
	if fast {
		return 3
	}
	return 1
}

// ErrFrameSize means a frame is too short or too long to send.
var ErrFrameSize = errors.New("tern: frame size out of range")

// Symbols returns the tone of every symbol in a transmission of frame from call. With
// fast set the payload uses the rate 3/4 code in place of rate 1/2.
func Symbols(call string, frame []byte, fast bool) ([]uint8, error) {
	out, err := preamble(call)
	if err != nil {
		return nil, err
	}
	info := pack(frame)
	if len(info) < MinInfo || len(frame) > MaxFrame {
		return nil, fmt.Errorf("%w: %d bytes", ErrFrameSize, len(frame))
	}
	code, err := CodeFor(len(info), rings(fast))
	if err != nil {
		return nil, err
	}
	pos := len(out) - syncLen
	for _, s := range headerSymbols(len(info), fast) {
		out = append(out, s^white[pos])
		pos++
	}
	for _, s := range code.Encode(info) {
		out = append(out, s^white[pos])
		pos++
	}
	return out, nil
}
