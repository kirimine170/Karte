//go:build (darwin && !universal) || (windows && cgo)

package audio

import (
	"fmt"

	"github.com/gordonklaus/portaudio"
)

func recordingPlatformError(string) error {
	return nil
}

func initializeRecordingPlatform() error {
	return portaudio.Initialize()
}

func openRecordingPlatformStream(callback func([]float32)) (recordingStreamAdapter, error) {
	inputDevice, err := portaudio.DefaultInputDevice()
	if err != nil {
		return nil, fmt.Errorf("failed to get default input device: %w", err)
	}
	parameters := portaudio.StreamParameters{
		Input: portaudio.StreamDeviceParameters{
			Device:   inputDevice,
			Channels: 1,
			Latency:  inputDevice.DefaultLowInputLatency,
		},
		SampleRate:      RecordingSampleRate,
		FramesPerBuffer: RecordingFrameSize,
	}
	stream, err := portaudio.OpenStream(parameters, callback)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}
	return stream, nil
}

func terminateRecordingPlatform() error {
	return portaudio.Terminate()
}

var _ recordingStreamAdapter = (*portaudio.Stream)(nil)
