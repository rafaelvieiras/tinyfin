package chapterimg

import (
	"container/heap"
	"encoding/binary"
	"image"
	"io"
	"sort"
)

// encodeWebP writes img as a lossless WebP (a RIFF container around a VP8L
// bitstream). Several transform / colour-cache settings are tried (see
// vp8l.go) and the smallest stream wins. The reference makes its WebP the
// same way (VP8L) at the default quality; below quality 100 it makes a lossy
// VP8 one (vp8.go).
func encodeWebP(w io.Writer, img *image.RGBA) error {
	b := img.Bounds()
	width, height := b.Dx(), b.Dy()
	n := width * height
	argb := make([]uint32, n)
	hasAlpha := false
	for y := 0; y < height; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+width*4]
		for x := 0; x < width; x++ {
			pr, pg, pb, pa := row[x*4], row[x*4+1], row[x*4+2], row[x*4+3]
			if pa != 255 {
				hasAlpha = true
				if pa > 0 { // the picture is premultiplied, the stream is not
					pr = uint8(min(255, (int(pr)*255+int(pa)/2)/int(pa)))
					pg = uint8(min(255, (int(pg)*255+int(pa)/2)/int(pa)))
					pb = uint8(min(255, (int(pb)*255+int(pa)/2)/int(pa)))
				}
			}
			argb[y*width+x] = uint32(pa)<<24 | uint32(pr)<<16 | uint32(pg)<<8 | uint32(pb)
		}
	}
	data := encodeVP8L(argb, width, height, hasAlpha)
	chunk := len(data)
	pad := chunk & 1
	var head [20]byte
	copy(head[0:], "RIFF")
	binary.LittleEndian.PutUint32(head[4:], uint32(4+8+chunk+pad))
	copy(head[8:], "WEBPVP8L")
	binary.LittleEndian.PutUint32(head[16:], uint32(chunk))
	if _, err := w.Write(head[:]); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if pad == 1 {
		_, err := w.Write([]byte{0})
		return err
	}
	return nil
}

type bitWriter struct {
	buf  []byte
	acc  uint64
	nacc uint
}

// put appends the n low bits of v, least significant first.
func (b *bitWriter) put(v uint32, n uint) {
	b.acc |= uint64(v) << b.nacc
	b.nacc += n
	for b.nacc >= 8 {
		b.buf = append(b.buf, byte(b.acc))
		b.acc >>= 8
		b.nacc -= 8
	}
}

func (b *bitWriter) bytes() []byte {
	if b.nacc > 0 {
		b.buf = append(b.buf, byte(b.acc))
		b.acc, b.nacc = 0, 0
	}
	return b.buf
}

// prefix is a canonical prefix code: per symbol its length and its code with
// the bits turned round, ready to be written least significant first. A code
// of one symbol has length 0 (it costs no bits).
type prefix struct {
	len  []uint8
	code []uint32
}

// bits is the cost of a symbol in bits.
func (p *prefix) bits(sym int) int { return int(p.len[sym]) }

func (p *prefix) write(b *bitWriter, sym int) {
	if l := p.len[sym]; l > 0 {
		b.put(p.code[sym], uint(l))
	}
}

// prefixCode writes the code for a histogram to the stream (alphabet symbols
// long) and returns it.
func (b *bitWriter) prefixCode(hist []int, alphabet int) *prefix {
	used := []int{}
	for s, c := range hist {
		if c > 0 {
			used = append(used, s)
		}
	}
	p := &prefix{len: make([]uint8, alphabet), code: make([]uint32, alphabet)}
	if len(used) == 0 {
		used = []int{0}
	}
	if len(used) <= 2 && used[len(used)-1] < 256 {
		// simple code: one or two symbols of up to 8 bits
		b.put(1, 1)
		b.put(uint32(len(used)-1), 1)
		if used[0] < 2 {
			b.put(0, 1)
			b.put(uint32(used[0]), 1)
		} else {
			b.put(1, 1)
			b.put(uint32(used[0]), 8)
		}
		if len(used) == 2 {
			b.put(uint32(used[1]), 8)
			p.len[used[0]], p.len[used[1]] = 1, 1
			p.code[used[0]], p.code[used[1]] = 0, 1
		}
		return p
	}
	full := make([]int, alphabet)
	copy(full, hist)
	lengths := huffmanLengths(full, 15)
	copy(p.len, lengths)
	if len(used) == 1 {
		// a lone symbol costs no bits in the data
		p.len = make([]uint8, alphabet)
	} else {
		assignCodes(p)
	}
	b.put(0, 1) // normal code
	// code lengths go out run-length coded (symbols 16 to 18)
	type tok struct{ sym, extra, nbits int }
	var toks []tok
	prev := 8
	for i := 0; i < alphabet; {
		v := int(lengths[i])
		j := i
		for j < alphabet && int(lengths[j]) == v {
			j++
		}
		c := j - i
		i = j
		if v == 0 {
			for c >= 3 {
				if c >= 11 {
					k := min(c, 138)
					toks = append(toks, tok{18, k - 11, 7})
					c -= k
				} else {
					toks = append(toks, tok{17, c - 3, 3})
					c = 0
				}
			}
			for ; c > 0; c-- {
				toks = append(toks, tok{0, 0, 0})
			}
			continue
		}
		if v != prev {
			toks = append(toks, tok{v, 0, 0})
			c--
			prev = v
		}
		for c >= 3 {
			k := min(c, 6)
			toks = append(toks, tok{16, k - 3, 2})
			c -= k
		}
		for ; c > 0; c-- {
			toks = append(toks, tok{v, 0, 0})
		}
	}
	clHist := make([]int, 19)
	for _, t := range toks {
		clHist[t.sym]++
	}
	clLens := huffmanLengths(clHist, 7)
	order := []int{17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	num := 19
	for num > 4 && clLens[order[num-1]] == 0 {
		num--
	}
	b.put(uint32(num-4), 4)
	for i := 0; i < num; i++ {
		b.put(uint32(clLens[order[i]]), 3)
	}
	b.put(0, 1) // as many lengths as symbols
	cl := &prefix{len: clLens, code: make([]uint32, 19)}
	if distinct(clHist) > 1 {
		assignCodes(cl)
	} else {
		// a single symbol costs no bits
		cl.len = make([]uint8, 19)
	}
	for _, t := range toks {
		cl.write(b, t.sym)
		if t.nbits > 0 {
			b.put(uint32(t.extra), uint(t.nbits))
		}
	}
	return p
}

func distinct(h []int) int {
	n := 0
	for _, c := range h {
		if c > 0 {
			n++
		}
	}
	return n
}

// assignCodes gives the canonical codes (shorter first, then by symbol) and
// stores them bit-reversed.
func assignCodes(p *prefix) {
	type sl struct{ s, l int }
	var order []sl
	for s, l := range p.len {
		if l > 0 {
			order = append(order, sl{s, int(l)})
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].l != order[j].l {
			return order[i].l < order[j].l
		}
		return order[i].s < order[j].s
	})
	code, prev := uint32(0), 0
	for _, o := range order {
		code <<= uint(o.l - prev)
		prev = o.l
		var rev uint32
		for i := 0; i < o.l; i++ {
			if code&(1<<uint(i)) != 0 {
				rev |= 1 << uint(o.l-1-i)
			}
		}
		p.code[o.s] = rev
		code++
	}
}

type hnode struct {
	count       int
	sym         int // -1 for an inner node
	left, right *hnode
}

type hheap []*hnode

func (h hheap) Len() int { return len(h) }
func (h hheap) Less(i, j int) bool {
	if h[i].count != h[j].count {
		return h[i].count < h[j].count
	}
	return h[i].sym > h[j].sym
}
func (h hheap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *hheap) Push(x any)   { *h = append(*h, x.(*hnode)) }
func (h *hheap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

// huffmanLengths returns the code length of every symbol of a histogram,
// none above maxLen (the counts are flattened until the tree fits).
func huffmanLengths(hist []int, maxLen int) []uint8 {
	out := make([]uint8, len(hist))
	minCount := 1
	for {
		h := &hheap{}
		for s, c := range hist {
			if c > 0 {
				if c < minCount {
					c = minCount
				}
				*h = append(*h, &hnode{count: c, sym: s})
			}
		}
		if len(*h) == 0 {
			return out
		}
		if len(*h) == 1 {
			out[(*h)[0].sym] = 1
			return out
		}
		heap.Init(h)
		for h.Len() > 1 {
			a := heap.Pop(h).(*hnode)
			c := heap.Pop(h).(*hnode)
			heap.Push(h, &hnode{count: a.count + c.count, sym: -1, left: a, right: c})
		}
		for i := range out {
			out[i] = 0
		}
		deep := false
		var walk func(n *hnode, d int)
		walk = func(n *hnode, d int) {
			if n.sym >= 0 {
				if d > maxLen {
					deep = true
				}
				out[n.sym] = uint8(d)
				return
			}
			walk(n.left, d+1)
			walk(n.right, d+1)
		}
		walk((*h)[0], 0)
		if !deep {
			return out
		}
		minCount *= 2
	}
}
