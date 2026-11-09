// Package libraryart composes the picture a media library shows on a
// dashboard card: a 16:9 mosaic of the library's own artwork with the
// library's name over it.
//
// Why this lives in the server at all: the reference server generates a
// library image from the library's media when the library has none of its
// own and publishes it as VirtualFolderInfo.PrimaryImageItemId — a client
// only draws a library card image when that field is set (docs/11's
// libraries-page pass). It is a presentation of already-cataloged artwork,
// not cataloging of its own (AGENTS.md principle 6), the same place trickplay
// and chapter thumbnails sit.
//
// The look is deliberately this project's own, not the reference's: the
// reference draws a single still, this draws up to four pictures side by side
// with a slanted edge between them (docs/00-decision-log.md, D36). What is
// matched is the contract around it — canvas size, PNG, the name rendered in
// white, and a stable file once generated.
package libraryart

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

const (
	// Width and Height are the canvas a dashboard card expects: 16:9, the
	// size the reference server's own generated library images are
	// (observed live: 960x540 PNG).
	Width  = 960
	Height = 540

	// MaxSources is how many pictures the mosaic uses at most. Four fills the
	// card without making any single picture a sliver.
	MaxSources = 4

	// slant is how far a band's edge leans over the canvas height, in pixels
	// per pixel: edges are the thing that makes this a mosaic of diagonal
	// bands instead of plain columns.
	slant = 0.22

	// nameMaxWidthFraction keeps the library name inside the card with a
	// margin, whatever its length.
	nameMaxWidthFraction = 0.82
	nameStartPointSize   = 108
	nameMinPointSize     = 18
)

// ErrNoSources is returned by Compose when there is nothing to draw: a library
// with no artwork of its own gets no generated image, so a dashboard keeps
// showing the collection-type icon (the same "no media with posters, no
// generated library image" rule the reference server follows).
var ErrNoSources = errors.New("libraryart: no source images")

// Compose writes a 16:9 PNG for a library called name, built from sources
// (at most MaxSources are used, in order). Each source fills one full-height
// band; the boundaries between bands lean, so consecutive pictures meet on a
// diagonal edge. name is set in white, centred, with a soft shadow.
func Compose(w io.Writer, name string, sources []image.Image) error {
	if len(sources) == 0 {
		return ErrNoSources
	}
	if len(sources) > MaxSources {
		sources = sources[:MaxSources]
	}

	canvas := image.NewRGBA(image.Rect(0, 0, Width, Height))
	// Opaque base: a source with transparency (a PNG logo, say) composites
	// onto black instead of producing a transparent card.
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)

	drawBands(canvas, sources)
	if err := drawName(canvas, name); err != nil {
		return err
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return fmt.Errorf("libraryart: encoding PNG: %w", err)
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return fmt.Errorf("libraryart: writing PNG: %w", err)
	}
	return nil
}

// drawBands paints sources as slanted bands across canvas. Band i owns the
// columns between edge i and edge i+1, where edge j is j's share of the width
// shifted by the band lean at that row — the bands therefore tile the canvas
// exactly, with no gap and no overlap.
func drawBands(canvas *image.RGBA, sources []image.Image) {
	n := len(sources)
	bandWidth := float64(Width) / float64(n)
	// Each band's picture is scaled to cover its own band plus the lean on
	// both sides, so the slanted window never has to smear an edge pixel.
	reach := math.Abs(slant)*float64(Height)/2 + 1
	coverWidth := int(math.Ceil(bandWidth + 2*reach))

	bands := make([]*image.RGBA, n)
	for i, src := range sources {
		bands[i] = scaleCover(src, coverWidth, Height)
	}

	for y := 0; y < Height; y++ {
		// Boundaries for this row, in pixels. Row y's centre is what the
		// lean is evaluated at, so the diagonal is continuous.
		lean := slant * (float64(y) + 0.5 - float64(Height)/2)
		for i := 0; i < n; i++ {
			lo := float64(i)*bandWidth + lean
			hi := lo + bandWidth
			if i == 0 {
				lo = -1 // the first band owns everything left of its edge
			}
			if i == n-1 {
				hi = float64(Width) + 1
			}
			originX := int(math.Round(float64(i)*bandWidth - reach))
			blendRow(canvas, bands[i], y, lo, hi, originX)
		}
	}
}

// blendRow composites one band's pixels for row y into canvas, weighting each
// pixel by how much of it the band actually covers so the diagonal edge is
// smooth rather than stair-stepped.
func blendRow(canvas *image.RGBA, band *image.RGBA, y int, lo, hi float64, originX int) {
	x0 := int(math.Floor(lo)) - 1
	if x0 < 0 {
		x0 = 0
	}
	x1 := int(math.Ceil(hi)) + 1
	if x1 > Width {
		x1 = Width
	}
	canvasRow := canvas.Pix[y*canvas.Stride:]
	bandRow := band.Pix[y*band.Stride:]
	for x := x0; x < x1; x++ {
		cov := math.Min(hi, float64(x)+0.5) - math.Max(lo, float64(x)-0.5)
		if cov <= 0 {
			continue
		}
		if cov > 1 {
			cov = 1
		}
		sx := x - originX
		if sx < 0 || sx >= band.Rect.Dx() {
			continue
		}
		si := sx * 4
		di := x * 4
		mix := func(dst, src uint8) uint8 {
			// Canvas is opaque and the band's pixels are premultiplied, so
			// straight source-over on the colour channels is correct here;
			// alpha is forced opaque once, at the end of the row.
			return uint8(float64(dst)*(1-cov) + float64(src)*cov + 0.5)
		}
		canvasRow[di] = mix(canvasRow[di], bandRow[si])
		canvasRow[di+1] = mix(canvasRow[di+1], bandRow[si+1])
		canvasRow[di+2] = mix(canvasRow[di+2], bandRow[si+2])
		canvasRow[di+3] = 0xFF
	}
}

// scaleCover scales src to fill exactly w×h, cropping the overflow from the
// centre — a poster becomes a full-height band without letterboxing, and a
// wide still keeps its middle third rather than being squashed.
func scaleCover(src image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	b := src.Bounds()
	sw, sh := float64(b.Dx()), float64(b.Dy())
	if sw == 0 || sh == 0 {
		return dst
	}
	scale := math.Max(float64(w)/sw, float64(h)/sh)
	cropW := float64(w) / scale
	cropH := float64(h) / scale
	// Centre the crop in the source's own coordinate space (Bounds need not
	// start at the origin — a decoded sub-image keeps its parent's offset).
	srcRect := image.Rect(
		b.Min.X+int(math.Round((sw-cropW)/2)),
		b.Min.Y+int(math.Round((sh-cropH)/2)),
		b.Min.X+int(math.Round((sw-cropW)/2+cropW)),
		b.Min.Y+int(math.Round((sh-cropH)/2+cropH)),
	)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, srcRect, xdraw.Src, nil)
	return dst
}

// drawName sets the library's name in white over the mosaic, shrinking it
// until it fits, with a soft dark shadow so it stays readable over a bright
// picture.
func drawName(canvas *image.RGBA, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}

	parsed, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return fmt.Errorf("libraryart: parsing embedded font: %w", err)
	}

	limit := nameMaxWidthFraction * Width
	size := float64(nameStartPointSize)
	var face font.Face
	for {
		face, err = opentype.NewFace(parsed, &opentype.FaceOptions{
			Size: size, DPI: 72, Hinting: font.HintingFull,
		})
		if err != nil {
			return fmt.Errorf("libraryart: building font face: %w", err)
		}
		if float64(font.MeasureString(face, name).Round()) <= limit || size <= nameMinPointSize {
			break
		}
		face.Close()
		size -= 4
	}
	defer face.Close()

	textWidth := font.MeasureString(face, name).Round()
	metrics := face.Metrics()
	x := (Width - textWidth) / 2
	y := (Height-metrics.Ascent.Round()-metrics.Descent.Round())/2 + metrics.Ascent.Round()

	// A dark outline at a few offsets first, so white text stays readable over
	// a bright poster, then the text itself.
	outline := image.NewUniform(color.RGBA{A: 0xB0})
	for _, off := range [][2]int{{-3, 0}, {3, 0}, {0, -3}, {0, 3}, {4, 4}} {
		d := font.Drawer{
			Dst:  canvas,
			Src:  outline,
			Face: face,
			Dot:  fixed.P(x+off[0], y+off[1]),
		}
		d.DrawString(name)
	}
	d := font.Drawer{
		Dst:  canvas,
		Src:  image.NewUniform(color.White),
		Face: face,
		Dot:  fixed.P(x, y),
	}
	d.DrawString(name)
	return nil
}
