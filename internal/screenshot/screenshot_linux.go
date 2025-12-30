//go:build linux

package screenshot

import "fmt"

// CaptureScreenInteractive is a Linux stub for testing environments.
func CaptureScreenInteractive(_ string) (string, error) {
	return "", fmt.Errorf("screenshot capture is not supported on Linux test build")
}
