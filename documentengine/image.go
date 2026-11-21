package documentengine

import (
	"bytes"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"strings"
)

// rotate turns img clockwise by 0/90/180/270 degrees, always into a fresh Go-owned buffer
// when rotating. ponytail: At/Set per pixel, ~100ms for a 300dpi page; switch to
// direct Pix copies if rotation shows up in profiles.
func rotate(src image.Image, deg int) image.Image {
	if deg == 0 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if deg == 90 || deg == 270 {
		dw, dh = h, w
	}
	var dst draw.Image = image.NewRGBA(image.Rect(0, 0, dw, dh))
	if _, ok := src.(*image.Gray); ok {
		dst = image.NewGray(image.Rect(0, 0, dw, dh))
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.At(b.Min.X+x, b.Min.Y+y)
			switch deg {
			case 90:
				dst.Set(h-1-y, x, c)
			case 180:
				dst.Set(w-1-x, h-1-y, c)
			case 270:
				dst.Set(y, w-1-x, c)
			}
		}
	}
	return dst
}

func encode(img image.Image, f ImageFormat, quality int) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	switch strings.ToLower(string(f)) {
	case "jpeg", "jpg":
		if quality < 1 || quality > 100 {
			quality = 90
		}
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	default:
		err = png.Encode(&buf, img)
	}
	return buf.Bytes(), err
}
