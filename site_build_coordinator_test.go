package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSiteBuildCoordinatorCoalescesBurstIntoOneRun(t *testing.T) {
	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	runs := make(chan siteBuildRequest, 2)
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(_ context.Context, request siteBuildRequest) error {
			runs <- request
			return nil
		},
		nil,
		clock,
		time.Second,
		8,
	)
	startSiteBuildTestCoordinator(t, lifecycle, coordinator)

	if !coordinator.Schedule("content/a.md") || !coordinator.Schedule("content/a.md", "content/b.md") {
		t.Fatal("burst requests were not accepted")
	}
	timer := clock.waitForTimer(t, 0)
	timer.Fire()
	request := receiveSiteBuildRequest(t, runs)
	assertSiteBuildPaths(t, request.Dirty, "content/a.md", "content/b.md")
	if request.Rescan {
		t.Fatal("bounded burst unexpectedly requested a rescan")
	}
	select {
	case duplicate := <-runs:
		t.Fatalf("burst triggered a duplicate run: %+v", duplicate)
	default:
	}
}

func TestSiteBuildCoordinatorWebClipTwoStageUpdateIsOneIncrementalRun(t *testing.T) {
	root := newSiteBuildTestRoot(t, map[string]string{"baseline.md": "baseline"})
	var rendered []string
	builder := newTestSiteBuilder(&rendered)
	if result, err := builder.BuildIncremental(context.Background(), root); err != nil || !result.Full {
		t.Fatalf("prepare baseline: result=%+v err=%v", result, err)
	}
	rendered = nil

	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	type runResult struct {
		request siteBuildRequest
		result  siteBuildResult
		err     error
	}
	runs := make(chan runResult, 1)
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(ctx context.Context, request siteBuildRequest) error {
			result, err := builder.BuildIncremental(ctx, root)
			runs <- runResult{request: request, result: result, err: err}
			return err
		},
		nil,
		clock,
		time.Second,
		8,
	)
	startSiteBuildTestCoordinator(t, lifecycle, coordinator)

	clipPath := "content/clips/article.md"
	writeSiteBuildTestFile(t, filepathFromSlash(root, clipPath), "initial clip")
	coordinator.Schedule(clipPath)
	// Image conversion updates the same Markdown during the debounce window.
	writeSiteBuildTestFile(t, filepathFromSlash(root, clipPath), "clip with converted image")
	coordinator.Schedule(clipPath)
	clock.waitForTimer(t, 0).Fire()

	select {
	case run := <-runs:
		if run.err != nil {
			t.Fatal(run.err)
		}
		if run.result.Full {
			t.Fatal("Web Clip two-stage update repeated a full build")
		}
		assertSiteBuildPaths(t, run.request.Dirty, clipPath)
		assertSiteBuildPaths(t, run.result.Rendered, clipPath)
		assertSiteBuildPaths(t, rendered, clipPath)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Web Clip build")
	}
}

func TestSiteBuildCoordinatorWebClipDelayedConversionDoesNotRepeatFullBuild(t *testing.T) {
	root := newSiteBuildTestRoot(t, map[string]string{"clips/article.md": "initial clip"})
	var rendered []string
	builder := newTestSiteBuilder(&rendered)
	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	runs := make(chan siteBuildResultAndError, 2)
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(ctx context.Context, _ siteBuildRequest) error {
			result, err := builder.BuildIncremental(ctx, root)
			runs <- siteBuildResultAndError{result: result, err: err}
			return err
		},
		nil,
		clock,
		time.Second,
		8,
	)
	startSiteBuildTestCoordinator(t, lifecycle, coordinator)

	clipPath := "content/clips/article.md"
	coordinator.Schedule(clipPath)
	clock.waitForTimer(t, 0).Fire()
	first := receiveSiteBuildResult(t, runs)
	if first.err != nil || !first.result.Full {
		t.Fatalf("initial Web Clip build was not the single full build: result=%+v err=%v", first.result, first.err)
	}
	assertSiteBuildPaths(t, first.result.Rendered, clipPath)

	// Production image conversion normally arrives after its three-second
	// startup delay，so exercise a distinct timer/run rather than coalescing it.
	writeSiteBuildTestFile(t, filepathFromSlash(root, clipPath), "clip with converted image")
	coordinator.Schedule(clipPath)
	clock.waitForTimer(t, 1).Fire()
	second := receiveSiteBuildResult(t, runs)
	if second.err != nil || second.result.Full {
		t.Fatalf("delayed Web Clip conversion repeated a full build: result=%+v err=%v", second.result, second.err)
	}
	assertSiteBuildPaths(t, second.result.Rendered, clipPath)
}

func TestSiteBuildCoordinatorScheduleDoesNotWaitForRunningBuild(t *testing.T) {
	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	started := make(chan struct{})
	release := make(chan struct{})
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(ctx context.Context, _ siteBuildRequest) error {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		nil,
		clock,
		time.Second,
		8,
	)
	startSiteBuildTestCoordinator(t, lifecycle, coordinator)

	coordinator.Schedule("content/a.md")
	clock.waitForTimer(t, 0).Fire()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("build did not start")
	}
	scheduled := make(chan bool, 1)
	go func() {
		scheduled <- coordinator.Schedule("content/b.md")
	}()
	select {
	case accepted := <-scheduled:
		if !accepted {
			t.Fatal("mutation was rejected while a build was running")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("mutation waited for the running build")
	}
	close(release)
}

func TestSiteBuildCoordinatorShutdownCancelsTimerAndRejectsCallbacks(t *testing.T) {
	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	var runCount atomic.Int32
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(context.Context, siteBuildRequest) error {
			runCount.Add(1)
			return nil
		},
		nil,
		clock,
		time.Second,
		8,
	)
	if !lifecycle.goWorker(coordinator.Run) {
		t.Fatal("failed to start coordinator")
	}
	coordinator.Schedule("content/a.md")
	timer := clock.waitForTimer(t, 0)
	if !lifecycle.beginShutdown() {
		t.Fatal("failed to begin shutdown")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !lifecycle.wait(waitCtx) {
		t.Fatal("coordinator did not drain on shutdown")
	}
	timer.Fire()
	if got := runCount.Load(); got != 0 {
		t.Fatalf("timer callback ran after shutdown: %d", got)
	}
	if coordinator.Schedule("content/b.md") {
		t.Fatal("coordinator accepted work after shutdown")
	}
}

func TestSiteBuildCoordinatorShutdownCancelsAndWaitsForActiveBuild(t *testing.T) {
	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	started := make(chan struct{})
	stopped := make(chan struct{})
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(ctx context.Context, _ siteBuildRequest) error {
			close(started)
			<-ctx.Done()
			close(stopped)
			return ctx.Err()
		},
		nil,
		clock,
		time.Second,
		8,
	)
	if !lifecycle.goWorker(coordinator.Run) {
		t.Fatal("failed to start coordinator")
	}
	coordinator.Schedule("content/a.md")
	clock.waitForTimer(t, 0).Fire()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("active build did not start")
	}
	if !lifecycle.beginShutdown() {
		t.Fatal("failed to begin shutdown")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !lifecycle.wait(waitCtx) {
		t.Fatal("shutdown did not wait for active build cancellation")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("active build did not observe lifecycle cancellation")
	}
}

func TestSiteBuildCoordinatorOverflowRescanDoesNotRenderUnchangedBaseline(t *testing.T) {
	root := newSiteBuildTestRoot(t, map[string]string{"a.md": "alpha"})
	var rendered []string
	builder := newTestSiteBuilder(&rendered)
	if _, err := builder.BuildIncremental(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	rendered = nil

	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	type runResult struct {
		request siteBuildRequest
		result  siteBuildResult
	}
	runs := make(chan runResult, 1)
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(ctx context.Context, request siteBuildRequest) error {
			result, err := builder.BuildIncremental(ctx, root)
			if err == nil {
				runs <- runResult{request: request, result: result}
			}
			return err
		},
		nil,
		clock,
		time.Second,
		2,
	)
	startSiteBuildTestCoordinator(t, lifecycle, coordinator)

	coordinator.Schedule("content/a.md", "content/b.md", "content/c.md")
	clock.waitForTimer(t, 0).Fire()
	select {
	case run := <-runs:
		if !run.request.Rescan || len(run.request.Dirty) != 2 {
			t.Fatalf("overflow was not bounded to a rescan marker: %+v", run.request)
		}
		if run.result.Full || len(run.result.Rendered) != 0 || len(rendered) != 0 {
			t.Fatalf("overflow rescan rerendered unchanged baseline: result=%+v rendered=%v", run.result, rendered)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for overflow rescan")
	}
}

func TestSiteBuildCoordinatorFailureCanRetryOnNextMutation(t *testing.T) {
	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	errorsSeen := make(chan error, 1)
	runs := make(chan int, 2)
	var runCount atomic.Int32
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(context.Context, siteBuildRequest) error {
			count := int(runCount.Add(1))
			runs <- count
			if count == 1 {
				return errors.New("transient build failure")
			}
			return nil
		},
		func(err error) { errorsSeen <- err },
		clock,
		time.Second,
		8,
	)
	startSiteBuildTestCoordinator(t, lifecycle, coordinator)

	coordinator.Schedule("content/a.md")
	clock.waitForTimer(t, 0).Fire()
	if got := receiveSiteBuildRunNumber(t, runs); got != 1 {
		t.Fatalf("first run number: %d", got)
	}
	select {
	case <-errorsSeen:
	case <-time.After(time.Second):
		t.Fatal("build failure was not reported")
	}
	coordinator.Schedule("content/a.md")
	clock.waitForTimer(t, 1).Fire()
	if got := receiveSiteBuildRunNumber(t, runs); got != 2 {
		t.Fatalf("retry run number: %d", got)
	}
}

func TestSiteBuildCoordinatorRestoresDirtyRequestAfterJobBackpressure(t *testing.T) {
	lifecycle := newSiteBuildTestLifecycle()
	clock := newManualSiteBuildClock()
	runs := make(chan siteBuildRequest, 2)
	var runCount atomic.Int32
	coordinator := newSiteBuildCoordinator(
		lifecycle.context(),
		func(_ context.Context, request siteBuildRequest) error {
			runs <- request
			if runCount.Add(1) == 1 {
				return errJobQueueFull
			}
			return nil
		},
		nil,
		clock,
		time.Second,
		8,
	)
	startSiteBuildTestCoordinator(t, lifecycle, coordinator)

	coordinator.Schedule("content/a.md", "content/b.md")
	clock.waitForTimer(t, 0).Fire()
	first := receiveSiteBuildRequest(t, runs)
	clock.waitForTimer(t, 1).Fire()
	second := receiveSiteBuildRequest(t, runs)
	if len(first.Dirty) != 2 || len(second.Dirty) != 2 ||
		first.Dirty[0] != second.Dirty[0] || first.Dirty[1] != second.Dirty[1] ||
		first.Rescan != second.Rescan {
		t.Fatalf("backpressure retry lost dirty state: first=%+v second=%+v", first, second)
	}
}

type manualSiteBuildClock struct {
	mu      sync.Mutex
	timers  []*manualSiteBuildTimer
	created chan struct{}
}

type manualSiteBuildTimer struct {
	mu      sync.Mutex
	channel chan time.Time
	stopped bool
}

func newManualSiteBuildClock() *manualSiteBuildClock {
	return &manualSiteBuildClock{created: make(chan struct{}, 16)}
}

func (clock *manualSiteBuildClock) NewTimer(time.Duration) siteBuildTimer {
	timer := &manualSiteBuildTimer{channel: make(chan time.Time, 1)}
	clock.mu.Lock()
	clock.timers = append(clock.timers, timer)
	clock.mu.Unlock()
	clock.created <- struct{}{}
	return timer
}

func (clock *manualSiteBuildClock) waitForTimer(t *testing.T, index int) *manualSiteBuildTimer {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		clock.mu.Lock()
		if len(clock.timers) > index {
			timer := clock.timers[index]
			clock.mu.Unlock()
			return timer
		}
		clock.mu.Unlock()
		select {
		case <-clock.created:
		case <-deadline:
			t.Fatalf("timed out waiting for timer %d", index)
		}
	}
}

func (timer *manualSiteBuildTimer) Chan() <-chan time.Time {
	return timer.channel
}

func (timer *manualSiteBuildTimer) Stop() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	wasActive := !timer.stopped
	timer.stopped = true
	return wasActive
}

func (timer *manualSiteBuildTimer) Reset(time.Duration) bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	wasActive := !timer.stopped
	timer.stopped = false
	return wasActive
}

func (timer *manualSiteBuildTimer) Fire() {
	select {
	case timer.channel <- time.Unix(1, 0):
	default:
	}
}

func newSiteBuildTestLifecycle() *appLifecycle {
	lifecycle := &appLifecycle{}
	lifecycle.start(context.Background())
	return lifecycle
}

func startSiteBuildTestCoordinator(t *testing.T, lifecycle *appLifecycle, coordinator *siteBuildCoordinator) {
	t.Helper()
	if !lifecycle.goWorker(coordinator.Run) {
		t.Fatal("failed to start coordinator")
	}
	t.Cleanup(func() {
		lifecycle.beginShutdown()
		waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if !lifecycle.wait(waitCtx) {
			t.Error("coordinator did not stop")
		}
	})
}

func receiveSiteBuildRequest(t *testing.T, requests <-chan siteBuildRequest) siteBuildRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for build request")
		return siteBuildRequest{}
	}
}

func receiveSiteBuildRunNumber(t *testing.T, runs <-chan int) int {
	t.Helper()
	select {
	case run := <-runs:
		return run
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for build run")
		return 0
	}
}

type siteBuildResultAndError struct {
	result siteBuildResult
	err    error
}

func receiveSiteBuildResult(t *testing.T, runs <-chan siteBuildResultAndError) siteBuildResultAndError {
	t.Helper()
	select {
	case result := <-runs:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for site build result")
		return siteBuildResultAndError{}
	}
}

func filepathFromSlash(root, relativePath string) string {
	return filepath.Join(root, filepath.FromSlash(relativePath))
}
