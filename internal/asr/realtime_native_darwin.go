//go:build darwin && !universal

package asr

import sherpa "github.com/k2-fsa/sherpa-onnx-go-macos"

type darwinOnlineRecognizer struct {
	recognizer *sherpa.OnlineRecognizer
}

type darwinOnlineStream struct {
	stream *sherpa.OnlineStream
}

func realtimePlatformError() error {
	return nil
}

func newOnlineRecognizerAdapter(spec onlineRecognizerSpec) (onlineRecognizerAdapter, error) {
	modelConfig := sherpa.OnlineModelConfig{
		Tokens:     spec.Model.Tokens,
		NumThreads: spec.Runtime.Threads,
		Provider:   spec.Runtime.Provider,
	}
	if spec.Model.ZipformerCTC != "" {
		modelConfig.Zipformer2Ctc = sherpa.OnlineZipformer2CtcModelConfig{Model: spec.Model.ZipformerCTC}
	} else {
		modelConfig.Transducer = sherpa.OnlineTransducerModelConfig{
			Encoder: spec.Model.Encoder,
			Decoder: spec.Model.Decoder,
			Joiner:  spec.Model.Joiner,
		}
		modelConfig.ModelType = ""
	}
	config := &sherpa.OnlineRecognizerConfig{
		FeatConfig:              sherpa.FeatureConfig{SampleRate: spec.SampleRate, FeatureDim: 80},
		ModelConfig:             modelConfig,
		DecodingMethod:          spec.Decoding.Method,
		MaxActivePaths:          4,
		EnableEndpoint:          1,
		Rule1MinTrailingSilence: 0.8,
		Rule2MinTrailingSilence: 1.2,
		Rule3MinUtteranceLength: 0.5,
		BlankPenalty:            0.0,
	}
	recognizer := sherpa.NewOnlineRecognizer(config)
	if recognizer == nil {
		return nil, errOnlineRecognizerUnavailable
	}
	return &darwinOnlineRecognizer{recognizer: recognizer}, nil
}

func (adapter *darwinOnlineRecognizer) NewStream() (onlineStreamAdapter, error) {
	stream := sherpa.NewOnlineStream(adapter.recognizer)
	if stream == nil {
		return nil, errOnlineStreamUnavailable
	}
	return &darwinOnlineStream{stream: stream}, nil
}

func (adapter *darwinOnlineRecognizer) native(stream onlineStreamAdapter) (*sherpa.OnlineStream, bool) {
	nativeStream, ok := stream.(*darwinOnlineStream)
	if !ok || nativeStream.stream == nil {
		return nil, false
	}
	return nativeStream.stream, true
}

func (adapter *darwinOnlineRecognizer) IsReady(stream onlineStreamAdapter) bool {
	nativeStream, ok := adapter.native(stream)
	return ok && adapter.recognizer.IsReady(nativeStream)
}

func (adapter *darwinOnlineRecognizer) Decode(stream onlineStreamAdapter) {
	if nativeStream, ok := adapter.native(stream); ok {
		adapter.recognizer.Decode(nativeStream)
	}
}

func (adapter *darwinOnlineRecognizer) Result(stream onlineStreamAdapter) string {
	if nativeStream, ok := adapter.native(stream); ok {
		return adapter.recognizer.GetResult(nativeStream).Text
	}
	return ""
}

func (adapter *darwinOnlineRecognizer) IsEndpoint(stream onlineStreamAdapter) bool {
	nativeStream, ok := adapter.native(stream)
	return ok && adapter.recognizer.IsEndpoint(nativeStream)
}

func (adapter *darwinOnlineRecognizer) Reset(stream onlineStreamAdapter) {
	if nativeStream, ok := adapter.native(stream); ok {
		adapter.recognizer.Reset(nativeStream)
	}
}

func (adapter *darwinOnlineRecognizer) Close() {
	if adapter.recognizer != nil {
		sherpa.DeleteOnlineRecognizer(adapter.recognizer)
		adapter.recognizer = nil
	}
}

func (adapter *darwinOnlineStream) AcceptWaveform(sampleRate int, samples []float32) {
	adapter.stream.AcceptWaveform(sampleRate, samples)
}

func (adapter *darwinOnlineStream) InputFinished() {
	adapter.stream.InputFinished()
}

func (adapter *darwinOnlineStream) Close() {
	if adapter.stream != nil {
		sherpa.DeleteOnlineStream(adapter.stream)
		adapter.stream = nil
	}
}

var _ onlineRecognizerAdapter = (*darwinOnlineRecognizer)(nil)
var _ onlineStreamAdapter = (*darwinOnlineStream)(nil)
