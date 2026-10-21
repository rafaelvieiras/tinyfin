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
}

func encodeVP8L(argb []uint32, w, h int, hasAlpha bool) []byte {
	var best []byte
	try := func(c vp8lConfig) {
		out := encodeVP8LWith(argb, w, h, hasAlpha, c)
		if best == nil || len(out) < len(best) {
			best = out
		}
	}
	for _, cb := range []int{0, 10} {
		try(vp8lConfig{true, true, true, cb, 4})
		try(vp8lConfig{true, true, false, cb, 4})
		try(vp8lConfig{true, false, false, cb, 4})
		try(vp8lConfig{false, false, false, cb, 4})
	}
	return best
}

func encodeVP8LWith(src []uint32, w, h int, hasAlpha bool, c vp8lConfig) []byte {
	pix := make([]uint32, len(src))
	copy(pix, src)
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
		bw.put(1, 1)
		bw.put(2, 2)
		for i, p := range pix {
			g := (p >> 8) & 0xff
			r := ((p>>16)&0xff - g) & 0xff
			b := (p&0xff - g) & 0xff
			pix[i] = (p & 0xff00ff00) | r<<16 | b
		}
	}
	if c.predictor {
		bw.put(1, 1)
		bw.put(0, 2)
		bw.put(uint32(c.tileBits-2), 3)
		modes, tw, th := chooseModes(pix, w, h, c.tileBits)
		writeSubImage(&bw, modes, tw, th)
		pix = applyPredictor(pix, w, h, c.tileBits, modes, tw)
	}
	if c.crossCol {
		cb := c.tileBits + 1
		bw.put(1, 1)
		bw.put(1, 2)
		bw.put(uint32(cb-2), 3)
		mult, tw, th := chooseColorTransform(pix, w, h, cb)
		writeSubImage(&bw, mult, tw, th)
		applyColorTransform(pix, w, h, cb, mult, tw)
	}
	bw.put(0, 1) // no more transforms
	writeEntropyImage(&bw, pix, w, h, c.cacheBits, true)
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
	writeEntropyImage(bw, pix, w, h, 0, false)
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

func writeEntropyImage(bw *bitWriter, pix []uint32, w, h, cacheBits int, main bool) {
	toks := tokenize(pix, w, cacheBits, true)
	if cacheBits > 0 {
		bw.put(1, 1)
		bw.put(uint32(cacheBits), 4)
	} else {
		bw.put(0, 1)
	}
	if main {
		bw.put(0, 1) // no meta prefix image
	}
	gsize := 256 + 24
	if cacheBits > 0 {
		gsize += 1 << uint(cacheBits)
	}
	hg := make([]int, gsize)
	hr := make([]int, 256)
	hb := make([]int, 256)
	ha := make([]int, 256)
	hd := make([]int, 40)
	for _, t := range toks {
		switch t.kind {
		case 0:
			hg[(t.a>>8)&0xff]++
			hr[(t.a>>16)&0xff]++
			hb[t.a&0xff]++
			ha[t.a>>24]++
		case 1:
			hg[280+int(t.a)]++
		default:
			s, _, _ := prefixSplit(int(t.length))
			hg[256+s]++
			s, _, _ = prefixSplit(int(t.dist))
			hd[s]++
		}
	}
	cg := bw.prefixCode(hg, gsize)
	cr := bw.prefixCode(hr, 256)
	cb := bw.prefixCode(hb, 256)
	ca := bw.prefixCode(ha, 256)
	cd := bw.prefixCode(hd, 40)
	for _, t := range toks {
		switch t.kind {
		case 0:
			cg.write(bw, int((t.a>>8)&0xff))
			cr.write(bw, int((t.a>>16)&0xff))
			cb.write(bw, int(t.a&0xff))
			ca.write(bw, int(t.a>>24))
		case 1:
			cg.write(bw, 280+int(t.a))
		default:
			s, nb, ex := prefixSplit(int(t.length))
			cg.write(bw, 256+s)
			if nb > 0 {
				bw.put(uint32(ex), uint(nb))
			}
			s, nb, ex = prefixSplit(int(t.dist))
			cd.write(bw, s)
			if nb > 0 {
				bw.put(uint32(ex), uint(nb))
			}
		}
	}
}
