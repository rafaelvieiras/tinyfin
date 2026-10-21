package conformance

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestCheckText(t *testing.T) {
	c := &Client{Vars: map[string]string{"k": "v"}}
	r := &Response{Body: []byte("WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello v\n")}
	ok := []TextAssert{
		{Op: "prefix", Value: "WEBVTT\n"},
		{Op: "contains", Value: "Hello {{k}}"},
		{Op: "notcontains", Value: "Goodbye"},
		{Op: "matches", Value: `(?s)^WEBVTT.*Hello`},
		{Op: "lines", Value: float64(3)},
		{Op: "eq", Value: "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nHello v\n"},
	}
	for _, a := range ok {
		if err := c.checkText(r, a, nil); err != nil {
			t.Errorf("%s %v: %v", a.Op, a.Value, err)
		}
	}
	bad := []TextAssert{
		{Op: "prefix", Value: "X"}, {Op: "contains", Value: "nope"}, {Op: "notcontains", Value: "Hello"},
		{Op: "matches", Value: `^Hello`}, {Op: "lines", Value: float64(2)}, {Op: "eq", Value: "x"}, {Op: "nope", Value: "x"},
	}
	for _, a := range bad {
		if err := c.checkText(r, a, nil); err == nil {
			t.Errorf("%s %v: no error", a.Op, a.Value)
		}
	}
}

func TestCheckTextBinary(t *testing.T) {
	c := &Client{}
	r := &Response{Body: []byte{'R', 'I', 'F', 'F', 0x9d, 0x01, 0x2a}}
	for _, a := range []TextAssert{{Op: "hexprefix", Value: "52494646"}, {Op: "hexcontains", Value: "9d012a"}, {Op: "bytes_lte", Value: float64(7)}, {Op: "bytes_gte", Value: float64(7)}} {
		if err := c.checkText(r, a, nil); err != nil {
			t.Errorf("%s: %v", a.Op, err)
		}
	}
	for _, a := range []TextAssert{{Op: "hexprefix", Value: "9d"}, {Op: "hexcontains", Value: "ff"}, {Op: "bytes_lte", Value: float64(6)}, {Op: "bytes_gte", Value: float64(8)}, {Op: "hexprefix", Value: "zz"}} {
		if err := c.checkText(r, a, nil); err == nil {
			t.Errorf("%s %v: no error", a.Op, a.Value)
		}
	}
}

func TestCheckImage(t *testing.T) {
	var buf bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for i := 3; i < len(img.Pix); i += 4 {
		img.Pix[i] = 255
	}
	img.SetRGBA(3, 2, color.RGBA{10, 20, 30, 255})
	png.Encode(&buf, img)
	r := &Response{Body: buf.Bytes()}
	if err := checkImage(r, &ImageAssert{Width: 4, Height: 3, Pixels: []PixelAssert{{X: -1, Y: -1, RGB: &[3]int{11, 20, 29}, Tol: 1}, {X: 0, Y: 0, RGB: &[3]int{0, 0, 0}, A: intp(255)}}}); err != nil {
		t.Error(err)
	}
	if err := checkImage(r, &ImageAssert{Pixels: []PixelAssert{{X: 3, Y: 2, RGB: &[3]int{12, 20, 30}, Tol: 1}}}); err == nil {
		t.Error("tolerance not enforced")
	}
	if err := checkImage(r, &ImageAssert{Width: 5}); err == nil {
		t.Error("width not checked")
	}
	if err := checkImage(&Response{Body: []byte("x")}, &ImageAssert{}); err == nil {
		t.Error("not a picture accepted")
	}
}

func intp(v int) *int { return &v }
