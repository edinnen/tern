# Tern format, version 1

This is the complete public description of Tern, a data mode for HF radio. It is
published so that anyone can receive and decode Tern transmissions: nothing in the
format is secret and no key is involved.

A Tern transmission carries one payload of 6 to 1536 bytes, together with the call
sign of the station sending it. What the bytes mean is up to the application.

The key words here are plain ones. "Is" and "are" describe what a transmitter does;
a receiver may be built any way that decodes it.

## 1. Signal

- Audio is described at 8000 samples a second. The signal is upper sideband audio,
  normally centred on 1500 Hz.
- A transmission is a run of symbols. Each symbol is one of 64 tones, held for one
  symbol period at constant level. The phase runs on without a break from one symbol
  to the next.
- Tone `t` (0 to 63) is at `centre + (t - 31.5) * spacing`.
- The spacing between tones equals the symbol rate, so tones are orthogonal over one
  symbol.

| Speed | Symbol period | Symbol rate and tone spacing | Occupied width |
|---|---|---|---|
| TERN31 | 256 samples, 32 ms | 31.25 Hz | 2000 Hz |
| TERN16 | 512 samples, 64 ms | 15.625 Hz | 1000 Hz |
| TERN8 | 1024 samples, 128 ms | 7.8125 Hz | 500 Hz |
| TERN4 | 2048 samples, 256 ms | 3.90625 Hz | 250 Hz |

The transmitter holds the first symbol's tone for 64 ms before the first symbol
starts. A receiver listens at all four speeds at once; the sender alone chooses.

## 2. Layout of a transmission

| Part | Symbols | Content |
|---|---|---|
| Sync | 4 | Tones 9, 54, 23, 40. |
| Call sign | 1 + n | The length n of the call sign (3 to 12), then one symbol per character. |
| Header | 8 | The size of the payload and its code rate (section 4). |
| Payload | K + m | The payload and its parity symbols (sections 5 and 6). |

Every transmission therefore starts with the station's call sign. A character's
symbol value is its position in `0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ/`, counting
from 0.

### Whitening

Every symbol after the sync is exclusive-ored with the next value of a fixed
sequence before it is sent, so that no content produces a long run of one tone. The
sequence is `w[i] = (next() >> 13) & 63` from the generator of section 7 seeded with
`0x7E12`; `w[0]` applies to the call sign's length symbol.

Example: the call sign field of N0CALL is the values 6, 23, 0, 12, 10, 21, 21, each
exclusive-ored with `w[0]` to `w[6]`. The sequence begins 40, 11, 12, 22, 61, 12, 17,
so the tones sent are 46, 28, 12, 26, 55, 25, 4.

## 3. Arithmetic

A symbol value is an element of the field GF(64), built on the polynomial
`x^6 + x + 1`: bit 5 of a value is the coefficient of `x^5`. Addition is
exclusive-or. `alpha` is the element 2.

## 4. Header

The header carries twelve bits: `v = (K - 1) + 2048 * rate`, where K is the number of
information symbols (8 to 2048) and rate is 0 for the rate 1/2 code and 1 for the
rate 3/4 code. With `a = v mod 64` and `b = v div 64`, the eight header symbols are

    h[j] = a + b * alpha^j        j = 0 to 7

then whitened. Any two different headers differ in at least seven of the eight
symbols.

## 5. Payload symbols

The payload's bytes are read as a string of bits, most significant bit of each byte
first, and cut into six-bit information symbols; the last is padded with zero bits.
A payload of n bytes gives `K = ceil(8n / 6)` symbols, and K symbols hold
`floor(6K / 8)` bytes, which is n again. The shortest payload is 6 bytes (K = 8) and
the longest is 1536 (K = 2048).

## 6. Error-correcting code

The code is a low-density parity-check code over GF(64) in which every symbol takes
part in exactly two checks. With `rings` = 1 (rate 1/2) or 3 (rate 3/4):

- There are `m = max(4, ceil(K / rings))` checks, numbered 0 to m-1.
- There are m parity symbols. Parity symbol i takes part in check i and check
  i+1 (mod m).
- There are `rings * m` information symbols, of which the first K carry the payload.
  The rest are zero and are not sent. Information symbol j of ring g takes part in
  checks `order[g][j]` and `order[g][j+1 mod m]`, where `order[g]` is an ordering of
  the checks.
- Each check requires that the sum of its symbols, each multiplied by that check's
  coefficient for it, is zero.

The orderings, the coefficients and the order of transmission are produced by the
generator of section 7, seeded with `4 * K + rings`, in this sequence:

1. For each ring in turn, draw 24 candidate orderings with `shuffle(m)`. Add each in
   turn to the graph whose vertices are the checks and whose edges are the symbols
   chosen so far (starting from the parity ring), and keep the candidate that gives
   the longest shortest cycle; among equals, the one with the fewest vertices from
   which a breadth-first search finds a cycle of that length; among those, the
   earliest drawn.
2. For i = 0 to m-1, draw the coefficient of parity symbol i in check i, then its
   coefficient in check i+1. Then for each ring, for j = 0 to m-1, draw the
   coefficient of information symbol j in its first check, then in its second. A
   coefficient is `1 + intn(63)`.
3. Solving the checks in order for the parity symbols multiplies a parity symbol by
   a fixed factor in one trip round the ring. While that factor is 1, replace the
   coefficient c of parity symbol 0 in check 0 by `c mod 63 + 1`.
4. List the K information symbols and then the m parity symbols; with
   `p = shuffle(K + m)`, the i-th symbol sent is entry `p[i]` of that list.

The payload symbols are whitened like the rest.

The reference implementation is the Go package in this repository, and its test
`TestCodesAreStable` holds fingerprints of three codes. Where this text and that
implementation differ, the implementation is what stations on the air do, and the
difference is an error in this document.

## 7. Generator

All pseudo-random choices come from xorshift32. All arithmetic is on unsigned 32-bit
values.

    state = seed * 2654435761 + 0x9E3779B9      (use 1 if that is 0)
    next():  state ^= state << 13;  state ^= state >> 17;  state ^= state << 5
             return state
    discard the first 8 values of next()
    intn(n) = (next() * n) >> 32                (a 64-bit product)
    shuffle(n): start with 0, 1, ..., n-1; for i = n-1 down to 1,
                swap entry i with entry intn(i + 1)

## 8. Receiving

This section describes the reference receiver. It is not part of the format.

A receiver that knows which call signs to expect searches for the sync tones followed
by a call sign field, at each speed, over a range of frequency error. One that does
not finds the sync tones alone and reads the call sign field one symbol at a time,
which needs a signal 3 to 4 dB stronger.

For each symbol the receiver measures the energy in each of the 64 tone filters. The
header is decoded by trying all 4096 values. The payload is decoded by belief
propagation on the code's graph, working with probabilities over the 64 symbol
values. A payload is accepted when every check is satisfied.

The code detects most damage but is not a guarantee: at the limit of reception a
receiver will occasionally accept a wrong payload. An application that must not act
on a wrong payload carries its own check inside the payload.

## 9. What the format leaves out

- **Speed selection.** The sender chooses the speed and the code rate for each
  transmission, and a receiver listens for all of them. How to choose is up to the
  application; a simple rule is to start at TERN31 and go one step slower each time
  a message has to be repeated.
- **Repeats, acknowledgements and addressing.** A transmission is one payload from
  one call sign. Anything more is the application's.
- **Secrecy and authentication.** There is none. The payload is sent in the clear.
- **A version field.** A future version that is not compatible with this one will
  use different sync tones.

## 10. Test vector

The six bytes `hello!` from N0CALL with the rate 1/2 code:

- Information symbols (K = 8): 26, 6, 21, 44, 27, 6, 60, 33.
- The whole transmission, 35 tones:

      sync      9 54 23 40
      call      46 28 12 26 55 25 4
      header    20 14 19 30 0 28 12 11
      payload   63 13 45 37 11 58 6 43 42 57 51 11 6 43 14 5
