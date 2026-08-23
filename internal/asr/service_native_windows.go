//go:build windows && cgo

package asr

import (
	"errors"
	"fmt"

	sherpa "github.com/k2-fsa/sherpa-onnx-go-windows"
)

type windowsOfflineRecognizer struct {
	recognizer *sherpa.OfflineRecognizer
}

type windowsOfflineStream struct {
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
	return &windowsOfflineRecognizer{recognizer: recognizer}, nil
}

func (adapter *windowsOfflineRecognizer) NewStream() (offlineStreamAdapter, error) {
	stream := sherpa.NewOfflineStream(adapter.recognizer)
	if stream == nil {
		return nil, errOfflineStreamUnavailable
	}
	return &windowsOfflineStream{stream: stream}, nil
}

func (adapter *windowsOfflineRecognizer) Decode(stream offlineStreamAdapter) (string, error) {
	nativeStream, ok := stream.(*windowsOfflineStream)
	if !ok || nativeStream.stream == nil {
		return "", errors.New("invalid windows offline stream")
	}
	adapter.recognizer.Decode(nativeStream.stream)
	return nativeStream.stream.GetResult().Text, nil
}

func (adapter *windowsOfflineRecognizer) Close() {
	if adapter.recognizer != nil {
		sherpa.DeleteOfflineRecognizer(adapter.recognizer)
		adapter.recognizer = nil
	}
}

func (adapter *windowsOfflineStream) AcceptWaveform(sampleRate int, samples []float32) error {
	if adapter.stream == nil {
		return errors.New("windows offline stream is closed")
	}
	adapter.stream.AcceptWaveform(sampleRate, samples)
	return nil
}

func (adapter *windowsOfflineStream) Close() {
	if adapter.stream != nil {
		sherpa.DeleteOfflineStream(adapter.stream)
		adapter.stream = nil
	}
}

var _ offlineRecognizerAdapter = (*windowsOfflineRecognizer)(nil)
var _ offlineStreamAdapter = (*windowsOfflineStream)(nil)
