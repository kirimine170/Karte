package main

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestJobManagerSchedulesPriorityFIFOAndAgesLowPriority(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{
		Workers:       1,
		MaxPending:    64,
		AgingInterval: 2,
	})

	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	blocker := manager.Submit(managedJob{
		Category: "test",
		Key:      "blocker",
		Priority: jobPriorityCritical,
		Run: func(ctx context.Context) error {
			close(blockerStarted)
			select {
			case <-releaseBlocker:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	requireJobAdmission(t, blocker, jobAccepted)
	receiveJobSignal(t, blockerStarted, "blocker start")

	order := make(chan string, 48)
	low := manager.Submit(testManagedJob("priority", "low", jobPriorityLow, func(context.Context) error {
		order <- "low"
		return nil
	}))
	requireJobAdmission(t, low, jobAccepted)
	for index := 0; index < 40; index++ {
		submission := manager.Submit(testManagedJob(
			"priority",
			fmt.Sprintf("critical-%02d", index),
			jobPriorityCritical,
			func(context.Context) error {
				order <- "critical"
				return nil
			},
		))
		requireJobAdmission(t, submission, jobAccepted)
	}
	close(releaseBlocker)

	lowPosition := -1
	for index := 0; index < 41; index++ {
		if value := receiveJobValue(t, order, "priority result"); value == "low" {
			lowPosition = index
			break
		}
	}
	if lowPosition < 0 || lowPosition > 6 {
		t.Fatalf("low priority job starved despite aging: position=%d", lowPosition)
	}
	if err := low.Handle.Wait(testJobTimeoutContext(t)); err != nil {
		t.Fatalf("wait for low priority job: %v", err)
	}

	// A fresh manager without aging pressure preserves strict priority and FIFO
	// for jobs at the same priority．
	priorityManager := startTestJobManager(t, jobManagerConfig{Workers: 1, MaxPending: 8})
	priorityBlockerStarted := make(chan struct{})
	priorityRelease := make(chan struct{})
	priorityManager.Submit(testManagedJob("test", "blocker", jobPriorityCritical, func(ctx context.Context) error {
		close(priorityBlockerStarted)
		select {
		case <-priorityRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	receiveJobSignal(t, priorityBlockerStarted, "priority blocker start")
	strictOrder := make(chan string, 3)
	for _, item := range []struct {
		key      string
		priority jobPriority
	}{
		{key: "low", priority: jobPriorityLow},
		{key: "high-1", priority: jobPriorityHigh},
		{key: "high-2", priority: jobPriorityHigh},
	} {
		item := item
		result := priorityManager.Submit(testManagedJob("strict", item.key, item.priority, func(context.Context) error {
			strictOrder <- item.key
			return nil
		}))
		requireJobAdmission(t, result, jobAccepted)
	}
	close(priorityRelease)
	for index, want := range []string{"high-1", "high-2", "low"} {
		if got := receiveJobValue(t, strictOrder, fmt.Sprintf("strict order %d", index)); got != want {
			t.Fatalf("strict priority order[%d] = %q, want %q", index, got, want)
		}
	}
}

func TestJobManagerOnlyDequeuesRunnableCategoriesAndLimitsASR(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{
		Workers:    3,
		MaxPending: 16,
		CategoryLimits: map[string]int{
			"asr-heavy": 1,
		},
	})

	var asrRunning atomic.Int32
	var asrPeak atomic.Int32
	asrStarted := make(chan string, 3)
	releaseASR := make(chan struct{})
	for index := 0; index < 3; index++ {
		key := fmt.Sprintf("asr-%d", index)
		result := manager.Submit(testManagedJob("asr-heavy", key, jobPriorityHigh, func(ctx context.Context) error {
			running := asrRunning.Add(1)
			updateAtomicPeak(&asrPeak, running)
			defer asrRunning.Add(-1)
			asrStarted <- key
			select {
			case <-releaseASR:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}))
		requireJobAdmission(t, result, jobAccepted)
	}
	receiveJobValue(t, asrStarted, "first ASR start")

	generalStarted := make(chan string, 2)
	for index := 0; index < 2; index++ {
		key := fmt.Sprintf("general-%d", index)
		result := manager.Submit(testManagedJob("general", key, jobPriorityNormal, func(context.Context) error {
			generalStarted <- key
			return nil
		}))
		requireJobAdmission(t, result, jobAccepted)
	}
	receiveJobValue(t, generalStarted, "first general start")
	receiveJobValue(t, generalStarted, "second general start")

	if stats := manager.Stats(); stats.RunningByGroup["asr-heavy"] != 1 {
		t.Fatalf("running ASR jobs = %d, want 1", stats.RunningByGroup["asr-heavy"])
	}
	close(releaseASR)
	for index := 1; index < 3; index++ {
		receiveJobValue(t, asrStarted, fmt.Sprintf("ASR start %d", index))
	}
	if got := asrPeak.Load(); got != 1 {
		t.Fatalf("peak ASR concurrency = %d, want 1", got)
	}
}

func TestJobManagerDeduplicatesAndReplacementCompletesOldWaiters(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{Workers: 1, MaxPending: 4})
	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	manager.Submit(testManagedJob("test", "blocker", jobPriorityHigh, func(ctx context.Context) error {
		close(blockerStarted)
		select {
		case <-releaseBlocker:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	receiveJobSignal(t, blockerStarted, "blocker start")

	runs := make(chan string, 1)
	original := manager.Submit(testManagedJob("replace", "same", jobPriorityLow, func(context.Context) error {
		runs <- "old"
		return nil
	}))
	requireJobAdmission(t, original, jobAccepted)
	duplicate := manager.Submit(testManagedJob("replace", "same", jobPriorityHigh, func(context.Context) error {
		runs <- "duplicate"
		return nil
	}))
	requireJobAdmission(t, duplicate, jobDeduplicated)

	replacementSpec := testManagedJob("replace", "same", jobPriorityHigh, func(context.Context) error {
		runs <- "latest"
		return nil
	})
	replacementSpec.Coalesce = jobReplacePending
	replacement := manager.Submit(replacementSpec)
	requireJobAdmission(t, replacement, jobReplacedPending)

	for name, handle := range map[string]*jobHandle{"original": original.Handle, "duplicate": duplicate.Handle} {
		if err := handle.Wait(testJobTimeoutContext(t)); !errors.Is(err, errJobReplaced) {
			t.Fatalf("%s waiter error = %v, want errJobReplaced", name, err)
		}
	}
	close(releaseBlocker)
	if err := replacement.Handle.Wait(testJobTimeoutContext(t)); err != nil {
		t.Fatalf("replacement wait: %v", err)
	}
	if got := receiveJobValue(t, runs, "replacement run"); got != "latest" {
		t.Fatalf("replacement ran %q, want latest", got)
	}
	select {
	case unexpected := <-runs:
		t.Fatalf("obsolete job ran: %s", unexpected)
	default:
	}
}

func TestJobManagerBoundsPendingAndRejectsOverflow(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{Workers: 1, MaxPending: 2})
	started := make(chan struct{})
	release := make(chan struct{})
	manager.Submit(testManagedJob("test", "running", jobPriorityNormal, func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	receiveJobSignal(t, started, "running job")
	for index := 0; index < 2; index++ {
		result := manager.Submit(testManagedJob("pending", fmt.Sprintf("job-%d", index), jobPriorityNormal, func(context.Context) error { return nil }))
		requireJobAdmission(t, result, jobAccepted)
	}
	overflow := manager.Submit(testManagedJob("pending", "overflow", jobPriorityCritical, func(context.Context) error { return nil }))
	requireJobAdmission(t, overflow, jobRejectedFull)
	if !errors.Is(overflow.Err, errJobQueueFull) {
		t.Fatalf("overflow error = %v, want errJobQueueFull", overflow.Err)
	}
	if stats := manager.Stats(); stats.Pending != 2 || stats.PeakPending != 2 {
		t.Fatalf("pending stats = %+v, want pending and peak 2", stats)
	}
	close(release)
}

func TestJobManagerCancelsPendingAndRunningByKey(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{Workers: 1, MaxPending: 4})
	runningStarted := make(chan struct{})
	running := manager.Submit(testManagedJob("cancel", "running", jobPriorityNormal, func(ctx context.Context) error {
		close(runningStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	requireJobAdmission(t, running, jobAccepted)
	receiveJobSignal(t, runningStarted, "running start")

	var pendingRan atomic.Bool
	pending := manager.Submit(testManagedJob("cancel", "pending", jobPriorityNormal, func(context.Context) error {
		pendingRan.Store(true)
		return nil
	}))
	requireJobAdmission(t, pending, jobAccepted)
	if !manager.Cancel("cancel", "pending") {
		t.Fatal("pending cancellation returned false")
	}
	if err := pending.Handle.Wait(testJobTimeoutContext(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pending error = %v, want context.Canceled", err)
	}
	if pendingRan.Load() {
		t.Fatal("canceled pending job ran")
	}
	if !manager.Cancel("cancel", "running") {
		t.Fatal("running cancellation returned false")
	}
	if err := running.Handle.Wait(testJobTimeoutContext(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("running error = %v, want context.Canceled", err)
	}
}

func TestJobManagerCancelsCategoryAndRecordingGroup(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{Workers: 2, MaxPending: 8})
	started := make(chan string, 2)
	jobs := make([]jobSubmission, 0, 4)
	for index, item := range []struct {
		category string
		group    string
	}{
		{category: "asr-heavy", group: "recording-session-1"},
		{category: "asr-heavy", group: "recording-session-1"},
		{category: "asr-heavy", group: "audio-import"},
		{category: "web-clip", group: "clip"},
	} {
		item := item
		spec := testManagedJob(item.category, fmt.Sprintf("job-%d", index), jobPriorityNormal, func(ctx context.Context) error {
			started <- item.group
			<-ctx.Done()
			return ctx.Err()
		})
		spec.Group = item.group
		jobs = append(jobs, manager.Submit(spec))
	}
	receiveJobValue(t, started, "first grouped job")
	receiveJobValue(t, started, "second grouped job")
	if canceled := manager.CancelGroup("recording-session-1"); canceled != 2 {
		t.Fatalf("CancelGroup canceled %d jobs, want 2", canceled)
	}
	for index := 0; index < 2; index++ {
		if err := jobs[index].Handle.Wait(testJobTimeoutContext(t)); !errors.Is(err, context.Canceled) {
			t.Fatalf("recording group job %d error = %v, want context.Canceled", index, err)
		}
	}
	if canceled := manager.CancelCategory("web-clip"); canceled != 1 {
		t.Fatalf("CancelCategory canceled %d jobs, want 1", canceled)
	}
	if err := jobs[3].Handle.Wait(testJobTimeoutContext(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("web clip category error = %v, want context.Canceled", err)
	}
	if jobs[2].Handle.Cancel() == false {
		t.Fatal("audio import job was unexpectedly canceled with recording group")
	}
}

func TestJobManagerShutdownClosesAdmissionCancelsAndWaits(t *testing.T) {
	manager, cancelParent := newStartedTestJobManager(t, jobManagerConfig{
		Workers:    2,
		MaxPending: 4,
		CategoryLimits: map[string]int{
			"shutdown": 1,
		},
	})
	defer cancelParent()
	runningStarted := make(chan struct{})
	running := manager.Submit(testManagedJob("shutdown", "running", jobPriorityNormal, func(ctx context.Context) error {
		close(runningStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	requireJobAdmission(t, running, jobAccepted)
	receiveJobSignal(t, runningStarted, "shutdown running start")

	pendingContext, cancelPending := context.WithCancel(context.Background())
	pendingSpec := testManagedJob("shutdown", "pending", jobPriorityNormal, func(context.Context) error { return nil })
	pendingSpec.Context = pendingContext
	pending := manager.Submit(pendingSpec)
	requireJobAdmission(t, pending, jobAccepted)

	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
	defer cancelShutdown()
	if !manager.Shutdown(shutdownContext) {
		t.Fatal("job manager shutdown timed out")
	}
	cancelPending()
	for name, handle := range map[string]*jobHandle{"running": running.Handle, "pending": pending.Handle} {
		if err := handle.Wait(testJobTimeoutContext(t)); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s shutdown error = %v, want context.Canceled", name, err)
		}
	}
	rejected := manager.Submit(testManagedJob("shutdown", "late", jobPriorityCritical, func(context.Context) error { return nil }))
	requireJobAdmission(t, rejected, jobRejectedClosed)
	if stats := manager.Stats(); !stats.Closing || stats.LiveWorkers != 0 || stats.Pending != 0 || stats.Running != 0 {
		t.Fatalf("unexpected shutdown stats: %+v", stats)
	}
}

func TestJobManagerPanicDoesNotKillWorker(t *testing.T) {
	panicObserved := make(chan any, 1)
	manager := startTestJobManager(t, jobManagerConfig{
		Workers:    1,
		MaxPending: 4,
		OnPanic: func(_ managedJob, recovered any) {
			panicObserved <- recovered
		},
	})
	panicking := manager.Submit(testManagedJob("panic", "panic", jobPriorityNormal, func(context.Context) error {
		panic("boom")
	}))
	requireJobAdmission(t, panicking, jobAccepted)
	var panicErr *jobPanicError
	if err := panicking.Handle.Wait(testJobTimeoutContext(t)); !errors.As(err, &panicErr) || panicErr.Value != "boom" {
		t.Fatalf("panic job error = %#v, want jobPanicError boom", err)
	}
	if recovered := receiveJobValue(t, panicObserved, "panic callback"); recovered != "boom" {
		t.Fatalf("panic callback = %v, want boom", recovered)
	}

	continued := make(chan struct{})
	next := manager.Submit(testManagedJob("panic", "next", jobPriorityNormal, func(context.Context) error {
		close(continued)
		return nil
	}))
	requireJobAdmission(t, next, jobAccepted)
	if err := next.Handle.Wait(testJobTimeoutContext(t)); err != nil {
		t.Fatalf("next job wait: %v", err)
	}
	receiveJobSignal(t, continued, "worker continuation")
}

func TestJobManagerCompletionCallbacksCanReenterAfterReplaceCancelAndFinish(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{Workers: 1, MaxPending: 6})
	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	manager.Submit(testManagedJob("block", "block", jobPriorityCritical, func(ctx context.Context) error {
		close(blockerStarted)
		select {
		case <-releaseBlocker:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	receiveJobSignal(t, blockerStarted, "completion callback blocker")

	callbacks := make(chan string, 3)
	callback := func(name string) func(error) {
		return func(error) {
			// Stats acquires manager.mu and would deadlock if OnFinish still ran
			// inside replacement，cancellation，or finish critical sections．
			_ = manager.Stats()
			callbacks <- name
		}
	}
	originalSpec := testManagedJob("callback", "replace", jobPriorityNormal, func(context.Context) error { return nil })
	originalSpec.OnFinish = callback("replace")
	original := manager.Submit(originalSpec)
	requireJobAdmission(t, original, jobAccepted)
	replacementSpec := testManagedJob("callback", "replace", jobPriorityNormal, func(context.Context) error { return nil })
	replacementSpec.Coalesce = jobReplacePending
	replacementSpec.OnFinish = callback("finish")
	replacement := manager.Submit(replacementSpec)
	requireJobAdmission(t, replacement, jobReplacedPending)
	if got := receiveJobValue(t, callbacks, "replacement callback"); got != "replace" {
		t.Fatalf("first callback = %q, want replace", got)
	}

	canceledSpec := testManagedJob("callback", "cancel", jobPriorityNormal, func(context.Context) error { return nil })
	canceledSpec.OnFinish = callback("cancel")
	canceled := manager.Submit(canceledSpec)
	requireJobAdmission(t, canceled, jobAccepted)
	if !canceled.Handle.Cancel() {
		t.Fatal("pending callback job was not canceled")
	}
	if got := receiveJobValue(t, callbacks, "cancellation callback"); got != "cancel" {
		t.Fatalf("second callback = %q, want cancel", got)
	}

	close(releaseBlocker)
	if err := replacement.Handle.Wait(testJobTimeoutContext(t)); err != nil {
		t.Fatalf("replacement finish: %v", err)
	}
	if got := receiveJobValue(t, callbacks, "finish callback"); got != "finish" {
		t.Fatalf("third callback = %q, want finish", got)
	}
}

func TestJobManagerSubmitAndWaitRejectsWorkerReentryAndHonorsCallerCancellation(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{Workers: 1, MaxPending: 4})
	reentrantResult := make(chan error, 1)
	outer := manager.Submit(testManagedJob("outer", "outer", jobPriorityNormal, func(ctx context.Context) error {
		err := manager.SubmitAndWait(ctx, testManagedJob("inner", "inner", jobPriorityNormal, func(context.Context) error { return nil }))
		reentrantResult <- err
		return nil
	}))
	requireJobAdmission(t, outer, jobAccepted)
	if err := outer.Handle.Wait(testJobTimeoutContext(t)); err != nil {
		t.Fatalf("outer job wait: %v", err)
	}
	if err := receiveJobValue(t, reentrantResult, "reentrant result"); !errors.Is(err, errJobManagerReentrant) {
		t.Fatalf("reentrant error = %v, want errJobManagerReentrant", err)
	}

	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	blocker := manager.Submit(testManagedJob("blocking", "blocking", jobPriorityCritical, func(ctx context.Context) error {
		close(blockerStarted)
		select {
		case <-releaseBlocker:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	requireJobAdmission(t, blocker, jobAccepted)
	receiveJobSignal(t, blockerStarted, "SubmitAndWait blocker")

	waitContext, cancelWait := context.WithCancel(context.Background())
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- manager.SubmitAndWait(waitContext, testManagedJob("site", "site", jobPriorityNormal, func(context.Context) error { return nil }))
	}()
	waitForJobPending(t, manager, 1)
	cancelWait()
	if err := receiveJobValue(t, waitResult, "canceled SubmitAndWait"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SubmitAndWait cancellation error = %v, want context.Canceled", err)
	}
	close(releaseBlocker)
}

func TestJobManagerDeduplicatedSubmitAndWaitCancellationDoesNotCancelOwner(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{Workers: 1, MaxPending: 4})
	ownerStarted := make(chan struct{})
	releaseOwner := make(chan struct{})
	owner := manager.Submit(testManagedJob("shared", "same", jobPriorityNormal, func(ctx context.Context) error {
		close(ownerStarted)
		select {
		case <-releaseOwner:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	requireJobAdmission(t, owner, jobAccepted)
	receiveJobSignal(t, ownerStarted, "shared owner start")

	baseWaitContext, cancelWait := context.WithCancel(context.Background())
	waitObserved := make(chan struct{})
	waitContext := &observableJobContext{Context: baseWaitContext, observed: waitObserved}
	waitResult := make(chan error, 1)
	go func() {
		waitResult <- manager.SubmitAndWait(waitContext, testManagedJob("shared", "same", jobPriorityNormal, func(context.Context) error {
			return errors.New("deduplicated job must not run")
		}))
	}()
	receiveJobSignal(t, waitObserved, "deduplicated waiter start")
	cancelWait()
	if err := receiveJobValue(t, waitResult, "deduplicated waiter cancellation"); !errors.Is(err, context.Canceled) {
		t.Fatalf("deduplicated waiter error = %v, want context.Canceled", err)
	}
	select {
	case <-owner.Handle.completion.done:
		t.Fatalf("deduplicated waiter canceled owner: %v", owner.Handle.Wait(context.Background()))
	default:
	}
	close(releaseOwner)
	if err := owner.Handle.Wait(testJobTimeoutContext(t)); err != nil {
		t.Fatalf("owner job error = %v, want nil", err)
	}
}

func TestJobManagerTenThousandLatestValueBurstStaysBounded(t *testing.T) {
	manager := startTestJobManager(t, jobManagerConfig{Workers: 1, MaxPending: 4})
	blockerStarted := make(chan struct{})
	releaseBlocker := make(chan struct{})
	manager.Submit(testManagedJob("block", "block", jobPriorityCritical, func(ctx context.Context) error {
		close(blockerStarted)
		select {
		case <-releaseBlocker:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}))
	receiveJobSignal(t, blockerStarted, "burst blocker")

	baselineGoroutines := runtime.NumGoroutine()
	var latestRan atomic.Int32
	var firstHandle *jobHandle
	var final jobSubmission
	for index := 0; index < 10_000; index++ {
		value := index
		spec := testManagedJob("ui-latest", "input-level", jobPriorityLow, func(context.Context) error {
			latestRan.Store(int32(value + 1))
			return nil
		})
		spec.Coalesce = jobReplacePending
		final = manager.Submit(spec)
		if index == 0 {
			firstHandle = final.Handle
			requireJobAdmission(t, final, jobAccepted)
		} else {
			requireJobAdmission(t, final, jobReplacedPending)
		}
	}
	if err := firstHandle.Wait(testJobTimeoutContext(t)); !errors.Is(err, errJobReplaced) {
		t.Fatalf("first burst waiter error = %v, want errJobReplaced", err)
	}
	stats := manager.Stats()
	if stats.Pending != 1 || stats.PeakPending != 1 || stats.LiveWorkers != 1 {
		t.Fatalf("burst was not bounded: %+v", stats)
	}
	if delta := runtime.NumGoroutine() - baselineGoroutines; delta > 2 {
		t.Fatalf("10000 submissions created unbounded goroutines: delta=%d", delta)
	}
	close(releaseBlocker)
	if err := final.Handle.Wait(testJobTimeoutContext(t)); err != nil {
		t.Fatalf("final burst job wait: %v", err)
	}
	if got := latestRan.Load(); got != 10_000 {
		t.Fatalf("latest burst value = %d, want 10000", got)
	}
}

func TestJobManagerSubmitShutdownRace(t *testing.T) {
	manager, cancelParent := newStartedTestJobManager(t, jobManagerConfig{Workers: 4, MaxPending: 64})
	defer cancelParent()

	start := make(chan struct{})
	var submitters sync.WaitGroup
	errorsSeen := make(chan error, 512)
	for index := 0; index < 512; index++ {
		index := index
		submitters.Add(1)
		go func() {
			defer submitters.Done()
			<-start
			result := manager.Submit(testManagedJob("race", fmt.Sprintf("job-%d", index), jobPriorityNormal, func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			}))
			switch result.Status {
			case jobAccepted, jobRejectedFull, jobRejectedClosed:
			default:
				errorsSeen <- fmt.Errorf("unexpected submission status: %s", result.Status)
			}
		}()
	}
	close(start)
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 2*time.Second)
	if !manager.Shutdown(shutdownContext) {
		t.Fatal("shutdown raced with submitters and timed out")
	}
	cancelShutdown()
	submitters.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Error(err)
	}
	if stats := manager.Stats(); stats.Pending != 0 || stats.Running != 0 || stats.LiveWorkers != 0 {
		t.Fatalf("race left work behind: %+v", stats)
	}
}

func startTestJobManager(t *testing.T, config jobManagerConfig) *jobManager {
	t.Helper()
	manager, cancelParent := newStartedTestJobManager(t, config)
	t.Cleanup(func() {
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), time.Second)
		if !manager.Shutdown(shutdownContext) {
			t.Errorf("job manager cleanup timed out: %+v", manager.Stats())
		}
		cancelShutdown()
		cancelParent()
	})
	return manager
}

func newStartedTestJobManager(t *testing.T, config jobManagerConfig) (*jobManager, context.CancelFunc) {
	t.Helper()
	parent, cancelParent := context.WithCancel(context.Background())
	manager := newJobManager(config)
	if !manager.Start(parent, func(worker func(context.Context)) bool {
		go worker(parent)
		return true
	}) {
		cancelParent()
		t.Fatal("failed to start job manager")
	}
	return manager, cancelParent
}

func testManagedJob(category, key string, priority jobPriority, run func(context.Context) error) managedJob {
	return managedJob{
		Category: category,
		Key:      key,
		Priority: priority,
		Coalesce: jobKeepExisting,
		Run:      run,
	}
}

func requireJobAdmission(t *testing.T, submission jobSubmission, want jobAdmissionStatus) {
	t.Helper()
	if submission.Status != want {
		t.Fatalf("job admission = %s (err=%v), want %s", submission.Status, submission.Err, want)
	}
	if want == jobAccepted || want == jobDeduplicated || want == jobReplacedPending {
		if submission.Err != nil || submission.Handle == nil {
			t.Fatalf("successful admission missing handle: %+v", submission)
		}
	}
}

func receiveJobSignal(t *testing.T, channel <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func receiveJobValue[T any](t *testing.T, channel <-chan T, description string) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", description)
		var zero T
		return zero
	}
}

func testJobTimeoutContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	t.Cleanup(cancel)
	return ctx
}

func waitForJobPending(t *testing.T, manager *jobManager, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if manager.Stats().Pending == want {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("pending jobs = %d, want %d", manager.Stats().Pending, want)
}

func updateAtomicPeak(peak *atomic.Int32, candidate int32) {
	for {
		current := peak.Load()
		if candidate <= current || peak.CompareAndSwap(current, candidate) {
			return
		}
	}
}

type observableJobContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (ctx *observableJobContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.observed) })
	return ctx.Context.Done()
}
