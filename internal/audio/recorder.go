package audio

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

const (
	RecordingSampleRate = 16000
	RecordingFrameSize  = 160
)

var (
	portAudioInitialized bool
	portAudioMu          sync.Mutex
)

type recordingStreamAdapter interface {
	Start() error
	Stop() error
	Close() error
}

// Recorder moves realtime callback frames through a fixed，preallocated ring．
// It never retains the complete recording and Stop drains every accepted frame．
type Recorder struct {
	mu sync.Mutex

	stream          recordingStreamAdapter
	running         bool
	stopping        bool
	stopDone        chan struct{}
	stopErr         error
	lastStats       RecorderStats
	overflowHandler func(RecorderStats)
	consumerWg      sync.WaitGroup
	frameRing       atomic.Pointer[RecordingFrameRing]
	openStream      func(func([]float32)) (recordingStreamAdapter, error)
}

func NewRecorder() (*Recorder, error) {
	if err := recordingPlatformError("new"); err != nil {
		return nil, err
	}
	portAudioMu.Lock()
	defer portAudioMu.Unlock()
	if !portAudioInitialized {
		if err := initializeRecordingPlatform(); err != nil {
			return nil, fmt.Errorf("failed to initialize PortAudio: %w", err)
		}
		portAudioInitialized = true
	}
	return &Recorder{}, nil
}

func (recorder *Recorder) Start(callback func([]float32)) error {
	if err := recordingPlatformError("start"); err != nil {
		return err
	}
	for {
		recorder.mu.Lock()
		if !recorder.stopping {
			break
		}
		done := recorder.stopDone
		recorder.mu.Unlock()
		<-done
	}
	defer recorder.mu.Unlock()
	if recorder.running {
		return fmt.Errorf("recorder already running")
	}

	openStream := recorder.openStream
	if openStream == nil {
		openStream = openRecordingPlatformStream
	}
	stream, err := openStream(func(input []float32) {
		recorder.processAudio(input)
	})
	if err != nil {
		return err
	}

	ring := NewRecordingFrameRing(DefaultRecordingFrameSlots, RecordingFrameSize)
	recorder.lastStats = RecorderStats{}
	recorder.frameRing.Store(ring)
	recorder.stream = stream
	recorder.stopErr = nil
	recorder.consumerWg.Add(1)
	go func(overflowHandler func(RecorderStats)) {
		defer recorder.consumerWg.Done()
		ring.Run(callback, overflowHandler)
	}(recorder.overflowHandler)
	if err := stream.Start(); err != nil {
		ring.StopAccepting()
		recorder.consumerWg.Wait()
		recorder.frameRing.Store(nil)
		recorder.stream = nil
		closeErr := stream.Close()
		return errors.Join(fmt.Errorf("failed to start stream: %w", err), closeErr)
	}
	recorder.running = true
	return nil
}

// processAudio is invoked on PortAudio's realtime callback thread．It performs
// one bounded copy and only nonblocking channel operations．
func (recorder *Recorder) processAudio(input []float32) {
	if ring := recorder.frameRing.Load(); ring != nil {
		ring.TrySubmit(input)
	}
}

func (recorder *Recorder) Stop() error {
	recorder.mu.Lock()
	if recorder.stopping {
		done := recorder.stopDone
		recorder.mu.Unlock()
		<-done
		recorder.mu.Lock()
		err := recorder.stopErr
		recorder.mu.Unlock()
		return err
	}
	if !recorder.running {
		recorder.mu.Unlock()
		return nil
	}
	recorder.running = false
	recorder.stopping = true
	recorder.stopDone = make(chan struct{})
	stream := recorder.stream
	ring := recorder.frameRing.Load()
	done := recorder.stopDone
	recorder.mu.Unlock()

	var stopErr error
	if stream != nil {
		stopErr = errors.Join(stream.Stop(), stream.Close())
	}
	// Native callbacks are quiescent before the admission boundary closes．
	if ring != nil {
		ring.StopAccepting()
	}
	recorder.consumerWg.Wait()
	stats := ring.Stats()

	recorder.mu.Lock()
	recorder.stream = nil
	recorder.lastStats = stats
	recorder.frameRing.Store(nil)
	recorder.stopErr = stopErr
	recorder.stopping = false
	close(done)
	recorder.mu.Unlock()
	return stopErr
}

func (recorder *Recorder) SetOverflowHandler(handler func(RecorderStats)) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.overflowHandler = handler
}

func (recorder *Recorder) Stats() RecorderStats {
	if ring := recorder.frameRing.Load(); ring != nil {
		return ring.Stats()
	}
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.lastStats
}

func (recorder *Recorder) GetDuration() float64 {
	return float64(recorder.Stats().AcceptedSamples) / float64(RecordingSampleRate)
}

func (recorder *Recorder) Close() error {
	return recorder.Stop()
}

func Terminate() error {
	portAudioMu.Lock()
	defer portAudioMu.Unlock()
	if !portAudioInitialized {
		return nil
	}
	if err := terminateRecordingPlatform(); err != nil {
		return fmt.Errorf("failed to terminate PortAudio: %w", err)
	}
	portAudioInitialized = false
	return nil
}

func (recorder *Recorder) IsRunning() bool {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.running
}
