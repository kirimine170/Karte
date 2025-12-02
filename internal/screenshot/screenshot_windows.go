//go:build windows

package screenshot

import "fmt"

// CaptureScreenInteractive is a stub implementation for Windows.
// Full screenshot support will be implemented in a future version.
func CaptureScreenInteractive(dataDir string) (string, error) {
	// Keep implementation minimal and avoid platform-specific dependencies so
	// that this file can compile on Windows without extra requirements.
	_ = dataDir
	return "", fmt.Errorf("screenshot is not yet supported on Windows")
}
