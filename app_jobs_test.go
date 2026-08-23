package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordingInputLevelBurstKeepsOnlyLatestPendingJob(t *testing.T) {
	app, manager := newAppJobTestHarness(t, jobManagerConfig{Workers: 1, MaxPending: 4})
	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	blocker := manager.Submit(testManagedJob("block", "block", jobPriorityCritical, func(ctx context.Context) error {
		close(blockerStarted)
		select {
		case <-releaseBlocker:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	requireJobAdmission(t, blocker, jobAccepted)
	receiveJobSignal(t, blockerStarted, "input-level blocker")

	for index := 0; index < 10_000; index++ {
		app.enqueueRecordingInputLevel(float32(index))
	}
	stats := manager.Stats()
	if stats.Pending != 1 || stats.PeakPending != 1 || stats.LiveWorkers != 1 {
		t.Fatalf("input-level burst was not bounded: %+v", stats)
	}
	close(releaseBlocker)
	waitForManagedJobs(t, manager, 0, 0)
}

func TestWebClipQueueDeduplicatesAndLogsRetryableBackpressure(t *testing.T) {
	app, manager := newAppJobTestHarness(t, jobManagerConfig{Workers: 1, MaxPending: 1})
	blockerStarted := make(chan struct{})
	manager.Submit(testManagedJob("block", "block", jobPriorityCritical, func(ctx context.Context) error {
		close(blockerStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	receiveJobSignal(t, blockerStarted, "Web Clip blocker")

	app.enqueueWebClipAssetConversion("content/clips/a.md", "content/clips/assets/a")
	app.enqueueWebClipAssetConversion("content/clips/a.md", "content/clips/assets/a")
	app.enqueueWebClipAssetConversion("content/clips/b.md", "content/clips/assets/b")
	if stats := manager.Stats(); stats.Pending != 1 || stats.PeakPending != 1 {
		t.Fatalf("Web Clip queue was not bounded and deduplicated: %+v", stats)
	}
	logData, err := os.ReadFile(app.logFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "original assets were preserved") ||
		!strings.Contains(string(logData), "retried by importing the clip again") {
		t.Fatalf("retryable Web Clip backpressure was not logged: %s", logData)
	}
}

func TestAudioImportQueueDeduplicatesAndReportsBackpressure(t *testing.T) {
	app, manager := newAppJobTestHarness(t, jobManagerConfig{Workers: 1, MaxPending: 2})
	blockerStarted := make(chan struct{})
	manager.Submit(testManagedJob("block", "block", jobPriorityCritical, func(ctx context.Context) error {
		close(blockerStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	receiveJobSignal(t, blockerStarted, "audio import blocker")

	app.startTranscriptionJob("/tmp/a.m4a", "data/audio/a.m4a")
	app.startTranscriptionJob("/tmp/a.m4a", "data/audio/a.m4a")
	app.startTranscriptionJob("/tmp/b.m4a", "data/audio/b.m4a")
	app.startTranscriptionJob("/tmp/c.m4a", "data/audio/c.m4a")
	if stats := manager.Stats(); stats.Pending != 2 || stats.PeakPending != 2 {
		t.Fatalf("audio import queue was not bounded and deduplicated: %+v", stats)
	}
	logData, err := os.ReadFile(app.logFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "ASR transcription queue is full") ||
		!strings.Contains(string(logData), "retry the audio import") {
		t.Fatalf("audio import backpressure was not reported: %s", logData)
	}
}

func TestRecordingFinalizerBackpressureIsReportedWithoutWaitGroupLeak(t *testing.T) {
	app, manager := newAppJobTestHarness(t, jobManagerConfig{Workers: 1, MaxPending: 1})
	blockerStarted := make(chan struct{})
	manager.Submit(testManagedJob("block", "block", jobPriorityCritical, func(ctx context.Context) error {
		close(blockerStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	receiveJobSignal(t, blockerStarted, "recording finalizer blocker")
	manager.Submit(testManagedJob("pending", "pending", jobPriorityNormal, func(context.Context) error { return nil }))

	app.recordingTranscriptPath = "content/transcripts/recording.md"
	app.startRecordingFinalizer(1600, make([]float32, 1600))
	waitContext, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if !waitForWaitGroup(waitContext, &app.recordingWg) {
		t.Fatal("rejected finalizer leaked its recording wait-group count")
	}
	logData, err := os.ReadFile(app.logFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logData), "Recording segment transcription was not queued") ||
		!strings.Contains(string(logData), "job queue is full") {
		t.Fatalf("recording segment loss was silent: %s", logData)
	}
}

func TestRecordingFinalizerPendingCancellationReleasesRecordingWaitGroup(t *testing.T) {
	app, manager := newAppJobTestHarness(t, jobManagerConfig{Workers: 1, MaxPending: 2})
	blockerStarted := make(chan struct{})
	manager.Submit(testManagedJob("block", "block", jobPriorityCritical, func(ctx context.Context) error {
		close(blockerStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	receiveJobSignal(t, blockerStarted, "recording cancellation blocker")
	app.recordingTranscriptPath = "content/transcripts/recording.md"
	app.startRecordingFinalizer(1600, make([]float32, 1600))
	if stats := manager.Stats(); stats.Pending != 1 {
		t.Fatalf("finalizer was not pending before cancellation: %+v", stats)
	}
	manager.Close()
	waitContext, cancelWait := context.WithTimeout(context.Background(), time.Second)
	defer cancelWait()
	if !waitForWaitGroup(waitContext, &app.recordingWg) {
		t.Fatal("pending finalizer cancellation leaked its recording wait-group count")
	}
}

func TestStopRecordingDrainWaitsForBlockingManagedFinalizerBeforeCleanup(t *testing.T) {
	app, _ := newAppJobTestHarness(t, jobManagerConfig{Workers: 1, MaxPending: 2})
	finalizerStarted := make(chan struct{})
	releaseFinalizer := make(chan struct{})
	app.recordingFinalizeRun = func(ctx context.Context, _ int, _ []float32) {
		close(finalizerStarted)
		select {
		case <-releaseFinalizer:
		case <-ctx.Done():
		}
	}
	app.recordingTranscriptPath = "content/transcripts/recording.md"
	app.startRecordingFinalizer(1600, make([]float32, 1600))
	receiveJobSignal(t, finalizerStarted, "blocking recording finalizer")

	cleanupDone := make(chan struct{})
	go func() {
		app.drainRecordingWork()
		app.recordingMu.Lock()
		app.recordingTranscriptPath = ""
		app.recordingMu.Unlock()
		close(cleanupDone)
	}()
	select {
	case <-cleanupDone:
		t.Fatal("recording cleanup passed a running finalizer")
	case <-time.After(50 * time.Millisecond):
	}
	app.recordingMu.Lock()
	pathBeforeRelease := app.recordingTranscriptPath
	app.recordingMu.Unlock()
	if pathBeforeRelease == "" {
		t.Fatal("transcript path was cleared while finalizer was running")
	}

	close(releaseFinalizer)
	receiveJobSignal(t, cleanupDone, "recording cleanup after finalizer")
	app.recordingMu.Lock()
	pathAfterRelease := app.recordingTranscriptPath
	app.recordingMu.Unlock()
	if pathAfterRelease != "" {
		t.Fatalf("transcript path remained after finalizer drain: %q", pathAfterRelease)
	}
}

func newAppJobTestHarness(t *testing.T, config jobManagerConfig) (*App, *jobManager) {
	t.Helper()
	dataDir := t.TempDir()
	app := NewAppWithFileSystem(OSFileSystem{})
	app.dataDir = dataDir
	app.root = dataDir
	app.logFilePath = filepath.Join(dataDir, "app.log")
	app.jobs.config = &config
	manager := app.getJobManager()
	if manager == nil {
		t.Fatal("failed to start test app job manager")
	}
	t.Cleanup(func() {
		manager.Close()
		app.lifecycle.beginShutdown()
		waitContext, cancelWait := context.WithTimeout(context.Background(), time.Second)
		defer cancelWait()
		if !manager.Shutdown(waitContext) {
			t.Errorf("test app job manager did not stop: %+v", manager.Stats())
		}
		if !app.lifecycle.wait(waitContext) {
			t.Errorf("test app lifecycle did not stop")
		}
	})
	return app, manager
}

func waitForManagedJobs(t *testing.T, manager *jobManager, pending, running int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		stats := manager.Stats()
		if stats.Pending == pending && stats.Running == running {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("managed jobs did not reach pending=%d running=%d: %+v", pending, running, manager.Stats())
}
