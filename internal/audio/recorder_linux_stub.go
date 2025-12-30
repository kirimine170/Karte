//go:build linux

package audio

import "fmt"

const (
	RecordingSampleRate = 16000
	RecordingFrameSize  = 160
)

// Recorder is a stub implementation for Linux tests.
type Recorder struct{}

func NewRecorder() (*Recorder, error) {
	return nil, fmt.Errorf("audio recording is not supported on Linux test build")
}

func (r *Recorder) Start(callback func(samples []float32)) error {
	return fmt.Errorf("audio recording is not supported on Linux test build")
}

func (r *Recorder) Stop() error { return nil }

func (r *Recorder) Close() error { return nil }

func (r *Recorder) GetSamples() []float32 { return nil }

func (r *Recorder) GetDuration() float64 { return 0 }

func (r *Recorder) IsRunning() bool { return false }
