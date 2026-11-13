package tern

import (
	"fmt"
	"sync"
)

// The error-correcting code is a low-density parity-check code over GF(64) in which
// every symbol takes part in exactly two checks. Such a code is a graph: the checks
// are its vertices and each symbol is an edge joining the two checks it appears in.
//
// The graph here is a set of rings laid over the same m vertices. The parity symbols
// form the plain ring 0-1-2-...-(m-1)-0. The information symbols form one or more
// further rings that visit the vertices in a scrambled order. With one information
// ring there are as many information symbols as parity symbols (rate 1/2); with three
// there are three times as many (rate 3/4).
//
// A code is built for the exact number of information symbols a frame needs, by a
// fixed procedure, so both ends arrive at the same code without a table.

const (
	// MinInfo and MaxInfo bound the number of information symbols in a frame.
	MinInfo = 8
	MaxInfo = 2048

	ringTrials = 24 // candidate orderings tried for each information ring
)

// Code is the code for one frame size and rate.
type Code struct {
	K     int // information symbols carried
	Rings int // information rings: 1 for rate 1/2, 3 for rate 3/4
	m     int // checks

	// Variables are numbered: information symbols 0..Rings*m-1 (those from K upward are
	// padding, fixed at zero and never sent), then parity symbols.
	nvar int
	// Per check, its symbols and their coefficients. Slots 0 and 1 are the parity
	// symbols: slot 0 is the previous one in the ring, slot 1 the check's own.
	chkVar  [][]int32
	chkCoef [][]uint8
	// Per variable, the two checks it joins and its slot in each.
	varChk  [][2]int32
	varSlot [][2]int8

	// order lists the transmitted variables (the K information symbols and the m
	// parity symbols) in the order they are sent.
	order []int32
}

// Sent returns the number of symbols transmitted for this code.
func (c *Code) Sent() int { return c.K + c.m }

var (
	codeMu    sync.Mutex
	codeCache = map[[2]int]*Code{}
)

// CodeFor returns the code that carries k information symbols with the given number
// of information rings (1 or 3).
func CodeFor(k, rings int) (*Code, error) {
	if k < MinInfo || k > MaxInfo {
		return nil, fmt.Errorf("tern: %d information symbols is outside %d..%d", k, MinInfo, MaxInfo)
	}
	if rings != 1 && rings != 3 {
		return nil, fmt.Errorf("tern: %d information rings; 1 or 3 are defined", rings)
	}
	key := [2]int{k, rings}
	codeMu.Lock()
	defer codeMu.Unlock()
	if c := codeCache[key]; c != nil {
		return c, nil
	}
	c := build(k, rings)
	codeCache[key] = c
	return c, nil
}

type edge struct {
	to int32
	id int32
}

func build(k, rings int) *Code {
	m := (k + rings - 1) / rings
	if m < 4 {
		m = 4
	}
	c := &Code{K: k, Rings: rings, m: m, nvar: (rings + 1) * m}
	r := newRNG(uint32(k)<<2 | uint32(rings))

	// Choose the information rings: for each, the ordering among ringTrials candidates
	// that leaves the graph with the largest girth and, among those, the fewest
	// vertices on a shortest cycle.
	adj := make([][]edge, m)
	var nextID int32
	addRing := func(a [][]edge, order []int32) {
		for j := 0; j < m; j++ {
			u, v := order[j], order[(j+1)%m]
			a[u] = append(a[u], edge{v, nextID + int32(j)})
			a[v] = append(a[v], edge{u, nextID + int32(j)})
		}
	}
	plain := make([]int32, m)
	for i := range plain {
		plain[i] = int32(i)
	}
	addRing(adj, plain)
	nextID += int32(m)
	orders := make([][]int32, rings)
	for ring := 0; ring < rings; ring++ {
		var best []int32
		bestGirth, bestCount := -1, 0
		for t := 0; t < ringTrials; t++ {
			cand := r.shuffle(m)
			trial := make([][]edge, m)
			for i := range adj {
				trial[i] = append([]edge(nil), adj[i]...)
			}
			addRing(trial, cand)
			g, n := girth(trial)
			if g > bestGirth || (g == bestGirth && n < bestCount) {
				best, bestGirth, bestCount = cand, g, n
			}
		}
		addRing(adj, best)
		nextID += int32(m)
		orders[ring] = best
	}

	// Lay out checks and variables. Parity symbol i joins check i (slot 1) and check
	// i+1 (slot 0). Information symbol j of ring g joins the two checks that are
	// neighbours at position j of that ring's ordering.
	deg := 2 + 2*rings
	c.chkVar = make([][]int32, m)
	c.chkCoef = make([][]uint8, m)
	for i := range c.chkVar {
		c.chkVar[i] = make([]int32, 2, deg)
		c.chkCoef[i] = make([]uint8, 2, deg)
	}
	c.varChk = make([][2]int32, c.nvar)
	c.varSlot = make([][2]int8, c.nvar)
	coef := func() uint8 { return uint8(1 + r.intn(63)) }
	parity := int32(rings * m)
	for i := 0; i < m; i++ {
		v := parity + int32(i)
		next := (i + 1) % m
		c.chkVar[i][1], c.chkCoef[i][1] = v, coef()
		c.chkVar[next][0], c.chkCoef[next][0] = v, coef()
		c.varChk[v] = [2]int32{int32(i), int32(next)}
		c.varSlot[v] = [2]int8{1, 0}
	}
	for ring := 0; ring < rings; ring++ {
		for j := 0; j < m; j++ {
			v := int32(ring*m + j)
			a, b := orders[ring][j], orders[ring][(j+1)%m]
			c.varChk[v] = [2]int32{a, b}
			c.varSlot[v] = [2]int8{int8(len(c.chkVar[a])), 0}
			c.chkVar[a] = append(c.chkVar[a], v)
			c.chkCoef[a] = append(c.chkCoef[a], coef())
			c.varSlot[v][1] = int8(len(c.chkVar[b]))
			c.chkVar[b] = append(c.chkVar[b], v)
			c.chkCoef[b] = append(c.chkCoef[b], coef())
		}
	}

	// The parity ring must be solvable for any information symbols: going once round
	// it must not bring a parity symbol back multiplied by exactly one. If it does,
	// nudge one coefficient.
	for c.ringGain() == 1 {
		c.chkCoef[0][1] = c.chkCoef[0][1]%63 + 1
	}

	// Transmission order: a pseudo-random interleaving of information and parity, so
	// that a burst of interference is spread across the graph.
	sent := make([]int32, 0, k+m)
	for v := 0; v < k; v++ {
		sent = append(sent, int32(v))
	}
	for i := 0; i < m; i++ {
		sent = append(sent, parity+int32(i))
	}
	perm := r.shuffle(len(sent))
	c.order = make([]int32, len(sent))
	for i, p := range perm {
		c.order[i] = sent[p]
	}
	return c
}

// ringGain is the factor a parity symbol is multiplied by in one trip round the parity
// ring when the checks are solved in order.
func (c *Code) ringGain() uint8 {
	g := uint8(1)
	for i := 0; i < c.m; i++ {
		g = div(mul(c.chkCoef[i][0], g), c.chkCoef[i][1])
	}
	return g
}

// girth returns the length of the shortest cycle in a multigraph and how many vertices
// lie on a cycle of that length (as seen by a search from each vertex). Parallel edges
// form a cycle of length two.
func girth(adj [][]edge) (length, count int) {
	n := len(adj)
	dist := make([]int32, n)
	via := make([]int32, n)
	queue := make([]int32, 0, n)
	length = 1 << 30
	for root := 0; root < n; root++ {
		for i := range dist {
			dist[i] = -1
		}
		dist[root], via[root] = 0, -1
		queue = append(queue[:0], int32(root))
		found := 1 << 30
		for head := 0; head < len(queue); head++ {
			u := queue[head]
			if int(2*dist[u]+1) >= found {
				break
			}
			for _, e := range adj[u] {
				if e.id == via[u] {
					continue
				}
				if dist[e.to] < 0 {
					dist[e.to], via[e.to] = dist[u]+1, e.id
					queue = append(queue, e.to)
				} else if l := int(dist[u] + dist[e.to] + 1); l < found {
					found = l
				}
			}
		}
		switch {
		case found < length:
			length, count = found, 1
		case found == length:
			count++
		}
	}
	return length, count
}

// Encode returns the symbols to transmit, in transmission order, for k information
// symbols (each 0..63).
func (c *Code) Encode(info []uint8) []uint8 {
	if len(info) != c.K {
		panic("tern: wrong number of information symbols")
	}
	val := make([]uint8, c.nvar)
	copy(val, info)
	m := c.m
	// Each check's sum over its information symbols.
	syn := make([]uint8, m)
	for i := 0; i < m; i++ {
		var s uint8
		for slot := 2; slot < len(c.chkVar[i]); slot++ {
			s ^= mul(c.chkCoef[i][slot], val[c.chkVar[i][slot]])
		}
		syn[i] = s
	}
	// Check i reads: a*p[i-1] + b*p[i] = syn[i]. Write p[i] = alpha[i]*x + beta[i] with
	// x = p[m-1], go once round, and solve for x.
	alpha := make([]uint8, m)
	beta := make([]uint8, m)
	pa, pb := uint8(1), uint8(0)
	for i := 0; i < m; i++ {
		a, b := c.chkCoef[i][0], c.chkCoef[i][1]
		pa = div(mul(a, pa), b)
		pb = div(syn[i]^mul(a, pb), b)
		alpha[i], beta[i] = pa, pb
	}
	x := div(beta[m-1], 1^alpha[m-1])
	parity := c.Rings * m
	for i := 0; i < m; i++ {
		val[parity+i] = mul(alpha[i], x) ^ beta[i]
	}
	out := make([]uint8, len(c.order))
	for i, v := range c.order {
		out[i] = val[v]
	}
	return out
}

// check reports whether a full assignment of variables satisfies every check.
func (c *Code) check(val []uint8) bool {
	for i := range c.chkVar {
		var s uint8
		for slot, v := range c.chkVar[i] {
			s ^= mul(c.chkCoef[i][slot], val[v])
		}
		if s != 0 {
			return false
		}
	}
	return true
}
