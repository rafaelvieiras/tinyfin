package chapterimg

import (
	"encoding/binary"
	"image"
	"io"
	"math"
)

// The lossy encoder proper: macroblock analysis with a rate-distortion
// choice between 16x16 and 4x4 luma prediction (and among the modes of
// each), adaptive token probabilities (two analysis passes, the second one
// costing bits with the statistics of the first), and a frame-wide loop
// filter level. Everything is written as a VP8 key frame (RFC 6386).

// 4x4 prediction modes, in the numbering of the mode probability table
const (
	bDC = iota
	bTM
	bVE
	bHE
	bRD
	bVR
	bLD
	bVL
	bHD
	bHU
)

// vp8Tuning knobs (found with the benchmark in bench_test.go).
var (
	vp8LambdaK  = 0.03 // rate-distortion multiplier, times the squared AC step
	vp8RoundOff = 0.45 // rounding offset of the quantiser (0.5 = nearest)
)

// ---- token sinks ----

type tokSink interface {
	bit(plane, band, ctx, node int, b bool)
	fixed(prob uint8, b bool)
}

type tokCounts [4][8][3][11][2]uint32
type vp8CostTab [4][8][3][11][2]int32
type vp8Probs [4][8][3][11]uint8

type tokCounter struct{ n *tokCounts }

func (c tokCounter) bit(plane, band, ctx, node int, b bool) {
	if b {
		c.n[plane][band][ctx][node][1]++
	} else {
		c.n[plane][band][ctx][node][0]++
	}
}
func (c tokCounter) fixed(uint8, bool) {}

type tokCoster struct {
	t    *vp8CostTab
	bits int32
}

func (c *tokCoster) bit(plane, band, ctx, node int, b bool) {
	if b {
		c.bits += c.t[plane][band][ctx][node][1]
	} else {
		c.bits += c.t[plane][band][ctx][node][0]
	}
}
func (c *tokCoster) fixed(p uint8, b bool) { c.bits += bitCost(p, b) }

type tokWriter struct {
	e *boolEnc
	p *vp8Probs
}

func (w tokWriter) bit(plane, band, ctx, node int, b bool) {
	w.e.put(w.p[plane][band][ctx][node], b)
}
func (w tokWriter) fixed(p uint8, b bool) { w.e.put(p, b) }

type nullSink struct{}

func (nullSink) bit(int, int, int, int, bool) {}
func (nullSink) fixed(uint8, bool)            {}

var bitCostTab = func() (t [256][2]int32) {
	for p := 0; p < 256; p++ {
		q := math.Max(float64(p), 1) / 256
		t[p][0] = int32(math.Round(-math.Log2(q) * 256))
		t[p][1] = int32(math.Round(-math.Log2(1-q) * 256))
	}
	return
}()

func bitCost(p uint8, b bool) int32 {
	if b {
		return bitCostTab[p][1]
	}
	return bitCostTab[p][0]
}

func costTable(p *vp8Probs) *vp8CostTab {
	t := new(vp8CostTab)
	for i := range p {
		for j := range p[i] {
			for k := range p[i][j] {
				for l := range p[i][j][k] {
					t[i][j][k][l][0] = bitCost(p[i][j][k][l], false)
					t[i][j][k][l][1] = bitCost(p[i][j][k][l], true)
				}
			}
		}
	}
	return t
}

// vp8Token emits one block of quantised levels (zigzag order, from index
// first) with the probabilities of plane and the context ctx, and says
// whether it had any non-zero level.
func vp8Token(s tokSink, levels *[16]int, plane, first, ctx int) bool {
	last := -1
	for i := 15; i >= first; i-- {
		if levels[i] != 0 {
			last = i
			break
		}
	}
	prevZero := false
	extra := func(v int, probs []uint8) {
		for i, p := range probs {
			s.fixed(p, v>>uint(len(probs)-1-i)&1 == 1)
		}
	}
	for n := first; n < 16; n++ {
		band := vp8Bands[n]
		if !prevZero {
			if n > last {
				s.bit(plane, band, ctx, 0, false)
				break
			}
			s.bit(plane, band, ctx, 0, true)
		}
		v := levels[n]
		a := v
		if a < 0 {
			a = -a
		}
		if a == 0 {
			s.bit(plane, band, ctx, 1, false)
			ctx = 0
			prevZero = true
			continue
		}
		prevZero = false
		s.bit(plane, band, ctx, 1, true)
		if a == 1 {
			s.bit(plane, band, ctx, 2, false)
			ctx = 1
		} else {
			s.bit(plane, band, ctx, 2, true)
			switch {
			case a <= 4:
				s.bit(plane, band, ctx, 3, false)
				if a == 2 {
					s.bit(plane, band, ctx, 4, false)
				} else {
					s.bit(plane, band, ctx, 4, true)
					s.bit(plane, band, ctx, 5, a == 4)
				}
			case a <= 10:
				s.bit(plane, band, ctx, 3, true)
				s.bit(plane, band, ctx, 6, false)
				if a <= 6 {
					s.bit(plane, band, ctx, 7, false)
					s.fixed(159, a == 6)
				} else {
					s.bit(plane, band, ctx, 7, true)
					extra(a-7, []uint8{165, 145})
				}
			case a <= 34:
				s.bit(plane, band, ctx, 3, true)
				s.bit(plane, band, ctx, 6, true)
				s.bit(plane, band, ctx, 8, false)
				if a <= 18 {
					s.bit(plane, band, ctx, 9, false)
					extra(a-11, []uint8{173, 148, 140})
				} else {
					s.bit(plane, band, ctx, 9, true)
					extra(a-19, []uint8{176, 155, 140, 135})
				}
			default:
				s.bit(plane, band, ctx, 3, true)
				s.bit(plane, band, ctx, 6, true)
				s.bit(plane, band, ctx, 8, true)
				if a <= 66 {
					s.bit(plane, band, ctx, 10, false)
					extra(a-35, []uint8{180, 157, 141, 134, 130})
				} else {
					s.bit(plane, band, ctx, 10, true)
					extra(a-67, []uint8{254, 254, 243, 230, 196, 177, 153, 140, 133, 130, 129})
				}
			}
			ctx = 2
		}
		s.fixed(128, v < 0)
	}
	return last >= first
}

// ---- macroblocks ----

type vp8MB struct {
	i4     bool
	ymode  int // 16x16 mode (vp8DC...), when !i4
	bmodes [16]int
	uvmode int
	skip   bool
	y2     [16]int
	y      [16][16]int // luma levels in zigzag order (index 0 unused unless i4)
	u, v   [4][16]int
}

// mbTokens emits the tokens of one macroblock and updates the non-zero
// contexts (top: this column's, left: this row's; 4 luma, 2+2 chroma, 1 Y2).
func mbTokens(s tokSink, m *vp8MB, top, left *[9]int) {
	if m.skip {
		n := 9
		if m.i4 {
			n = 8 // the Y2 context is left alone
		}
		for i := 0; i < n; i++ {
			top[i], left[i] = 0, 0
		}
		return
	}
	set := func(l, u *int, nz bool) {
		v := 0
		if nz {
			v = 1
		}
		*l, *u = v, v
	}
	if !m.i4 {
		nz := vp8Token(s, &m.y2, 1, 0, left[8]+top[8])
		set(&left[8], &top[8], nz)
	}
	for i := 0; i < 16; i++ {
		bx, by := i%4, i/4
		var nz bool
		if m.i4 {
			nz = vp8Token(s, &m.y[i], 3, 0, left[by]+top[bx])
		} else {
			nz = vp8Token(s, &m.y[i], 0, 1, left[by]+top[bx])
		}
		set(&left[by], &top[bx], nz)
	}
	for pl, blocks := range []*[4][16]int{&m.u, &m.v} {
		for i := 0; i < 4; i++ {
			bx, by := i%2, i/2
			li, ai := 4+pl*2+by, 4+pl*2+bx
			nz := vp8Token(s, &blocks[i], 2, 0, left[li]+top[ai])
			set(&left[li], &top[ai], nz)
		}
	}
}

// putBMode emits the tree of a 4x4 mode with its context probabilities.
func putBMode(put func(p uint8, b bool), p *[9]uint8, m int) {
	switch m {
	case bDC:
		put(p[0], false)
		return
	case bTM:
		put(p[0], true)
		put(p[1], false)
		return
	case bVE:
		put(p[0], true)
		put(p[1], true)
		put(p[2], false)
		return
	}
	put(p[0], true)
	put(p[1], true)
	put(p[2], true)
	switch m {
	case bHE:
		put(p[3], false)
		put(p[4], false)
	case bRD:
		put(p[3], false)
		put(p[4], true)
		put(p[5], false)
	case bVR:
		put(p[3], false)
		put(p[4], true)
		put(p[5], true)
	case bLD:
		put(p[3], true)
		put(p[6], false)
	case bVL:
		put(p[3], true)
		put(p[6], true)
		put(p[7], false)
	case bHD:
		put(p[3], true)
		put(p[6], true)
		put(p[7], true)
		put(p[8], false)
	default:
		put(p[3], true)
		put(p[6], true)
		put(p[7], true)
		put(p[8], true)
	}
}

func bModeCost(above, left, m int) int32 {
	var c int32
	putBMode(func(p uint8, b bool) { c += bitCost(p, b) }, &vp8BModeProb[above][left], m)
	return c
}

// bModeOf maps a 16x16 mode to the 4x4 mode used as context by neighbours.
func bModeOf(ymode int) int {
	switch ymode {
	case vp8V:
		return bVE
	case vp8H:
		return bHE
	case vp8TM:
		return bTM
	}
	return bDC
}

// ---- analysis ----

type vp8Enc struct {
	src, rec  vp8Planes
	q         vp8Quant
	lambda    float64
	cost      *vp8CostTab
	mbw, mbh  int
	top       [][9]int
	left      [9]int
	topB      []int // 4 per macroblock column
	leftB     [4]int
	bestTrial [256]int
}

func (e *vp8Enc) j(dist int, bits int32) float64 {
	return float64(dist) + e.lambda*float64(bits)/256
}

func quantBlock(coef *[16]float64, dcStep, acStep, first int) (lv [16]int, dq [16]int) {
	for n := first; n < 16; n++ {
		pos := vp8Zigzag[n]
		step := acStep
		if n == 0 {
			step = dcStep
		}
		a := math.Abs(coef[pos])/float64(step) + vp8RoundOff
		l := int(a)
		if l > 2047 {
			l = 2047
		}
		if coef[pos] < 0 {
			l = -l
		}
		lv[n] = l
		dq[pos] = l * step
	}
	return
}

func blockBits(t *vp8CostTab, lv *[16]int, plane, first, ctx int) (int32, bool) {
	c := tokCoster{t: t}
	nz := vp8Token(&c, lv, plane, first, ctx)
	return c.bits, nz
}

func ssd(src []int, sstride, sx, sy int, rec []int, rstride, rx, ry, w, h int) int {
	d := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := src[(sy+y)*sstride+sx+x] - rec[(ry+y)*rstride+rx+x]
			d += v * v
		}
	}
	return d
}

func vp8ModeList(mx, my int) []int {
	m := []int{vp8DC}
	if my > 0 {
		m = append(m, vp8V)
	}
	if mx > 0 {
		m = append(m, vp8H)
	}
	if mx > 0 && my > 0 {
		m = append(m, vp8TM)
	}
	return m
}

// analyseChroma picks the chroma mode by rate and distortion, fills the
// levels and reconstructs both planes.
func (e *vp8Enc) analyseChroma(m *vp8MB, mx, my int) {
	cw := e.src.cw
	cx0, cy0 := mx*8, my*8
	bestJ := math.MaxFloat64
	var bestRec [2][64]int
	var bestLv [2][4][16]int
	bestMode := vp8DC
	var cpred [64]int
	for _, mode := range vp8ModeList(mx, my) {
		var rec [2][64]int
		var lvs [2][4][16]int
		dist := 0
		var bits int32
		for pi, pl := range [][2][]int{{e.src.u, e.rec.u}, {e.src.v, e.rec.v}} {
			vp8Predict(pl[1], cw, cx0, cy0, 8, mode, cpred[:])
			rec[pi] = cpred
			var lnz, tnz [2]int
			for k := 0; k < 2; k++ {
				lnz[k] = e.left[4+pi*2+k]
				tnz[k] = e.top[mx][4+pi*2+k]
			}
			for blk := 0; blk < 4; blk++ {
				bx, by := blk%2, blk/2
				var res [16]int
				for y := 0; y < 4; y++ {
					for x := 0; x < 4; x++ {
						res[y*4+x] = pl[0][(cy0+by*4+y)*cw+cx0+bx*4+x] - cpred[(by*4+y)*8+bx*4+x]
					}
				}
				cf := vp8FDCT(&res)
				lv, dq := quantBlock(&cf, e.q.uv[0], e.q.uv[1], 0)
				lvs[pi][blk] = lv
				vp8IDCT(&dq, rec[pi][:], 8, bx*4, by*4)
				b, nz := blockBits(e.cost, &lv, 2, 0, lnz[by]+tnz[bx])
				bits += b
				v := 0
				if nz {
					v = 1
				}
				lnz[by], tnz[bx] = v, v
			}
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					d := pl[0][(cy0+y)*cw+cx0+x] - rec[pi][y*8+x]
					dist += d * d
				}
			}
		}
		if j := e.j(dist, bits); j < bestJ {
			bestJ, bestMode, bestRec, bestLv = j, mode, rec, lvs
		}
	}
	m.uvmode = bestMode
	m.u, m.v = bestLv[0], bestLv[1]
	for pi, pl := range [][]int{e.rec.u, e.rec.v} {
		for y := 0; y < 8; y++ {
			copy(pl[(cy0+y)*cw+cx0:(cy0+y)*cw+cx0+8], bestRec[pi][y*8:y*8+8])
		}
	}
}

// analyseI16 evaluates the 16x16 luma modes; it returns the best J and
// leaves the mode, levels and reconstruction in m / rec16.
func (e *vp8Enc) analyseI16(m *vp8MB, mx, my int, rec16 *[256]int) float64 {
	x0, y0 := mx*16, my*16
	stride := e.src.stride
	bestJ := math.MaxFloat64
	var pred [256]int
	for _, mode := range vp8ModeList(mx, my) {
		vp8Predict(e.rec.y, stride, x0, y0, 16, mode, pred[:])
		var coef [16][16]float64
		for blk := 0; blk < 16; blk++ {
			bx, by := blk%4*4, blk/4*4
			var res [16]int
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					res[y*4+x] = e.src.y[(y0+by+y)*stride+x0+bx+x] - pred[(by+y)*16+bx+x]
				}
			}
			coef[blk] = vp8FDCT(&res)
		}
		var d, y2 [16]float64
		for i := 0; i < 16; i++ {
			d[i] = coef[i][0]
		}
		wht := [4][4]float64{{1, 1, 1, 1}, {1, 1, -1, -1}, {1, -1, -1, 1}, {1, -1, 1, -1}}
		for a := 0; a < 4; a++ {
			for b := 0; b < 4; b++ {
				var s float64
				for i := 0; i < 4; i++ {
					for j := 0; j < 4; j++ {
						s += wht[i][a] * d[i*4+j] * wht[j][b]
					}
				}
				y2[a*4+b] = s / 2
			}
		}
		var cand vp8MB
		var y2dq [16]int
		var y2raster [16]float64
		copy(y2raster[:], y2[:])
		lv2, dq2 := quantBlockY2(&y2raster, e.q.y2)
		cand.y2 = lv2
		y2dq = dq2
		dcs := vp8IWHT(&y2dq)
		var rec [256]int
		var bits int32
		b2, nz2 := blockBits(e.cost, &cand.y2, 1, 0, e.left[8]+e.top[mx][8])
		bits += b2
		lnz := [4]int{e.left[0], e.left[1], e.left[2], e.left[3]}
		tnz := [4]int{e.top[mx][0], e.top[mx][1], e.top[mx][2], e.top[mx][3]}
		_ = nz2
		for blk := 0; blk < 16; blk++ {
			bx, by := blk%4*4, blk/4*4
			lv, dq := quantBlock(&coef[blk], 0, e.q.y1[1], 1)
			dq[0] = dcs[blk]
			cand.y[blk] = lv
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					rec[(by+y)*16+bx+x] = pred[(by+y)*16+bx+x]
				}
			}
			vp8IDCT(&dq, rec[:], 16, bx, by)
			b, nz := blockBits(e.cost, &lv, 0, 1, lnz[by/4]+tnz[bx/4])
			bits += b
			v := 0
			if nz {
				v = 1
			}
			lnz[by/4], tnz[bx/4] = v, v
		}
		dist := 0
		for y := 0; y < 16; y++ {
			for x := 0; x < 16; x++ {
				dd := e.src.y[(y0+y)*stride+x0+x] - rec[y*16+x]
				dist += dd * dd
			}
		}
		if j := e.j(dist, bits); j < bestJ {
			bestJ = j
			m.ymode = mode
			m.y2, m.y = cand.y2, cand.y
			*rec16 = rec
		}
	}
	return bestJ
}

func quantBlockY2(c *[16]float64, steps [2]int) (lv [16]int, dq [16]int) {
	for i := 0; i < 16; i++ {
		step := steps[1]
		if i == 0 {
			step = steps[0]
		}
		a := math.Abs(c[i])/float64(step) + vp8RoundOff
		l := int(a)
		if l > 2047 {
			l = 2047
		}
		if c[i] < 0 {
			l = -l
		}
		lv[vp8ZigIndex(i)] = l
		dq[i] = l * step
	}
	return
}

// edge4 collects the border of the 4x4 luma block (bi, bj) of macroblock
// (mx, my) from the reconstruction: left[3..0] s r q p, top-left a, top 0..7.
type edge4 struct {
	left [4]int
	tl   int
	top  [8]int
}

func (e *vp8Enc) edge(mx, my, bi, bj int) (ed edge4) {
	W := e.rec.stride
	pl := e.rec.y
	x, y := mx*16+bi*4, my*16+bj*4
	for k := 0; k < 4; k++ {
		if x == 0 {
			ed.left[k] = 129
		} else {
			ed.left[k] = pl[(y+k)*W+x-1]
		}
	}
	if y == 0 {
		for k := range ed.top {
			ed.top[k] = 127
		}
		ed.tl = 127
		return
	}
	for k := 0; k < 4; k++ {
		ed.top[k] = pl[(y-1)*W+x+k]
	}
	mbTR := func(k int) int { // above-right of the macroblock
		if my == 0 {
			return 127
		}
		if mx == e.mbw-1 {
			return pl[(my*16-1)*W+mx*16+15]
		}
		return pl[(my*16-1)*W+mx*16+16+k]
	}
	for k := 0; k < 4; k++ {
		switch {
		case bi == 3:
			ed.top[4+k] = mbTR(k)
		default:
			ed.top[4+k] = pl[(y-1)*W+x+4+k]
		}
	}
	if x == 0 {
		ed.tl = 129
	} else {
		ed.tl = pl[(y-1)*W+x-1]
	}
	return
}

func pred4(mode int, ed *edge4, out *[16]int) {
	s, r, q, p := ed.left[3], ed.left[2], ed.left[1], ed.left[0]
	a := ed.tl
	b, c, d, ee, f := ed.top[0], ed.top[1], ed.top[2], ed.top[3], ed.top[4]
	h2 := ed.top[7]
	avg3 := func(x, y, z int) int { return (x + 2*y + z + 2) / 4 }
	avg2 := func(x, y int) int { return (x + y + 1) / 2 }
	set := func(y, x, v int) { out[y*4+x] = v }
	switch mode {
	case bDC:
		v := (b + c + d + ee + p + q + r + s + 4) / 8
		for i := range out {
			out[i] = v
		}
	case bTM:
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				set(y, x, vp8Clamp(ed.left[y]+ed.top[x]-a))
			}
		}
	case bVE:
		v := [4]int{avg3(a, b, c), avg3(b, c, d), avg3(c, d, ee), avg3(d, ee, f)}
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				set(y, x, v[x])
			}
		}
	case bHE:
		v := [4]int{avg3(a, p, q), avg3(p, q, r), avg3(q, r, s), avg3(r, s, s)}
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				set(y, x, v[y])
			}
		}
	case bRD:
		srq, rqp, qpa, pab := avg3(s, r, q), avg3(r, q, p), avg3(q, p, a), avg3(p, a, b)
		abc, bcd, cde := avg3(a, b, c), avg3(b, c, d), avg3(c, d, ee)
		*out = [16]int{pab, abc, bcd, cde, qpa, pab, abc, bcd, rqp, qpa, pab, abc, srq, rqp, qpa, pab}
	case bVR:
		ab, bc, cd, de := avg2(a, b), avg2(b, c), avg2(c, d), avg2(d, ee)
		rqp, qpa, pab := avg3(r, q, p), avg3(q, p, a), avg3(p, a, b)
		abc, bcd, cde := avg3(a, b, c), avg3(b, c, d), avg3(c, d, ee)
		*out = [16]int{ab, bc, cd, de, pab, abc, bcd, cde, qpa, ab, bc, cd, rqp, pab, abc, bcd}
	case bLD:
		// top[0..7] = A..H in the format's own names
		A, B, C, D, E, F, G, H := ed.top[0], ed.top[1], ed.top[2], ed.top[3], ed.top[4], ed.top[5], ed.top[6], h2
		t1, t2, t3, t4 := avg3(A, B, C), avg3(B, C, D), avg3(C, D, E), avg3(D, E, F)
		t5, t6, t7 := avg3(E, F, G), avg3(F, G, H), avg3(G, H, H)
		*out = [16]int{t1, t2, t3, t4, t2, t3, t4, t5, t3, t4, t5, t6, t4, t5, t6, t7}
	case bVL:
		A, B, C, D, E, F, G, H := ed.top[0], ed.top[1], ed.top[2], ed.top[3], ed.top[4], ed.top[5], ed.top[6], h2
		ab, bc, cd, de := avg2(A, B), avg2(B, C), avg2(C, D), avg2(D, E)
		abc, bcd, cde, def := avg3(A, B, C), avg3(B, C, D), avg3(C, D, E), avg3(D, E, F)
		efg, fgh := avg3(E, F, G), avg3(F, G, H)
		*out = [16]int{ab, bc, cd, de, abc, bcd, cde, def, bc, cd, de, efg, bcd, cde, def, fgh}
	case bHD:
		sr, rq, qp, pa := avg2(s, r), avg2(r, q), avg2(q, p), avg2(p, a)
		srq, rqp, qpa, pab := avg3(s, r, q), avg3(r, q, p), avg3(q, p, a), avg3(p, a, b)
		abc, bcd := avg3(a, b, c), avg3(b, c, d)
		*out = [16]int{pa, pab, abc, bcd, qp, qpa, pa, pab, rq, rqp, qp, qpa, sr, srq, rq, rqp}
	default: // bHU
		pq, qr, rs := avg2(p, q), avg2(q, r), avg2(r, s)
		pqr, qrs, rss := avg3(p, q, r), avg3(q, r, s), avg3(r, s, s)
		*out = [16]int{pq, pqr, qr, qrs, qr, qrs, rs, rss, rs, rss, s, s, s, s, s, s}
	}
}

// analyseI4 chooses each 4x4 block's mode in turn, reconstructing into
// e.rec as it goes. It returns J (including the mode bits).
func (e *vp8Enc) analyseI4(m *vp8MB, mx, my int) float64 {
	stride := e.src.stride
	x0, y0 := mx*16, my*16
	lnz := [4]int{e.left[0], e.left[1], e.left[2], e.left[3]}
	tnz := [4]int{e.top[mx][0], e.top[mx][1], e.top[mx][2], e.top[mx][3]}
	var modes [16]int
	total := 0.0
	for blk := 0; blk < 16; blk++ {
		bi, bj := blk%4, blk/4
		ed := e.edge(mx, my, bi, bj)
		above := e.topB[mx*4+bi]
		if bj > 0 {
			above = modes[blk-4]
		}
		left := e.leftB[bj]
		if bi > 0 {
			left = modes[blk-1]
		}
		bestJ := math.MaxFloat64
		var bestLv [16]int
		var bestRec [16]int
		bestMode := bDC
		bestNZ := false
		var pred, rec [16]int
		for mode := 0; mode < 10; mode++ {
			pred4(mode, &ed, &pred)
			var res [16]int
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					res[y*4+x] = e.src.y[(y0+bj*4+y)*stride+x0+bi*4+x] - pred[y*4+x]
				}
			}
			cf := vp8FDCT(&res)
			lv, dq := quantBlock(&cf, e.q.y1[0], e.q.y1[1], 0)
			rec = pred
			vp8IDCT(&dq, rec[:], 4, 0, 0)
			dist := 0
			for k := 0; k < 16; k++ {
				d := e.src.y[(y0+bj*4+k/4)*stride+x0+bi*4+k%4] - rec[k]
				dist += d * d
			}
			bits, nz := blockBits(e.cost, &lv, 3, 0, lnz[bj]+tnz[bi])
			bits += bModeCost(above, left, mode)
			if j := e.j(dist, bits); j < bestJ {
				bestJ, bestMode, bestLv, bestRec, bestNZ = j, mode, lv, rec, nz
			}
		}
		total += bestJ
		modes[blk] = bestMode
		m.y[blk] = bestLv
		v := 0
		if bestNZ {
			v = 1
		}
		lnz[bj], tnz[bi] = v, v
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				e.rec.y[(y0+bj*4+y)*stride+x0+bi*4+x] = bestRec[y*4+x]
			}
		}
	}
	m.bmodes = modes
	return total
}

func (e *vp8Enc) analyseMB(m *vp8MB, mx, my int) {
	*m = vp8MB{}
	e.analyseChroma(m, mx, my)
	var rec16 [256]int
	m16 := *m
	j16 := e.analyseI16(&m16, mx, my, &rec16)
	// the 4x4 search overwrites the reconstruction; keep a way back
	stride := e.rec.stride
	x0, y0 := mx*16, my*16
	var m4 vp8MB
	m4.i4 = true
	j4 := e.analyseI4(&m4, mx, my)
	j4 += e.lambda * 16 // the extra flag and mode overhead of 4x4 prediction
	if j4 < j16 {
		m4.uvmode, m4.u, m4.v = m.uvmode, m.u, m.v
		*m = m4
	} else {
		*m = m16
		for y := 0; y < 16; y++ {
			copy(e.rec.y[(y0+y)*stride+x0:(y0+y)*stride+x0+16], rec16[y*16:y*16+16])
		}
	}
	// skip when nothing is coded
	nonzero := false
	check := func(l *[16]int, first int) {
		for i := first; i < 16; i++ {
			if l[i] != 0 {
				nonzero = true
			}
		}
	}
	if !m.i4 {
		check(&m.y2, 0)
	}
	for i := 0; i < 16; i++ {
		if m.i4 {
			check(&m.y[i], 0)
		} else {
			check(&m.y[i], 1)
		}
	}
	for i := 0; i < 4; i++ {
		check(&m.u[i], 0)
		check(&m.v[i], 0)
	}
	m.skip = !nonzero
	mbTokens(nullSink{}, m, &e.top[mx], &e.left)
	// 4x4 mode contexts for the neighbours
	for k := 0; k < 4; k++ {
		if m.i4 {
			e.topB[mx*4+k] = m.bmodes[12+k]
			e.leftB[k] = m.bmodes[k*4+3]
		} else {
			bm := bModeOf(m.ymode)
			e.topB[mx*4+k], e.leftB[k] = bm, bm
		}
	}
}

func (e *vp8Enc) analyse(src *vp8Planes, qi int, cost *vp8CostTab) []vp8MB {
	e.src = *src
	e.rec = vp8Planes{w: src.w, h: src.h, cw: src.cw, stride: src.stride,
		y: make([]int, len(src.y)), u: make([]int, len(src.u)), v: make([]int, len(src.v))}
	e.cost = cost
	e.top = make([][9]int, e.mbw)
	e.topB = make([]int, e.mbw*4)
	mbs := make([]vp8MB, e.mbw*e.mbh)
	for my := 0; my < e.mbh; my++ {
		e.left = [9]int{}
		e.leftB = [4]int{}
		for mx := 0; mx < e.mbw; mx++ {
			e.analyseMB(&mbs[my*e.mbw+mx], mx, my)
		}
	}
	return mbs
}

// countTokens gathers the token statistics of a set of macroblocks.
func countTokens(mbs []vp8MB, mbw int) *tokCounts {
	n := new(tokCounts)
	top := make([][9]int, mbw)
	var left [9]int
	for i := range mbs {
		mx := i % mbw
		if mx == 0 {
			left = [9]int{}
		}
		mbTokens(tokCounter{n}, &mbs[i], &top[mx], &left)
	}
	return n
}

// adaptProbs derives the frame's token probabilities from the statistics and
// says which of them are worth sending.
func adaptProbs(n *tokCounts) (p vp8Probs, send [4][8][3][11]bool) {
	p = vp8Probs(vp8DefaultProb)
	for i := range n {
		for j := range n[i] {
			for k := range n[i][j] {
				for l := range n[i][j][k] {
					n0, n1 := int64(n[i][j][k][l][0]), int64(n[i][j][k][l][1])
					tot := n0 + n1
					if tot == 0 {
						continue
					}
					np := int(255 - n1*255/tot)
					np = max(1, min(255, np))
					old := vp8DefaultProb[i][j][k][l]
					oldCost := int64(bitCost(old, false))*n0 + int64(bitCost(old, true))*n1
					newCost := int64(bitCost(uint8(np), false))*n0 + int64(bitCost(uint8(np), true))*n1
					up := vp8UpdateProb[i][j][k][l]
					if newCost+int64(bitCost(up, true))+8*256 < oldCost+int64(bitCost(up, false)) {
						p[i][j][k][l] = uint8(np)
						send[i][j][k][l] = true
					}
				}
			}
		}
	}
	return
}

// encodeVP8 writes img as a lossy WebP of the given quality (0 to 99).
func encodeVP8(w io.Writer, img *image.RGBA, quality int) error {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	mbw, mbh := (width+15)/16, (height+15)/16
	W, H := mbw*16, mbh*16
	cw := W / 2
	// RGB to studio-range YCbCr, chroma averaged over 2x2
	src := vp8Planes{w: W, h: H, cw: cw, stride: W, y: make([]int, W*H), u: make([]int, cw*(H/2)), v: make([]int, cw*(H/2))}
	at := func(x, y int) (int, int, int) {
		if x >= width {
			x = width - 1
		}
		if y >= height {
			y = height - 1
		}
		o := y*img.Stride + x*4
		return int(img.Pix[o]), int(img.Pix[o+1]), int(img.Pix[o+2])
	}
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			r, g, bl := at(x, y)
			src.y[y*W+x] = (66*r + 129*g + 25*bl + 128 + 16*256) >> 8
		}
	}
	for y := 0; y < H/2; y++ {
		for x := 0; x < cw; x++ {
			var r, g, bl int
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					rr, gg, bb := at(2*x+dx, 2*y+dy)
					r, g, bl = r+rr, g+gg, bl+bb
				}
			}
			src.u[y*cw+x] = vp8Clamp(int(math.Round(128 + float64(-38*r-74*g+112*bl)/1024)))
			src.v[y*cw+x] = vp8Clamp(int(math.Round(128 + float64(112*r-94*g-18*bl)/1024)))
		}
	}
	qi := qualityToIndex(quality)
	steps := vp8Steps(qi)
	enc := &vp8Enc{q: steps, mbw: mbw, mbh: mbh}
	enc.lambda = vp8LambdaK * float64(steps.y1[1]*steps.y1[1])
	// pass 1 costs with the default probabilities, pass 2 with what pass 1 saw
	mbs := enc.analyse(&src, qi, costTable((*vp8Probs)(&vp8DefaultProb)))
	probs, _ := adaptProbs(countTokens(mbs, mbw))
	mbs = enc.analyse(&src, qi, costTable(&probs))
	probs, send := adaptProbs(countTokens(mbs, mbw))

	skipped := 0
	for i := range mbs {
		if mbs[i].skip {
			skipped++
		}
	}
	probSkip := 255
	if skipped > 0 {
		probSkip = (len(mbs) - skipped) * 256 / len(mbs)
	}
	probSkip = max(1, min(255, probSkip))
	hp := newBoolEnc()
	hp.lit(0, 1) // colour space
	hp.lit(0, 1) // clamping required
	hp.lit(0, 1) // no segments
	hp.lit(0, 1) // normal filter type
	hp.lit(vp8FilterLevel(qi), 6)
	hp.lit(0, 3) // sharpness
	hp.lit(0, 1) // no filter adjustments
	hp.lit(0, 2) // one token partition
	hp.lit(qi, 7)
	for i := 0; i < 5; i++ {
		hp.lit(0, 1) // no quantiser deltas
	}
	hp.lit(1, 1) // refresh entropy probabilities
	for i := range vp8UpdateProb {
		for j := range vp8UpdateProb[i] {
			for k := range vp8UpdateProb[i][j] {
				for l := range vp8UpdateProb[i][j][k] {
					if send[i][j][k][l] {
						hp.put(vp8UpdateProb[i][j][k][l], true)
						hp.lit(int(probs[i][j][k][l]), 8)
					} else {
						hp.put(vp8UpdateProb[i][j][k][l], false)
					}
				}
			}
		}
	}
	hp.lit(1, 1) // macroblocks may be skipped
	hp.lit(probSkip, 8)
	writeModes(hp, mbs, mbw, probSkip)
	first := hp.finish()
	// token partition
	tp := newBoolEnc()
	top := make([][9]int, mbw)
	var left [9]int
	for i := range mbs {
		mx := i % mbw
		if mx == 0 {
			left = [9]int{}
		}
		mbTokens(tokWriter{tp, &probs}, &mbs[i], &top[mx], &left)
	}
	tokens := tp.finish()
	var frame []byte
	tag := uint32(0) | 1<<4 | uint32(len(first))<<5
	frame = append(frame, byte(tag), byte(tag>>8), byte(tag>>16), 0x9d, 0x01, 0x2a)
	frame = binary.LittleEndian.AppendUint16(frame, uint16(width))
	frame = binary.LittleEndian.AppendUint16(frame, uint16(height))
	frame = append(frame, first...)
	frame = append(frame, tokens...)
	pad := len(frame) & 1
	var head [20]byte
	copy(head[0:], "RIFF")
	binary.LittleEndian.PutUint32(head[4:], uint32(4+8+len(frame)+pad))
	copy(head[8:], "WEBPVP8 ")
	binary.LittleEndian.PutUint32(head[16:], uint32(len(frame)))
	if _, err := w.Write(head[:]); err != nil {
		return err
	}
	if _, err := w.Write(frame); err != nil {
		return err
	}
	if pad == 1 {
		_, err := w.Write([]byte{0})
		return err
	}
	return nil
}

// vp8FilterLevel is the loop filter strength (0 to 63) for a quantiser index.
func vp8FilterLevel(qi int) int { return min(63, 3+qi/3) }

// writeModes emits, per macroblock, the skip flag and the prediction modes.
func writeModes(hp *boolEnc, mbs []vp8MB, mbw, probSkip int) *boolEnc {
	topB := make([]int, mbw*4)
	var leftB [4]int
	put := func(p uint8, b bool) { hp.put(p, b) }
	for i := range mbs {
		m := &mbs[i]
		mx := i % mbw
		if mx == 0 {
			leftB = [4]int{}
		}
		hp.put(uint8(probSkip), m.skip)
		if m.i4 {
			hp.put(145, false)
			for blk := 0; blk < 16; blk++ {
				bi, bj := blk%4, blk/4
				above := topB[mx*4+bi]
				if bj > 0 {
					above = m.bmodes[blk-4]
				}
				left := leftB[bj]
				if bi > 0 {
					left = m.bmodes[blk-1]
				}
				putBMode(put, &vp8BModeProb[above][left], m.bmodes[blk])
			}
			for k := 0; k < 4; k++ {
				topB[mx*4+k] = m.bmodes[12+k]
				leftB[k] = m.bmodes[k*4+3]
			}
		} else {
			hp.put(145, true)
			switch m.ymode {
			case vp8DC:
				hp.put(156, false)
				hp.put(163, false)
			case vp8V:
				hp.put(156, false)
				hp.put(163, true)
			case vp8H:
				hp.put(156, true)
				hp.put(128, false)
			default:
				hp.put(156, true)
				hp.put(128, true)
			}
			bm := bModeOf(m.ymode)
			for k := 0; k < 4; k++ {
				topB[mx*4+k], leftB[k] = bm, bm
			}
		}
		switch m.uvmode {
		case vp8DC:
			hp.put(142, false)
		case vp8V:
			hp.put(142, true)
			hp.put(114, false)
		case vp8H:
			hp.put(142, true)
			hp.put(114, true)
			hp.put(183, false)
		default:
			hp.put(142, true)
			hp.put(114, true)
			hp.put(183, true)
		}
	}
	return hp
}
