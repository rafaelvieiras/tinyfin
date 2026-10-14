package scan

import (
	"bufio"
	"errors"
	"image"
	"image/color"
	"io"
)

// A minimal WebP reader: only the picture's size is read (VP8, VP8L and VP8X
// headers), which is all the scanner needs of a picture; the pixels are not
// decoded.

func init() {
	image.RegisterFormat("webp", "RIFF????WEBP", decodeWebP, decodeWebPConfig)
}

func decodeWebP(r io.Reader) (image.Image, error) {
	return nil, errors.New("webp: pixel decoding is not supported")
}

func decodeWebPConfig(r io.Reader) (image.Config, error) {
	br := bufio.NewReader(r)
	head := make([]byte, 30)
	if _, err := io.ReadFull(br, head); err != nil {
		return image.Config{}, err
	}
	if string(head[0:4]) != "RIFF" || string(head[8:12]) != "WEBP" {
		return image.Config{}, errors.New("webp: not a webp file")
	}
	var w, h int
	switch string(head[12:16]) {
	case "VP8 ":
		// frame tag (3 bytes), start code (3 bytes), then 14-bit sizes
		if head[23] != 0x9d || head[24] != 0x01 || head[25] != 0x2a {
			return image.Config{}, errors.New("webp: bad VP8 frame")
		}
		w = int(head[26]) | int(head[27]&0x3f)<<8
		h = int(head[28]) | int(head[29]&0x3f)<<8
	case "VP8L":
		if head[20] != 0x2f {
			return image.Config{}, errors.New("webp: bad VP8L signature")
		}
		bits := uint32(head[21]) | uint32(head[22])<<8 | uint32(head[23])<<16 | uint32(head[24])<<24
		w = int(bits&0x3fff) + 1
		h = int((bits>>14)&0x3fff) + 1
	case "VP8X":
		w = (int(head[24]) | int(head[25])<<8 | int(head[26])<<16) + 1
		h = (int(head[27]) | int(head[28])<<8 | int(head[29])<<16) + 1
	default:
		return image.Config{}, errors.New("webp: unknown chunk")
	}
	return image.Config{ColorModel: color.RGBAModel, Width: w, Height: h}, nil
}
