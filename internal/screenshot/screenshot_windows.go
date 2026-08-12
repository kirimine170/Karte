//go:build windows

package screenshot

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"time"
	"unsafe"

	"karte/internal/webputil"

	"golang.org/x/sys/windows"
)

const (
	clipboardFormatDIB         = 8
	bitmapCompressionRGB       = 0
	bitmapCompressionBitfields = 3
	windowsCaptureTimeout      = 2 * time.Minute
)

var (
	errScreenClippingCancelled     = errors.New("Windows screen clipping cancelled or timed out")
	user32                         = windows.NewLazySystemDLL("user32.dll")
	kernel32                       = windows.NewLazySystemDLL("kernel32.dll")
	procOpenClipboard              = user32.NewProc("OpenClipboard")
	procCloseClipboard             = user32.NewProc("CloseClipboard")
	procGetClipboardData           = user32.NewProc("GetClipboardData")
	procIsClipboardFormatAvailable = user32.NewProc("IsClipboardFormatAvailable")
	procGetClipboardSequenceNumber = user32.NewProc("GetClipboardSequenceNumber")
	procGlobalLock                 = kernel32.NewProc("GlobalLock")
	procGlobalUnlock               = kernel32.NewProc("GlobalUnlock")
	procGlobalSize                 = kernel32.NewProc("GlobalSize")
)

// CaptureScreenInteractive opens the Windows 11 screen clipping UI. Windows
// handles region selection, monitor coordinates, and DPI scaling; Karte reads
// the resulting DIB from the clipboard and stores a lossless WebP image.
func CaptureScreenInteractive(dataDir string) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("dataDir is empty")
	}
	sequence, _, _ := procGetClipboardSequenceNumber.Call()
	cmd := exec.Command("explorer.exe", "ms-screenclip:")
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start Windows screen clipping: %w", err)
	}
	_ = cmd.Process.Release()

	dib, err := waitForClipboardDIB(uint32(sequence), windowsCaptureTimeout)
	if errors.Is(err, errScreenClippingCancelled) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	img, err := decodeDIB(dib)
	if err != nil {
		return "", fmt.Errorf("decode Windows screen clipping: %w", err)
	}

	imageDir := filepath.Join(dataDir, "data", "image")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return "", fmt.Errorf("create image directory: %w", err)
	}
	outPath := filepath.Join(imageDir, fmt.Sprintf("screenshot-%s.webp", time.Now().Format("20060102-150405.000")))
	out, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create screenshot WebP: %w", err)
	}
	if err := webputil.EncodeWebP(out, img, true); err != nil {
		_ = out.Close()
		_ = os.Remove(outPath)
		return "", fmt.Errorf("encode screenshot WebP: %w", err)
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(outPath)
		return "", fmt.Errorf("close screenshot WebP: %w", err)
	}
	rel, err := filepath.Rel(dataDir, outPath)
	if err != nil {
		return filepath.ToSlash(outPath), nil
	}
	return filepath.ToSlash(rel), nil
}

func waitForClipboardDIB(initialSequence uint32, timeout time.Duration) ([]byte, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sequence, _, _ := procGetClipboardSequenceNumber.Call()
		if uint32(sequence) != initialSequence {
			if dib, err := readClipboardDIB(); err == nil {
				return dib, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return nil, errScreenClippingCancelled
}

func readClipboardDIB() ([]byte, error) {
	available, _, _ := procIsClipboardFormatAvailable.Call(clipboardFormatDIB)
	if available == 0 {
		return nil, fmt.Errorf("screen clipping did not provide a DIB image")
	}
	opened, _, openErr := procOpenClipboard.Call(0)
	if opened == 0 {
		return nil, fmt.Errorf("open clipboard: %v", openErr)
	}
	defer procCloseClipboard.Call()

	handle, _, dataErr := procGetClipboardData.Call(clipboardFormatDIB)
	if handle == 0 {
		return nil, fmt.Errorf("get clipboard DIB: %v", dataErr)
	}
	size, _, sizeErr := procGlobalSize.Call(handle)
	if size == 0 {
		return nil, fmt.Errorf("get clipboard DIB size: %v", sizeErr)
	}
	pointer, _, lockErr := procGlobalLock.Call(handle)
	if pointer == 0 {
		return nil, fmt.Errorf("lock clipboard DIB: %v", lockErr)
	}
	defer procGlobalUnlock.Call(handle)

	if size > uintptr(^uint(0)>>1) {
		return nil, fmt.Errorf("clipboard DIB is too large: %d bytes", size)
	}
	data := make([]byte, int(size))
	copy(data, unsafe.Slice((*byte)(unsafe.Pointer(pointer)), int(size)))
	return data, nil
}

func decodeDIB(data []byte) (image.Image, error) {
	if len(data) < 40 {
		return nil, fmt.Errorf("DIB header is truncated")
	}
	headerSize := int(binary.LittleEndian.Uint32(data[0:4]))
	width := int(int32(binary.LittleEndian.Uint32(data[4:8])))
	rawHeight := int(int32(binary.LittleEndian.Uint32(data[8:12])))
	planes := binary.LittleEndian.Uint16(data[12:14])
	bitsPerPixel := int(binary.LittleEndian.Uint16(data[14:16]))
	compression := binary.LittleEndian.Uint32(data[16:20])
	if headerSize < 40 || headerSize > len(data) {
		return nil, fmt.Errorf("invalid DIB header size: %d", headerSize)
	}
	if width <= 0 || rawHeight == 0 || planes != 1 {
		return nil, fmt.Errorf("invalid DIB dimensions or planes: %dx%d planes=%d", width, rawHeight, planes)
	}
	if bitsPerPixel != 24 && bitsPerPixel != 32 {
		return nil, fmt.Errorf("unsupported DIB bit depth: %d", bitsPerPixel)
	}
	if compression != bitmapCompressionRGB && compression != bitmapCompressionBitfields {
		return nil, fmt.Errorf("unsupported DIB compression: %d", compression)
	}

	pixelOffset := headerSize
	if headerSize == 40 && compression == bitmapCompressionBitfields {
		pixelOffset += 12
	}
	height := rawHeight
	bottomUp := height > 0
	if height < 0 {
		height = -height
	}
	stride := ((width*bitsPerPixel + 31) / 32) * 4
	required := pixelOffset + stride*height
	if required > len(data) {
		return nil, fmt.Errorf("DIB pixel data is truncated: need %d bytes, have %d", required, len(data))
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	bytesPerPixel := bitsPerPixel / 8
	for y := 0; y < height; y++ {
		sourceY := y
		if bottomUp {
			sourceY = height - 1 - y
		}
		row := data[pixelOffset+sourceY*stride : pixelOffset+(sourceY+1)*stride]
		for x := 0; x < width; x++ {
			source := x * bytesPerPixel
			destination := img.PixOffset(x, y)
			img.Pix[destination] = row[source+2]
			img.Pix[destination+1] = row[source+1]
			img.Pix[destination+2] = row[source]
			img.Pix[destination+3] = 0xff
		}
	}
	return img, nil
}
