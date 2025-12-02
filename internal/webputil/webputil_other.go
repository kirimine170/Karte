//go:build !darwin

package webputil

import (
	"image"
	"image/png"
	"io"
)

// EncodeWebP は非 macOS では cgo 依存を避けるため、実際には PNG としてエンコードする。
// 拡張子は .webp のままだが、ブラウザやビューアはヘッダからフォーマットを判別できるため、
// このアプリの用途では問題にならない想定。
func EncodeWebP(w io.Writer, img image.Image, lossless bool) error {
	_ = lossless
	return png.Encode(w, img)
}


