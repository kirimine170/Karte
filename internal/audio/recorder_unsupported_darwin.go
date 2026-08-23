//go:build darwin && universal

package audio

import "errors"

func recordingPlatformError(operation string) error {
	if operation == "start" {
		return errors.New("audio recording is not supported on Universal Binary builds")
	}
	return errors.New("audio recording is not supported on Universal Binary builds (PortAudio backend is disabled for universal builds)")
}
