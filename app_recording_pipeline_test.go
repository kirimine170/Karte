package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"karte/internal/asr"
	"karte/internal/audio"
)

type recordingPipelineTestWriter struct {
	mu sync.Mutex

	target        string
	temp          string
	samples       uint64
	writeErr      error
	finalizeErr   error
	writeCount    int
	finalizeCount int
	abortCount    int
	published     bool
	publishBytes  bool
	finalizeStart chan struct{}
	finalizeGate  <-chan struct{}
}

type recordingPipelineWriterSnapshot struct {
	target        string
	samples       uint64
	writeCount    int
	finalizeCount int
	abortCount    int
	published     bool
}

func captureRecordingStoppedEvents(app *App) func() []map[string]interface{} {
	var mu sync.Mutex
	var payloads []map[string]interface{}
	app.transcripts.emit = func(_ *App, name string, data interface{}) {
		if name != "recording-stopped" {
			return
		}
		payload, _ := data.(map[string]interface{})
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()
	}
	return func() []map[string]interface{} {
		mu.Lock()
		defer mu.Unlock()
		return append([]map[string]interface{}(nil), payloads...)
	}
}

func newRecordingPipelineTestWriter(target string) *recordingPipelineTestWriter {
	return &recordingPipelineTestWriter{target: target, temp: target + ".tmp"}
}

func (writer *recordingPipelineTestWriter) WriteSamples(samples []float32) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.writeCount++
	if writer.writeErr != nil {
		return writer.writeErr
	}
	writer.samples += uint64(len(samples))
	return nil
}

func (writer *recordingPipelineTestWriter) Finalize() (string, error) {
	writer.mu.Lock()
	writer.finalizeCount++
	start := writer.finalizeStart
	gate := writer.finalizeGate
	writer.mu.Unlock()
	if start != nil {
		close(start)
	}
	if gate != nil {
		<-gate
	}
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.finalizeErr != nil {
		return "", writer.finalizeErr
	}
	if writer.publishBytes {
		if err := os.WriteFile(writer.target, []byte("published WAV"), 0o600); err != nil {
			return "", err
		}
	}
	writer.published = true
	return writer.target, nil
}

func (writer *recordingPipelineTestWriter) Abort() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if !writer.published {
		writer.abortCount++
	}
	return nil
}

func (writer *recordingPipelineTestWriter) SampleCount() uint64 {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.samples
}

func (writer *recordingPipelineTestWriter) TargetPath() string { return writer.target }
func (writer *recordingPipelineTestWriter) TempPath() string   { return writer.temp }

func (writer *recordingPipelineTestWriter) snapshot() recordingPipelineWriterSnapshot {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return recordingPipelineWriterSnapshot{
		target:        writer.target,
		samples:       writer.samples,
		writeCount:    writer.writeCount,
		finalizeCount: writer.finalizeCount,
		abortCount:    writer.abortCount,
		published:     writer.published,
	}
}

type recordingPipelineTestRecorder struct {
	mu sync.Mutex

	callback        func([]float32)
	overflowHandler func(audio.RecorderStats)
	stats           audio.RecorderStats
	startErr        error
	stopErr         error
	startCount      int
	stopCount       int
	closeCount      int
}

func (recorder *recordingPipelineTestRecorder) Start(callback func([]float32)) error {
	recorder.mu.Lock()
	recorder.startCount++
	recorder.callback = callback
	err := recorder.startErr
	recorder.mu.Unlock()
	return err
}

func (recorder *recordingPipelineTestRecorder) Emit(samples []float32) {
	recorder.mu.Lock()
	callback := recorder.callback
	recorder.mu.Unlock()
	if callback != nil {
		callback(samples)
	}
}

func (recorder *recordingPipelineTestRecorder) Stop() error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.stopCount++
	return recorder.stopErr
}

func (recorder *recordingPipelineTestRecorder) Close() error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.closeCount++
	return nil
}

func (recorder *recordingPipelineTestRecorder) SetOverflowHandler(handler func(audio.RecorderStats)) {
	recorder.mu.Lock()
	recorder.overflowHandler = handler
	recorder.mu.Unlock()
}

func (recorder *recordingPipelineTestRecorder) Stats() audio.RecorderStats {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.stats
}

func configureRecordingPipelineHarness(
	t *testing.T,
	streaming bool,
	recorder *recordingPipelineTestRecorder,
	writer *recordingPipelineTestWriter,
) *App {
	t.Helper()
	app, _ := newAppASRTestHarness(t)
	app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{
			Service:     &fakeAppASRService{},
			Config:      testASRConfig(streaming),
			IdleTimeout: time.Minute,
		}, nil
	}
	app.recordingNewRecorder = func() (appAudioRecorder, error) { return recorder, nil }
	app.recordingNewWAVWriter = func(target string, _ int) (appRecordingWAVWriter, error) {
		writer.target = target
		writer.temp = target + ".tmp"
		return writer, nil
	}
	app.recordingNow = func() time.Time {
		return time.Date(2026, 8, 23, 12, 34, 56, 0, time.UTC)
	}
	return app
}

func TestRecordingPipelinePrioritizesWAVWhenRealtimeASRIsBlocked(t *testing.T) {
	recorder := &recordingPipelineTestRecorder{}
	writer := newRecordingPipelineTestWriter("")
	app := configureRecordingPipelineHarness(t, true, recorder, writer)
	processStarted := make(chan struct{})
	allowProcessing := make(chan struct{})
	realtime := &fakeAppRealtimeASRService{
		processStart: processStarted,
		processGate:  allowProcessing,
	}
	app.recordingNewRealtime = func(*asr.Config, asr.LogFunc) (appRealtimeASRService, error) {
		return realtime, nil
	}
	if err := app.StartRecording(); err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, audio.RecordingFrameSize)
	recorder.Emit(frame)
	receiveJobSignal(t, processStarted, "blocked realtime ASR")
	const additionalFrames = recordingProcessingFrameSlots * 4
	for range additionalFrames {
		recorder.Emit(frame)
	}
	if got := writer.SampleCount(); got != uint64((additionalFrames+1)*audio.RecordingFrameSize) {
		t.Fatalf("WAV samples while ASR blocked = %d", got)
	}
	app.recordingMu.Lock()
	processingStats := app.recordingPipeline.processing.Stats()
	app.recordingMu.Unlock()
	if processingStats.DroppedFrames == 0 {
		t.Fatalf("test did not create processing pressure: %+v", processingStats)
	}
	close(allowProcessing)
	audioPath, err := app.StopRecording()
	if err != nil {
		t.Fatalf("complete audio was rejected after ASR-only gap: %v", err)
	}
	if !strings.HasSuffix(audioPath, ".wav") {
		t.Fatalf("audio path = %q，want WAV", audioPath)
	}
	snapshot := writer.snapshot()
	if !snapshot.published || snapshot.finalizeCount != 1 || snapshot.abortCount != 0 {
		t.Fatalf("WAV publication state = %+v", snapshot)
	}
	if realtime.resetCount.Load() == 0 {
		t.Fatal("realtime recognizer was not reset after processing gap")
	}
	logData, err := os.ReadFile(app.logFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "Recording ASR processing gap") {
		t.Fatalf("ASR gap was not observable: %s", logData)
	}
}

func TestRecordingCaptureOverflowAbortsWAVAndReturnsIntegrityError(t *testing.T) {
	recorder := &recordingPipelineTestRecorder{stats: audio.RecorderStats{
		DroppedFrames:   2,
		DroppedSamples:  320,
		OversizedFrames: 1,
	}}
	writer := newRecordingPipelineTestWriter("")
	app := configureRecordingPipelineHarness(t, false, recorder, writer)
	if err := app.StartRecording(); err != nil {
		t.Fatal(err)
	}
	recorder.Emit(make([]float32, audio.RecordingFrameSize))
	path, err := app.StopRecording()
	if path != "" || !errors.Is(err, errRecordingCaptureOverflow) {
		t.Fatalf("StopRecording = %q，%v", path, err)
	}
	snapshot := writer.snapshot()
	if snapshot.finalizeCount != 0 || snapshot.abortCount == 0 || snapshot.published {
		t.Fatalf("overflow WAV state = %+v", snapshot)
	}
}

func TestRecordingWAVWriteFailureDrainsThenAborts(t *testing.T) {
	wantErr := errors.New("disk write failed")
	recorder := &recordingPipelineTestRecorder{}
	writer := newRecordingPipelineTestWriter("")
	writer.writeErr = wantErr
	app := configureRecordingPipelineHarness(t, false, recorder, writer)
	if err := app.StartRecording(); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		recorder.Emit(make([]float32, audio.RecordingFrameSize))
	}
	_, err := app.StopRecording()
	if !errors.Is(err, errRecordingWAVWrite) || !errors.Is(err, wantErr) {
		t.Fatalf("StopRecording error = %v", err)
	}
	snapshot := writer.snapshot()
	if snapshot.writeCount != 1 {
		t.Fatalf("failed writer was retried %d times，want 1", snapshot.writeCount)
	}
	if snapshot.abortCount == 0 || snapshot.finalizeCount != 0 {
		t.Fatalf("write fault WAV state = %+v", snapshot)
	}
}

func TestRecordingTranscriptSyncFaultStillPublishesWAVAndEmitsStoppedOnce(t *testing.T) {
	recorder := &recordingPipelineTestRecorder{}
	writer := newRecordingPipelineTestWriter("")
	app := configureRecordingPipelineHarness(t, true, recorder, writer)
	realtime := &fakeAppRealtimeASRService{flushResult: "confirmed final"}
	app.recordingNewRealtime = func(*asr.Config, asr.LogFunc) (appRealtimeASRService, error) {
		return realtime, nil
	}
	wantErr := errors.New("transcript sync fault")
	transcriptFile := &fakeTranscriptAppendFile{syncErr: wantErr}
	clock := &manualTranscriptClock{}
	hooks := defaultTranscriptBufferHooks()
	hooks.clock = clock
	hooks.open = func(string) (transcriptAppendFile, error) { return transcriptFile, nil }
	app.transcripts.hooks = &hooks
	var eventMu sync.Mutex
	var stoppedPayloads []map[string]interface{}
	app.transcripts.emit = func(_ *App, name string, data interface{}) {
		if name != "recording-stopped" {
			return
		}
		payload, _ := data.(map[string]interface{})
		eventMu.Lock()
		stoppedPayloads = append(stoppedPayloads, payload)
		eventMu.Unlock()
	}

	if err := app.StartRecording(); err != nil {
		t.Fatal(err)
	}
	recorder.Emit(make([]float32, audio.RecordingFrameSize))
	audioPath, err := app.StopRecording()
	if !errors.Is(err, wantErr) {
		t.Fatalf("StopRecording error = %v", err)
	}
	if audioPath == "" {
		t.Fatal("successful WAV path was lost on transcript fault")
	}
	snapshot := writer.snapshot()
	if !snapshot.published || snapshot.finalizeCount != 1 || snapshot.abortCount != 0 {
		t.Fatalf("WAV state after transcript fault = %+v", snapshot)
	}
	eventMu.Lock()
	gotPayloads := append([]map[string]interface{}(nil), stoppedPayloads...)
	eventMu.Unlock()
	if len(gotPayloads) != 1 {
		t.Fatalf("recording-stopped events = %d，want 1", len(gotPayloads))
	}
	if gotPayloads[0]["audioPath"] != audioPath || !strings.Contains(fmt.Sprint(gotPayloads[0]["error"]), wantErr.Error()) {
		t.Fatalf("recording-stopped payload = %#v", gotPayloads[0])
	}
}

func TestRecordingStoppedIsExactOnceAcrossTerminalFaults(t *testing.T) {
	t.Run("missing-pipeline", func(t *testing.T) {
		app := &App{isRecording: true, recordingTranscriptPath: "content/transcripts/missing.md"}
		payloads := captureRecordingStoppedEvents(app)
		if _, err := app.StopRecording(); err == nil {
			t.Fatal("missing pipeline unexpectedly succeeded")
		}
		got := payloads()
		if len(got) != 1 || got[0]["error"] == nil {
			t.Fatalf("missing pipeline stopped payloads = %#v", got)
		}
	})

	t.Run("capture-integrity", func(t *testing.T) {
		recorder := &recordingPipelineTestRecorder{stats: audio.RecorderStats{DroppedFrames: 1, DroppedSamples: 160}}
		writer := newRecordingPipelineTestWriter("")
		app := configureRecordingPipelineHarness(t, false, recorder, writer)
		payloads := captureRecordingStoppedEvents(app)
		if err := app.StartRecording(); err != nil {
			t.Fatal(err)
		}
		recorder.Emit(make([]float32, audio.RecordingFrameSize))
		if _, err := app.StopRecording(); !errors.Is(err, errRecordingCaptureOverflow) {
			t.Fatalf("capture integrity error = %v", err)
		}
		got := payloads()
		if len(got) != 1 || got[0]["error"] == nil {
			t.Fatalf("capture integrity stopped payloads = %#v", got)
		}
	})

	t.Run("wav-finalize", func(t *testing.T) {
		recorder := &recordingPipelineTestRecorder{}
		wantErr := errors.New("WAV finalize failed")
		writer := newRecordingPipelineTestWriter("")
		writer.finalizeErr = wantErr
		app := configureRecordingPipelineHarness(t, false, recorder, writer)
		payloads := captureRecordingStoppedEvents(app)
		if err := app.StartRecording(); err != nil {
			t.Fatal(err)
		}
		recorder.Emit(make([]float32, audio.RecordingFrameSize))
		if _, err := app.StopRecording(); !errors.Is(err, wantErr) {
			t.Fatalf("WAV finalize error = %v", err)
		}
		got := payloads()
		if len(got) != 1 || got[0]["error"] == nil {
			t.Fatalf("WAV finalize stopped payloads = %#v", got)
		}
	})
}

func TestRecordingPipelinePublishesExactVariableFramesAsWAV(t *testing.T) {
	recorder := &recordingPipelineTestRecorder{}
	app, _ := newAppASRTestHarness(t)
	app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: &fakeAppASRService{}, Config: testASRConfig(false), IdleTimeout: time.Minute}, nil
	}
	app.recordingNewRecorder = func() (appAudioRecorder, error) { return recorder, nil }
	app.recordingNow = func() time.Time {
		return time.Date(2026, 8, 23, 12, 45, 0, 0, time.UTC)
	}
	if err := app.StartRecording(); err != nil {
		t.Fatal(err)
	}
	frames := [][]float32{
		make([]float32, 80),
		make([]float32, 160),
		make([]float32, 40),
	}
	frames[0][0] = 1
	frames[2][39] = -1
	for _, frame := range frames {
		recorder.Emit(frame)
	}
	relativePath, err := app.StopRecording()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(app.dataDir, filepath.FromSlash(relativePath)))
	if err != nil {
		t.Fatal(err)
	}
	const samples = 80 + 160 + 40
	if len(data) != 44+samples*2 {
		t.Fatalf("WAV length = %d，want %d", len(data), 44+samples*2)
	}
	if got := binary.LittleEndian.Uint32(data[40:44]); got != samples*2 {
		t.Fatalf("WAV data bytes = %d，want %d", got, samples*2)
	}
	if got := int16(binary.LittleEndian.Uint16(data[44:46])); got != 32767 {
		t.Fatalf("first PCM sample = %d", got)
	}
	if got := int16(binary.LittleEndian.Uint16(data[len(data)-2:])); got != -32767 {
		t.Fatalf("last PCM sample = %d", got)
	}
}

func TestRecordingIdentityUsesOneCollisionFreeWAVAndTranscriptSuffix(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "録音 データ")
	app := NewAppWithFileSystem(OSFileSystem{})
	app.dataDir = dataDir
	app.root = dataDir
	for _, directory := range []string{filepath.Join(dataDir, "content", "transcripts"), filepath.Join(dataDir, "data", "audio")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 8, 23, 12, 34, 56, 0, time.Local)
	baseAudio := filepath.Join(dataDir, "data", "audio", "20260823-123456_recording.wav")
	if err := os.WriteFile(baseAudio, []byte("existing audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := app.nextRecordingIdentity(now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(identity.audioAbs) != "20260823-123456_recording-2.wav" || filepath.Base(identity.transcriptAbs) != "20260823-123456_recording-2.md" {
		t.Fatalf("mismatched collision identity: %+v", identity)
	}
	if err := os.WriteFile(identity.transcriptAbs, []byte("existing transcript"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err = app.nextRecordingIdentity(now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(identity.audioAbs) != "20260823-123456_recording-3.wav" || filepath.Base(identity.transcriptAbs) != "20260823-123456_recording-3.md" {
		t.Fatalf("second collision identity: %+v", identity)
	}
}

func TestRecordingIdentityUnexpectedLstatErrorFailsWithoutSuffixLoop(t *testing.T) {
	app := NewAppWithFileSystem(OSFileSystem{})
	app.dataDir = t.TempDir()
	app.root = app.dataDir
	wantErr := errors.New("permission denied")
	var calls atomic.Int32
	app.recordingLstat = func(string) (os.FileInfo, error) {
		calls.Add(1)
		return nil, wantErr
	}
	_, err := app.nextRecordingIdentity(time.Now())
	if !errors.Is(err, wantErr) {
		t.Fatalf("identity error = %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Lstat calls = %d，want finite first failure", calls.Load())
	}
}

func TestRecordingTranscriptCreateCollisionUsesNextIdentityWithoutOverwrite(t *testing.T) {
	recorder := &recordingPipelineTestRecorder{}
	writer := newRecordingPipelineTestWriter("")
	app := configureRecordingPipelineHarness(t, false, recorder, writer)
	baseRel := "content/transcripts/20260823-123456_recording.md"
	externalBytes := []byte("external recording transcript\n")
	app.transcripts.beforeInstall = func(relPath string) {
		if relPath != baseRel {
			return
		}
		absPath, ok := app.resolveContentPath(relPath)
		if !ok {
			t.Errorf("invalid collision path: %s", relPath)
			return
		}
		if err := os.WriteFile(absPath, externalBytes, 0o600); err != nil {
			t.Errorf("create recording collision: %v", err)
		}
	}
	if err := app.StartRecording(); err != nil {
		t.Fatal(err)
	}
	app.recordingMu.Lock()
	transcriptPath := app.recordingPipeline.transcriptPath
	app.recordingMu.Unlock()
	if transcriptPath != "content/transcripts/20260823-123456_recording-2.md" {
		t.Fatalf("recording collision transcript = %s", transcriptPath)
	}
	baseBytes, err := os.ReadFile(filepath.Join(app.dataDir, filepath.FromSlash(baseRel)))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(baseBytes, externalBytes) {
		t.Fatalf("external recording collision was overwritten: %q", baseBytes)
	}
	recorder.Emit(make([]float32, audio.RecordingFrameSize))
	if _, err := app.StopRecording(); err != nil {
		t.Fatal(err)
	}
}

func TestRecordingStartFailureAbortsTempAndPreservesDiagnosticTranscript(t *testing.T) {
	recorder := &recordingPipelineTestRecorder{startErr: errors.New("microphone unavailable")}
	writer := newRecordingPipelineTestWriter("")
	app := configureRecordingPipelineHarness(t, false, recorder, writer)
	if err := app.StartRecording(); err == nil {
		t.Fatal("StartRecording unexpectedly succeeded")
	}
	snapshot := writer.snapshot()
	if snapshot.abortCount == 0 || snapshot.finalizeCount != 0 {
		t.Fatalf("failed start writer state = %+v", snapshot)
	}
	transcriptPath := filepath.Join(app.dataDir, "content", "transcripts", "20260823-123456_recording.md")
	content, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("diagnostic transcript was not preserved: %v", err)
	}
	// SaveFile may already have committed metadata and doc_map state．Keeping the
	// newly-created empty transcript is safer and retryable than a partial direct
	// rollback of those mutation boundaries．The temporary WAV is always removed．
	if !strings.Contains(string(content), "20260823-123456_recording.wav") {
		t.Fatalf("diagnostic transcript lost planned WAV identity: %s", content)
	}
}

func TestRecordingSegmentPoolTransfersOwnershipWithoutGrowing(t *testing.T) {
	app, _ := newAppJobTestHarness(t, jobManagerConfig{
		Workers:    1,
		MaxPending: recordingSegmentPoolSlots,
		CategoryLimits: map[string]int{
			appJobCategoryASRHeavy: 1,
		},
	})
	pipeline := newAppRecordingPipeline(
		newRecordingPipelineTestWriter(filepath.Join(app.dataDir, "recording.wav")),
		"data/audio/recording.wav",
		"content/transcripts/recording.md",
		audio.NewSimpleVAD(0.01, 1, 1),
		nil,
		nil,
	)
	var pooledPointers []uintptr
	pooled := make([][]float32, 0, recordingSegmentPoolSlots)
	for range recordingSegmentPoolSlots {
		buffer := <-pipeline.segmentPool
		pooled = append(pooled, buffer)
		pooledPointers = append(pooledPointers, uintptr(unsafe.Pointer(&buffer[:cap(buffer)][0])))
	}
	for _, buffer := range pooled {
		pipeline.segmentPool <- buffer
	}
	var finalizedMu sync.Mutex
	var finalizedPointers []uintptr
	app.recordingFinalizeRun = func(_ context.Context, _ int, samples []float32) {
		finalizedMu.Lock()
		finalizedPointers = append(finalizedPointers, uintptr(unsafe.Pointer(&samples[0])))
		finalizedMu.Unlock()
	}
	frame := make([]float32, audio.RecordingFrameSize)
	for range 6_000 {
		pipeline.appendSpeechFrame(app, frame, pipeline.processedUntil)
		pipeline.processedUntil += uint64(len(frame))
	}
	pipeline.finishActiveSegment(app)
	app.drainRecordingWork()
	if len(pipeline.segmentPool) != recordingSegmentPoolSlots {
		t.Fatalf("retained segment buffers = %d，want %d", len(pipeline.segmentPool), recordingSegmentPoolSlots)
	}
	finalizedMu.Lock()
	gotPointers := append([]uintptr(nil), finalizedPointers...)
	finalizedMu.Unlock()
	if len(gotPointers) == 0 {
		t.Fatal("long stream produced no detached segment")
	}
	for _, pointer := range gotPointers {
		found := false
		for _, pooledPointer := range pooledPointers {
			if pointer == pooledPointer {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("finalizer received copied/non-pooled buffer %x", pointer)
		}
	}
	var returnedCaps []int
	for range recordingSegmentPoolSlots {
		buffer := <-pipeline.segmentPool
		returnedCaps = append(returnedCaps, cap(buffer))
	}
	wantCaps := []int{recordingMaxSegmentSamples, recordingMaxSegmentSamples, recordingMaxSegmentSamples, recordingMaxSegmentSamples}
	if !reflect.DeepEqual(returnedCaps, wantCaps) {
		t.Fatalf("segment capacities changed: %v", returnedCaps)
	}
}

func TestRecordingSequenceIsAtomicAndUnique(t *testing.T) {
	var sequence recordingSequence
	const workers = 100
	values := make(chan uint64, workers)
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			values <- sequence.Next()
		}()
	}
	group.Wait()
	close(values)
	seen := make(map[uint64]struct{}, workers)
	for value := range values {
		seen[value] = struct{}{}
	}
	if len(seen) != workers {
		t.Fatalf("unique sequence values = %d，want %d", len(seen), workers)
	}
	for value := uint64(1); value <= workers; value++ {
		if _, ok := seen[value]; !ok {
			t.Fatalf("missing sequence value %d", value)
		}
	}
}

func TestShutdownGracefullyPublishesWAVAndFinalOfflineSegmentBeforeClosingManagers(t *testing.T) {
	app, _ := newAppASRTestHarness(t)
	app.jobs.closing = false
	jobConfig := jobManagerConfig{
		Workers:    2,
		MaxPending: 8,
		CategoryLimits: map[string]int{
			appJobCategoryASRHeavy: 1,
			appJobCategorySite:     1,
		},
	}
	app.jobs.config = &jobConfig
	manager := app.getJobManager()
	if manager == nil {
		t.Fatal("job manager was not initialized")
	}
	importStarted := make(chan struct{})
	importCanceled := make(chan struct{})
	importSubmission := manager.Submit(managedJob{
		Category: appJobCategoryASRHeavy,
		Group:    appJobGroupAudioImport,
		Key:      "blocking-import.wav",
		Priority: jobPriorityHigh,
		Coalesce: jobKeepExisting,
		Run: func(ctx context.Context) error {
			close(importStarted)
			<-ctx.Done()
			close(importCanceled)
			return ctx.Err()
		},
	})
	requireJobAdmission(t, importSubmission, jobAccepted)
	receiveJobSignal(t, importStarted, "running audio import")
	processStarted := make(chan struct{})
	offline := &fakeAppASRService{processStart: processStarted, processResult: "shutdown final segment"}
	app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: offline, Config: testASRConfig(false), IdleTimeout: time.Minute}, nil
	}
	recorder := &recordingPipelineTestRecorder{}
	writer := newRecordingPipelineTestWriter("")
	writer.publishBytes = true
	app.recordingNewRecorder = func() (appAudioRecorder, error) { return recorder, nil }
	app.recordingNewVAD = func() *audio.SimpleVAD { return audio.NewSimpleVAD(0.01, 1, 1) }
	app.recordingNewWAVWriter = func(target string, _ int) (appRecordingWAVWriter, error) {
		writer.target = target
		writer.temp = target + ".tmp"
		return writer, nil
	}
	app.recordingNow = func() time.Time {
		return time.Date(2026, 8, 23, 13, 0, 0, 0, time.UTC)
	}
	if err := app.StartRecording(); err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, audio.RecordingFrameSize)
	for index := range frame {
		frame[index] = 0.5
	}
	for range 10 {
		recorder.Emit(frame)
	}
	app.shutdown(context.Background())
	receiveJobSignal(t, importCanceled, "audio import cancellation before recording finalizer")
	receiveJobSignal(t, processStarted, "shutdown offline finalizer")

	snapshot := writer.snapshot()
	if snapshot.finalizeCount != 1 || !snapshot.published || snapshot.abortCount != 0 {
		t.Fatalf("graceful shutdown WAV state = %+v", snapshot)
	}
	if data, err := os.ReadFile(snapshot.target); err != nil || string(data) != "published WAV" {
		t.Fatalf("published shutdown WAV = %q，%v", data, err)
	}
	transcriptPath := filepath.Join(app.dataDir, "content", "transcripts", "20260823-130000_recording.md")
	transcript, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(transcript), "shutdown final segment") {
		t.Fatalf("shutdown transcript did not drain final segment: %s", transcript)
	}
	if offline.closeCount.Load() != 1 {
		t.Fatalf("offline close count = %d，want 1", offline.closeCount.Load())
	}
	if stats := manager.Stats(); !stats.Closing || stats.Pending != 0 || stats.Running != 0 {
		t.Fatalf("manager was not closed after recording completion: %+v", stats)
	}
}

func TestStopAndShutdownRacePublishesAndReleasesExactlyOnce(t *testing.T) {
	app, _ := newAppASRTestHarness(t)
	offline := &fakeAppASRService{}
	app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: offline, Config: testASRConfig(false), IdleTimeout: time.Minute}, nil
	}
	recorder := &recordingPipelineTestRecorder{}
	writer := newRecordingPipelineTestWriter("")
	finalizeStarted := make(chan struct{})
	allowFinalize := make(chan struct{})
	writer.finalizeStart = finalizeStarted
	writer.finalizeGate = allowFinalize
	app.recordingNewRecorder = func() (appAudioRecorder, error) { return recorder, nil }
	app.recordingNewWAVWriter = func(target string, _ int) (appRecordingWAVWriter, error) {
		writer.target = target
		return writer, nil
	}
	if err := app.StartRecording(); err != nil {
		t.Fatal(err)
	}
	recorder.Emit(make([]float32, audio.RecordingFrameSize))
	stopResult := make(chan error, 1)
	go func() {
		_, err := app.StopRecording()
		stopResult <- err
	}()
	receiveJobSignal(t, finalizeStarted, "blocked WAV finalization")
	shutdownDone := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown crossed the recording publication boundary")
	case <-time.After(20 * time.Millisecond):
	}
	close(allowFinalize)
	if err := <-stopResult; err != nil {
		t.Fatal(err)
	}
	receiveJobSignal(t, shutdownDone, "shutdown after StopRecording")
	snapshot := writer.snapshot()
	if snapshot.finalizeCount != 1 || !snapshot.published {
		t.Fatalf("race WAV publication = %+v", snapshot)
	}
	recorder.mu.Lock()
	stopCount := recorder.stopCount
	recorder.mu.Unlock()
	if stopCount != 1 {
		t.Fatalf("recorder Stop count = %d，want 1", stopCount)
	}
	if offline.closeCount.Load() != 1 {
		t.Fatalf("offline close count = %d，want 1", offline.closeCount.Load())
	}
}
