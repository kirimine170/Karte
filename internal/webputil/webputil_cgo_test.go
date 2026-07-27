//go:build cgo

package webputil

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

func TestEncodeWebPProducesRIFFContainer(t *testing.T) {
	for _, lossless := range []bool{false, true} {
		t.Run(map[bool]string{false: "lossy", true: "lossless"}[lossless], func(t *testing.T) {
			img := image.NewRGBA(image.Rect(0, 0, 2, 2))
			img.Set(0, 0, color.RGBA{R: 255, A: 255})

			var output bytes.Buffer
			if err := EncodeWebP(&output, img, lossless); err != nil {
				t.Fatal(err)
			}
			data := output.Bytes()
			if len(data) < 12 {
				t.Fatalf("encoded WebP is too short: %d bytes", len(data))
			}
			if got := string(data[:4]); got != "RIFF" {
				t.Fatalf("encoded image has %q header, want RIFF", got)
			}
			if got := string(data[8:12]); got != "WEBP" {
				t.Fatalf("encoded image has %q format, want WEBP", got)
			}
		})
	}
}
