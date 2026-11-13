package tern

import (
	"math"
	"sort"
	"time"

	"github.com/edinnen/tern/internal/dsp"
)

// Config sets up a receiver.
type Config struct {
	// Profiles are the symbol rates to listen for, all at once. Default: all of them.
	Profiles []Profile
	// Centre is the audio frequency the signal is centred on. Default 1500 Hz.
	Centre float64
	// Calls are call signs to listen for. A transmission from a listed call sign is
	// found by the tones of its sync and call sign field together, which works at
	// weaker signals than finding the sync alone.
	Calls []string
	// AnyCall also receives transmissions from call signs that are not listed, by
	// finding the sync tones and reading the call sign field. It is always on when
	// Calls is empty.
	AnyCall bool
	// MaxAir is the longest transmission worth waiting for. Default 150 s.
	MaxAir time.Duration
}

// Frame is a received transmission whose every parity check was satisfied.
type Frame struct {
	Call    string
	Data    []byte
	Profile Profile
	Fast    bool
	SNR     float64 // estimated signal-to-noise ratio in dB, in 2.5 kHz
	Offset  float64 // how far the signal was from the expected frequency, in Hz
	Passes  int     // decoder passes used
}

// Receiver finds and decodes transmissions in a stream of audio.
type Receiver struct {
	cfg       Config
	templates []template
	search    []*searcher
	pending   []*candidate

	buf   []float32
	start int64 // index in the stream of buf[0]
	total int64 // samples received so far

	lastData string
	lastEnd  int64
}

type template struct {
	call  string
	tones []uint8
	// blind marks the template that is the sync tones alone: the call sign is read
	// from the transmission once it has been found.
	blind bool
}

// maxOffset is how far off frequency a signal may be and still be found, in Hz.
const maxOffset = 50.0

// NewReceiver creates a receiver.
func NewReceiver(cfg Config) (*Receiver, error) {
	if len(cfg.Profiles) == 0 {
		cfg.Profiles = All
	}
	if cfg.Centre == 0 {
		cfg.Centre = 1500
	}
	if cfg.MaxAir <= 0 {
		cfg.MaxAir = 150 * time.Second
	}
	r := &Receiver{cfg: cfg}
	seen := map[string]bool{}
	for _, call := range cfg.Calls {
		tones, err := preamble(call)
		if err != nil {
			return nil, err
		}
		if !seen[string(tones)] {
			seen[string(tones)] = true
			r.templates = append(r.templates, template{call: call, tones: tones})
		}
	}
	if cfg.AnyCall || len(r.templates) == 0 {
		r.templates = append(r.templates, template{tones: syncTones[:], blind: true})
	}
	for _, p := range cfg.Profiles {
		r.search = append(r.search, newSearcher(p, cfg.Centre))
	}
	return r, nil
}

// searcher watches for the start of a transmission at one symbol rate. It keeps a
// short history of spectra taken eight times per symbol at half-tone resolution.
type searcher struct {
	p      Profile
	hop    int
	fft    *dsp.FFT
	binHz  float64
	lo, nb int // lowest spectrum bin kept, and how many
	b0     int // bin of tone 0 with no frequency offset
	fmax   int // largest offset searched, in bins
	cols   [][]float32
	means  []float32
	peaks  []float32
	ncol   int64
	re, im []float64
	sr, si []float64 // scratch for one symbol
}

func newSearcher(p Profile, centre float64) *searcher {
	s := &searcher{p: p, hop: p.SymLen / 8, fft: dsp.NewFFT(2 * p.SymLen)}
	s.binHz = SampleRate / float64(2*p.SymLen)
	s.b0 = int(math.Round(p.base(centre) / s.binHz))
	s.fmax = int(math.Ceil(maxOffset / s.binHz))
	s.lo = s.b0 - s.fmax
	s.nb = 2*(Tones-1) + 2*s.fmax + 1
	ring := 8*(syncLen+1+MaxCall) + 24
	s.cols = make([][]float32, ring)
	for i := range s.cols {
		s.cols[i] = make([]float32, s.nb)
	}
	s.means = make([]float32, ring)
	s.peaks = make([]float32, ring)
	s.re = make([]float64, 2*p.SymLen)
	s.im = make([]float64, 2*p.SymLen)
	s.sr = make([]float64, p.SymLen)
	s.si = make([]float64, p.SymLen)
	return s
}

// candidate is a possible transmission being followed.
type candidate struct {
	s      *searcher
	tmpl   *template
	col    int64
	fo     int
	metric float64

	stage int     // 0 found, 1 placed and waiting for the header, 2 waiting for the payload
	begin float64 // sample at which the first sync symbol starts
	fbase float64 // frequency of tone 0
	need  int64   // sample that must have arrived before the next step

	code  *Code
	fast  bool
	sig   float64 // signal energy per symbol, from the known symbols
	noise float64 // noise energy per tone filter, measured with them
}

// Feed takes the next samples of the stream (SampleRate, mono) and returns the frames
// completed by them.
func (r *Receiver) Feed(pcm []float32) []Frame {
	r.buf = append(r.buf, pcm...)
	r.total += int64(len(pcm))
	for _, s := range r.search {
		for s.ncol*int64(s.hop)+int64(s.p.SymLen) <= r.total {
			r.column(s)
		}
	}
	var out []Frame
	keep := r.pending[:0]
	for _, c := range r.pending {
		if f, done := r.advance(c); done {
			if f != nil {
				out = append(out, *f)
			}
			continue
		}
		keep = append(keep, c)
	}
	r.pending = keep
	r.trim()
	return out
}

// column computes the next spectrum for a searcher and looks for transmissions whose
// known symbols end with it.
func (r *Receiver) column(s *searcher) {
	n := s.p.SymLen
	at := s.ncol*int64(s.hop) - r.start
	for i := 0; i < n; i++ {
		s.re[i] = float64(r.buf[at+int64(i)])
	}
	for i := n; i < 2*n; i++ {
		s.re[i] = 0
	}
	for i := range s.im {
		s.im[i] = 0
	}
	s.fft.Transform(s.re, s.im, false)
	slot := int(s.ncol % int64(len(s.cols)))
	col := s.cols[slot]
	var sum, peak float32
	for b := range col {
		k := s.lo + b
		var e float32
		if k >= 0 && k < n {
			e = float32(s.re[k]*s.re[k] + s.im[k]*s.im[k])
		}
		col[b] = e
		sum += e
		peak = max(peak, e)
	}
	s.means[slot] = sum / float32(s.nb)
	s.peaks[slot] = peak
	s.ncol++

	for ti := range r.templates {
		t := &r.templates[ti]
		p := len(t.tones)
		c0 := s.ncol - 1 - int64(8*(p-1))
		if c0 < 0 {
			continue
		}
		var noise float64
		for j := 0; j < p; j++ {
			noise += float64(s.means[int((c0+int64(8*j))%int64(len(s.cols)))])
		}
		noise /= float64(p)
		if noise <= 0 {
			continue
		}
		// A known call sign gives many symbols to match, so half of them standing out
		// is enough. The four sync tones alone must all stand out, and more strongly.
		need, margin := (p+1)/2, 6.0
		if t.blind {
			need, margin = p, 8.0
		}
		best, bestF := 0.0, 0
		for f := -s.fmax; f <= s.fmax; f++ {
			var e float64
			strong := 0
			for j, tone := range t.tones {
				slot := int((c0 + int64(8*j)) % int64(len(s.cols)))
				v := s.cols[slot][s.b0-s.lo+2*int(tone)+f]
				e += float64(v)
				if v >= 0.5*s.peaks[slot] {
					strong++
				}
			}
			if strong >= need && e > best {
				best, bestF = e, f
			}
		}
		m := best / noise
		if m < float64(p)+margin*math.Sqrt(float64(p)) {
			continue
		}
		// Keep one candidate per transmission: the strongest of those close together.
		merged := false
		for _, c := range r.pending {
			if c.stage == 0 && c.s == s && c.tmpl == t && c0-c.col <= 8 && abs(bestF-c.fo) <= 4 {
				if m > c.metric {
					c.col, c.fo, c.metric = c0, bestF, m
				}
				merged = true
				break
			}
		}
		if !merged {
			r.pending = append(r.pending, &candidate{s: s, tmpl: t, col: c0, fo: bestF, metric: m})
		}
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// energy returns the squared magnitude of the correlation of n samples starting at
// start with a tone at freq.
func (r *Receiver) energy(start float64, n int, freq float64) float64 {
	at := int64(math.Round(start)) - r.start
	if at < 0 || at+int64(n) > int64(len(r.buf)) {
		return 0
	}
	w := 2 * math.Pi * freq / SampleRate
	cw, sw := math.Cos(w), math.Sin(w)
	cr, ci := 1.0, 0.0
	var re, im float64
	for _, x := range r.buf[at : at+int64(n)] {
		re += float64(x) * cr
		im -= float64(x) * ci
		cr, ci = cr*cw-ci*sw, cr*sw+ci*cw
	}
	return re*re + im*im
}

// symbol measures the energy in each of the 64 tone filters over one symbol.
func (r *Receiver) symbol(s *searcher, start float64, fbase float64, out *[Tones]float64) bool {
	n := s.p.SymLen
	at := int64(math.Round(start)) - r.start
	if at < 0 || at+int64(n) > int64(len(r.buf)) {
		return false
	}
	w := 2 * math.Pi * fbase / SampleRate
	cw, sw := math.Cos(w), math.Sin(w)
	cr, ci := 1.0, 0.0
	for i, x := range r.buf[at : at+int64(n)] {
		s.sr[i] = float64(x) * cr
		s.si[i] = -float64(x) * ci
		cr, ci = cr*cw-ci*sw, cr*sw+ci*cw
		if i&255 == 255 { // keep the rotating phasor on the unit circle
			g := 1 / math.Hypot(cr, ci)
			cr, ci = cr*g, ci*g
		}
	}
	dsp.NewFFT(n).Transform(s.sr, s.si, false)
	for k := range out {
		out[k] = s.sr[k]*s.sr[k] + s.si[k]*s.si[k]
	}
	return true
}

// known returns the total energy at the tones a transmission is known to start with,
// for a trial start time and frequency.
func (r *Receiver) known(c *candidate, begin, fbase float64) float64 {
	n := c.s.p.SymLen
	sp := c.s.p.Spacing()
	var e float64
	for j, tone := range c.tmpl.tones {
		e += r.energy(begin+float64(j*n), n, fbase+float64(tone)*sp)
	}
	return e
}

// place finds the start time and frequency at which a candidate's known tones are
// strongest, searching around a first estimate: coarsely, then finely.
func (r *Receiver) place(c *candidate, begin, fbase float64) (float64, float64) {
	s := c.s
	n := s.p.SymLen
	for _, grid := range []struct{ dt, df float64 }{
		{float64(n) / 32, s.binHz / 4},
		{float64(n) / 128, s.binHz / 16},
	} {
		reach := 4
		if grid.dt < float64(n)/32 {
			reach = 2
		}
		best, bt, bf := -1.0, 0.0, 0.0
		for t := -reach; t <= reach; t++ {
			for f := -2; f <= 2; f++ {
				dt, df := math.Round(float64(t)*grid.dt), float64(f)*grid.df
				if e := r.known(c, begin+dt, fbase+df); e > best {
					best, bt, bf = e, dt, df
				}
			}
		}
		begin, fbase = begin+bt, fbase+bf
	}
	return begin, fbase
}

// readCall reads the call sign field of a transmission found by its sync tones, taking
// the strongest tone in each symbol. It fails if what it reads cannot be a call sign.
func (r *Receiver) readCall(s *searcher, begin, fbase float64) (*template, bool) {
	n := s.p.SymLen
	read := func(j int) (uint8, bool) {
		var e [Tones]float64
		if !r.symbol(s, begin+float64((syncLen+j)*n), fbase, &e) {
			return 0, false
		}
		top := 0
		for k := range e {
			if e[k] > e[top] {
				top = k
			}
		}
		return uint8(top) ^ white[j], true
	}
	length, ok := read(0)
	if !ok || length < MinCall || length > MaxCall {
		return nil, false
	}
	call := make([]byte, length)
	for j := range call {
		v, ok := read(1 + j)
		if !ok || int(v) >= len(callAlphabet) {
			return nil, false
		}
		call[j] = callAlphabet[v]
	}
	tones, err := preamble(string(call))
	if err != nil {
		return nil, false
	}
	return &template{call: string(call), tones: tones}, true
}

// advance moves a candidate on as far as the audio received so far allows. It reports
// done when the candidate is finished with, with a frame if it decoded.
func (r *Receiver) advance(c *candidate) (*Frame, bool) {
	s := c.s
	n := s.p.SymLen
	p := len(c.tmpl.tones)
	switch c.stage {
	case 0:
		// Wait until no stronger neighbour can turn up and the audio a little past the
		// known symbols has arrived, then place the transmission precisely.
		if s.ncol-1-int64(8*(p-1)) < c.col+9 {
			return nil, false
		}
		begin := float64(c.col * int64(s.hop))
		if int64(begin)-int64(s.hop) < r.start {
			return nil, true
		}
		fbase := float64(s.b0+c.fo) * s.binHz
		if c.tmpl.blind {
			// Found by the sync tones alone: place it roughly, read the call sign field,
			// and carry on as for a call sign that was expected.
			if int64(begin)+int64(s.hop)+int64((syncLen+1+MaxCall)*n) > r.total {
				return nil, false
			}
			begin, fbase = r.place(c, begin, fbase)
			t, ok := r.readCall(s, begin, fbase)
			if !ok {
				return nil, true
			}
			c.tmpl = t
			p = len(t.tones)
		}
		if int64(begin)+int64(s.hop)+int64(p*n) > r.total {
			return nil, false
		}
		c.begin, c.fbase = r.place(c, begin, fbase)
		c.stage = 1
		c.need = int64(c.begin) + int64((p+headerLen)*n) + 2
		fallthrough
	case 1:
		if r.total < c.need {
			return nil, false
		}
		// Measure the known symbols and the header.
		e := make([][Tones]float64, p+headerLen)
		var all []float64
		for j := range e {
			if !r.symbol(s, c.begin+float64(j*n), c.fbase, &e[j]) {
				return nil, true
			}
			all = append(all, e[j][:]...)
		}
		noise := median(all) / math.Ln2
		if noise <= 0 {
			return nil, true
		}
		var sig float64
		for j, tone := range c.tmpl.tones {
			sig += e[j][tone]
		}
		sig = sig/float64(p) - noise
		snr := math.Max(sig/noise, 1)
		var ll [headerLen][Tones]float64
		for j := 0; j < headerLen; j++ {
			for k, v := range e[p+j] {
				ll[j][k] = lnI0(2 * math.Sqrt(snr*v/noise))
			}
		}
		// A transmission at this speed puts each symbol's energy into one tone filter. A
		// strong signal at another speed spreads it over several, and can still line up
		// with enough of the expected tones to be taken for a transmission. So when
		// there is plainly a strong signal here, most of it must sit in one filter per
		// symbol.
		var total, tops float64
		for j := range e {
			top := 0.0
			for _, v := range e[j] {
				total += v
				top = math.Max(top, v)
			}
			tops += top - noise
		}
		if excess := total - float64(len(e)*Tones)*noise; excess > 30*noise*float64(len(e)) && tops < 0.5*excess {
			return nil, true
		}
		k, fast, tones := decodeHeader(&ll, p-syncLen)
		// Estimate the signal again with the header's symbols included, and give up if
		// it is too weak to carry anything.
		sig = 0
		for j, tone := range c.tmpl.tones {
			sig += e[j][tone]
		}
		for j, tone := range tones {
			sig += e[p+j][tone]
		}
		sig = sig/float64(p+headerLen) - noise
		// Noise that got this far scores about twice the noise level here, because the
		// search and the header decoder both pick out its strongest filters. A signal
		// just strong enough to decode scores about five times.
		if sig < 3*noise {
			return nil, true
		}
		code, err := CodeFor(k, rings(fast))
		if err != nil {
			return nil, true
		}
		symbols := p + headerLen + code.Sent()
		if time.Duration(float64(symbols*n)/SampleRate*float64(time.Second)) > r.cfg.MaxAir {
			return nil, true
		}
		c.code, c.fast, c.sig, c.noise = code, fast, sig, noise
		c.stage = 2
		c.need = int64(c.begin) + int64(symbols*n) + int64(n/8)
		fallthrough
	default:
		if r.total < c.need {
			// A strong signal that has stopped is not going to deliver the rest.
			return nil, r.gone(c)
		}
		return r.payload(c), true
	}
}

// payload measures a transmission's payload symbols, following any slow drift in its
// timing, and decodes them.
func (r *Receiver) payload(c *candidate) *Frame {
	s := c.s
	n := s.p.SymLen
	p := len(c.tmpl.tones)
	sent := c.code.Sent()
	first := p + headerLen
	e := make([][Tones]float64, sent)
	sp := s.p.Spacing()
	shift, step := 0.0, float64(n)/32
	const block = 16
	for i := 0; i < sent; i++ {
		if !r.symbol(s, c.begin+shift+float64((first+i)*n), c.fbase, &e[i]) {
			return nil
		}
		if i%block == block-1 && i+1 < sent {
			// Compare the strongest tone's energy a little early and a little late with
			// what was just measured, and lean towards whichever is larger.
			var on, early, late float64
			for j := i - block + 1; j <= i; j++ {
				top := 0
				for k := range e[j] {
					if e[j][k] > e[j][top] {
						top = k
					}
				}
				at := c.begin + shift + float64((first+j)*n)
				f := c.fbase + float64(top)*sp
				on += e[j][top]
				early += r.energy(at-step, n, f)
				late += r.energy(at+step, n, f)
			}
			switch {
			case late > 1.01*on && late >= early:
				shift += step
			case early > 1.01*on:
				shift -= step
			}
		}
	}
	all := make([]float64, 0, sent*Tones)
	var tops float64
	for i := range e {
		all = append(all, e[i][:]...)
		top := 0.0
		for _, v := range e[i] {
			top = math.Max(top, v)
		}
		tops += top
	}
	noise := median(all) / math.Ln2
	if noise <= 0 {
		return nil
	}
	// The signal's strength over the whole payload: the strongest filter in each
	// symbol is nearly always the signal.
	sig := math.Max(tops/float64(sent)-noise, noise)
	pos := p - syncLen + headerLen
	try := func(local bool) ([]uint8, bool, int) {
		like := make([][Tones]float64, sent)
		for i := range e {
			snr := sig / noise
			if local {
				// Follow fading: judge each symbol by the signal strength around it.
				lo, hi := max(0, i-4), min(sent, i+5)
				var t float64
				for j := lo; j < hi; j++ {
					top := 0.0
					for _, v := range e[j] {
						top = math.Max(top, v)
					}
					t += top
				}
				snr = math.Max(t/float64(hi-lo)/noise-1, 0.25)
			}
			var scaled [Tones]float64
			for k, v := range e[i] {
				scaled[k] = v / noise
			}
			l := likelihoods(&scaled, snr)
			w := white[pos+i]
			for x := range like[i] {
				like[i][x] = l[uint8(x)^w]
			}
		}
		return c.code.Decode(like, nil, 80)
	}
	info, ok, passes := try(false)
	if !ok {
		info, ok, passes = try(true)
	}
	if !ok {
		return nil
	}
	data := unpack(info)
	end := int64(c.begin) + int64((first+sent)*n)
	if string(data) == r.lastData && end-r.lastEnd < int64(4*n) && r.lastEnd-end < int64(4*n) {
		return nil // the same transmission, found twice
	}
	r.lastData, r.lastEnd = string(data), end
	return &Frame{
		Call: c.tmpl.call, Data: data, Profile: s.p, Fast: c.fast, Passes: passes,
		// A clean recording has almost no noise to measure; 40 dB says "very strong".
		SNR:    math.Min(10*math.Log10(sig/noise*s.p.Spacing()/2500), 40),
		Offset: c.fbase - s.p.base(r.cfg.Centre),
	}
}

// gone reports whether a transmission whose header has been read has stopped before
// its end. It can tell only for a strong signal, whose tones stand far above the
// noise: if not one symbol in the last second (or the last eight symbols, if that is
// longer) holds a tone within 15 dB of the strength the transmission started with,
// it is over. A weaker signal is never judged gone; it may only have faded.
func (r *Receiver) gone(c *candidate) bool {
	if c.stage != 2 || c.sig < 300*c.noise {
		return false
	}
	n := c.s.p.SymLen
	span := max(8, SampleRate/n)
	done := int((float64(r.total) - c.begin) / float64(n)) // whole symbols received
	if done-span < len(c.tmpl.tones)+headerLen {
		return false
	}
	var e [Tones]float64
	for j := done - span; j < done; j++ {
		if !r.symbol(c.s, c.begin+float64(j*n), c.fbase, &e) {
			return false
		}
		for _, v := range e {
			if v > 0.03*c.sig {
				return false
			}
		}
	}
	return true
}

// present reports whether a transmission whose header has been read still has a
// signal in it. Other alignments of a signal, and now and then plain noise, can pass
// for the start of a transmission and promise minutes more of it; what they cannot do
// is keep a tone standing out of the noise once the real signal has ended.
//
// Over the last half second or so it compares the strongest tone filter in each symbol
// with the noise measured across all the filters in those same symbols. With noise
// alone the strongest of 64 filters averages 4.7 times the noise; with a signal just
// strong enough to decode it averages about 7.
func (r *Receiver) present(c *candidate) bool {
	n := c.s.p.SymLen
	first := len(c.tmpl.tones) + headerLen
	done := int((float64(r.total) - c.begin) / float64(n)) // whole symbols received
	// About half a second, but no fewer than four symbols and no more than sixteen.
	span := min(16, max(4, SampleRate/(2*n)))
	from := max(first, done-span)
	if done-from < 4 {
		return true // too soon to tell
	}
	var e [Tones]float64
	var tops float64
	all := make([]float64, 0, (done-from)*Tones)
	for j := from; j < done; j++ {
		if !r.symbol(c.s, c.begin+float64(j*n), c.fbase, &e) {
			return true
		}
		top := 0.0
		for _, v := range e {
			top = math.Max(top, v)
		}
		tops += top
		all = append(all, e[:]...)
	}
	noise := median(all) / math.Ln2
	return noise <= 0 || tops/float64(done-from) > 5.6*noise
}

// Receiving reports how much longer a transmission that is arriving now will last:
// zero when none is in progress. A transmission counts once its header has been read
// and for as long as its signal is still there.
// Like Feed, it must not be called while another call on the receiver is running.
func (r *Receiver) Receiving() time.Duration {
	var left int64
	for _, c := range r.pending {
		if c.stage == 2 && r.present(c) {
			left = max(left, c.need-r.total)
		}
	}
	return time.Duration(float64(left) / SampleRate * float64(time.Second))
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	sort.Float64s(v)
	return v[len(v)/2]
}

// trim discards audio nothing can need any more.
func (r *Receiver) trim() {
	longest := 0
	for _, s := range r.search {
		longest = max(longest, s.p.SymLen)
	}
	from := r.total - int64(28*longest)
	for _, c := range r.pending {
		at := c.col*int64(c.s.hop) - int64(2*c.s.p.SymLen)
		if c.stage > 0 {
			at = int64(c.begin) - int64(c.s.p.SymLen)
		}
		from = min(from, at)
	}
	for _, s := range r.search {
		from = min(from, s.ncol*int64(s.hop))
	}
	if drop := from - r.start; drop > int64(len(r.buf))/2 && drop > 0 {
		r.buf = append(r.buf[:0], r.buf[drop:]...)
		r.start += drop
	}
}
