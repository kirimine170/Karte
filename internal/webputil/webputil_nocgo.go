//go:build !cgo

package webputil

import (
	"errors"
	"image"
	"io"
)

// EncodeWebP fails explicitly when the native encoder is unavailable. Writing
// PNG bytes with a .webp extension would create an invalid RIFF container and
// break Karte's WebP metadata chunks.
func EncodeWebP(_ io.Writer, _ image.Image, _ bool) error {
	return errors.New("WebP encoding requires cgo")
}
