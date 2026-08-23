package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"karte/internal/asr"
	"karte/internal/audio"
)

type fakeAppRealtimeASRService struct {
	closeCount   atomic.Int32
	flushCount   atomic.Int32
	flushStart   chan struct{}
	flushGate    <-chan struct{}
	flushOnce    sync.Once
	flushResult  string
	processCount atomic.Int32
	processStart chan struct{}
	processGate  <-chan struct{}
	processOnce  sync.Once
	resetCount   atomic.Int32
	onClose      func()
}

func (service *fakeAppRealtimeASRService) Close() {
	service.closeCount.Add(1)
	if service.onClose != nil {
		service.onClose()
	}
}

func (service *fakeAppRealtimeASRService) ProcessAudio([]float32) (string, string, bool) {
	service.processCount.Add(1)
	if service.processStart != nil {
		service.processOnce.Do(func() { close(service.processStart) })
	}
	if service.processGate != nil {
		<-service.processGate
	}
	return "", "", false
}

func (service *fakeAppRealtimeASRService) Flush() string {
	service.flushCount.Add(1)
	if service.flushStart != nil {
		service.flushOnce.Do(func() { close(service.flushStart) })
	}
	if service.flushGate != nil {
		<-service.flushGate
	}
	return service.flushResult
}

func (service *fakeAppRealtimeASRService) Reset() {
	service.resetCount.Add(1)
}

type fakeAppAudioRecorder struct {
	startErr   error
	startHook  func()
	startCount atomic.Int32
	stopCount  atomic.Int32
	closeCount atomic.Int32
}

func (recorder *fakeAppAudioRecorder) Start(func([]float32)) error {
	recorder.startCount.Add(1)
	if recorder.startHook != nil {
		recorder.startHook()
	}
	return recorder.startErr
}

func (recorder *fakeAppAudioRecorder) Stop() error {
	recorder.stopCount.Add(1)
	return nil
}

func (recorder *fakeAppAudioRecorder) Close() error {
	recorder.closeCount.Add(1)
	return nil
}

func newAppASRTestHarness(t *testing.T) (*App, *manualASRIdleClock) {
	t.Helper()
	dataDir := t.TempDir()
	for _, directory := range []string{
		filepath.Join(dataDir, "content", "transcripts"),
		filepath.Join(dataDir, ".mdsys"),
		filepath.Join(dataDir, "data", "asr"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	app := NewAppWithFileSystem(OSFileSystem{})
	app.dataDir = dataDir
	app.root = dataDir
	app.logFilePath = filepath.Join(dataDir, "app.log")
	// These tests exercise ASR ownership only．Suppress unrelated site and UI
	// queue creation at SaveFile mutation boundaries．
	app.jobs.closing = true
	clock := &manualASRIdleClock{}
	app.asrResource.clock = clock
	t.Cleanup(func() {
		manager := app.closeASRAdmission()
		if manager != nil {
			shutdownASRResourceManager(t, manager)
		}
		app.lifecycle.beginShutdown()
	})
	return app, clock
}

func testASRConfig(streaming bool) *asr.Config {
	encoder := "offline-encoder.onnx"
	if streaming {
		encoder = "streaming-encoder-chunk-16.onnx"
	}
	return &asr.Config{
		Enabled:    true,
		SampleRate: 16000,
		Model: asr.ModelSpec{
			Tokens:  "tokens.txt",
			Encoder: encoder,
			Decoder: "decoder.onnx",
			Joiner:  "joiner.onnx",
		},
		Runtime: asr.RuntimeSpec{
			Threads:            2,
			Provider:           "cpu",
			IdleTimeoutSeconds: 300,
		},
	}
}

func TestAppASRStartupStateIsLazyAndStatusTracksSingleFlight(t *testing.T) {
	app, _ := newAppASRTestHarness(t)
	loadStarted := make(chan struct{})
	allowLoad := make(chan struct{})
	service := &fakeAppASRService{}
	var loadCount atomic.Int32
	app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
		loadCount.Add(1)
		close(loadStarted)
		<-allowLoad
		return asrResourceLoadResult{
			Service:     service,
			Config:      testASRConfig(false),
			IdleTimeout: time.Minute,
		}, nil
	}

	app.resetASRResourceManager()
	app.getASRResourceManager()
	if got := loadCount.Load(); got != 0 {
		t.Fatalf("startup manager creation loaded ASR %d times，want 0", got)
	}
	if status := app.GetASRStatus(); status.Initialized || status.Initializing {
		t.Fatalf("startup status = %+v，want unloaded and idle", status)
	}

	leaseResult := make(chan *asrResourceLease, 1)
	errorResult := make(chan error, 1)
	go func() {
		lease, err := app.acquireASRResource(context.Background())
		leaseResult <- lease
		errorResult <- err
	}()
	<-loadStarted
	if status := app.GetASRStatus(); status.Initialized || !status.Initializing {
		t.Fatalf("loading status = %+v，want initializing", status)
	}
	close(allowLoad)
	lease := <-leaseResult
	if err := <-errorResult; err != nil {
		t.Fatal(err)
	}
	if status := app.GetASRStatus(); !status.Initialized || status.Initializing {
		t.Fatalf("loaded status = %+v", status)
	}
	lease.Release()
}

func TestAppASRDefaultLoaderPropagatesRuntimeBudget(t *testing.T) {
	app, clock := newAppASRTestHarness(t)
	configPath := filepath.Join(app.dataDir, "data", "asr", "config.json")
	configJSON := `{
		"enabled": true,
		"sampleRate": 16000,
		"model": {
			"tokens": "models/tokens.txt",
			"encoder": "models/encoder.onnx",
			"decoder": "models/decoder.onnx",
			"joiner": "models/joiner.onnx"
		},
		"runtime": {
			"threads": 7,
			"provider": "cpu",
			"idleTimeoutSeconds": 23
		}
	}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &fakeAppASRService{}
	var captured *asr.Config
	app.asrResource.newService = func(config *asr.Config) (appASRService, error) {
		captured = config
		return service, nil
	}

	lease, err := app.acquireASRResource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if captured == nil {
		t.Fatal("ASR service factory did not receive config")
	}
	if captured.Runtime.Threads != 7 || captured.Runtime.IdleTimeoutSeconds != 23 {
		t.Fatalf("runtime budget was not propagated: %+v", captured.Runtime)
	}
	if !filepath.IsAbs(captured.Model.Encoder) || !filepath.IsAbs(captured.Model.Tokens) {
		t.Fatalf("model paths were not resolved before service creation: %+v", captured.Model)
	}
	lease.Release()
	if clock.Count() != 2 || clock.Delay(1) != 23*time.Second {
		t.Fatalf("configured idle timeout was not propagated: timers=%d delay=%v", clock.Count(), clock.Delay(1))
	}
}

func TestAudioImportHoldsASRLeaseUntilTranscriptionCompletes(t *testing.T) {
	app, clock := newAppASRTestHarness(t)
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
	jobManager := app.getJobManager()
	if jobManager == nil {
		t.Fatal("failed to start job manager")
	}
	t.Cleanup(func() {
		jobManager.Close()
		waitContext, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if !jobManager.Shutdown(waitContext) {
			t.Errorf("job manager did not shut down: %+v", jobManager.Stats())
		}
	})

	transcribeStarted := make(chan struct{})
	allowTranscribe := make(chan struct{})
	service := &fakeAppASRService{
		transcribeStart:  transcribeStarted,
		transcribeGate:   allowTranscribe,
		transcribeResult: "import transcript",
	}
	app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: service, Config: testASRConfig(false), IdleTimeout: time.Minute}, nil
	}

	app.startTranscriptionJob(filepath.Join(app.dataDir, "data", "audio", "memo.m4a"), "data/audio/memo.m4a")
	receiveJobSignal(t, transcribeStarted, "ASR import transcription")
	manager := app.currentASRResourceManager()
	if status := manager.Status(); status.Active != 1 || !status.Loaded {
		t.Fatalf("import did not hold ASR lease while TranscribeFile ran: %+v", status)
	}
	if clock.Fire(0) {
		t.Fatal("idle unload timer fired while import lease was active")
	}
	close(allowTranscribe)
	waitForASRResourceStatus(t, manager, func(status asrResourceStatus) bool {
		return status.Active == 0 && status.Loaded && status.IdleArmed
	})
	waitForManagedJobs(t, jobManager, 0, 0)
	if got := service.closeCount.Load(); got != 0 {
		t.Fatalf("import service unloaded before idle timeout: %d", got)
	}
}

func TestStartRecordingFailurePathsReleaseSessionLeaseExactlyOnce(t *testing.T) {
	tests := []struct {
		name              string
		streaming         bool
		configure         func(*App, *fakeAppAudioRecorder)
		wantRecorderStart int32
		wantRecorderClose int32
	}{
		{
			name:      "realtime and recorder initialization fail",
			streaming: true,
			configure: func(app *App, _ *fakeAppAudioRecorder) {
				app.recordingNewRealtime = func(*asr.Config, asr.LogFunc) (appRealtimeASRService, error) {
					return nil, errors.New("realtime init failed")
				}
				app.recordingNewRecorder = func() (appAudioRecorder, error) {
					return nil, errors.New("recorder init failed")
				}
			},
		},
		{
			name: "VAD initialization fails",
			configure: func(app *App, recorder *fakeAppAudioRecorder) {
				app.recordingNewRecorder = func() (appAudioRecorder, error) { return recorder, nil }
				app.recordingNewVAD = func() *audio.SimpleVAD { return nil }
			},
			wantRecorderClose: 1,
		},
		{
			name: "recorder start fails",
			configure: func(app *App, recorder *fakeAppAudioRecorder) {
				recorder.startErr = errors.New("recorder start failed")
				app.recordingNewRecorder = func() (appAudioRecorder, error) { return recorder, nil }
			},
			wantRecorderStart: 1,
			wantRecorderClose: 1,
		},
		{
			name: "lifecycle closes after recorder starts",
			configure: func(app *App, recorder *fakeAppAudioRecorder) {
				recorder.startHook = func() { app.lifecycle.beginShutdown() }
				app.recordingNewRecorder = func() (appAudioRecorder, error) { return recorder, nil }
			},
			wantRecorderStart: 1,
			wantRecorderClose: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, _ := newAppASRTestHarness(t)
			service := &fakeAppASRService{}
			app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
				return asrResourceLoadResult{
					Service:     service,
					Config:      testASRConfig(test.streaming),
					IdleTimeout: time.Minute,
				}, nil
			}
			recorder := &fakeAppAudioRecorder{}
			test.configure(app, recorder)

			if err := app.StartRecording(); err == nil {
				t.Fatal("StartRecording unexpectedly succeeded")
			}
			manager := app.currentASRResourceManager()
			status := manager.Status()
			if status.Active != 0 {
				t.Fatalf("failed StartRecording leaked %d active leases: %+v", status.Active, status)
			}
			app.recordingMu.Lock()
			storedLease := app.recordingASRLease
			storedRealtime := app.realtimeService
			app.recordingMu.Unlock()
			if storedLease != nil || storedRealtime != nil {
				t.Fatalf("failed StartRecording published resources: lease=%v realtime=%v", storedLease != nil, storedRealtime != nil)
			}
			if got := recorder.startCount.Load(); got != test.wantRecorderStart {
				t.Fatalf("recorder Start count = %d，want %d", got, test.wantRecorderStart)
			}
			if got := recorder.closeCount.Load(); got != test.wantRecorderClose {
				t.Fatalf("recorder Close count = %d，want %d", got, test.wantRecorderClose)
			}
			shutdownASRResourceManager(t, manager)
			if got := service.closeCount.Load(); got != 1 {
				t.Fatalf("offline service close count = %d，want 1", got)
			}
		})
	}
}

func TestStopRecordingAndShutdownRaceSerializesFlushAndReleasesOnce(t *testing.T) {
	app, _ := newAppASRTestHarness(t)
	var orderMu sync.Mutex
	var order []string
	offline := &fakeAppASRService{}
	offlineWithOrder := &orderedAppASRService{fakeAppASRService: offline, onClose: func() {
		orderMu.Lock()
		order = append(order, "offline")
		orderMu.Unlock()
	}}
	app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: offlineWithOrder, Config: testASRConfig(false), IdleTimeout: time.Minute}, nil
	}
	lease, err := app.acquireASRResource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	manager := app.currentASRResourceManager()
	flushStarted := make(chan struct{})
	allowFlush := make(chan struct{})
	realtime := &fakeAppRealtimeASRService{
		flushStart: flushStarted,
		flushGate:  allowFlush,
		onClose: func() {
			orderMu.Lock()
			order = append(order, "realtime")
			orderMu.Unlock()
		}}
	recorder := &fakeAppAudioRecorder{}
	app.recordingMu.Lock()
	app.recordingASRLease = lease
	app.realtimeService = realtime
	app.recorder = recorder
	app.isRecording = true
	app.recordingMu.Unlock()

	stopDone := make(chan error, 1)
	go func() {
		_, stopErr := app.StopRecording()
		stopDone <- stopErr
	}()
	receiveJobSignal(t, flushStarted, "StopRecording realtime Flush")

	shutdownDone := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown passed a running realtime Flush")
	case <-time.After(20 * time.Millisecond):
	}
	if got := realtime.closeCount.Load(); got != 0 {
		t.Fatalf("realtime closed %d times while Flush was running", got)
	}
	close(allowFlush)
	select {
	case stopErr := <-stopDone:
		if stopErr == nil {
			t.Fatal("StopRecording without samples unexpectedly succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("StopRecording did not finish after Flush")
	}
	receiveJobSignal(t, shutdownDone, "shutdown after StopRecording")

	if got := realtime.flushCount.Load(); got != 1 {
		t.Fatalf("realtime Flush count = %d，want 1", got)
	}
	if got := realtime.closeCount.Load(); got != 1 {
		t.Fatalf("realtime close count = %d，want 1", got)
	}
	if got := offline.closeCount.Load(); got != 1 {
		t.Fatalf("offline close count = %d，want 1", got)
	}
	orderMu.Lock()
	gotOrder := append([]string(nil), order...)
	orderMu.Unlock()
	if !reflect.DeepEqual(gotOrder, []string{"realtime", "offline"}) {
		t.Fatalf("native close order = %v，want [realtime offline]", gotOrder)
	}
	if status := manager.Status(); status.Active != 0 || status.Loaded {
		t.Fatalf("StopRecording/shutdown race left ASR owned: %+v", status)
	}
}

func TestShutdownWaitsForRecordingProcessorBeforeRealtimeClose(t *testing.T) {
	app, _ := newAppASRTestHarness(t)
	offline := &fakeAppASRService{}
	app.asrResource.loader = func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: offline, Config: testASRConfig(false), IdleTimeout: time.Minute}, nil
	}
	lease, err := app.acquireASRResource(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	processStarted := make(chan struct{})
	allowProcess := make(chan struct{})
	realtime := &fakeAppRealtimeASRService{
		processStart: processStarted,
		processGate:  allowProcess,
	}
	app.recordingMu.Lock()
	app.recordingASRLease = lease
	app.realtimeService = realtime
	app.recordingStopCh = make(chan struct{})
	app.isRecording = true
	app.recordingMu.Unlock()
	app.recordingWg.Add(1)
	go func() {
		defer app.recordingWg.Done()
		realtime.ProcessAudio(make([]float32, 160))
	}()
	receiveJobSignal(t, processStarted, "recording ProcessAudio")

	shutdownDone := make(chan struct{})
	go func() {
		app.shutdown(context.Background())
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
		t.Fatal("shutdown passed a running recording processor")
	case <-time.After(20 * time.Millisecond):
	}
	if got := realtime.closeCount.Load(); got != 0 {
		t.Fatalf("realtime close count during ProcessAudio = %d，want 0", got)
	}
	close(allowProcess)
	receiveJobSignal(t, shutdownDone, "shutdown after ProcessAudio")
	if got := realtime.processCount.Load(); got != 1 {
		t.Fatalf("ProcessAudio count = %d，want 1", got)
	}
	if got := realtime.closeCount.Load(); got != 1 {
		t.Fatalf("realtime close count = %d，want 1", got)
	}
	if got := offline.closeCount.Load(); got != 1 {
		t.Fatalf("offline close count = %d，want 1", got)
	}
}

type orderedAppASRService struct {
	*fakeAppASRService
	onClose func()
}

func (service *orderedAppASRService) Close() {
	service.fakeAppASRService.Close()
	if service.onClose != nil {
		service.onClose()
	}
}
