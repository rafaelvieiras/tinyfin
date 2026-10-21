package chapterimg

import (
	"math"
)

// This file holds the pieces of the lossy WebP encoder that are shared by
// its stages: the boolean entropy coder, the transforms and the 16x16 / 8x8
// predictors. The encoder itself (a VP8 key frame of RFC 6386 with 16x16 and
// 4x4 intra prediction, a rate-distortion choice of modes, adaptive token
// probabilities and a loop filter level) is in vp8enc.go.

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

// vp8ZigIndex is the zigzag position of raster index i.
func vp8ZigIndex(i int) int {
	for n, p := range vp8Zigzag {
		if p == i {
			return n
		}
	}
	return 0
}
