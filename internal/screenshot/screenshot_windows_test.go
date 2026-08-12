//go:build windows

package screenshot

import (
	"encoding/binary"
	"errors"
	"image/color"
	"testing"
)

func TestWaitForClipboardDIBReportsCancellation(t *testing.T) {
	_, err := waitForClipboardDIB(0, 0)
	if !errors.Is(err, errScreenClippingCancelled) {
		t.Fatalf("waitForClipboardDIB() error = %v, want cancellation", err)
	}
}

func TestDecodeDIBHandlesBottomUpBGRPixels(t *testing.T) {
	const width, height = 2, 2
	stride := 8
	dib := make([]byte, 40+stride*height)
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], width)
	binary.LittleEndian.PutUint32(dib[8:12], height)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 24)

	// DIB rows are bottom-up and pixels are BGR.
	copy(dib[40:48], []byte{255, 0, 0, 255, 255, 255, 0, 0})
	copy(dib[48:56], []byte{0, 0, 255, 0, 255, 0, 0, 0})
	decoded, err := decodeDIB(dib)
	if err != nil {
		t.Fatal(err)
	}
	if got := color.RGBAModel.Convert(decoded.At(0, 0)).(color.RGBA); got != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("top-left = %#v, want red", got)
	}
	if got := color.RGBAModel.Convert(decoded.At(1, 0)).(color.RGBA); got != (color.RGBA{G: 255, A: 255}) {
		t.Fatalf("top-right = %#v, want green", got)
	}
	if got := color.RGBAModel.Convert(decoded.At(0, 1)).(color.RGBA); got != (color.RGBA{B: 255, A: 255}) {
		t.Fatalf("bottom-left = %#v, want blue", got)
	}
}

func TestDecodeDIBRejectsTruncatedPixels(t *testing.T) {
	dib := make([]byte, 40)
	binary.LittleEndian.PutUint32(dib[0:4], 40)
	binary.LittleEndian.PutUint32(dib[4:8], 10)
	binary.LittleEndian.PutUint32(dib[8:12], 10)
	binary.LittleEndian.PutUint16(dib[12:14], 1)
	binary.LittleEndian.PutUint16(dib[14:16], 32)
	if _, err := decodeDIB(dib); err == nil {
		t.Fatal("expected truncated DIB error")
	}
}
