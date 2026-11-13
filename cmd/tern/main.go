// Command tern turns payloads into Tern audio files and back, and measures how the
// mode holds up on simulated channels.
package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/edinnen/tern"
	"github.com/edinnen/tern/channel"
	"github.com/edinnen/tern/internal/dsp"
	"github.com/edinnen/tern/internal/wav"
)

const usage = `tern: a single-tone data mode for HF radio

  tern tx -call CALL -out FILE.wav [-speed TERN31] [-fast] (-text TEXT | -hex HEX | -in FILE)
        write a transmission as a WAV file
  tern rx [-call CALL,CALL] FILE.wav
        decode every transmission in a recording
  tern airtime -len BYTES [-call CALL]
        how long a payload takes at each speed
  tern sweep [-speed TERN31] [-fast] [-chan awgn] [-snr -14:-8:1] [-len 17] [-trials 100]
        measure delivery against signal level on a simulated channel

Run a command with -h for its flags. SPEC.md describes the format.`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "tx":
		err = cmdTx(os.Args[2:])
	case "rx":
		err = cmdRx(os.Args[2:])
	case "airtime":
		err = cmdAirtime(os.Args[2:])
	case "sweep":
		err = cmdSweep(os.Args[2:])
	case "help", "-h", "--help":
		fmt.Println(usage)
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", os.Args[1], usage)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tern:", err)
		os.Exit(1)
	}
}

func speedFlag(fs *flag.FlagSet) *string {
	return fs.String("speed", "TERN31", "speed: TERN31, TERN16, TERN8 or TERN4")
}

func cmdTx(args []string) error {
	fs := flag.NewFlagSet("tx", flag.ExitOnError)
	call := fs.String("call", "", "the transmitting station's call sign")
	speed := speedFlag(fs)
	fast := fs.Bool("fast", false, "use the rate 3/4 code: a quarter less airtime, for a signal about 2 dB stronger")
	centre := fs.Float64("centre", 1500, "audio centre frequency in Hz")
	rate := fs.Int("rate", tern.SampleRate, "sample rate of the file: 8000 or 48000")
	out := fs.String("out", "", "WAV file to write")
	text := fs.String("text", "", "payload as text")
	hx := fs.String("hex", "", "payload as hexadecimal")
	in := fs.String("in", "", "file holding the payload")
	fs.Parse(args)
	if *call == "" || *out == "" {
		return errors.New("tx needs -call and -out")
	}
	var payload []byte
	switch {
	case *text != "":
		payload = []byte(*text)
	case *hx != "":
		b, err := hex.DecodeString(strings.ReplaceAll(*hx, " ", ""))
		if err != nil {
			return fmt.Errorf("-hex: %w", err)
		}
		payload = b
	case *in != "":
		b, err := os.ReadFile(*in)
		if err != nil {
			return err
		}
		payload = b
	default:
		return errors.New("tx needs a payload: -text, -hex or -in")
	}
	if len(payload) < tern.MinFrame || len(payload) > tern.MaxFrame {
		return fmt.Errorf("the payload is %d bytes; Tern carries %d to %d", len(payload), tern.MinFrame, tern.MaxFrame)
	}
	p, err := tern.ProfileByName(*speed)
	if err != nil {
		return err
	}
	pcm, err := tern.Modulate(p, *centre, *call, payload, *fast)
	if err != nil {
		return err
	}
	seconds := float64(len(pcm)) / tern.SampleRate
	switch *rate {
	case tern.SampleRate:
	case tern.SampleRate * dsp.Factor:
		pcm = dsp.Upsample(pcm)
	default:
		return errors.New("-rate must be 8000 or 48000")
	}
	// Leave headroom: a file at full scale clips in some players.
	for i := range pcm {
		pcm[i] *= 0.8
	}
	if err := wav.Write(*out, *rate, pcm); err != nil {
		return err
	}
	fmt.Printf("%s: %d bytes from %s at %s, %.1f s, %.0f Hz wide\n", *out, len(payload), strings.ToUpper(*call), p.Name, seconds, p.Width())
	return nil
}

func cmdRx(args []string) error {
	fs := flag.NewFlagSet("rx", flag.ExitOnError)
	calls := fs.String("call", "", "call signs to listen for, separated by commas; these are found at weaker signals. Others are still received")
	only := fs.Bool("only", false, "receive only the call signs given with -call")
	centre := fs.Float64("centre", 1500, "audio centre frequency in Hz")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("rx needs one WAV file")
	}
	rate, pcm, err := wav.Read(fs.Arg(0))
	if err != nil {
		return err
	}
	switch rate {
	case tern.SampleRate:
	case tern.SampleRate * dsp.Factor:
		var d dsp.Decimator
		pcm = d.Process(pcm)
	default:
		return fmt.Errorf("%s is %d Hz; 8000 or 48000 is needed", fs.Arg(0), rate)
	}
	cfg := tern.Config{Centre: *centre, AnyCall: !*only}
	for _, c := range strings.Split(*calls, ",") {
		if c = strings.TrimSpace(c); c != "" {
			cfg.Calls = append(cfg.Calls, c)
		}
	}
	if *only && len(cfg.Calls) == 0 {
		return errors.New("-only needs -call")
	}
	rx, err := tern.NewReceiver(cfg)
	if err != nil {
		return err
	}
	// A second of silence after the recording lets a transmission that runs to its
	// very end be finished.
	frames := rx.Feed(pcm)
	frames = append(frames, rx.Feed(make([]float32, tern.SampleRate))...)
	for _, f := range frames {
		code := "rate 1/2"
		if f.Fast {
			code = "rate 3/4"
		}
		fmt.Printf("%s  %s %s  %+.0f dB  %+.1f Hz  %d bytes: %s\n", f.Call, f.Profile.Name, code, f.SNR, f.Offset, len(f.Data), show(f.Data))
	}
	if len(frames) == 0 {
		fmt.Fprintln(os.Stderr, "nothing decoded")
	}
	return nil
}

// show prints a payload as text when it is text, and as hexadecimal otherwise.
func show(b []byte) string {
	for _, r := range string(b) {
		if r == unicode.ReplacementChar || !unicode.IsPrint(r) {
			return hex.EncodeToString(b)
		}
	}
	return strconv.Quote(string(b))
}

func cmdAirtime(args []string) error {
	fs := flag.NewFlagSet("airtime", flag.ExitOnError)
	n := fs.Int("len", 17, "payload length in bytes")
	call := fs.String("call", "N0CALL", "call sign; only its length matters")
	fs.Parse(args)
	if *n < tern.MinFrame || *n > tern.MaxFrame {
		return fmt.Errorf("Tern carries %d to %d bytes", tern.MinFrame, tern.MaxFrame)
	}
	fmt.Printf("%d bytes, call sign of %d characters\n", *n, len(*call))
	fmt.Printf("%-8s %10s %10s %9s\n", "speed", "rate 1/2", "rate 3/4", "width")
	for _, p := range tern.All {
		fmt.Printf("%-8s %8.1f s %8.1f s %6.0f Hz\n", p.Name,
			tern.Airtime(p, len(*call), *n, false).Seconds(), tern.Airtime(p, len(*call), *n, true).Seconds(), p.Width())
	}
	return nil
}

func cmdSweep(args []string) error {
	fs := flag.NewFlagSet("sweep", flag.ExitOnError)
	speed := speedFlag(fs)
	fast := fs.Bool("fast", false, "use the rate 3/4 code")
	cond := fs.String("chan", "awgn", "channel: awgn, good, moderate, poor, nvis or flat")
	snrs := fs.String("snr", "-14:-8:1", "signal-to-noise ratios in dB in 2.5 kHz: FROM:TO:STEP")
	size := fs.Int("len", 17, "payload length in bytes")
	trials := fs.Int("trials", 100, "transmissions per signal level")
	seed := fs.Int64("seed", 1, "seed for the noise, the fading and the payloads")
	blind := fs.Bool("any", false, "receive without being told the call sign")
	fs.Parse(args)
	p, err := tern.ProfileByName(*speed)
	if err != nil {
		return err
	}
	model, err := channel.Lookup(*cond)
	if err != nil {
		return err
	}
	part := strings.Split(*snrs, ":")
	if len(part) != 3 {
		return errors.New("-snr wants FROM:TO:STEP")
	}
	var lim [3]float64
	for i := range lim {
		if lim[i], err = strconv.ParseFloat(part[i], 64); err != nil {
			return err
		}
	}
	if lim[2] <= 0 {
		return errors.New("the -snr step must be positive")
	}
	const call = "N0CALL"
	cfg := tern.Config{Profiles: []tern.Profile{p}}
	if !*blind {
		cfg.Calls = []string{call}
	}
	fmt.Println(model)
	fmt.Println("speed,channel,snr_db,payload_bytes,trials,delivered,airtime_s")
	tail := 1 + 12*float64(p.SymLen)/tern.SampleRate
	for snr := lim[0]; snr <= lim[1]+1e-9; snr += lim[2] {
		rng := rand.New(rand.NewSource(*seed*1000003 + int64(math.Round(snr*10))))
		delivered := 0
		var air time.Duration
		for i := 0; i < *trials; i++ {
			payload := make([]byte, *size)
			rng.Read(payload)
			pcm, err := tern.Modulate(p, 1500, call, payload, *fast)
			if err != nil {
				return err
			}
			air = time.Duration(float64(len(pcm)) / tern.SampleRate * float64(time.Second))
			rx, err := tern.NewReceiver(cfg)
			if err != nil {
				return err
			}
			for _, f := range rx.Feed(model.Apply(pcm, tern.SampleRate, snr, 0.7, tail, rng)) {
				if string(f.Data) == string(payload) {
					delivered++
					break
				}
			}
		}
		name := p.Name
		if *fast {
			name += "F"
		}
		fmt.Printf("%s,%s,%.1f,%d,%d,%d,%.2f\n", name, model.Name, snr, *size, *trials, delivered, air.Seconds())
	}
	return nil
}
