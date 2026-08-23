//go:build (darwin && universal) || linux || (windows && !cgo)

package audio

func initializeRecordingPlatform() error {
	return recordingPlatformError("new")
}

func openRecordingPlatformStream(func([]float32)) (recordingStreamAdapter, error) {
	return nil, recordingPlatformError("start")
}

func terminateRecordingPlatform() error {
	return nil
}
