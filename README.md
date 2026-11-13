# Tern

Tern is a data mode for HF radio. It carries short binary messages, one tone at a
time, and is built for weak and fading signals.

- **One tone at a time, at constant level.** Any transmitter can send it, including
  the simple ones that turn an audio tone straight into RF and cannot send PSK or
  multi-carrier modes.
- **Binary payloads of 6 to 1536 bytes.** No text encoding, no fixed message size.
- **The call sign opens every transmission**, in a documented encoding.
- **Four speeds**, from 2 kHz wide and fast to 250 Hz wide and slow. A receiver
  listens for all of them at once, so the sender just picks one.
- **Error correction matched to the modulation.** Each symbol is one of 64 tones and
  also one symbol of a code over a 64-element field, so the decoder works from the
  strength of every tone and nothing is thrown away in between.
- **An open format.** [SPEC.md](SPEC.md) describes it completely, with a test vector.

This repository holds the format description, a reference implementation in Go with
no dependencies outside the standard library, and a small command-line tool.

## Status

Experimental. Version 1 of the format is fixed by the tests here, but it is new and
has had little use.

- Measured on simulated channels (below).
- Sent from a 5 W single-tone transceiver into a dummy load and received on a second
  radio a few metres away: every test transmission decoded.
- Not yet tried over a real path between two antennas.

## Speeds

Airtime is for a 17-byte payload from a six-character call sign. The signal levels
are signal-to-noise ratios in 2.5 kHz, the lowest at which at least 9 transmissions
in 10 were delivered in simulation, with the receiver told which call sign to expect.

| Speed | Symbols a second | Width | Airtime | White noise | Fading |
|---|---|---|---|---|---|
| TERN31, rate 3/4 | 31.25 | 2000 Hz | 1.7 s | -10 dB | 0 dB |
| TERN31 | 31.25 | 2000 Hz | 2.1 s | -11 dB | -5 dB |
| TERN16 | 15.6 | 1000 Hz | 4.2 s | -14 dB | -8 dB |
| TERN8 | 7.8 | 500 Hz | 8.4 s | -17 dB | -12 dB |
| TERN4 | 3.9 | 250 Hz | 16.7 s | -20 dB | -16 dB |

"Fading" is the two-path model HF modems are usually tested against: two paths of
equal strength 1 ms apart, each fading independently with a 0.5 Hz Doppler spread
(the "moderate" condition of ITU-R F.1487).

Every step down the table doubles the airtime and works with a signal 3 dB weaker.
A fixed 0.6 s of each TERN31 transmission is sync, call sign and header; the rest
is payload at about 94 bits a second (rate 1/2) or 141 (rate 3/4).

A receiver that has not been told the call sign finds a transmission by its four
sync tones alone and needs a signal 3 to 4 dB stronger.

### Against text modes

For comparison, the same 17 bytes were sent the way binary data usually goes over a
keyboard mode: as one line of base64 text behind a call sign, through fldigi's
modems, and through the same simulated channels.

| Mode | Airtime | White noise | Fading |
|---|---|---|---|
| TERN31 | 2.1 s | -11 dB | -5 dB |
| TERN16 | 4.2 s | -14 dB | -8 dB |
| MFSK64 | 4.5 s | -7 dB | 3 dB |
| MFSK32 | 7.5 s | -10 dB | -1 dB |
| TERN8 | 8.4 s | -17 dB | -12 dB |
| THOR22 | 9.9 s | -9 dB | 4 dB |
| MFSK16 | 14.7 s | -13 dB | -5 dB |
| Olivia 8/500 | 15.4 s | -10 dB | -2 dB |
| TERN4 | 16.7 s | -20 dB | -16 dB |
| Olivia 8/250 | 28.7 s | -14 dB | -6 dB |

This is a comparison for short binary messages, which is what Tern is for. Much of
the difference in airtime comes from the text modes' fixed cost per transmission and
from sending binary as text, not from their modulation, and it narrows for long
messages. Those modes also do things Tern does not: they carry live keyboard text
and can be read by ear and eye on a waterfall.

For scale against the weak-signal modes: FT8 carries 77 bits in 12.6 s and is quoted
at about -21 dB. TERN4 carries 136 bits in 16.7 s at -20 dB here. The two were not
measured side by side.

## Try it

    go install github.com/edinnen/tern/cmd/tern@latest

    tern tx -call N0CALL -text "hello from tern" -out hello.wav
    tern rx hello.wav
    tern airtime -len 40
    tern sweep -speed TERN16 -chan moderate -snr -14:-4:2

`tx` writes a WAV file at 8000 or 48000 samples a second; play it into a
transmitter's audio input in upper sideband. `rx` decodes every transmission in a
recording made the same way. `sweep` repeats the measurements above.

Replace `N0CALL` with your own call sign before anything goes on the air.

## Use it from Go

    import "github.com/edinnen/tern"

    // Payload to audio: 8000 samples a second, mono, peak 1.
    audio, err := tern.Modulate(tern.T31, 1500, "N0CALL", payload, false)

    // Audio to payloads, fed as it arrives.
    rx, err := tern.NewReceiver(tern.Config{})
    for _, f := range rx.Feed(samples) {
        fmt.Println(f.Call, f.Profile.Name, f.SNR, f.Data)
    }

`Config.Calls` lists call signs to listen for; they are found at weaker signals
than others. `Receiver.Receiving` says whether a transmission is arriving right now,
so an application can avoid transmitting over it. The package does no sound card or
transmitter control; it turns bytes into samples and samples into bytes.

The `channel` package is the simulator behind the measurements: white noise and
two-path fading for any modem's audio.

## What Tern does not do

- **No acknowledgements, repeats or addressing.** A transmission is one payload from
  one call sign. A link protocol goes on top.
- **No encryption and no authentication.** The payload is sent in the clear and the
  call sign is whatever the sender put there.
- **No guarantee against a wrong payload.** The code rejects nearly all damaged
  transmissions, but at the very limit of reception a wrong payload can get through.
  Put a checksum or an authenticator in the payload if that matters.
- **No listening before transmitting.** That is the operator's and the application's
  job.

## Notes for single-tone transmitters

Tern was built with a QRP Labs QDX at one end, which measures the frequency of the
audio it is given and synthesises that one frequency directly. Measured on one:

- It places a tone within about 0.3 Hz, which is ample even for TERN4's 3.9 Hz
  spacing.
- Each change of tone costs it 3 to 4 ms. That is about 1 dB at TERN31 and less at
  the slower speeds, and is why Tern does not use shorter symbols.
- TERN31 spans 516 to 2484 Hz at the default centre of 1500 Hz. The receiving
  radio's filter needs to pass that: 2.4 kHz or wider.

## On the air

Tern is published so that anyone can decode it, and every transmission starts with
the sender's call sign. Whether and where you may use a new data mode still depends
on the rules for your licence and band, so check them. TERN31 is 2 kHz wide: listen
first, and use a narrower speed where the band is busy.

## Layout

| Path | Contents |
|---|---|
| `SPEC.md` | The format. |
| `*.go` | Package `tern`: the code, encoder, decoder, modulator and receiver. |
| `channel` | Channel models for simulation. |
| `cmd/tern` | The command-line tool. |
| `internal/dsp` | Fourier transform and 8 kHz to 48 kHz conversion. |
| `internal/wav` | WAV files. |

## Tests

    go test ./...

`TestCodesAreStable` and `TestSpecVector` pin the format: if either fails, a change
has made the implementation incompatible with every station already using it.

## Licence

Copyright 2026 VA7CVE.

This program is free software: you can redistribute it and/or modify it under the
terms of the GNU Affero General Public License, version 3, as published by the Free
Software Foundation. It is distributed in the hope that it will be useful, but
without any warranty. See [LICENSE](LICENSE) for the full text.

In short: you may use, study, change and share this code, but software built on it
must be offered under the same licence with its source, including software that is
only offered as a service over a network.

The licence covers this implementation and these documents. The format they
describe can be implemented independently by anyone.
