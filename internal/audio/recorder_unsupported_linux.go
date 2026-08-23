//go:build linux

package audio

import "errors"

func recordingPlatformError(operation string) error {
	if operation == "start" {
		return errors.New("audio recording is not supported on Linux test build")
	}
	return errors.New("audio recording is not supported on Linux test build")
}
