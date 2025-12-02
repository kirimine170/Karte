//go:build darwin

package screenshot

import (
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/chai2010/webp"
)

// CaptureScreenInteractive invokes macOS screencapture in interactive mode,
// converts the captured image to WebP, and stores it under
// <dataDir>/data/image/. It returns the path relative to dataDir
// (e.g. "data/image/screenshot-YYYYMMDD-HHMMSS.webp").
func CaptureScreenInteractive(dataDir string) (string, error) {
	if dataDir == "" {
		return "", fmt.Errorf("dataDir is empty")
	}

	tmpDir := os.TempDir()
	if tmpDir == "" {
		tmpDir = "."
	}
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("karte-screenshot-%d.png", time.Now().UnixNano()))

	// Run screencapture in interactive mode.
	cmd := exec.Command("screencapture", "-i", "-t", "png", tmpFile)
	if err := cmd.Run(); err != nil {
		// If the user cancels the capture, screencapture returns a non-zero exit code
		// and the file may not exist. Treat this as a non-fatal cancellation.
		if _, statErr := os.Stat(tmpFile); os.IsNotExist(statErr) {
			return "", fmt.Errorf("screencapture cancelled")
		}
		return "", fmt.Errorf("screencapture failed: %w", err)
	}

	// Ensure temporary file is cleaned up.
	defer os.Remove(tmpFile)

	f, err := os.Open(tmpFile)
	if err != nil {
		return "", fmt.Errorf("open temp screenshot: %w", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return "", fmt.Errorf("decode temp screenshot png: %w", err)
	}

	imageDir := filepath.Join(dataDir, "data", "image")
	if err := os.MkdirAll(imageDir, 0o755); err != nil {
		return "", fmt.Errorf("create image directory: %w", err)
	}

	timestamp := time.Now().Format("20060102-150405")
	outPath := filepath.Join(imageDir, fmt.Sprintf("screenshot-%s.webp", timestamp))

	outFile, err := os.Create(outPath)
	if err != nil {
		return "", fmt.Errorf("create webp file: %w", err)
	}
	defer outFile.Close()

	// Encode as WebP. Use lossless mode to preserve text/line sharpness.
	if err := webp.Encode(outFile, img, &webp.Options{Lossless: true, Quality: 90}); err != nil {
		return "", fmt.Errorf("encode webp: %w", err)
	}

	rel, err := filepath.Rel(dataDir, outPath)
	if err != nil {
		// Fallback to absolute if relative fails (should be rare).
		return filepath.ToSlash(outPath), nil
	}
	return filepath.ToSlash(rel), nil
}
