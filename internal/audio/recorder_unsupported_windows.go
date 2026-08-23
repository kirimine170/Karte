//go:build windows && !cgo

package audio

import "errors"

func recordingPlatformError(string) error {
	return errors.New("audio recording requires the Windows PortAudio runtime")
}
