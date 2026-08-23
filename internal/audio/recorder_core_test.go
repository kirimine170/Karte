package audio

import (
	"errors"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

type recorderPublicAPI interface {
	Start(func([]float32)) error
	Stop() error
	Close() error
	GetDuration() float64
	SetOverflowHandler(func(RecorderStats))
	Stats() RecorderStats
	IsRunning() bool
}

var _ recorderPublicAPI = (*Recorder)(nil)

type recorderCoreTestStream struct {
	operations *[]string
	callback   func([]float32)
	startErr   error
	stopErr    error
	closeErr   error
	stopStart  chan struct{}
	stopGate   <-chan struct{}
	stopOnce   sync.Once
}

func (stream *recorderCoreTestStream) Start() error {
	*stream.operations = append(*stream.operations, "start")
	if stream.startErr == nil && stream.callback != nil {
		stream.callback([]float32{0.25, -0.5, 0.75})
	}
	return stream.startErr
}

func (stream *recorderCoreTestStream) Stop() error {
	*stream.operations = append(*stream.operations, "stop")
	if stream.stopStart != nil {
		stream.stopOnce.Do(func() { close(stream.stopStart) })
	}
	if stream.stopGate != nil {
		<-stream.stopGate
	}
	return stream.stopErr
}

func (stream *recorderCoreTestStream) Close() error {
	*stream.operations = append(*stream.operations, "close")
	return stream.closeErr
}

func newRecorderCoreTestHarness(stream *recorderCoreTestStream) *Recorder {
	return &Recorder{openStream: func(callback func([]float32)) (recordingStreamAdapter, error) {
		stream.callback = callback
		return stream, nil
	}}
}

func TestRecorderCoreStartsDrainsAndClosesPlatformStream(t *testing.T) {
	if err := recordingPlatformError("start"); err != nil {
		t.Skip("native recorder is unavailable on this build")
	}
	operations := []string{}
	stream := &recorderCoreTestStream{operations: &operations}
	recorder := newRecorderCoreTestHarness(stream)
	consumed := make(chan []float32, 1)
	if err := recorder.Start(func(samples []float32) {
		consumed <- append([]float32(nil), samples...)
	}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Stop(); err != nil {
		t.Fatal(err)
	}
	if got := <-consumed; !reflect.DeepEqual(got, []float32{0.25, -0.5, 0.75}) {
		t.Fatalf("consumed samples = %v", got)
	}
	if !reflect.DeepEqual(operations, []string{"start", "stop", "close"}) {
		t.Fatalf("platform stream operations = %v", operations)
	}
	stats := recorder.Stats()
	if stats.AcceptedSamples != 3 {
		t.Fatalf("recorder stats = %#v", stats)
	}
	if recorder.IsRunning() {
		t.Fatal("recorder remained running after Stop")
	}
}

func TestRecorderCoreStartFailureClosesStreamAndDrainsConsumer(t *testing.T) {
	if err := recordingPlatformError("start"); err != nil {
		t.Skip("native recorder is unavailable on this build")
	}
	operations := []string{}
	wantErr := errors.New("start failed")
	stream := &recorderCoreTestStream{operations: &operations, startErr: wantErr}
	recorder := newRecorderCoreTestHarness(stream)
	if err := recorder.Start(func([]float32) {}); !errors.Is(err, wantErr) {
		t.Fatalf("Start error = %v", err)
	}
	if !reflect.DeepEqual(operations, []string{"start", "close"}) {
		t.Fatalf("failed start operations = %v", operations)
	}
	if recorder.frameRing.Load() != nil || recorder.IsRunning() {
		t.Fatal("failed Start retained active recorder state")
	}
}

func TestRecorderCoreConcurrentStopRunsPlatformStopOnce(t *testing.T) {
	if err := recordingPlatformError("start"); err != nil {
		t.Skip("native recorder is unavailable on this build")
	}
	operations := []string{}
	stopStarted := make(chan struct{})
	allowStop := make(chan struct{})
	stream := &recorderCoreTestStream{
		operations: &operations,
		stopStart:  stopStarted,
		stopGate:   allowStop,
	}
	recorder := newRecorderCoreTestHarness(stream)
	if err := recorder.Start(func([]float32) {}); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() { results <- recorder.Stop() }()
	<-stopStarted
	go func() { results <- recorder.Stop() }()
	close(allowStop)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(operations, ","); got != "start,stop,close" {
		t.Fatalf("concurrent Stop operations = %s", got)
	}
}

func TestSharedRecorderCoreDoesNotImportPortAudio(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "recorder.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	for _, imported := range file.Imports {
		path, err := strconv.Unquote(imported.Path.Value)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(path, "portaudio") {
			t.Fatalf("PortAudio binding leaked into recorder core: %s", path)
		}
	}
}
