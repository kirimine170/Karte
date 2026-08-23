package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"karte/internal/asr"
)

type fakeAppASRService struct {
	closeCount       atomic.Int32
	closeGate        <-chan struct{}
	closePanic       bool
	transcribeStart  chan struct{}
	transcribeGate   <-chan struct{}
	transcribeOnce   sync.Once
	transcribeResult string
	transcribeErr    error
	processStart     chan struct{}
	processGate      <-chan struct{}
	processOnce      sync.Once
	processResult    string
	processErr       error
}

func (service *fakeAppASRService) Close() {
	service.closeCount.Add(1)
	if service.closeGate != nil {
		<-service.closeGate
	}
	if service.closePanic {
		panic("fake close panic")
	}
}

func (service *fakeAppASRService) TranscribeFile(ctx context.Context, _ string, _ func(string, int, int, float64)) (string, error) {
	if service.transcribeStart != nil {
		service.transcribeOnce.Do(func() { close(service.transcribeStart) })
	}
	if service.transcribeGate != nil {
		select {
		case <-service.transcribeGate:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return service.transcribeResult, service.transcribeErr
}

func (service *fakeAppASRService) ProcessSamples([]float32) (string, error) {
	if service.processStart != nil {
		service.processOnce.Do(func() { close(service.processStart) })
	}
	if service.processGate != nil {
		<-service.processGate
	}
	return service.processResult, service.processErr
}

type manualASRIdleClock struct {
	mu     sync.Mutex
	timers []*manualASRIdleTimer
}

type manualASRIdleTimer struct {
	clock            *manualASRIdleClock
	callback         func()
	delay            time.Duration
	stopped          bool
	fired            bool
	stopReturnsFalse bool
}

func (clock *manualASRIdleClock) AfterFunc(delay time.Duration, callback func()) asrIdleTimer {
	timer := &manualASRIdleTimer{
		clock:    clock,
		callback: callback,
		delay:    delay,
	}
	clock.mu.Lock()
	clock.timers = append(clock.timers, timer)
	clock.mu.Unlock()
	return timer
}

func (timer *manualASRIdleTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	if timer.fired || timer.stopped || timer.stopReturnsFalse {
		return false
	}
	timer.stopped = true
	return true
}

func (clock *manualASRIdleClock) Count() int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return len(clock.timers)
}

func (clock *manualASRIdleClock) Delay(index int) time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.timers[index].delay
}

func (clock *manualASRIdleClock) MakeStopRace(index int) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.timers[index].stopReturnsFalse = true
}

func (clock *manualASRIdleClock) Fire(index int) bool {
	clock.mu.Lock()
	timer := clock.timers[index]
	if timer.fired || timer.stopped {
		clock.mu.Unlock()
		return false
	}
	timer.fired = true
	callback := timer.callback
	clock.mu.Unlock()
	callback()
	return true
}

func waitForASRResourceStatus(t *testing.T, manager *asrResourceManager, check func(asrResourceStatus) bool) asrResourceStatus {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		status := manager.Status()
		if check(status) {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASR resource status did not converge: %+v", status)
		}
		time.Sleep(time.Millisecond)
	}
}

func shutdownASRResourceManager(t *testing.T, manager *asrResourceManager) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !manager.Shutdown(ctx) {
		t.Fatal("ASR resource manager did not shut down")
	}
}

func TestASRResourceManagerStartsUnloadedAndLoadsOnceForConcurrentAcquire(t *testing.T) {
	clock := &manualASRIdleClock{}
	service := &fakeAppASRService{}
	loadStarted := make(chan struct{})
	allowLoad := make(chan struct{})
	var loadCount atomic.Int32
	manager := newASRResourceManager(context.Background(), func(context.Context) (asrResourceLoadResult, error) {
		if loadCount.Add(1) == 1 {
			close(loadStarted)
		}
		<-allowLoad
		return asrResourceLoadResult{
			Service:     service,
			Config:      &asr.Config{},
			IdleTimeout: 17 * time.Second,
		}, nil
	}, clock)

	if status := manager.Status(); status.Loaded || status.Loading || status.Active != 0 || clock.Count() != 0 {
		t.Fatalf("manager performed eager work: status=%+v timers=%d", status, clock.Count())
	}

	const callers = 16
	leases := make(chan *asrResourceLease, callers)
	errorsCh := make(chan error, callers)
	for range callers {
		go func() {
			lease, err := manager.Acquire(context.Background())
			leases <- lease
			errorsCh <- err
		}()
	}
	<-loadStarted
	if got := loadCount.Load(); got != 1 {
		t.Fatalf("concurrent first use loaded %d times，want 1", got)
	}
	close(allowLoad)

	acquired := make([]*asrResourceLease, 0, callers)
	for range callers {
		lease := <-leases
		if err := <-errorsCh; err != nil {
			t.Fatalf("Acquire failed: %v", err)
		}
		acquired = append(acquired, lease)
	}
	status := manager.Status()
	if !status.Loaded || status.Loading || status.Active != callers {
		t.Fatalf("unexpected status after concurrent Acquire: %+v", status)
	}
	if got := clock.Count(); got != 1 {
		t.Fatalf("load should arm one provisional idle timer before waiters acquire，got %d", got)
	}
	if got := clock.Delay(0); got != 17*time.Second {
		t.Fatalf("idle timeout = %v，want 17s", got)
	}

	for _, lease := range acquired {
		lease.Release()
	}
	if status := manager.Status(); !status.IdleArmed || status.Active != 0 {
		t.Fatalf("last release did not arm idle timer: %+v", status)
	}
	shutdownASRResourceManager(t, manager)
	if got := service.closeCount.Load(); got != 1 {
		t.Fatalf("service close count = %d，want 1", got)
	}
}

func TestASRResourceManagerKeepsBusyServiceLoadedThenUnloadsAndReloads(t *testing.T) {
	clock := &manualASRIdleClock{}
	var loadCount atomic.Int32
	var servicesMu sync.Mutex
	var services []*fakeAppASRService
	manager := newASRResourceManager(context.Background(), func(context.Context) (asrResourceLoadResult, error) {
		loadCount.Add(1)
		service := &fakeAppASRService{}
		servicesMu.Lock()
		services = append(services, service)
		servicesMu.Unlock()
		return asrResourceLoadResult{Service: service, IdleTimeout: time.Minute}, nil
	}, clock)

	lease, err := manager.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if clock.Fire(0) {
		t.Fatal("stopped provisional timer unexpectedly fired while service was busy")
	}
	if got := services[0].closeCount.Load(); got != 0 {
		t.Fatalf("busy service was unloaded %d times", got)
	}

	lease.Release()
	if !clock.Fire(1) {
		t.Fatal("idle timer did not fire")
	}
	waitForASRResourceStatus(t, manager, func(status asrResourceStatus) bool {
		return !status.Loaded && !status.IdleArmed
	})
	if got := services[0].closeCount.Load(); got != 1 {
		t.Fatalf("idle service close count = %d，want 1", got)
	}

	lease, err = manager.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := loadCount.Load(); got != 2 {
		t.Fatalf("reload count = %d，want 2", got)
	}
	lease.Release()
	shutdownASRResourceManager(t, manager)
	if got := services[1].closeCount.Load(); got != 1 {
		t.Fatalf("reloaded service close count = %d，want 1", got)
	}
}

func TestASRResourceManagerIgnoresStaleIdleTimerGeneration(t *testing.T) {
	clock := &manualASRIdleClock{}
	service := &fakeAppASRService{}
	manager := newASRResourceManager(context.Background(), func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: service, IdleTimeout: time.Minute}, nil
	}, clock)

	first, err := manager.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	first.Release()
	// Timer 0 was armed at publication and stopped by Acquire．Timer 1 is the
	// first real idle timer．Simulate its callback already racing with Stop．
	clock.MakeStopRace(1)
	second, err := manager.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	second.Release()
	if got := clock.Count(); got != 3 {
		t.Fatalf("timer count = %d，want 3", got)
	}
	if !clock.Fire(1) {
		t.Fatal("stale racing timer did not run")
	}
	if status := manager.Status(); !status.Loaded || status.Active != 0 {
		t.Fatalf("stale timer unloaded current generation: %+v", status)
	}
	if got := service.closeCount.Load(); got != 0 {
		t.Fatalf("stale timer closed service %d times", got)
	}
	if !clock.Fire(2) {
		t.Fatal("current idle timer did not run")
	}
	if got := service.closeCount.Load(); got != 1 {
		t.Fatalf("current timer close count = %d，want 1", got)
	}
	shutdownASRResourceManager(t, manager)
}

func TestASRResourceManagerArmsIdleAfterAllLoadWaitersCancel(t *testing.T) {
	clock := &manualASRIdleClock{}
	service := &fakeAppASRService{}
	loadStarted := make(chan struct{})
	allowLoad := make(chan struct{})
	manager := newASRResourceManager(context.Background(), func(context.Context) (asrResourceLoadResult, error) {
		close(loadStarted)
		<-allowLoad
		return asrResourceLoadResult{Service: service, IdleTimeout: time.Minute}, nil
	}, clock)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(ctx)
		result <- err
	}()
	<-loadStarted
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire error = %v，want context.Canceled", err)
	}
	close(allowLoad)
	waitForASRResourceStatus(t, manager, func(status asrResourceStatus) bool {
		return status.Loaded && !status.Loading && status.Active == 0 && status.IdleArmed
	})
	if clock.Count() != 1 || !clock.Fire(0) {
		t.Fatal("successful orphaned load did not arm its idle timer")
	}
	if got := service.closeCount.Load(); got != 1 {
		t.Fatalf("orphaned service close count = %d，want 1", got)
	}
	shutdownASRResourceManager(t, manager)
}

func TestASRResourceManagerLoadFailureIsNotPublishedAndCanRetry(t *testing.T) {
	clock := &manualASRIdleClock{}
	failedService := &fakeAppASRService{}
	retryService := &fakeAppASRService{}
	wantErr := errors.New("load failed")
	var calls atomic.Int32
	manager := newASRResourceManager(context.Background(), func(context.Context) (asrResourceLoadResult, error) {
		if calls.Add(1) == 1 {
			return asrResourceLoadResult{Service: failedService}, wantErr
		}
		return asrResourceLoadResult{Service: retryService, IdleTimeout: time.Minute}, nil
	}, clock)

	if _, err := manager.Acquire(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("first Acquire error = %v，want %v", err, wantErr)
	}
	waitForASRResourceStatus(t, manager, func(status asrResourceStatus) bool {
		return !status.Loaded && !status.Loading
	})
	if got := failedService.closeCount.Load(); got != 1 {
		t.Fatalf("service returned with load error was closed %d times，want 1", got)
	}

	lease, err := manager.Acquire(context.Background())
	if err != nil {
		t.Fatalf("retry Acquire failed: %v", err)
	}
	if lease.Service() != retryService {
		t.Fatal("retry service was not published")
	}
	lease.Release()
	shutdownASRResourceManager(t, manager)
}

func TestASRResourceManagerCloseRejectsAcquireAndDrainsLease(t *testing.T) {
	clock := &manualASRIdleClock{}
	service := &fakeAppASRService{}
	manager := newASRResourceManager(context.Background(), func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: service, IdleTimeout: time.Minute}, nil
	}, clock)
	lease, err := manager.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	lease, err = manager.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	manager.CloseAdmission()
	if _, err := manager.Acquire(context.Background()); !errors.Is(err, errASRResourceClosed) {
		t.Fatalf("Acquire after CloseAdmission error = %v，want closed", err)
	}
	done := make(chan bool, 1)
	go func() {
		done <- manager.Shutdown(context.Background())
	}()
	select {
	case <-done:
		t.Fatal("Shutdown returned before active lease drained")
	case <-time.After(20 * time.Millisecond):
	}
	lease.Release()
	lease.Release()
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("Shutdown reported timeout")
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not finish after lease release")
	}
	if got := service.closeCount.Load(); got != 1 {
		t.Fatalf("normal Stop/shutdown-style double release closed service %d times，want 1", got)
	}
}

func TestASRResourceManagerShutdownCancelsLoadAndClosesLateResult(t *testing.T) {
	clock := &manualASRIdleClock{}
	service := &fakeAppASRService{}
	loadStarted := make(chan struct{})
	allowLoad := make(chan struct{})
	manager := newASRResourceManager(context.Background(), func(context.Context) (asrResourceLoadResult, error) {
		close(loadStarted)
		<-allowLoad
		return asrResourceLoadResult{Service: service, IdleTimeout: time.Minute}, nil
	}, clock)

	acquireErr := make(chan error, 1)
	go func() {
		_, err := manager.Acquire(context.Background())
		acquireErr <- err
	}()
	<-loadStarted
	manager.CloseAdmission()
	if err := <-acquireErr; !errors.Is(err, errASRResourceClosed) {
		t.Fatalf("Acquire during shutdown error = %v，want closed", err)
	}

	shutdownDone := make(chan bool, 1)
	go func() {
		shutdownDone <- manager.Shutdown(context.Background())
	}()
	select {
	case <-shutdownDone:
		t.Fatal("Shutdown returned before loader completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(allowLoad)
	select {
	case ok := <-shutdownDone:
		if !ok {
			t.Fatal("Shutdown reported timeout")
		}
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not wait for late load cleanup")
	}
	if status := manager.Status(); status.Loaded || status.Loading || status.Active != 0 {
		t.Fatalf("late result was published during shutdown: %+v", status)
	}
	if got := service.closeCount.Load(); got != 1 {
		t.Fatalf("late service close count = %d，want 1", got)
	}
}

func TestASRResourceManagerSurvivesServiceClosePanic(t *testing.T) {
	clock := &manualASRIdleClock{}
	service := &fakeAppASRService{closePanic: true}
	manager := newASRResourceManager(context.Background(), func(context.Context) (asrResourceLoadResult, error) {
		return asrResourceLoadResult{Service: service, IdleTimeout: time.Minute}, nil
	}, clock)
	lease, err := manager.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	lease.Release()
	shutdownASRResourceManager(t, manager)
}
