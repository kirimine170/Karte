//go:build cgo

package webputil

import (
	"image"
	"io"

	"github.com/chai2010/webp"
)

// EncodeWebP uses the bundled libwebp encoder to produce a real RIFF/WebP
// container on every platform where cgo is enabled.
func EncodeWebP(w io.Writer, img image.Image, lossless bool) error {
	opts := &webp.Options{Lossless: lossless}
	if !lossless {
		opts.Quality = 90
	}
	return webp.Encode(w, img, opts)
}
