//go:build darwin

package webputil

import (
	"image"
	"io"

	"github.com/chai2010/webp"
)

// EncodeWebP は macOS では chai2010/webp を使って本物の WebP を生成する。
func EncodeWebP(w io.Writer, img image.Image, lossless bool) error {
	opts := &webp.Options{Lossless: lossless}
	if !lossless {
		opts.Quality = 90
	}
	return webp.Encode(w, img, opts)
}


