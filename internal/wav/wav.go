// Package wav reads and writes mono 16-bit PCM WAV files, for bench recordings and
// test signals.
package wav

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
)

// Write stores samples (-1..1) as a mono 16-bit WAV file at the given rate.
func Write(path string, rate int, samples []float32) error {
	data := make([]byte, 44+2*len(samples))
	copy(data[0:], "RIFF")
	binary.LittleEndian.PutUint32(data[4:], uint32(36+2*len(samples)))
	copy(data[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(data[16:], 16)
	binary.LittleEndian.PutUint16(data[20:], 1) // PCM
	binary.LittleEndian.PutUint16(data[22:], 1) // mono
	binary.LittleEndian.PutUint32(data[24:], uint32(rate))
	binary.LittleEndian.PutUint32(data[28:], uint32(2*rate))
	binary.LittleEndian.PutUint16(data[32:], 2)
	binary.LittleEndian.PutUint16(data[34:], 16)
	copy(data[36:], "data")
	binary.LittleEndian.PutUint32(data[40:], uint32(2*len(samples)))
	for i, s := range samples {
		v := math.Round(float64(s) * 32767)
		v = math.Max(-32768, math.Min(32767, v))
		binary.LittleEndian.PutUint16(data[44+2*i:], uint16(int16(v)))
	}
	return os.WriteFile(path, data, 0o644)
}

// Read loads a 16-bit PCM WAV file. Stereo files are reduced to their first channel.
func Read(path string) (rate int, samples []float32, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, nil, err
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return 0, nil, errors.New("wav: not a WAV file")
	}
	channels := 0
	for pos := 12; pos+8 <= len(b); {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4:]))
		body := b[pos+8:]
		if size > len(body) {
			size = len(body)
		}
		switch id {
		case "fmt ":
			if size < 16 {
				return 0, nil, errors.New("wav: short format chunk")
			}
			if f := binary.LittleEndian.Uint16(body[0:]); f != 1 {
				return 0, nil, fmt.Errorf("wav: format %d is not PCM", f)
			}
			channels = int(binary.LittleEndian.Uint16(body[2:]))
			rate = int(binary.LittleEndian.Uint32(body[4:]))
			if bits := binary.LittleEndian.Uint16(body[14:]); bits != 16 {
				return 0, nil, fmt.Errorf("wav: %d-bit samples; only 16-bit is supported", bits)
			}
		case "data":
			if channels < 1 {
				return 0, nil, errors.New("wav: data before format")
			}
			n := size / (2 * channels)
			samples = make([]float32, n)
			for i := range samples {
				samples[i] = float32(int16(binary.LittleEndian.Uint16(body[2*channels*i:]))) / 32768
			}
			return rate, samples, nil
		}
		pos += 8 + size + size%2
	}
	return 0, nil, errors.New("wav: no audio data")
}
