package asr

import (
	"context"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type offlineServiceAPI interface {
	Close()
	CountSegments(context.Context, string) (int, error)
	TranscribeFile(context.Context, string, func(string, int, int, float64)) (string, error)
	ProcessSamples([]float32) (string, error)
}

type realtimeServiceAPI interface {
	Close()
	ProcessAudio([]float32) (string, string, bool)
	Flush() string
	Reset()
}

var _ offlineServiceAPI = (*Service)(nil)
var _ realtimeServiceAPI = (*RealtimeService)(nil)

type offlineCoreTestRecognizer struct {
	stream     *offlineCoreTestStream
	closeCount int
}

type offlineCoreTestStream struct {
	samples    []float32
	sampleRate int
	closed     bool
}

func (recognizer *offlineCoreTestRecognizer) NewStream() (offlineStreamAdapter, error) {
	recognizer.stream = &offlineCoreTestStream{}
	return recognizer.stream, nil
}

func (recognizer *offlineCoreTestRecognizer) Decode(offlineStreamAdapter) (string, error) {
	return "  shared result  ", nil
}

func (recognizer *offlineCoreTestRecognizer) Close() {
	recognizer.closeCount++
}

func (stream *offlineCoreTestStream) AcceptWaveform(sampleRate int, samples []float32) error {
	stream.sampleRate = sampleRate
	stream.samples = append(stream.samples, samples...)
	return nil
}

func (stream *offlineCoreTestStream) Close() {
	stream.closed = true
}

func TestOfflineServiceCoreOwnsStreamAndRecognizerLifecycle(t *testing.T) {
	if err := offlinePlatformError("samples"); err != nil {
		t.Skip("offline recognizer is unavailable on this build")
	}
	recognizer := &offlineCoreTestRecognizer{}
	service := &Service{
		cfg:        &Config{SampleRate: 16_000},
		recognizer: recognizer,
	}
	text, err := service.ProcessSamples([]float32{0.25, -0.5})
	if err != nil {
		t.Fatal(err)
	}
	if text != "shared result" {
		t.Fatalf("ProcessSamples result = %q", text)
	}
	if recognizer.stream == nil || recognizer.stream.sampleRate != 16_000 || !recognizer.stream.closed {
		t.Fatalf("offline stream lifecycle = %#v", recognizer.stream)
	}
	service.Close()
	service.Close()
	if recognizer.closeCount != 1 {
		t.Fatalf("recognizer close count = %d", recognizer.closeCount)
	}
}

type onlineCoreTestState struct {
	operations []string
	ready      int
	result     string
	endpoint   bool
	resets     int
}

type onlineCoreTestRecognizer struct {
	state *onlineCoreTestState
}

type onlineCoreTestStream struct {
	state *onlineCoreTestState
}

func (recognizer *onlineCoreTestRecognizer) NewStream() (onlineStreamAdapter, error) {
	return &onlineCoreTestStream{state: recognizer.state}, nil
}

func (recognizer *onlineCoreTestRecognizer) IsReady(onlineStreamAdapter) bool {
	return recognizer.state.ready > 0
}

func (recognizer *onlineCoreTestRecognizer) Decode(onlineStreamAdapter) {
	recognizer.state.ready--
	recognizer.state.operations = append(recognizer.state.operations, "decode")
}

func (recognizer *onlineCoreTestRecognizer) Result(onlineStreamAdapter) string {
	return recognizer.state.result
}

func (recognizer *onlineCoreTestRecognizer) IsEndpoint(onlineStreamAdapter) bool {
	return recognizer.state.endpoint
}

func (recognizer *onlineCoreTestRecognizer) Reset(onlineStreamAdapter) {
	recognizer.state.resets++
	recognizer.state.operations = append(recognizer.state.operations, "reset")
}

func (recognizer *onlineCoreTestRecognizer) Close() {
	recognizer.state.operations = append(recognizer.state.operations, "close-recognizer")
}

func (stream *onlineCoreTestStream) AcceptWaveform(int, []float32) {
	stream.state.operations = append(stream.state.operations, "accept")
}

func (stream *onlineCoreTestStream) InputFinished() {
	stream.state.operations = append(stream.state.operations, "input-finished")
}

func (stream *onlineCoreTestStream) Close() {
	stream.state.operations = append(stream.state.operations, "close-stream")
}

func TestRealtimeServiceCoreOwnsDecodeEndpointFlushAndCloseOrder(t *testing.T) {
	if err := realtimePlatformError(); err != nil {
		t.Skip("realtime recognizer is unavailable on this build")
	}
	state := &onlineCoreTestState{ready: 2, result: "partial", endpoint: true}
	recognizer := &onlineCoreTestRecognizer{state: state}
	stream := &onlineCoreTestStream{state: state}
	service := &RealtimeService{recognizer: recognizer, stream: stream, sampleRate: 16_000}
	partial, final, isFinal := service.ProcessAudio([]float32{0.1})
	if partial != "partial" || final != "partial" || !isFinal {
		t.Fatalf("ProcessAudio = %q, %q, %v", partial, final, isFinal)
	}
	state.ready = 1
	state.result = "tail"
	if final := service.Flush(); final != "tail" {
		t.Fatalf("Flush = %q", final)
	}
	service.Close()
	want := "accept,decode,decode,reset,input-finished,decode,reset,close-stream,close-recognizer"
	if got := strings.Join(state.operations, ","); got != want {
		t.Fatalf("realtime operation order = %s", got)
	}
}

func TestRecognizerCoreConcurrentCloseAndProcessIsRaceFree(t *testing.T) {
	offlineRecognizer := &offlineCoreTestRecognizer{}
	offline := &Service{cfg: &Config{SampleRate: 16_000}, recognizer: offlineRecognizer}
	onlineState := &onlineCoreTestState{result: "partial"}
	online := &RealtimeService{
		recognizer: &onlineCoreTestRecognizer{state: onlineState},
		stream:     &onlineCoreTestStream{state: onlineState},
		sampleRate: 16_000,
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(2)
		go func() {
			defer workers.Done()
			_, _ = offline.ProcessSamples([]float32{0.1})
		}()
		go func() {
			defer workers.Done()
			online.ProcessAudio([]float32{0.1})
		}()
	}
	offline.Close()
	online.Close()
	workers.Wait()
}

func TestSharedRecognizerCoreDoesNotImportNativeSherpaTypes(t *testing.T) {
	for _, path := range []string{"config.go", "service.go", "realtime.go", "transcription.go"} {
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imported := range file.Imports {
			importPath, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(importPath, "sherpa-onnx") {
				t.Fatalf("native sherpa import leaked into shared core %s", path)
			}
		}
	}
}
