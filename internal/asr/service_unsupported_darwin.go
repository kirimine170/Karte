//go:build darwin && universal

package asr

import "fmt"

func unsupportedOfflinePlatformError(operation string) error {
	switch operation {
	case "transcribe":
		return fmt.Errorf("ASR transcription is not supported on Universal Binary or Intel Mac builds")
	case "samples":
		return fmt.Errorf("ASR process samples is not supported on Universal Binary or Intel Mac builds")
	default:
		return fmt.Errorf("ASR is not supported on Universal Binary or Intel Mac builds")
	}
}

func unsupportedRealtimePlatformError() error {
	return fmt.Errorf("Realtime ASR is not supported on Universal Binary or Intel Mac builds")
}
