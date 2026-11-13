package tern

import "math"

// Decode recovers the information symbols from what the demodulator measured.
// like[i] holds, for the i-th transmitted symbol, the likelihood of each of the 64
// values (any positive scale). known, if not nil, gives information symbols the
// receiver already knows: known[j] >= 0 fixes information symbol j to that value.
//
// It returns the information symbols, whether every parity check was satisfied, and
// the number of passes used. The decoder passes probability vectors along the edges
// of the code's graph (belief propagation), visiting the checks in turn.
func (c *Code) Decode(like [][Tones]float64, known []int16, maxIter int) (info []uint8, ok bool, iters int) {
	if len(like) != len(c.order) {
		panic("tern: wrong number of received symbols")
	}
	prior := make([][Tones]float64, c.nvar)
	for v := range prior {
		prior[v][0] = 1 // padding: known to be zero
	}
	for i, v := range c.order {
		p := &prior[v]
		var sum float64
		for _, x := range like[i] {
			sum += x
		}
		if sum <= 0 || math.IsNaN(sum) || math.IsInf(sum, 0) {
			for x := range p {
				p[x] = 1.0 / Tones
			}
			continue
		}
		for x := range p {
			p[x] = like[i][x]/sum + 1e-12
		}
	}
	for j, k := range known {
		if k >= 0 && j < c.K {
			prior[j] = [Tones]float64{}
			prior[j][k] = 1
		}
	}

	// Messages from checks to variables, indexed by check and slot.
	base := make([]int, c.m+1)
	for i := 0; i < c.m; i++ {
		base[i+1] = base[i] + len(c.chkVar[i])
	}
	msg := make([][Tones]float64, base[c.m])
	for i := range msg {
		for x := range msg[i] {
			msg[i][x] = 1.0 / Tones
		}
	}
	other := func(v int32, chk int, slot int) *[Tones]float64 {
		o := 0
		if c.varChk[v][0] == int32(chk) && int(c.varSlot[v][0]) == slot {
			o = 1
		}
		return &msg[base[c.varChk[v][o]]+int(c.varSlot[v][o])]
	}

	deg := 2 + 2*c.Rings
	f := make([][Tones]float64, deg)
	pre := make([][Tones]float64, deg+1)
	suf := make([][Tones]float64, deg+1)
	val := make([]uint8, c.nvar)
	for iters = 1; iters <= maxIter; iters++ {
		for chk := 0; chk < c.m; chk++ {
			vars, coefs := c.chkVar[chk], c.chkCoef[chk]
			for slot, v := range vars {
				mo := other(v, chk, slot)
				p := &prior[v]
				var in [Tones]float64
				var sum float64
				for x := 0; x < Tones; x++ {
					in[x] = p[x] * mo[x]
					sum += in[x]
				}
				if sum <= 0 {
					in, sum = *p, 1
				}
				// Move to the check's domain: the term h*x takes the value y = h*x.
				h := &gfMul[coefs[slot]]
				var t [Tones]float64
				scale := 1 / sum
				for x := 0; x < Tones; x++ {
					t[h[x]] = in[x] * scale
				}
				wht(&t)
				f[slot] = t
			}
			// For each slot, the product of the transforms of all the others.
			for x := range pre[0] {
				pre[0][x], suf[len(vars)][x] = 1, 1
			}
			for s := 0; s < len(vars); s++ {
				for x := 0; x < Tones; x++ {
					pre[s+1][x] = pre[s][x] * f[s][x]
				}
			}
			for s := len(vars) - 1; s >= 0; s-- {
				for x := 0; x < Tones; x++ {
					suf[s][x] = suf[s+1][x] * f[s][x]
				}
			}
			for slot := range vars {
				var g [Tones]float64
				for x := 0; x < Tones; x++ {
					g[x] = pre[slot][x] * suf[slot+1][x]
				}
				wht(&g)
				var sum float64
				for x := 0; x < Tones; x++ {
					if g[x] < 1e-9 {
						g[x] = 1e-9
					}
					sum += g[x]
				}
				h := &gfMul[coefs[slot]]
				out := &msg[base[chk]+slot]
				scale := 1 / sum
				for x := 0; x < Tones; x++ {
					out[x] = g[h[x]] * scale
				}
			}
		}
		// Decide each symbol from everything known about it, and stop if the result is
		// a codeword.
		for v := range val {
			a := &msg[base[c.varChk[v][0]]+int(c.varSlot[v][0])]
			b := &msg[base[c.varChk[v][1]]+int(c.varSlot[v][1])]
			p := &prior[v]
			best, bestX := -1.0, 0
			for x := 0; x < Tones; x++ {
				if q := p[x] * a[x] * b[x]; q > best {
					best, bestX = q, x
				}
			}
			val[v] = uint8(bestX)
		}
		if c.check(val) {
			return append([]uint8(nil), val[:c.K]...), true, iters
		}
	}
	return append([]uint8(nil), val[:c.K]...), false, maxIter
}

// lnI0 is the logarithm of the modified Bessel function of the first kind, order zero.
// For a tone of known strength in white noise, the log-likelihood of a tone given the
// amplitude heard in its filter is lnI0 of twice the product of the two amplitudes
// over the noise power.
func lnI0(x float64) float64 {
	if x < 3.75 {
		t := x / 3.75
		t *= t
		return math.Log(1 + t*(3.5156229+t*(3.0899424+t*(1.2067492+t*(0.2659732+t*(0.0360768+t*0.0045813))))))
	}
	t := 3.75 / x
	p := 0.39894228 + t*(0.01328592+t*(0.00225319+t*(-0.00157565+t*(0.00916281+t*(-0.02057706+t*(0.02635537+t*(-0.01647633+t*0.00392377)))))))
	return x - 0.5*math.Log(x) + math.Log(p)
}

// likelihoods turns the energies heard in the 64 tone filters during one symbol into
// likelihoods. energy is in units of the noise power per filter, and snr is the
// signal's energy per symbol in the same units.
func likelihoods(energy *[Tones]float64, snr float64) [Tones]float64 {
	var l [Tones]float64
	top := math.Inf(-1)
	for k, e := range energy {
		l[k] = lnI0(2 * math.Sqrt(snr*e))
		top = math.Max(top, l[k])
	}
	for k := range l {
		l[k] = math.Exp(l[k] - top)
	}
	return l
}
