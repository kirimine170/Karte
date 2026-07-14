//go:build darwin && universal

package audio

import "fmt"

// Universal Binary 向けの簡易 Recorder スタブ
// PortAudio には依存せず、API だけ満たして録音機能は無効化する

const (
	RecordingSampleRate = 16000
	RecordingFrameSize  = 160
)

type Recorder struct{}

func NewRecorder() (*Recorder, error) {
	return nil, fmt.Errorf("audio recording is not supported on Universal Binary builds (PortAudio backend is disabled for universal builds)")
}

func (r *Recorder) Start(callback func(samples []float32)) error {
	return fmt.Errorf("audio recording is not supported on Universal Binary builds")
}

func (r *Recorder) Stop() error {
	return nil
}

func (r *Recorder) Close() error {
	return nil
}

func (r *Recorder) GetSamples() []float32 {
	return nil
}

func (r *Recorder) GetDuration() float64 {
	return 0
}

func (r *Recorder) IsRunning() bool {
	return false
}
