package scan

import (
	"image"
	imagedraw "image/draw"
	"math"
	"os"
	"strings"
)

// BlurHash (https://blurha.sh) of a picture, the compact placeholder clients
// draw while the real image loads. The number of components follows the
// picture's aspect ratio the way the reference server picks it: about 20 in
// total, up to 9 along the long side.

const blurAlphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~"

// blurComponents returns the number of horizontal and vertical components
// for a picture of the given size.
func blurComponents(w, h int) (int, int) {
	long, short := w, h
	portrait := false
	if h > w {
		long, short = h, w
		portrait = true
	}
	r := float64(long) / float64(short)
	var x, y int
	switch {
	case r < 1.02:
		x, y = 5, 5
	case r < 1.57:
		x, y = 5, 4
	case r < 1.79:
		x, y = 6, 4
	case r < 2.25:
		x, y = 6, 3
	case r < 3.1:
		x, y = 7, 3
	case r < 3.75:
		x, y = 8, 3
	case r < 4.25:
		x, y = 9, 3
	case r < 17:
		x, y = 9, 2
	default:
		x, y = 9, 1
	}
	if portrait {
		x, y = y, x
	}
	return x, y
}

// blurHashOf encodes img with the components blurComponents picks.
func blurHashOf(img image.Image) string {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return ""
	}
	xc, yc := blurComponents(w, h)
	// Work on a bounded copy: the transform is O(pixels x components).
	img = shrinkTo(img, 32)
	b = img.Bounds()
	w, h = b.Dx(), b.Dy()
	rgba := image.NewNRGBA(image.Rect(0, 0, w, h))
	imagedraw.Draw(rgba, rgba.Bounds(), img, b.Min, imagedraw.Src)

	lin := make([]float64, 256)
	for i := range lin {
		v := float64(i) / 255
		if v <= 0.04045 {
			lin[i] = v / 12.92
		} else {
			lin[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	factors := make([][3]float64, 0, xc*yc)
	for j := 0; j < yc; j++ {
		for i := 0; i < xc; i++ {
			norm := 2.0
			if i == 0 && j == 0 {
				norm = 1
			}
			var r, g, bl float64
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					basis := norm * math.Cos(math.Pi*float64(i)*float64(x)/float64(w)) * math.Cos(math.Pi*float64(j)*float64(y)/float64(h))
					o := rgba.PixOffset(x, y)
					r += basis * lin[rgba.Pix[o]]
					g += basis * lin[rgba.Pix[o+1]]
					bl += basis * lin[rgba.Pix[o+2]]
				}
			}
			s := 1 / float64(w*h)
			factors = append(factors, [3]float64{r * s, g * s, bl * s})
		}
	}
	var sb strings.Builder
	enc := func(v, n int) {
		out := make([]byte, n)
		for i := n - 1; i >= 0; i-- {
			out[i] = blurAlphabet[v%83]
			v /= 83
		}
		sb.Write(out)
	}
	enc((xc-1)+(yc-1)*9, 1)
	var maxAC float64
	ac := factors[1:]
	if len(ac) > 0 {
		for _, f := range ac {
			for _, c := range f {
				if a := math.Abs(c); a > maxAC {
					maxAC = a
				}
			}
		}
		q := int(math.Max(0, math.Min(82, math.Floor(maxAC*166-0.5))))
		maxAC = float64(q+1) / 166
		enc(q, 1)
	} else {
		maxAC = 1
		enc(0, 1)
	}
	toSRGB := func(v float64) int {
		v = math.Max(0, math.Min(1, v))
		if v <= 0.0031308 {
			return int(v*12.92*255 + 0.5)
		}
		return int((1.055*math.Pow(v, 1/2.4)-0.055)*255 + 0.5)
	}
	dc := factors[0]
	enc(toSRGB(dc[0])<<16|toSRGB(dc[1])<<8|toSRGB(dc[2]), 4)
	signPow := func(v float64) float64 {
		return math.Copysign(math.Pow(math.Abs(v), 0.5), v)
	}
	for _, f := range ac {
		q := func(c float64) int {
			return int(math.Max(0, math.Min(18, math.Floor(signPow(c/maxAC)*9+9.5))))
		}
		enc(q(f[0])*19*19+q(f[1])*19+q(f[2]), 2)
	}
	return sb.String()
}

// shrinkTo returns img scaled down (area average) so that neither side exceeds
// max pixels; pictures already that small are returned as they are.
func shrinkTo(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return img
	}
	nw, nh := max, max
	if w > h {
		nh = h * max / w
	} else {
		nw = w * max / h
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	out := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := 0; y < nh; y++ {
		y0, y1 := y*h/nh, (y+1)*h/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < nw; x++ {
			x0, x1 := x*w/nw, (x+1)*w/nw
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint64
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, _ := img.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
					r += uint64(cr >> 8)
					g += uint64(cg >> 8)
					bl += uint64(cb >> 8)
					n++
				}
			}
			o := out.PixOffset(x, y)
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(bl/n), 255
		}
	}
	return out
}

// imageFileBlurHash decodes the picture at path and returns its BlurHash
// ("" when it cannot be decoded: webp and tbn files in an unsupported
// encoding simply carry no hash).
// ImageFileBlurHash is imageFileBlurHash for a caller outside this package that
// makes an image of its own and stores it as a file — api's generated library
// card image is the only one today (internal/api/library_image.go). Same
// "empty string when the file cannot be read or the hash cannot be built"
// contract as the unexported form: a blurhash is a nicety, never a reason to
// fail the picture it describes.
func ImageFileBlurHash(path string) string { return imageFileBlurHash(path) }

func imageFileBlurHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return ""
	}
	return blurHashOf(img)
}
