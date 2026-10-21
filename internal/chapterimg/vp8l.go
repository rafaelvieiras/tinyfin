package chapterimg

import (
	"math"
	"math/bits"
)

// A VP8L (lossless WebP) encoder built from the public WebP lossless
// bitstream specification: subtract-green, predictor and cross-colour
// transforms, LZ77 backward references with the 2-D distance codes, a colour
// cache, and canonical prefix codes.

// vp8lConfig is one combination of tools tried by encodeVP8L.
type vp8lConfig struct {
	subGreen  bool
	predictor bool
	crossCol  bool
	cacheBits int // 0 for none
	tileBits  int // predictor / cross-colour tile size (log2)
	meta      bool
}

func encodeVP8L(argb []uint32, w, h int, hasAlpha bool) []byte {
	var best []byte
	var bestCfg vp8lConfig
	var preps [2]vp8lPrep // without / with the predictor
	try := func(c vp8lConfig) {
		pi := 0
		if c.predictor {
			pi = 1
		}
		out := encodeVP8LWith(argb, w, h, hasAlpha, c, &preps[pi])
		if best == nil || len(out) < len(best) {
			best, bestCfg = out, c
		}
	}
	for _, cb := range []int{0, 10} {
		try(vp8lConfig{true, true, true, cb, 4, false})
		try(vp8lConfig{true, true, false, cb, 4, false})
		try(vp8lConfig{true, false, false, cb, 4, false})
		try(vp8lConfig{false, false, false, cb, 4, false})
	}
	// the winner again, with several prefix code groups when they pay
	bestCfg.meta = true
	try(bestCfg)
	return best
}

// vp8lPrep memoises the transform stages shared by the configurations.
type vp8lPrep struct {
	sg                       []uint32
	modes                    []uint32
	mtw, mth                 int
	pred                     []uint32
	mult                     []uint32
	ctw, cth                 int
	cc                       []uint32
	haveSG, havePred, haveCC bool
}

func encodeVP8LWith(src []uint32, w, h int, hasAlpha bool, c vp8lConfig, pr *vp8lPrep) []byte {
	pix := src
	var bw bitWriter
	bw.put(0x2f, 8)
	bw.put(uint32(w-1), 14)
	bw.put(uint32(h-1), 14)
	if hasAlpha {
		bw.put(1, 1)
	} else {
		bw.put(0, 1)
	}
	bw.put(0, 3) // version
	if c.subGreen {
		if !pr.haveSG {
			pr.sg = make([]uint32, len(src))
			for i, p := range src {
				g := (p >> 8) & 0xff
				r := ((p>>16)&0xff - g) & 0xff
				b := (p&0xff - g) & 0xff
				pr.sg[i] = (p & 0xff00ff00) | r<<16 | b
			}
			pr.haveSG = true
		}
		pix = pr.sg
		bw.put(1, 1)
		bw.put(2, 2)
	}
	if c.predictor {
		if !pr.havePred {
			pr.modes, pr.mtw, pr.mth = chooseModes(pix, w, h, c.tileBits)
			pr.pred = applyPredictor(pix, w, h, c.tileBits, pr.modes, pr.mtw)
			pr.havePred = true
		}
		bw.put(1, 1)
		bw.put(0, 2)
		bw.put(uint32(c.tileBits-2), 3)
		writeSubImage(&bw, pr.modes, pr.mtw, pr.mth)
		pix = pr.pred
	}
	if c.crossCol {
		cb := c.tileBits + 1
		if !pr.haveCC {
			pr.mult, pr.ctw, pr.cth = chooseColorTransform(pix, w, h, cb)
			pr.cc = make([]uint32, len(pix))
			copy(pr.cc, pix)
			applyColorTransform(pr.cc, w, h, cb, pr.mult, pr.ctw)
			pr.haveCC = true
		}
		bw.put(1, 1)
		bw.put(1, 2)
		bw.put(uint32(cb-2), 3)
		writeSubImage(&bw, pr.mult, pr.ctw, pr.cth)
		pix = pr.cc
	}
	bw.put(0, 1) // no more transforms
	writeEntropyImage(&bw, pix, w, h, c.cacheBits, true, c.meta)
	return bw.bytes()
}

func ch(p uint32, shift uint) int { return int(p>>shift) & 0xff }

func avg2(a, b uint32) uint32 {
	return ((a^b)&0xfefefefe)>>1 + a&b
}

func clampL(v int) uint32 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint32(v)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func predict(mode int, l, t, tr, tl uint32) uint32 {
	switch mode {
	case 0:
		return 0xff000000
	case 1:
		return l
	case 2:
		return t
	case 3:
		return tr
	case 4:
		return tl
	case 5:
		return avg2(avg2(l, tr), t)
	case 6:
		return avg2(l, tl)
	case 7:
		return avg2(l, t)
	case 8:
		return avg2(tl, t)
	case 9:
		return avg2(t, tr)
	case 10:
		return avg2(avg2(l, tl), avg2(t, tr))
	case 11:
		pl, pt := 0, 0
		for s := uint(0); s < 32; s += 8 {
			p := ch(l, s) + ch(t, s) - ch(tl, s)
			pl += absInt(p - ch(l, s))
			pt += absInt(p - ch(t, s))
		}
		if pl < pt {
			return l
		}
		return t
	case 12:
		var o uint32
		for s := uint(0); s < 32; s += 8 {
			o |= clampL(ch(l, s)+ch(t, s)-ch(tl, s)) << s
		}
		return o
	default:
		a := avg2(l, t)
		var o uint32
		for s := uint(0); s < 32; s += 8 {
			o |= clampL(ch(a, s)+(ch(a, s)-ch(tl, s))/2) << s
		}
		return o
	}
}

// sub is the per-channel difference modulo 256.
func sub(a, b uint32) uint32 {
	var o uint32
	for s := uint(0); s < 32; s += 8 {
		o |= uint32((ch(a, s)-ch(b, s))&0xff) << s
	}
	return o
}

var log2tab = func() [4097]float64 {
	var t [4097]float64
	for i := 1; i < len(t); i++ {
		t[i] = math.Log2(float64(i))
	}
	return t
}()

// entropyBits estimates the bits needed for a histogram of n samples.
func entropyBits(h *[256]int32, n int) float64 {
	if n < len(log2tab) {
		ln := log2tab[n]
		var s float64
		for _, c := range h {
			if c > 0 {
				s += float64(c) * (ln - log2tab[c])
			}
		}
		return s
	}
	var s float64
	for _, c := range h {
		if c > 0 {
			s += float64(c) * (math.Log2(float64(n)) - math.Log2(float64(c)))
		}
	}
	return s
}

func predictAt(mode int, pix []uint32, i, x, y, w int) uint32 {
	switch {
	case x == 0 && y == 0:
		return 0xff000000
	case y == 0:
		return pix[i-1]
	case x == 0:
		return pix[i-w]
	}
	return predict(mode, pix[i-1], pix[i-w], pix[i-w+1], pix[i-w-1])
}

func chooseModes(pix []uint32, w, h, tb int) ([]uint32, int, int) {
	ts := 1 << uint(tb)
	tw, th := (w+ts-1)>>uint(tb), (h+ts-1)>>uint(tb)
	modes := make([]uint32, tw*th)
	var hist [4][256]int32
	for ty := 0; ty < th; ty++ {
		for tx := 0; tx < tw; tx++ {
			x0, y0 := tx*ts, ty*ts
			x1, y1 := min(x0+ts, w), min(y0+ts, h)
			bestMode, bestCost := 1, math.MaxFloat64
			for mode := 0; mode < 14; mode++ {
				hist = [4][256]int32{}
				n := 0
				for y := y0; y < y1; y++ {
					for x := x0; x < x1; x++ {
						i := y*w + x
						r := sub(pix[i], predictAt(mode, pix, i, x, y, w))
						hist[0][r&0xff]++
						hist[1][(r>>8)&0xff]++
						hist[2][(r>>16)&0xff]++
						hist[3][r>>24]++
						n++
					}
				}
				cost := entropyBits(&hist[0], n) + entropyBits(&hist[1], n) + entropyBits(&hist[2], n) + entropyBits(&hist[3], n)
				// a mild preference for the cheap-to-describe modes
				if mode == 1 || mode == 2 {
					cost -= 2
				}
				if cost < bestCost {
					bestCost, bestMode = cost, mode
				}
			}
			modes[ty*tw+tx] = 0xff000000 | uint32(bestMode)<<8
		}
	}
	return modes, tw, th
}

func applyPredictor(pix []uint32, w, h, tb int, modes []uint32, tw int) []uint32 {
	out := make([]uint32, len(pix))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			mode := int(modes[(y>>uint(tb))*tw+(x>>uint(tb))]>>8) & 0xff
			out[i] = sub(pix[i], predictAt(mode, pix, i, x, y, w))
		}
	}
	return out
}

func cdelta(t, c int) int { return (int(int8(t)) * int(int8(c))) >> 5 }

func chooseColorTransform(pix []uint32, w, h, tb int) ([]uint32, int, int) {
	ts := 1 << uint(tb)
	tw, th := (w+ts-1)>>uint(tb), (h+ts-1)>>uint(tb)
	mult := make([]uint32, tw*th)
	var hist [256]int32
	for ty := 0; ty < th; ty++ {
		for tx := 0; tx < tw; tx++ {
			x0, y0 := tx*ts, ty*ts
			x1, y1 := min(x0+ts, w), min(y0+ts, h)
			n := (x1 - x0) * (y1 - y0)
			cost := func(sel func(p uint32) int) float64 {
				hist = [256]int32{}
				for y := y0; y < y1; y++ {
					for x := x0; x < x1; x++ {
						hist[sel(pix[y*w+x])&0xff]++
					}
				}
				return entropyBits(&hist, n)
			}
			search := func(f func(v int) func(p uint32) int) int {
				best, bestCost := 0, cost(f(0))
				try := func(v int) {
					if v < -128 || v > 127 {
						return
					}
					if c := cost(f(v)); c < bestCost-1 {
						best, bestCost = v, c
					}
				}
				for v := -128; v < 128; v += 8 {
					try(v)
				}
				for _, d := range []int{4, 2, 1} {
					c := best
					try(c - d)
					try(c + d)
				}
				return best
			}
			g2r := search(func(v int) func(p uint32) int {
				return func(p uint32) int { return ch(p, 16) - cdelta(v, ch(p, 8)) }
			})
			g2b := search(func(v int) func(p uint32) int {
				return func(p uint32) int { return ch(p, 0) - cdelta(v, ch(p, 8)) }
			})
			r2b := search(func(v int) func(p uint32) int {
				return func(p uint32) int {
					return ch(p, 0) - cdelta(g2b, ch(p, 8)) - cdelta(v, ch(p, 16))
				}
			})
			mult[ty*tw+tx] = 0xff000000 | uint32(r2b&0xff)<<16 | uint32(g2b&0xff)<<8 | uint32(g2r&0xff)
		}
	}
	return mult, tw, th
}

func applyColorTransform(pix []uint32, w, h, tb int, mult []uint32, tw int) {
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m := mult[(y>>uint(tb))*tw+(x>>uint(tb))]
			g2r, g2b, r2b := int(m&0xff), int(m>>8)&0xff, int(m>>16)&0xff
			p := pix[y*w+x]
			g, r, b := ch(p, 8), ch(p, 16), ch(p, 0)
			nr := (r - cdelta(g2r, g)) & 0xff
			nb := (b - cdelta(g2b, g) - cdelta(r2b, r)) & 0xff
			pix[y*w+x] = p&0xff00ff00 | uint32(nr)<<16 | uint32(nb)
		}
	}
}

func writeSubImage(bw *bitWriter, pix []uint32, w, h int) {
	writeEntropyImage(bw, pix, w, h, 0, false, false)
}

// ---- backward references ----

var planeCodes = [120][2]int{
	{0, 1}, {1, 0}, {1, 1}, {-1, 1}, {0, 2}, {2, 0}, {1, 2}, {-1, 2},
	{2, 1}, {-2, 1}, {2, 2}, {-2, 2}, {0, 3}, {3, 0}, {1, 3}, {-1, 3},
	{3, 1}, {-3, 1}, {2, 3}, {-2, 3}, {3, 2}, {-3, 2}, {0, 4}, {4, 0},
	{1, 4}, {-1, 4}, {4, 1}, {-4, 1}, {3, 3}, {-3, 3}, {2, 4}, {-2, 4},
	{4, 2}, {-4, 2}, {0, 5}, {3, 4}, {-3, 4}, {4, 3}, {-4, 3}, {5, 0},
	{1, 5}, {-1, 5}, {5, 1}, {-5, 1}, {2, 5}, {-2, 5}, {5, 2}, {-5, 2},
	{4, 4}, {-4, 4}, {3, 5}, {-3, 5}, {5, 3}, {-5, 3}, {0, 6}, {6, 0},
	{1, 6}, {-1, 6}, {6, 1}, {-6, 1}, {2, 6}, {-2, 6}, {6, 2}, {-6, 2},
	{4, 5}, {-4, 5}, {5, 4}, {-5, 4}, {3, 6}, {-3, 6}, {6, 3}, {-6, 3},
	{0, 7}, {7, 0}, {1, 7}, {-1, 7}, {5, 5}, {-5, 5}, {7, 1}, {-7, 1},
	{4, 6}, {-4, 6}, {6, 4}, {-6, 4}, {2, 7}, {-2, 7}, {7, 2}, {-7, 2},
	{3, 7}, {-3, 7}, {7, 3}, {-7, 3}, {5, 6}, {-5, 6}, {6, 5}, {-6, 5},
	{8, 0}, {4, 7}, {-4, 7}, {7, 4}, {-7, 4}, {8, 1}, {8, 2}, {6, 6},
	{-6, 6}, {8, 3}, {5, 7}, {-5, 7}, {7, 5}, {-7, 5}, {8, 4}, {6, 7},
	{-6, 7}, {7, 6}, {-7, 6}, {8, 5}, {7, 7}, {-7, 7}, {8, 6}, {8, 7},
}

// distToCode maps a pixel distance to its distance code for an image w wide.
func distToCode(dist int, w int, lookup map[int]int) int {
	if c, ok := lookup[dist]; ok {
		return c
	}
	return dist + 120
}

func planeLookup(w int) (map[int]int, []int) {
	m := make(map[int]int, 120)
	var ds []int
	for i, p := range planeCodes {
		d := p[0] + p[1]*w
		if d < 1 {
			d = 1
		}
		if _, ok := m[d]; !ok {
			m[d] = i + 1
			ds = append(ds, d)
		}
	}
	return m, ds
}

// prefixSplit splits v >= 1 into a prefix symbol and extra bits.
func prefixSplit(v int) (sym, nbits, extra int) {
	d := v - 1
	if d < 2 {
		return d, 0, 0
	}
	hi := bits.Len(uint(d)) - 1
	second := (d >> uint(hi-1)) & 1
	nbits = hi - 1
	return 2*hi + second, nbits, d & (1<<uint(nbits) - 1)
}

type token struct {
	kind   uint8 // 0 literal, 1 cache index, 2 copy
	a      uint32
	length int32
	dist   int32 // distance code
}

const maxMatch = 4096

func tokenize(pix []uint32, w int, cacheBits int, lz bool) []token {
	n := len(pix)
	var cache []uint32
	var shift uint
	if cacheBits > 0 {
		cache = make([]uint32, 1<<uint(cacheBits))
		shift = uint(32 - cacheBits)
	}
	look, near := planeLookup(w)
	const hbits = 16
	head := make([]int32, 1<<hbits)
	for i := range head {
		head[i] = -1
	}
	prev := make([]int32, n)
	hash := func(i int) uint32 {
		return (pix[i]*0x1e35a7bd ^ pix[i+1]*0x9e3779b1 ^ pix[i+2]*0x85ebca6b) >> (32 - hbits)
	}
	inserted := 0
	insertTo := func(upto int) { // insert positions < upto
		for ; inserted < upto; inserted++ {
			if inserted+2 < n {
				hh := hash(inserted)
				prev[inserted] = head[hh]
				head[hh] = int32(inserted)
			}
		}
	}
	matchLen := func(i, d int) int {
		m := min(maxMatch, n-i)
		l := 0
		for l < m && pix[i+l] == pix[i+l-d] {
			l++
		}
		return l
	}
	score := func(l, d int) int {
		return l*6 - 2*bits.Len(uint(distToCode(d, w, look)))
	}
	find := func(i int) (bl, bd int) {
		if i+2 >= n {
			return 0, 0
		}
		insertTo(i)
		bestScore := 0
		consider := func(d int) {
			if d < 1 || d > i {
				return
			}
			l := matchLen(i, d)
			if l < 3 {
				return
			}
			if s := score(l, d); s > bestScore {
				bestScore, bl, bd = s, l, d
			}
		}
		for _, d := range near {
			consider(d)
		}
		cnt := 0
		for j := head[hash(i)]; j >= 0 && cnt < 40; j = prev[j] {
			consider(i - int(j))
			cnt++
		}
		return
	}
	var toks []token
	lit := func(i int) {
		p := pix[i]
		if cache != nil {
			k := (p * 0x1e35a7bd) >> shift
			if cache[k] == p {
				toks = append(toks, token{kind: 1, a: k})
				return
			}
			cache[k] = p
		}
		toks = append(toks, token{kind: 0, a: p})
	}
	for i := 0; i < n; {
		var bl, bd int
		if lz {
			bl, bd = find(i)
		}
		if bl >= 3 {
			// lazy: a clearly better match one pixel later wins
			if i+1 < n {
				l2, d2 := find(i + 1)
				if l2 >= 3 && score(l2, d2) > score(bl, bd)+6 {
					lit(i)
					i++
					continue
				}
			}
			toks = append(toks, token{kind: 2, length: int32(bl), dist: int32(distToCode(bd, w, look))})
			if cache != nil {
				for k := i; k < i+bl; k++ {
					cache[(pix[k]*0x1e35a7bd)>>shift] = pix[k]
				}
			}
			i += bl
			continue
		}
		lit(i)
		i++
	}
	return toks
}

func writeEntropyImage(bw *bitWriter, pix []uint32, w, h, cacheBits int, main, meta bool) {
	toks := tokenize(pix, w, cacheBits, true)
	if !main {
		writeTokens(bw, toks, w, h, cacheBits, false, nil)
		return
	}
	best := bw.clone()
	writeTokens(&best, toks, w, h, cacheBits, true, nil)
	if !meta {
		*bw = best
		return
	}
	size := func(b *bitWriter) int { return len(b.buf)*8 + int(b.nacc) }
	// with and without backward references; several groups of prefix codes
	// let the regions that are all the same cost no bits at all
	for _, lz := range []bool{true, false} {
		t := toks
		if !lz {
			t = tokenize(pix, w, cacheBits, false)
			b := bw.clone()
			writeTokens(&b, t, w, h, cacheBits, true, nil)
			if size(&b) < size(&best) {
				best = b
			}
		}
		if m := planGroups(t, w, h, cacheBits); m != nil {
			b := bw.clone()
			writeTokens(&b, t, w, h, cacheBits, true, m)
			if size(&b) < size(&best) {
				best = b
			}
		}
	}
	*bw = best
}

func (b *bitWriter) clone() bitWriter {
	return bitWriter{buf: append([]byte(nil), b.buf...), acc: b.acc, nacc: b.nacc}
}

// metaPlan says which prefix code group codes the tokens that start in each
// tile of the picture.
type metaPlan struct {
	bits   int
	tw, th int
	group  []int // per tile
	n      int   // number of groups
}

// histogram layout of one group: green (with lengths and cache), red, blue,
// alpha, distance
type histLayout struct{ g, total int }

func (l histLayout) segs() [5][2]int {
	return [5][2]int{{0, l.g}, {l.g, l.g + 256}, {l.g + 256, l.g + 512}, {l.g + 512, l.g + 768}, {l.g + 768, l.g + 808}}
}

func addToken(h []int32, l histLayout, t token) {
	switch t.kind {
	case 0:
		h[(t.a>>8)&0xff]++
		h[l.g+int((t.a>>16)&0xff)]++
		h[l.g+256+int(t.a&0xff)]++
		h[l.g+512+int(t.a>>24)]++
	case 1:
		h[280+int(t.a)]++
	default:
		s, _, _ := prefixSplit(int(t.length))
		h[256+s]++
		s, _, _ = prefixSplit(int(t.dist))
		h[l.g+768+s]++
	}
}

var log2f = func() (t [8192]float64) {
	for i := 1; i < len(t); i++ {
		t[i] = math.Log2(float64(i))
	}
	return
}()

func lg(c int32) float64 {
	if int(c) < len(log2f) {
		return log2f[c]
	}
	return math.Log2(float64(c))
}

// segCost estimates the bits of a histogram segment: its code and its data.
func segCost(seg []int32) float64 {
	var tot int32
	n := 0
	for _, c := range seg {
		if c > 0 {
			tot += c
			n++
		}
	}
	switch n {
	case 0:
		return 4
	case 1:
		return 4
	case 2:
		if len(seg) <= 256 {
			// simple code
			var d float64
			lt := lg(tot)
			for _, c := range seg {
				if c > 0 {
					d += float64(c) * (lt - lg(c))
				}
			}
			return 12 + d
		}
	}
	d := 0.0
	lt := lg(tot)
	for _, c := range seg {
		if c > 0 {
			d += float64(c) * (lt - lg(c))
		}
	}
	return 70 + 3*float64(n) + d
}

func histCost(h []int32, l histLayout) float64 {
	c := 0.0
	for _, s := range l.segs() {
		c += segCost(h[s[0]:s[1]])
	}
	return c
}

// planGroups clusters the tiles of the picture by the tokens that start in
// them; it returns nil when one group is as good.
func planGroups(toks []token, w, h, cacheBits int) *metaPlan {
	bits := 3
	for ((w+(1<<uint(bits))-1)>>uint(bits))*((h+(1<<uint(bits))-1)>>uint(bits)) > 300 {
		bits++
	}
	tw, th := (w+(1<<uint(bits))-1)>>uint(bits), (h+(1<<uint(bits))-1)>>uint(bits)
	if tw*th < 2 {
		return nil
	}
	gsize := 256 + 24
	if cacheBits > 0 {
		gsize += 1 << uint(cacheBits)
	}
	l := histLayout{g: gsize, total: gsize + 808}
	nt := tw * th
	hs := make([][]int32, nt)
	// l.g is the offset of red; the green segment is [0, g)
	pos := 0
	for _, t := range toks {
		x, y := pos%w, pos/w
		ti := (y>>uint(bits))*tw + (x >> uint(bits))
		if hs[ti] == nil {
			hs[ti] = make([]int32, l.total)
		}
		addToken(hs[ti], l, t)
		if t.kind == 2 {
			pos += int(t.length)
		} else {
			pos++
		}
	}
	// tiles with the same histogram share a cluster from the start
	type cl struct {
		h     []int32
		cost  float64
		alive bool
	}
	var cls []*cl
	tileCl := make([]int, nt)
	seen := map[string]int{}
	for ti := 0; ti < nt; ti++ {
		if hs[ti] == nil {
			hs[ti] = make([]int32, l.total)
		}
		key := string(int32Bytes(hs[ti]))
		if ci, ok := seen[key]; ok {
			tileCl[ti] = ci
			continue
		}
		seen[key] = len(cls)
		tileCl[ti] = len(cls)
		cls = append(cls, &cl{h: hs[ti], cost: histCost(hs[ti], l), alive: true})
	}
	n := len(cls)
	parent := make([]int, n)
	for i := range parent {
		parent[i] = i
	}
	tmp := make([]int32, l.total)
	mergeCost := func(a, b *cl) float64 {
		for i := range tmp {
			tmp[i] = a.h[i] + b.h[i]
		}
		return histCost(tmp, l) - a.cost - b.cost
	}
	delta := make([][]float64, n)
	for i := range delta {
		delta[i] = make([]float64, n)
	}
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			delta[i][j] = mergeCost(cls[i], cls[j])
		}
	}
	alive := n
	for alive > 1 {
		bi, bj, best := -1, -1, 0.0
		for i := 0; i < n; i++ {
			if !cls[i].alive {
				continue
			}
			for j := i + 1; j < n; j++ {
				if cls[j].alive && delta[i][j] < best {
					bi, bj, best = i, j, delta[i][j]
				}
			}
		}
		if bi < 0 {
			break
		}
		for k := range cls[bi].h {
			cls[bi].h[k] += cls[bj].h[k]
		}
		cls[bi].cost = histCost(cls[bi].h, l)
		cls[bj].alive = false
		parent[bj] = bi
		alive--
		for k := 0; k < n; k++ {
			if !cls[k].alive || k == bi {
				continue
			}
			if k < bi {
				delta[k][bi] = mergeCost(cls[k], cls[bi])
			} else {
				delta[bi][k] = mergeCost(cls[bi], cls[k])
			}
		}
	}
	if alive < 2 {
		return nil
	}
	find := func(i int) int {
		for parent[i] != i {
			i = parent[i]
		}
		return i
	}
	idx := map[int]int{}
	m := &metaPlan{bits: bits, tw: tw, th: th, group: make([]int, nt)}
	for ti := 0; ti < nt; ti++ {
		r := find(tileCl[ti])
		g, ok := idx[r]
		if !ok {
			g = len(idx)
			idx[r] = g
		}
		m.group[ti] = g
	}
	m.n = len(idx)
	return m
}

func int32Bytes(h []int32) []byte {
	b := make([]byte, 0, len(h)*4)
	for _, v := range h {
		b = append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
	}
	return b
}

func writeTokens(bw *bitWriter, toks []token, w, h, cacheBits int, main bool, m *metaPlan) {
	if cacheBits > 0 {
		bw.put(1, 1)
		bw.put(uint32(cacheBits), 4)
	} else {
		bw.put(0, 1)
	}
	gsize := 256 + 24
	if cacheBits > 0 {
		gsize += 1 << uint(cacheBits)
	}
	l := histLayout{g: gsize, total: gsize + 808}
	groups := 1
	if m != nil && main {
		bw.put(1, 1)
		bw.put(uint32(m.bits-2), 3)
		img := make([]uint32, len(m.group))
		for i, g := range m.group {
			img[i] = uint32(g) << 8
		}
		writeEntropyImage2(bw, img, m.tw, m.th)
		groups = m.n
	} else if main {
		bw.put(0, 1) // no meta prefix image
	}
	hs := make([][]int32, groups)
	for i := range hs {
		hs[i] = make([]int32, l.total)
	}
	pos := 0
	groupAt := func(pos int) int {
		if m == nil {
			return 0
		}
		x, y := pos%w, pos/w
		return m.group[(y>>uint(m.bits))*m.tw+(x>>uint(m.bits))]
	}
	for _, t := range toks {
		addToken(hs[groupAt(pos)], l, t)
		if t.kind == 2 {
			pos += int(t.length)
		} else {
			pos++
		}
	}
	type codes struct{ g, r, b, a, d *prefix }
	cs := make([]codes, groups)
	for gi := range cs {
		hh := hs[gi]
		toInt := func(s []int32) []int {
			o := make([]int, len(s))
			for i, v := range s {
				o[i] = int(v)
			}
			return o
		}
		cs[gi].g = bw.prefixCode(toInt(hh[:l.g]), l.g)
		cs[gi].r = bw.prefixCode(toInt(hh[l.g:l.g+256]), 256)
		cs[gi].b = bw.prefixCode(toInt(hh[l.g+256:l.g+512]), 256)
		cs[gi].a = bw.prefixCode(toInt(hh[l.g+512:l.g+768]), 256)
		cs[gi].d = bw.prefixCode(toInt(hh[l.g+768:l.g+808]), 40)
	}
	pos = 0
	for _, t := range toks {
		c := &cs[groupAt(pos)]
		switch t.kind {
		case 0:
			c.g.write(bw, int((t.a>>8)&0xff))
			c.r.write(bw, int((t.a>>16)&0xff))
			c.b.write(bw, int(t.a&0xff))
			c.a.write(bw, int(t.a>>24))
			pos++
		case 1:
			c.g.write(bw, 280+int(t.a))
			pos++
		default:
			s, nb, ex := prefixSplit(int(t.length))
			c.g.write(bw, 256+s)
			if nb > 0 {
				bw.put(uint32(ex), uint(nb))
			}
			s, nb, ex = prefixSplit(int(t.dist))
			c.d.write(bw, s)
			if nb > 0 {
				bw.put(uint32(ex), uint(nb))
			}
			pos += int(t.length)
		}
	}
}

// writeEntropyImage2 writes a sub-image (meta prefix image): no cache, no
// further meta image.
func writeEntropyImage2(bw *bitWriter, pix []uint32, w, h int) {
	writeTokens(bw, tokenize(pix, w, 0, true), w, h, 0, false, nil)
}
