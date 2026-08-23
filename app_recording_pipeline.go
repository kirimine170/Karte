package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"karte/internal/asr"
	"karte/internal/audio"
)

const (
	recordingProcessingFrameSlots = 64
	recordingSegmentPoolSlots     = 4
	recordingMaxSegmentSamples    = audio.RecordingSampleRate * 15
	recordingMinSegmentSamples    = audio.RecordingSampleRate / 10
)

var (
	errRecordingCaptureOverflow = errors.New("recording capture buffer overflowed")
	errRecordingWAVWrite        = errors.New("incremental WAV write failed")
)

type appRecordingWAVWriter interface {
	WriteSamples([]float32) error
	Finalize() (string, error)
	Abort() error
	SampleCount() uint64
	TargetPath() string
	TempPath() string
}

type recordingSequence struct {
	value atomic.Uint64
}

func (sequence *recordingSequence) Reset() {
	sequence.value.Store(0)
}

func (sequence *recordingSequence) Next() uint64 {
	return sequence.value.Add(1)
}

type appRecordingPipeline struct {
	wav            appRecordingWAVWriter
	audioRelPath   string
	transcriptPath string
	transcript     *transcriptBuffer
	processing     *audio.RecordingFrameRing
	stopOnce       sync.Once

	realtime       appRealtimeASRService
	offline        appASRService
	vad            *audio.SimpleVAD
	segmentPool    chan []float32
	segment        []float32
	segmentStart   uint64
	processedUntil uint64
	segmentPoolGap bool

	capturedSamples atomic.Uint64
	captureFailed   atomic.Bool
	captureErrMu    sync.Mutex
	captureErr      error
	captureStatsMu  sync.Mutex
	captureStats    audio.RecorderStats
	asrGapCount     atomic.Uint64
}

func newAppRecordingPipeline(
	wav appRecordingWAVWriter,
	audioRelPath string,
	transcriptPath string,
	vad *audio.SimpleVAD,
	realtime appRealtimeASRService,
	offline appASRService,
	transcripts ...*transcriptBuffer,
) *appRecordingPipeline {
	pipeline := &appRecordingPipeline{
		wav:            wav,
		audioRelPath:   audioRelPath,
		transcriptPath: transcriptPath,
		processing:     audio.NewRecordingFrameRing(recordingProcessingFrameSlots, audio.RecordingFrameSize),
		realtime:       realtime,
		offline:        offline,
		vad:            vad,
		segmentPool:    make(chan []float32, recordingSegmentPoolSlots),
	}
	if len(transcripts) > 0 {
		pipeline.transcript = transcripts[0]
	}
	for range recordingSegmentPoolSlots {
		pipeline.segmentPool <- make([]float32, 0, recordingMaxSegmentSamples)
	}
	return pipeline
}

// capture is the priority recording consumer．The native callback has already
// performed its single bounded copy before this function runs．Every accepted
// frame is written to WAV before ASR/VAD admission，so slow recognizers can only
// create an observable transcription gap and cannot delay persisted audio．
func (pipeline *appRecordingPipeline) capture(app *App, samples []float32) {
	if pipeline == nil || len(samples) == 0 {
		return
	}
	if !pipeline.captureFailed.Load() {
		if err := pipeline.wav.WriteSamples(samples); err != nil {
			pipeline.setCaptureError(fmt.Errorf("%w: %w", errRecordingWAVWrite, err))
		}
	}
	offset := pipeline.capturedSamples.Add(uint64(len(samples))) - uint64(len(samples))
	pipeline.processing.TrySubmitAt(samples, offset)
}

func (pipeline *appRecordingPipeline) setCaptureError(err error) {
	if pipeline == nil || err == nil {
		return
	}
	if pipeline.captureFailed.CompareAndSwap(false, true) {
		pipeline.captureErrMu.Lock()
		pipeline.captureErr = err
		pipeline.captureErrMu.Unlock()
	}
}

func (pipeline *appRecordingPipeline) setCaptureStats(stats audio.RecorderStats) {
	if pipeline == nil {
		return
	}
	pipeline.captureStatsMu.Lock()
	if stats.AcceptedFrames >= pipeline.captureStats.AcceptedFrames || stats.DroppedFrames >= pipeline.captureStats.DroppedFrames {
		pipeline.captureStats = stats
	}
	pipeline.captureStatsMu.Unlock()
}

func (pipeline *appRecordingPipeline) integrityError() error {
	if pipeline == nil {
		return errors.New("recording pipeline is unavailable")
	}
	pipeline.captureErrMu.Lock()
	writeErr := pipeline.captureErr
	pipeline.captureErrMu.Unlock()
	pipeline.captureStatsMu.Lock()
	stats := pipeline.captureStats
	pipeline.captureStatsMu.Unlock()
	if stats.DroppedFrames > 0 {
		return errors.Join(writeErr, fmt.Errorf(
			"%w: frames=%d samples=%d oversized=%d",
			errRecordingCaptureOverflow,
			stats.DroppedFrames,
			stats.DroppedSamples,
			stats.OversizedFrames,
		))
	}
	return writeErr
}

func (pipeline *appRecordingPipeline) stopProcessing() {
	if pipeline == nil || pipeline.processing == nil {
		return
	}
	pipeline.stopOnce.Do(func() {
		pipeline.processing.StopAccepting()
	})
}

func (pipeline *appRecordingPipeline) run(app *App) {
	if pipeline == nil || pipeline.processing == nil {
		return
	}
	pipeline.processing.RunWithOffsets(func(samples []float32, offset uint64) {
		pipeline.processFrame(app, samples, offset)
	}, nil)
	if pipeline.processedUntil < pipeline.capturedSamples.Load() {
		pipeline.resetForProcessingGap(app, pipeline.processedUntil, pipeline.capturedSamples.Load())
	}
	pipeline.finishActiveSegment(app)
}

func (pipeline *appRecordingPipeline) processFrame(app *App, samples []float32, offset uint64) {
	if offset != pipeline.processedUntil {
		pipeline.resetForProcessingGap(app, pipeline.processedUntil, offset)
	}
	pipeline.processedUntil = offset + uint64(len(samples))
	// Input level telemetry is deliberately downstream of the lossy processing
	// ring．JobManager contention or UI delivery can never delay WAV persistence．
	app.enqueueRecordingInputLevel(calculateRMS(samples))

	if pipeline.realtime != nil {
		partialText, finalText, isFinal := pipeline.realtime.ProcessAudio(samples)
		if partialText != "" && pipeline.transcript != nil {
			timestamp := float64(offset) / float64(audio.RecordingSampleRate)
			_ = pipeline.transcript.UpdatePartial(partialText, timestamp)
		}
		if isFinal && strings.TrimSpace(finalText) != "" {
			app.publishRecordingTranscriptFinal(pipeline.transcript, pipeline.transcriptPath, finalText, offset)
		}
		return
	}

	if pipeline.vad == nil {
		return
	}
	isSpeech, flush := pipeline.vad.Process(samples)
	if isSpeech {
		pipeline.appendSpeechFrame(app, samples, offset)
	}
	if flush {
		pipeline.detachActiveSegment(app)
		pipeline.vad.Reset()
	}
}

func (pipeline *appRecordingPipeline) appendSpeechFrame(app *App, samples []float32, offset uint64) {
	for len(samples) > 0 {
		if pipeline.segment == nil {
			select {
			case pipeline.segment = <-pipeline.segmentPool:
				pipeline.segment = pipeline.segment[:0]
				pipeline.segmentStart = offset
				pipeline.segmentPoolGap = false
			default:
				if !pipeline.segmentPoolGap {
					pipeline.resetForSegmentPoolPressure(app, offset, uint64(len(samples)))
					pipeline.segmentPoolGap = true
				}
				return
			}
		}
		available := cap(pipeline.segment) - len(pipeline.segment)
		if available > len(samples) {
			available = len(samples)
		}
		pipeline.segment = append(pipeline.segment, samples[:available]...)
		samples = samples[available:]
		offset += uint64(available)
		if len(pipeline.segment) == cap(pipeline.segment) {
			pipeline.detachActiveSegment(app)
			pipeline.vad.Reset()
		}
	}
}

func (pipeline *appRecordingPipeline) detachActiveSegment(app *App) {
	if pipeline.segment == nil {
		return
	}
	samples := pipeline.segment
	start := pipeline.segmentStart
	pipeline.segment = nil
	pipeline.segmentStart = 0
	if len(samples) < recordingMinSegmentSamples {
		pipeline.releaseSegment(samples)
		return
	}
	app.startRecordingFinalizerOwned(pipeline, int(start), samples)
}

func (pipeline *appRecordingPipeline) finishActiveSegment(app *App) {
	pipeline.detachActiveSegment(app)
}

func (pipeline *appRecordingPipeline) releaseSegment(samples []float32) {
	if pipeline == nil || samples == nil {
		return
	}
	samples = samples[:0]
	select {
	case pipeline.segmentPool <- samples:
	default:
		// The pool was prefilled and each buffer has a single owner．A full pool
		// here means an exact-once ownership bug，so do not block a job callback．
	}
}

func (pipeline *appRecordingPipeline) resetForProcessingGap(app *App, expected, actual uint64) {
	if pipeline == nil || actual <= expected {
		return
	}
	if pipeline.segment != nil {
		pipeline.releaseSegment(pipeline.segment)
		pipeline.segment = nil
	}
	if pipeline.vad != nil {
		pipeline.vad.Reset()
	}
	if pipeline.realtime != nil {
		pipeline.realtime.Reset()
	}
	pipeline.asrGapCount.Add(1)
	message := fmt.Sprintf("Recording ASR processing gap: start_sample=%d end_sample=%d dropped_samples=%d", expected, actual, actual-expected)
	app.logError(message)
	app.emitEvent("recording-asr-gap", map[string]interface{}{
		"startSample":    expected,
		"endSample":      actual,
		"droppedSamples": actual - expected,
		"error":          message,
	})
}

func (pipeline *appRecordingPipeline) resetForSegmentPoolPressure(app *App, offset, sampleCount uint64) {
	if pipeline.vad != nil {
		pipeline.vad.Reset()
	}
	pipeline.asrGapCount.Add(1)
	message := fmt.Sprintf("Recording ASR segment pool exhausted: start_sample=%d samples=%d", offset, sampleCount)
	app.logError(message)
	app.emitEvent("recording-asr-gap", map[string]interface{}{
		"startSample": offset,
		"sampleCount": sampleCount,
		"error":       message,
	})
}

func (app *App) publishRecordingTranscriptFinal(buffer *transcriptBuffer, transcriptPath, text string, sampleOffset uint64) {
	if strings.TrimSpace(text) == "" {
		return
	}
	timestamp := float64(sampleOffset) / float64(audio.RecordingSampleRate)
	emit := func() {
		segmentIndex := app.recordingSequence.Next()
		app.emitTranscriptEvent("recording-transcript-final", map[string]interface{}{
			"text":           text,
			"segmentIndex":   segmentIndex,
			"timestamp":      timestamp,
			"transcriptPath": transcriptPath,
		})
	}
	if buffer != nil {
		minutes := int(timestamp) / 60
		seconds := int(timestamp) % 60
		if err := buffer.AppendFinalAndEmit(fmt.Sprintf("**%02d:%02d** %s", minutes, seconds, text), emit); err != nil {
			return
		}
		return
	}
	emit()
}

type recordingIdentity struct {
	transcriptRel string
	transcriptAbs string
	audioRel      string
	audioAbs      string
}

func (app *App) nextRecordingIdentity(now time.Time) (recordingIdentity, error) {
	baseName := now.Format("20060102-150405") + "_recording"
	transcriptDirectory := filepath.ToSlash(filepath.Join("content", "transcripts"))
	audioDirectory := filepath.Join(app.dataDir, "data", "audio")
	if err := os.MkdirAll(audioDirectory, 0o755); err != nil {
		return recordingIdentity{}, fmt.Errorf("prepare recording audio directory: %w", err)
	}
	for suffix := 1; suffix <= 10_000; suffix++ {
		name := baseName
		if suffix > 1 {
			name = fmt.Sprintf("%s-%d", baseName, suffix)
		}
		transcriptRel := filepath.ToSlash(filepath.Join(transcriptDirectory, name+".md"))
		transcriptAbs, ok := app.resolveContentPath(transcriptRel)
		if !ok {
			return recordingIdentity{}, fmt.Errorf("invalid transcript path: %s", transcriptRel)
		}
		audioRel := filepath.ToSlash(filepath.Join("data", "audio", name+".wav"))
		audioAbs := filepath.Join(audioDirectory, name+".wav")
		transcriptFree, err := app.recordingPathAvailable(transcriptAbs)
		if err != nil {
			return recordingIdentity{}, err
		}
		audioFree, err := app.recordingPathAvailable(audioAbs)
		if err != nil {
			return recordingIdentity{}, err
		}
		if transcriptFree && audioFree {
			return recordingIdentity{
				transcriptRel: transcriptRel,
				transcriptAbs: transcriptAbs,
				audioRel:      audioRel,
				audioAbs:      audioAbs,
			}, nil
		}
	}
	return recordingIdentity{}, fmt.Errorf("recording identity space exhausted for %s", baseName)
}

func (app *App) recordingPathAvailable(path string) (bool, error) {
	lstat := os.Lstat
	if app.recordingLstat != nil {
		lstat = app.recordingLstat
	}
	_, err := lstat(path)
	switch {
	case err == nil:
		return false, nil
	case errors.Is(err, os.ErrNotExist):
		return true, nil
	default:
		return false, fmt.Errorf("inspect recording identity %q: %w", path, err)
	}
}

func (app *App) newIncrementalRecordingWriter(target string) (appRecordingWAVWriter, error) {
	if app.recordingNewWAVWriter != nil {
		return app.recordingNewWAVWriter(target, audio.RecordingSampleRate)
	}
	return audio.NewIncrementalWAVWriter(target, audio.RecordingSampleRate)
}

func (app *App) recordingTimeNow() time.Time {
	if app.recordingNow != nil {
		return app.recordingNow()
	}
	return time.Now()
}

func (app *App) reportRecordingIntegrityError(err error) {
	if err == nil {
		return
	}
	message := "Recording was not published because audio integrity could not be guaranteed: " + err.Error()
	app.logError(message)
	app.emitTranscriptEvent("recording-error", map[string]interface{}{
		"error": message,
	})
}

func (app *App) reportRecordingTranscriptError(err error) {
	if err == nil {
		return
	}
	message := "Recording audio was preserved，but the transcript could not be completed: " + err.Error()
	app.logError(message)
	app.emitTranscriptEvent("recording-error", map[string]interface{}{
		"error": message,
	})
}

func (app *App) startRecordingProcessor(pipeline *appRecordingPipeline) bool {
	app.recordingWg.Add(1)
	if !app.lifecycle.goWorker(func(context.Context) {
		defer app.recordingWg.Done()
		pipeline.run(app)
	}) {
		app.recordingWg.Done()
		return false
	}
	return true
}

// StartRecording acquires the ASR session lease，creates the final WAV identity
// up front，and starts the two bounded consumers before native capture begins．
func (app *App) StartRecording() error {
	app.logInfo("[Recording] StartRecording called")
	app.recordingControlMu.Lock()
	defer app.recordingControlMu.Unlock()
	if app.lifecycle.isClosing() {
		return fmt.Errorf("application is shutting down")
	}
	app.recordingMu.Lock()
	alreadyRecording := app.isRecording
	app.recordingMu.Unlock()
	if alreadyRecording {
		return fmt.Errorf("recording already in progress")
	}

	lease, err := app.acquireASRResource(app.lifecycle.context())
	if err != nil {
		if errors.Is(err, errASRResourceDisabled) {
			return fmt.Errorf("ASR not enabled")
		}
		return fmt.Errorf("ASR service unavailable: %w", err)
	}
	cfg := lease.Config()
	if cfg == nil || !cfg.Enabled {
		lease.Release()
		return fmt.Errorf("ASR not enabled")
	}

	realtimeService := app.tryCreateRecordingRealtime(cfg)
	recorder, err := app.newRecordingRecorderInstance()
	if err != nil {
		closeRecordingASRResources(realtimeService, lease)
		return fmt.Errorf("failed to create recorder: %w", err)
	}
	vad := app.newRecordingVADInstance()
	if vad == nil {
		_ = recorder.Close()
		closeRecordingASRResources(realtimeService, lease)
		return fmt.Errorf("failed to initialize voice activity detector")
	}

	var identity recordingIdentity
	var transcriptBuffer *transcriptBuffer
	for range 10_000 {
		identity, err = app.nextRecordingIdentity(app.recordingTimeNow())
		if err != nil {
			_ = recorder.Close()
			closeRecordingASRResources(realtimeService, lease)
			return err
		}
		if err := os.MkdirAll(filepath.Dir(identity.transcriptAbs), 0o755); err != nil {
			_ = recorder.Close()
			closeRecordingASRResources(realtimeService, lease)
			return fmt.Errorf("prepare transcript directory: %w", err)
		}
		body := app.composeTranscriptMarkdown(identity.audioRel, "")
		transcriptBuffer, err = app.createTranscriptDocumentAndBuffer(
			identity.transcriptRel,
			body,
			func(payload transcriptPartialPayload) {
				app.emitTranscriptEvent("recording-transcript-partial", map[string]interface{}{
					"text":           payload.Text,
					"timestamp":      payload.Timestamp,
					"transcriptPath": payload.TranscriptPath,
				})
			},
			app.reportRecordingTranscriptError,
		)
		if errors.Is(err, errTranscriptPathExists) {
			continue
		}
		if err != nil {
			_ = recorder.Close()
			closeRecordingASRResources(realtimeService, lease)
			return fmt.Errorf("create recording transcript buffer: %w", err)
		}
		break
	}
	if transcriptBuffer == nil {
		_ = recorder.Close()
		closeRecordingASRResources(realtimeService, lease)
		return fmt.Errorf("recording transcript identity space exhausted")
	}
	wavWriter, err := app.newIncrementalRecordingWriter(identity.audioAbs)
	if err != nil {
		_ = transcriptBuffer.Abort()
		_ = recorder.Close()
		closeRecordingASRResources(realtimeService, lease)
		return fmt.Errorf("start incremental WAV: %w", err)
	}
	abortBeforeStart := func(pipeline *appRecordingPipeline) {
		_ = recorder.Close()
		if pipeline != nil {
			pipeline.stopProcessing()
			app.drainRecordingWork()
		}
		if transcriptBuffer != nil {
			_ = transcriptBuffer.Abort()
		}
		_ = wavWriter.Abort()
		closeRecordingASRResources(realtimeService, lease)
	}

	pipeline := newAppRecordingPipeline(
		wavWriter,
		identity.audioRel,
		identity.transcriptRel,
		vad,
		realtimeService,
		lease.Service(),
		transcriptBuffer,
	)
	app.recordingSequence.Reset()
	if overflowRecorder, ok := recorder.(interface {
		SetOverflowHandler(func(audio.RecorderStats))
	}); ok {
		overflowRecorder.SetOverflowHandler(pipeline.setCaptureStats)
	}
	if !app.startRecordingProcessor(pipeline) {
		abortBeforeStart(pipeline)
		return fmt.Errorf("application is shutting down")
	}
	if err := recorder.Start(func(samples []float32) {
		pipeline.capture(app, samples)
	}); err != nil {
		abortBeforeStart(pipeline)
		return fmt.Errorf("failed to start recording: %w", err)
	}
	if app.lifecycle.isClosing() {
		abortBeforeStart(pipeline)
		return fmt.Errorf("application is shutting down")
	}

	app.recordingMu.Lock()
	app.recorder = recorder
	app.realtimeService = realtimeService
	app.recordingASRLease = lease
	app.recordingPipeline = pipeline
	app.recordingTranscriptPath = identity.transcriptRel
	app.recordingStopCh = make(chan struct{})
	app.recordingStopOnce = sync.Once{}
	app.isRecording = true
	app.recordingMu.Unlock()
	app.emitEvent("recording-started", map[string]interface{}{
		"transcriptPath": identity.transcriptRel,
		"audioPath":      identity.audioRel,
	})
	return nil
}

func (app *App) tryCreateRecordingRealtime(cfg *asr.Config) appRealtimeASRService {
	if cfg == nil || !recordingConfigIsStreaming(cfg) {
		return nil
	}
	logger := func(format string, args ...interface{}) {
		app.logInfo(fmt.Sprintf("[Recording] [RealtimeASR] "+format, args...))
	}
	var service appRealtimeASRService
	var serviceErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				serviceErr = fmt.Errorf("panic in NewRealtimeService: %v", recovered)
			}
		}()
		service, serviceErr = app.newRecordingRealtimeService(cfg, logger)
	}()
	if serviceErr != nil {
		app.logError(fmt.Sprintf("[Recording] Realtime ASR unavailable，using offline ASR: %v", serviceErr))
		if service != nil {
			service.Close()
		}
		return nil
	}
	return service
}

func recordingConfigIsStreaming(cfg *asr.Config) bool {
	if cfg == nil {
		return false
	}
	for _, path := range []string{cfg.Model.Encoder, cfg.Model.Decoder} {
		lower := strings.ToLower(path)
		if strings.Contains(lower, "chunk") || strings.Contains(lower, "left") || strings.Contains(lower, "right") || strings.Contains(lower, "streaming") {
			return true
		}
	}
	return false
}

func (app *App) startRecordingFinalizer(startSampleIndex int, samples []float32) {
	app.recordingMu.Lock()
	pipeline := app.recordingPipeline
	app.recordingMu.Unlock()
	app.startRecordingFinalizerOwned(pipeline, startSampleIndex, samples)
}

func (app *App) startRecordingFinalizerOwned(pipeline *appRecordingPipeline, startSampleIndex int, samples []float32) {
	if len(samples) == 0 {
		return
	}
	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			if pipeline != nil {
				pipeline.releaseSegment(samples)
			}
		})
	}
	app.recordingWg.Add(1)
	manager := app.recordingJobManager()
	if manager == nil {
		app.recordingWg.Done()
		release()
		app.reportRecordingFinalizerRejection(startSampleIndex, len(samples), errJobManagerClosed)
		return
	}
	transcriptPath := ""
	var transcriptBuffer *transcriptBuffer
	realtimeActive := false
	var offlineService appASRService
	if pipeline != nil {
		transcriptPath = pipeline.transcriptPath
		transcriptBuffer = pipeline.transcript
		realtimeActive = pipeline.realtime != nil
		offlineService = pipeline.offline
	} else {
		app.recordingMu.Lock()
		transcriptPath = app.recordingTranscriptPath
		realtimeActive = app.realtimeService != nil
		if app.recordingASRLease != nil {
			offlineService = app.recordingASRLease.Service()
		}
		app.recordingMu.Unlock()
	}
	group := "recording:" + transcriptPath
	key := fmt.Sprintf("%s:%012d", group, startSampleIndex)
	submission := manager.Submit(managedJob{
		Category: appJobCategoryASRHeavy,
		Group:    group,
		Key:      key,
		Priority: jobPriorityCritical,
		Coalesce: jobKeepExisting,
		Run: func(ctx context.Context) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if app.recordingFinalizeRun != nil {
				app.recordingFinalizeRun(ctx, startSampleIndex, samples)
			} else {
				app.finalizeRecordingSegment(ctx, offlineService, realtimeActive, transcriptBuffer, transcriptPath, startSampleIndex, samples)
			}
			return ctx.Err()
		},
		OnFinish: func(error) {
			release()
			app.recordingWg.Done()
		},
	})
	switch submission.Status {
	case jobAccepted, jobReplacedPending:
		return
	case jobDeduplicated:
		app.recordingWg.Done()
		release()
	case jobRejectedFull:
		app.recordingWg.Done()
		release()
		app.reportRecordingFinalizerRejection(startSampleIndex, len(samples), errJobQueueFull)
	case jobRejectedClosed, jobRejectedCanceled:
		app.recordingWg.Done()
		release()
		app.reportRecordingFinalizerRejection(startSampleIndex, len(samples), errJobManagerClosed)
	default:
		app.recordingWg.Done()
		release()
		app.reportRecordingFinalizerRejection(startSampleIndex, len(samples), submission.Err)
	}
}

func (app *App) reportRecordingFinalizerRejection(startSampleIndex, sampleCount int, err error) {
	message := fmt.Sprintf(
		"Recording segment transcription was not queued: start_sample=%d samples=%d error=%v",
		startSampleIndex,
		sampleCount,
		err,
	)
	app.logError(message)
	app.emitEvent("recording-segment-error", map[string]interface{}{
		"startSample": startSampleIndex,
		"sampleCount": sampleCount,
		"error":       message,
	})
}

func (app *App) finalizeRecordingSegment(
	ctx context.Context,
	service appASRService,
	realtimeActive bool,
	buffer *transcriptBuffer,
	transcriptPath string,
	startSampleIndex int,
	samples []float32,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			app.logError(fmt.Sprintf("[Recording] Panic in finalizeRecordingSegment: %v", recovered))
		}
	}()
	if realtimeActive || service == nil || len(samples) < recordingMinSegmentSamples {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return
	}
	text, err := service.ProcessSamples(samples)
	if err != nil {
		app.logError(fmt.Sprintf("[Recording] ASR processing failed: %v", err))
		return
	}
	if ctx.Err() == nil {
		app.publishRecordingTranscriptFinal(buffer, transcriptPath, text, uint64(startSampleIndex))
	}
}

// StopRecording first closes native capture，then drains the WAV consumer，the
// processing ring，and every accepted finalizer before recognizer ownership or
// transcript paths are released．
func (app *App) StopRecording() (string, error) {
	app.logInfo("[Recording] StopRecording called")
	app.recordingControlMu.Lock()
	defer app.recordingControlMu.Unlock()
	return app.finishRecordingSession(true)
}

// finishRecordingSession is shared by the explicit Stop API and graceful app
// shutdown．The caller owns recordingControlMu，which makes publication，Flush，
// realtime Close，and offline lease Release exact-once under Stop/shutdown races．
func (app *App) finishRecordingSession(emitStoppedEvent bool) (resultAudioPath string, resultErr error) {
	stoppedTranscriptPath := ""
	var stoppedASRGapCount uint64
	defer func() {
		if !emitStoppedEvent {
			return
		}
		payload := map[string]interface{}{
			"audioPath":      resultAudioPath,
			"transcriptPath": stoppedTranscriptPath,
			"asrGapCount":    stoppedASRGapCount,
		}
		if resultErr != nil {
			payload["error"] = resultErr.Error()
		}
		app.emitTranscriptEvent("recording-stopped", payload)
	}()

	app.recordingMu.Lock()
	if !app.isRecording {
		app.recordingMu.Unlock()
		return "", fmt.Errorf("not recording")
	}
	recorder := app.recorder
	pipeline := app.recordingPipeline
	realtimeService := app.realtimeService
	stoppedTranscriptPath = app.recordingTranscriptPath
	app.recordingMu.Unlock()
	if pipeline != nil {
		stoppedTranscriptPath = pipeline.transcriptPath
		stoppedASRGapCount = pipeline.asrGapCount.Load()
	}

	var recorderErr error
	if recorder != nil {
		recorderErr = recorder.Stop()
		if statsRecorder, ok := recorder.(interface{ Stats() audio.RecorderStats }); ok && pipeline != nil {
			pipeline.setCaptureStats(statsRecorder.Stats())
		}
	}
	app.signalRecordingStop()
	app.drainRecordingWork()

	if realtimeService != nil {
		finalText := realtimeService.Flush()
		if strings.TrimSpace(finalText) != "" {
			sampleOffset := uint64(0)
			transcriptPath := ""
			var transcriptBuffer *transcriptBuffer
			if pipeline != nil {
				sampleOffset = pipeline.capturedSamples.Load()
				transcriptPath = pipeline.transcriptPath
				transcriptBuffer = pipeline.transcript
			}
			app.publishRecordingTranscriptFinal(transcriptBuffer, transcriptPath, finalText, sampleOffset)
		}
	}

	if pipeline == nil {
		integrityErr := errors.Join(recorderErr, errors.New("no audio recorded"))
		app.reportRecordingIntegrityError(integrityErr)
		app.cleanupRecording()
		return "", integrityErr
	}
	var transcriptErr error
	if pipeline.transcript != nil {
		transcriptErr = pipeline.transcript.Close()
		pipeline.transcript.drainEvents()
	}
	publishTranscript := func() error {
		if transcriptErr != nil {
			return nil
		}
		return app.publishTranscriptBuffer(pipeline.transcript)
	}
	integrityErr := errors.Join(recorderErr, pipeline.integrityError())
	if pipeline.wav.SampleCount() == 0 {
		integrityErr = errors.Join(integrityErr, errors.New("no audio recorded"))
	}
	if integrityErr != nil {
		integrityErr = errors.Join(integrityErr, pipeline.wav.Abort())
		derivedErr := publishTranscript()
		app.reportRecordingIntegrityError(integrityErr)
		if derivedErr != nil {
			app.reportRecordingTranscriptError(derivedErr)
		}
		app.cleanupRecording()
		return "", errors.Join(integrityErr, transcriptErr, derivedErr)
	}
	if _, err := pipeline.wav.Finalize(); err != nil {
		publishErr := errors.Join(fmt.Errorf("finalize recording WAV: %w", err), pipeline.wav.Abort())
		derivedErr := publishTranscript()
		app.reportRecordingIntegrityError(publishErr)
		if derivedErr != nil {
			app.reportRecordingTranscriptError(derivedErr)
		}
		app.cleanupRecording()
		return "", errors.Join(publishErr, transcriptErr, derivedErr)
	}

	relAudioPath := pipeline.audioRelPath
	derivedErr := publishTranscript()
	if derivedErr != nil {
		app.reportRecordingTranscriptError(derivedErr)
	}
	app.cleanupRecording()
	return relAudioPath, errors.Join(transcriptErr, derivedErr)
}

func (app *App) drainRecordingWork() {
	app.recordingWg.Wait()
}

func (app *App) signalRecordingStop() chan struct{} {
	app.recordingMu.Lock()
	stopChannel := app.recordingStopCh
	pipeline := app.recordingPipeline
	if stopChannel != nil {
		app.recordingStopOnce.Do(func() {
			close(stopChannel)
		})
	}
	app.recordingMu.Unlock()
	if pipeline != nil {
		pipeline.stopProcessing()
	}
	return stopChannel
}
