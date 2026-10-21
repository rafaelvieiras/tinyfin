package chapterimg

import (
	"encoding/binary"
	"image"
	"io"
	"math"
)

// This file is a lossy WebP encoder: a VP8 key frame (RFC 6386) made of one
// partition of modes and one of tokens, 16x16 intra prediction only (DC,
// vertical, horizontal and TrueMotion, the last two only where their
// neighbours exist), the format's default coefficient probabilities and no
// loop filter. It is much simpler than libwebp's (no 4x4 prediction, no
// rate-distortion search, no segments, no filter), so at the same quality its
// pictures are somewhat larger or rougher, but it is a real VP8 stream that
// any decoder takes.

// boolEnc is the VP8 boolean entropy encoder (RFC 6386, section 7.3).
type boolEnc struct {
	out    []byte
	rng    uint32
	bottom uint32
	bits   int
}

func newBoolEnc() *boolEnc { return &boolEnc{rng: 255, bits: 24} }

func (e *boolEnc) carry() {
	i := len(e.out) - 1
	for i >= 0 && e.out[i] == 255 {
		e.out[i] = 0
		i--
	}
	if i >= 0 {
		e.out[i]++
	}
}

// put writes bit, which is 0 with probability prob/256.
func (e *boolEnc) put(prob uint8, bit bool) {
	split := 1 + (((e.rng - 1) * uint32(prob)) >> 8)
	if bit {
		e.bottom += split
		e.rng -= split
	} else {
		e.rng = split
	}
	for e.rng < 128 {
		e.rng <<= 1
		if e.bottom&(1<<31) != 0 {
			e.carry()
		}
		e.bottom <<= 1
		e.bits--
		if e.bits == 0 {
			e.out = append(e.out, byte(e.bottom>>24))
			e.bottom &= (1 << 24) - 1
			e.bits = 8
		}
	}
}

// lit writes an n-bit number, most significant bit first, at even odds.
func (e *boolEnc) lit(v, n int) {
	for i := n - 1; i >= 0; i-- {
		e.put(128, v>>uint(i)&1 == 1)
	}
}

func (e *boolEnc) finish() []byte {
	c := e.bits
	v := e.bottom
	if v&(1<<uint(32-c)) != 0 {
		e.carry()
	}
	v <<= uint(c & 7)
	for i := c >> 3; i > 0; i-- {
		v <<= 8
	}
	for i := 0; i < 4; i++ {
		e.out = append(e.out, byte(v>>24))
		v <<= 8
	}
	return e.out
}

var (
	vp8Zigzag = [16]int{0, 1, 4, 8, 5, 2, 3, 6, 9, 12, 13, 10, 7, 11, 14, 15}
	vp8Bands  = [16]int{0, 1, 2, 3, 6, 4, 5, 6, 6, 6, 6, 6, 6, 6, 6, 7}
)

// prediction modes, in the numbering of the format's tree
const (
	vp8DC = iota
	vp8V
	vp8H
	vp8TM
)

// token writes one block of quantised coefficients (levels in zigzag order,
// from index first) with the probabilities of plane and the context ctx, and
// says whether it had any non-zero level.
func (e *boolEnc) token(levels *[16]int, plane, first, ctx int) bool {
	last := -1
	for i := 15; i >= first; i-- {
		if levels[i] != 0 {
			last = i
			break
		}
	}
	probs := &vp8DefaultProb[plane]
	prevZero := false
	for n := first; n < 16; n++ {
		p := &probs[vp8Bands[n]][ctx]
		if !prevZero {
			if n > last {
				e.put(p[0], false)
				break
			}
			e.put(p[0], true)
		}
		v := levels[n]
		a := v
		if a < 0 {
			a = -a
		}
		if a == 0 {
			e.put(p[1], false)
			ctx = 0
			prevZero = true
			continue
		}
		prevZero = false
		e.put(p[1], true)
		if a == 1 {
			e.put(p[2], false)
			ctx = 1
		} else {
			e.put(p[2], true)
			ctx = 2
			switch {
			case a <= 4:
				e.put(p[3], false)
				if a == 2 {
					e.put(p[4], false)
				} else {
					e.put(p[4], true)
					e.put(p[5], a == 4)
				}
			case a <= 10:
				e.put(p[3], true)
				e.put(p[6], false)
				if a <= 6 {
					e.put(p[7], false)
					e.put(159, a == 6)
				} else {
					e.put(p[7], true)
					e.extra(a-7, []uint8{165, 145})
				}
			case a <= 34:
				e.put(p[3], true)
				e.put(p[6], true)
				e.put(p[8], false)
				if a <= 18 {
					e.put(p[9], false)
					e.extra(a-11, []uint8{173, 148, 140})
				} else {
					e.put(p[9], true)
					e.extra(a-19, []uint8{176, 155, 140, 135})
				}
			default:
				e.put(p[3], true)
				e.put(p[6], true)
				e.put(p[8], true)
				if a <= 66 {
					e.put(p[10], false)
					e.extra(a-35, []uint8{180, 157, 141, 134, 130})
				} else {
					e.put(p[10], true)
					e.extra(a-67, []uint8{254, 254, 243, 230, 196, 177, 153, 140, 133, 130, 129})
				}
			}
		}
		e.put(128, v < 0)
	}
	return last >= first
}

// extra writes the low bits of v, most significant first, with the given
// probabilities (one per bit).
func (e *boolEnc) extra(v int, probs []uint8) {
	for i, p := range probs {
		e.put(p, v>>uint(len(probs)-1-i)&1 == 1)
	}
}

// vp8MB is one macroblock as it will be written.
type vp8MB struct {
	ymode, uvmode int
	skip          bool
	y2            [16]int
	y             [16][16]int // levels in zigzag order (index 0 unused)
	u, v          [4][16]int
}

// qualityToIndex is the quantiser index (0 finest to 127 coarsest) for a
// quality of 0 to 100, on libwebp's curve.
func qualityToIndex(q int) int {
	c := float64(q) / 100
	lin := c * 2 / 3
	if c >= 0.75 {
		lin = 2*c - 1
	}
	return int(math.Round(127 * (1 - math.Cbrt(lin))))
}

type vp8Planes struct {
	w, h   int // in pixels, multiples of 16
	y      []int
	u, v   []int
	cw     int
	stride int
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
			// r, g, bl are sums over four pixels
			src.u[y*cw+x] = vp8Clamp(int(math.Round(128 + float64(-38*r-74*g+112*bl)/1024)))
			src.v[y*cw+x] = vp8Clamp(int(math.Round(128 + float64(112*r-94*g-18*bl)/1024)))
		}
	}
	qi := qualityToIndex(quality)
	steps := vp8Steps(qi)
	rec := vp8Planes{w: W, h: H, cw: cw, stride: W, y: make([]int, W*H), u: make([]int, cw*(H/2)), v: make([]int, cw*(H/2))}
	mbs := make([]vp8MB, mbw*mbh)
	for my := 0; my < mbh; my++ {
		for mx := 0; mx < mbw; mx++ {
			vp8Macroblock(&src, &rec, &mbs[my*mbw+mx], mx, my, steps)
		}
	}
	// header partition
	skipped := 0
	for i := range mbs {
		if mbs[i].skip {
			skipped++
		}
	}
	probSkip := 128
	if skipped > 0 && skipped < len(mbs) {
		probSkip = (len(mbs) - skipped) * 256 / len(mbs)
	} else if skipped == len(mbs) {
		probSkip = 1
	} else {
		probSkip = 255
	}
	probSkip = max(1, min(255, probSkip))
	hp := newBoolEnc()
	hp.lit(0, 1) // colour space
	hp.lit(0, 1) // clamping required
	hp.lit(0, 1) // no segments
	hp.lit(0, 1) // normal filter type
	hp.lit(0, 6) // loop filter level: none
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
					hp.put(vp8UpdateProb[i][j][k][l], false)
				}
			}
		}
	}
	hp.lit(1, 1) // macroblocks may be skipped
	hp.lit(probSkip, 8)
	for i := range mbs {
		m := &mbs[i]
		hp.put(uint8(probSkip), m.skip)
		hp.put(145, true) // not 4x4 prediction
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
	first := hp.finish()
	// token partition
	tp := newBoolEnc()
	above := make([][9]int, mbw) // 4 luma, 2 + 2 chroma, 1 Y2
	for my := 0; my < mbh; my++ {
		var left [9]int
		for mx := 0; mx < mbw; mx++ {
			m := &mbs[my*mbw+mx]
			a := &above[mx]
			if m.skip {
				for i := range left {
					left[i], a[i] = 0, 0
				}
				continue
			}
			set := func(l, u *int, nz bool) {
				v := 0
				if nz {
					v = 1
				}
				*l, *u = v, v
			}
			var y2 [16]int
			for i := 0; i < 16; i++ {
				y2[i] = m.y2[i]
			}
			nz := tp.token(&y2, 1, 0, left[8]+a[8])
			set(&left[8], &a[8], nz)
			for i := 0; i < 16; i++ {
				bx, by := i%4, i/4
				nz := tp.token(&m.y[i], 0, 1, left[by]+a[bx])
				set(&left[by], &a[bx], nz)
			}
			for pl, blocks := range []*[4][16]int{&m.u, &m.v} {
				for i := 0; i < 4; i++ {
					bx, by := i%2, i/2
					li, ai := 4+pl*2+by, 4+pl*2+bx
					nz := tp.token(&blocks[i], 2, 0, left[li]+a[ai])
					set(&left[li], &a[ai], nz)
				}
			}
		}
	}
	tokens := tp.finish()
	// container
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

func vp8Clamp(v int) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// vp8Quant holds the step sizes of one frame: [0] DC, [1] AC.
type vp8Quant struct{ y1, y2, uv [2]int }

func vp8Steps(qi int) vp8Quant {
	var q vp8Quant
	q.y1 = [2]int{vp8DCStep[qi], vp8ACStep[qi]}
	q.y2 = [2]int{vp8DCStep[qi] * 2, max(8, vp8ACStep[qi]*155/100)}
	q.uv = [2]int{vp8DCStep[min(qi, 117)], vp8ACStep[qi]}
	return q
}

// forward 4x4 transform of the residual (VP8 scaling: twice the orthonormal
// DCT), coefficients in raster order, vertical frequency first.
var vp8Basis = func() (c [4][4]float64) {
	for k := 0; k < 4; k++ {
		a := math.Sqrt(2.0 / 4)
		if k == 0 {
			a = 0.5
		}
		for n := 0; n < 4; n++ {
			c[k][n] = a * math.Cos(float64(2*n+1)*float64(k)*math.Pi/8)
		}
	}
	return
}()

func vp8FDCT(res *[16]int) (out [16]float64) {
	var t [16]float64
	for r := 0; r < 4; r++ { // rows: along x
		for u := 0; u < 4; u++ {
			var s float64
			for x := 0; x < 4; x++ {
				s += vp8Basis[u][x] * float64(res[r*4+x])
			}
			t[r*4+u] = s
		}
	}
	for u := 0; u < 4; u++ {
		for v := 0; v < 4; v++ {
			var s float64
			for y := 0; y < 4; y++ {
				s += vp8Basis[v][y] * t[y*4+u]
			}
			out[v*4+u] = 2 * s
		}
	}
	return
}

// quant quantises c with step, rounding toward zero a little (a dead zone).
func vp8Quantise(c float64, step int) int {
	a := math.Abs(c)/float64(step) + 0.4
	l := int(a)
	if l > 2047 {
		l = 2047
	}
	if c < 0 {
		return -l
	}
	return l
}

// vp8IDCT adds the inverse transform of the dequantised coefficients (raster
// order) to the 4x4 block of the plane at (x, y).
func vp8IDCT(co *[16]int, pl []int, stride, x, y int) {
	const c1, c2 = 85627, 35468
	var m [4][4]int
	for i := 0; i < 4; i++ {
		a := co[i] + co[8+i]
		b := co[i] - co[8+i]
		c := (co[4+i]*c2)>>16 - (co[12+i]*c1)>>16
		d := (co[4+i]*c1)>>16 + (co[12+i]*c2)>>16
		m[i][0], m[i][1], m[i][2], m[i][3] = a+d, b+c, b-c, a-d
	}
	for j := 0; j < 4; j++ {
		dc := m[0][j] + 4
		a := dc + m[2][j]
		b := dc - m[2][j]
		c := (m[1][j]*c2)>>16 - (m[3][j]*c1)>>16
		d := (m[1][j]*c1)>>16 + (m[3][j]*c2)>>16
		row := pl[(y+j)*stride+x:]
		row[0] = vp8Clamp(row[0] + (a+d)>>3)
		row[1] = vp8Clamp(row[1] + (b+c)>>3)
		row[2] = vp8Clamp(row[2] + (b-c)>>3)
		row[3] = vp8Clamp(row[3] + (a-d)>>3)
	}
}

// vp8IWHT is the inverse Walsh-Hadamard transform of the Y2 block.
func vp8IWHT(in *[16]int) (out [16]int) {
	var t [16]int
	for i := 0; i < 4; i++ {
		a := in[i] + in[12+i]
		b := in[4+i] + in[8+i]
		c := in[4+i] - in[8+i]
		d := in[i] - in[12+i]
		t[i], t[4+i], t[8+i], t[12+i] = a+b, c+d, a-b, d-c
	}
	for i := 0; i < 4; i++ {
		r := t[i*4 : i*4+4]
		a := r[0] + r[3]
		b := r[1] + r[2]
		c := r[1] - r[2]
		d := r[0] - r[3]
		out[i*4+0] = (a + b + 3) >> 3
		out[i*4+1] = (c + d + 3) >> 3
		out[i*4+2] = (a - b + 3) >> 3
		out[i*4+3] = (d - c + 3) >> 3
	}
	return
}

// predict fills pred (size x size) for mode from the reconstructed plane.
func vp8Predict(pl []int, stride, x0, y0, size, mode int, pred []int) {
	top, left := y0 > 0, x0 > 0
	shift := 3 // log2(size)
	if size == 16 {
		shift = 4
	}
	switch mode {
	case vp8V:
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				pred[y*size+x] = pl[(y0-1)*stride+x0+x]
			}
		}
	case vp8H:
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				pred[y*size+x] = pl[(y0+y)*stride+x0-1]
			}
		}
	case vp8TM:
		tl := pl[(y0-1)*stride+x0-1]
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				pred[y*size+x] = vp8Clamp(pl[(y0+y)*stride+x0-1] + pl[(y0-1)*stride+x0+x] - tl)
			}
		}
	default:
		sum, n := 0, 0
		if top {
			for x := 0; x < size; x++ {
				sum += pl[(y0-1)*stride+x0+x]
			}
			n++
		}
		if left {
			for y := 0; y < size; y++ {
				sum += pl[(y0+y)*stride+x0-1]
			}
			n++
		}
		dc := 128
		if n > 0 {
			dc = (sum + (size*n)/2) >> uint(shift+n-1)
		}
		for i := 0; i < size*size; i++ {
			pred[i] = dc
		}
	}
}

func vp8Modes(mx, my int) []int {
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

// bestMode picks the mode whose prediction is closest (sum of absolute
// differences) to the source block.
func vp8BestMode(src, rec []int, stride, x0, y0, size, mx, my int, pred []int) int {
	best, bestSAD := vp8DC, math.MaxInt
	for _, mode := range vp8Modes(mx, my) {
		vp8Predict(rec, stride, x0, y0, size, mode, pred)
		sad := 0
		for y := 0; y < size; y++ {
			for x := 0; x < size; x++ {
				d := src[(y0+y)*stride+x0+x] - pred[y*size+x]
				if d < 0 {
					d = -d
				}
				sad += d
			}
		}
		if sad < bestSAD {
			best, bestSAD = mode, sad
		}
	}
	return best
}

// vp8Macroblock chooses the modes of one macroblock, quantises its residual
// and reconstructs it into rec (what a decoder will have).
func vp8Macroblock(src, rec *vp8Planes, m *vp8MB, mx, my int, q vp8Quant) {
	nonzero := false
	// luma
	x0, y0 := mx*16, my*16
	var pred [256]int
	m.ymode = vp8BestMode(src.y, rec.y, src.stride, x0, y0, 16, mx, my, pred[:])
	vp8Predict(rec.y, rec.stride, x0, y0, 16, m.ymode, pred[:])
	var coef [16][16]float64
	for blk := 0; blk < 16; blk++ {
		bx, by := blk%4*4, blk/4*4
		var res [16]int
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				res[y*4+x] = src.y[(y0+by+y)*src.stride+x0+bx+x] - pred[(by+y)*16+bx+x]
			}
		}
		coef[blk] = vp8FDCT(&res)
	}
	// Y2: Walsh-Hadamard of the sixteen DCs
	var d [16]float64
	for i := 0; i < 16; i++ {
		d[i] = coef[i][0]
	}
	var y2 [16]float64
	wht := [4][4]float64{{1, 1, 1, 1}, {1, 1, -1, -1}, {1, -1, -1, 1}, {1, -1, 1, -1}}
	for a := 0; a < 4; a++ { // Wt D W / 2
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
	var y2dq [16]int
	for i := 0; i < 16; i++ {
		step := q.y2[1]
		if i == 0 {
			step = q.y2[0]
		}
		l := vp8Quantise(y2[i], step)
		m.y2[vp8ZigIndex(i)] = l
		y2dq[i] = l * step
		if l != 0 {
			nonzero = true
		}
	}
	dcs := vp8IWHT(&y2dq)
	for blk := 0; blk < 16; blk++ {
		var dq [16]int
		dq[0] = dcs[blk]
		for n := 1; n < 16; n++ {
			pos := vp8Zigzag[n]
			l := vp8Quantise(coef[blk][pos], q.y1[1])
			m.y[blk][n] = l
			dq[pos] = l * q.y1[1]
			if l != 0 {
				nonzero = true
			}
		}
		bx, by := blk%4*4, blk/4*4
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				rec.y[(y0+by+y)*rec.stride+x0+bx+x] = pred[(by+y)*16+bx+x]
			}
		}
		vp8IDCT(&dq, rec.y, rec.stride, x0+bx, y0+by)
	}
	// chroma
	cx0, cy0 := mx*8, my*8
	var cpred [64]int
	// one mode for both planes, chosen on the sum of the two
	best, bestSAD := vp8DC, math.MaxInt
	for _, mode := range vp8Modes(mx, my) {
		sad := 0
		for _, pl := range [][2][]int{{src.u, rec.u}, {src.v, rec.v}} {
			vp8Predict(pl[1], src.cw, cx0, cy0, 8, mode, cpred[:])
			for y := 0; y < 8; y++ {
				for x := 0; x < 8; x++ {
					dd := pl[0][(cy0+y)*src.cw+cx0+x] - cpred[y*8+x]
					if dd < 0 {
						dd = -dd
					}
					sad += dd
				}
			}
		}
		if sad < bestSAD {
			best, bestSAD = mode, sad
		}
	}
	m.uvmode = best
	for pi, pl := range [][2][]int{{src.u, rec.u}, {src.v, rec.v}} {
		blocks := &m.u
		if pi == 1 {
			blocks = &m.v
		}
		vp8Predict(pl[1], src.cw, cx0, cy0, 8, best, cpred[:])
		for blk := 0; blk < 4; blk++ {
			bx, by := blk%2*4, blk/2*4
			var res [16]int
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					res[y*4+x] = pl[0][(cy0+by+y)*src.cw+cx0+bx+x] - cpred[(by+y)*8+bx+x]
				}
			}
			cf := vp8FDCT(&res)
			var dq [16]int
			for n := 0; n < 16; n++ {
				pos := vp8Zigzag[n]
				step := q.uv[1]
				if n == 0 {
					step = q.uv[0]
				}
				l := vp8Quantise(cf[pos], step)
				blocks[blk][n] = l
				dq[pos] = l * step
				if l != 0 {
					nonzero = true
				}
			}
			for y := 0; y < 4; y++ {
				for x := 0; x < 4; x++ {
					pl[1][(cy0+by+y)*src.cw+cx0+bx+x] = cpred[(by+y)*8+bx+x]
				}
			}
			vp8IDCT(&dq, pl[1], src.cw, cx0+bx, cy0+by)
		}
	}
	m.skip = !nonzero
}

// vp8ZigIndex is the zigzag position of raster index i.
func vp8ZigIndex(i int) int {
	for n, p := range vp8Zigzag {
		if p == i {
			return n
		}
	}
	return 0
}
