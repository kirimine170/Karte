//go:build darwin && !universal

package asr

import (
	"errors"
	"fmt"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"
)

type darwinOfflineRecognizer struct {
	recognizer *sherpa.OfflineRecognizer
}

type darwinOfflineStream struct {
	stream *sherpa.OfflineStream
}

func offlinePlatformError(string) error {
	return nil
}

func newOfflineRecognizerAdapter(config *Config) (offlineRecognizerAdapter, error) {
	recognizer := sherpa.NewOfflineRecognizer(config.offlineRecognizerConfig())
	if recognizer == nil {
		return nil, fmt.Errorf("failed to initialize offline recognizer")
	}
	return &darwinOfflineRecognizer{recognizer: recognizer}, nil
}

func (adapter *darwinOfflineRecognizer) NewStream() (offlineStreamAdapter, error) {
	stream := sherpa.NewOfflineStream(adapter.recognizer)
	if stream == nil {
		return nil, errOfflineStreamUnavailable
	}
	return &darwinOfflineStream{stream: stream}, nil
}

func (adapter *darwinOfflineRecognizer) Decode(stream offlineStreamAdapter) (string, error) {
	nativeStream, ok := stream.(*darwinOfflineStream)
	if !ok || nativeStream.stream == nil {
		return "", errors.New("invalid darwin offline stream")
	}
	adapter.recognizer.Decode(nativeStream.stream)
	return nativeStream.stream.GetResult().Text, nil
}

func (adapter *darwinOfflineRecognizer) Close() {
	if adapter.recognizer != nil {
		sherpa.DeleteOfflineRecognizer(adapter.recognizer)
		adapter.recognizer = nil
	}
}

func (adapter *darwinOfflineStream) AcceptWaveform(sampleRate int, samples []float32) error {
	if adapter.stream == nil {
		return errors.New("darwin offline stream is closed")
	}
	adapter.stream.AcceptWaveform(sampleRate, samples)
	return nil
}

func (adapter *darwinOfflineStream) Close() {
	if adapter.stream != nil {
		sherpa.DeleteOfflineStream(adapter.stream)
		adapter.stream = nil
	}
}

var _ offlineRecognizerAdapter = (*darwinOfflineRecognizer)(nil)
var _ offlineStreamAdapter = (*darwinOfflineStream)(nil)
