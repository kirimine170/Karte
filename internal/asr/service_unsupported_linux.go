//go:build linux

package asr

import "fmt"

func unsupportedOfflinePlatformError(operation string) error {
	switch operation {
	case "transcribe":
		return fmt.Errorf("ASR transcription is not supported on Linux test build")
	case "samples":
		return fmt.Errorf("ASR process samples is not supported on Linux test build")
	default:
		return fmt.Errorf("ASR is not supported on Linux test build")
	}
}

func unsupportedRealtimePlatformError() error {
	return fmt.Errorf("Realtime ASR is not supported on Linux test build")
}
