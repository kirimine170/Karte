package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"karte/internal/asr"
)

const defaultASRIdleTimeout = 5 * time.Minute

var (
	errASRResourceClosed   = errors.New("ASR resource manager is closed")
	errASRResourceDisabled = errors.New("ASR is disabled")
)

type appASRService interface {
	Close()
	TranscribeFile(context.Context, string, func(string, int, int, float64)) (string, error)
	ProcessSamples([]float32) (string, error)
}

type asrResourceLoadResult struct {
	Service     appASRService
	Config      *asr.Config
	IdleTimeout time.Duration
}

type asrResourceLoader func(context.Context) (asrResourceLoadResult, error)

type asrIdleTimer interface {
	Stop() bool
}

type asrIdleClock interface {
	AfterFunc(time.Duration, func()) asrIdleTimer
}

type realASRIdleClock struct{}

func (realASRIdleClock) AfterFunc(delay time.Duration, callback func()) asrIdleTimer {
	return time.AfterFunc(delay, callback)
}

type asrLoadAttempt struct {
	done chan struct{}
	err  error
}

type asrResourceStatus struct {
	Loaded     bool
	Loading    bool
	Active     int
	Closing    bool
	IdleArmed  bool
	Generation uint64
}

type asrResourceManager struct {
	mu sync.Mutex

	ctx              context.Context
	cancel           context.CancelFunc
	stopParentCancel func() bool
	loader           asrResourceLoader
	clock            asrIdleClock

	service     appASRService
	config      *asr.Config
	idleTimeout time.Duration
	active      int
	loading     *asrLoadAttempt
	closing     bool
	generation  uint64
	idleTimer   asrIdleTimer

	timerCallbacks int
	activeCloses   int
	changed        chan struct{}
}

func newASRResourceManager(parent context.Context, loader asrResourceLoader, clock asrIdleClock) *asrResourceManager {
	if parent == nil {
		parent = context.Background()
	}
	if clock == nil {
		clock = realASRIdleClock{}
	}
	ctx, cancel := context.WithCancel(parent)
	manager := &asrResourceManager{
		ctx:         ctx,
		cancel:      cancel,
		loader:      loader,
		clock:       clock,
		idleTimeout: defaultASRIdleTimeout,
		changed:     make(chan struct{}),
	}
	manager.stopParentCancel = context.AfterFunc(parent, manager.CloseAdmission)
	return manager
}

type asrResourceLease struct {
	manager *asrResourceManager
	service appASRService
	config  *asr.Config
	once    sync.Once
}

func (lease *asrResourceLease) Service() appASRService {
	if lease == nil {
		return nil
	}
	return lease.service
}

func (lease *asrResourceLease) Config() *asr.Config {
	if lease == nil {
		return nil
	}
	return lease.config
}

func (lease *asrResourceLease) Release() {
	if lease == nil || lease.manager == nil {
		return
	}
	lease.once.Do(func() {
		lease.manager.release(lease.service)
	})
}

func (manager *asrResourceManager) Acquire(ctx context.Context) (*asrResourceLease, error) {
	if manager == nil {
		return nil, errASRResourceClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		manager.mu.Lock()
		if manager.closing || manager.ctx.Err() != nil {
			manager.mu.Unlock()
			return nil, errASRResourceClosed
		}
		if manager.service != nil {
			manager.stopIdleTimerLocked()
			manager.active++
			lease := &asrResourceLease{
				manager: manager,
				service: manager.service,
				config:  manager.config,
			}
			manager.notifyLocked()
			manager.mu.Unlock()
			return lease, nil
		}
		if manager.activeCloses > 0 {
			changed := manager.changed
			manager.mu.Unlock()
			if err := waitForASRStateChange(ctx, manager.ctx, changed); err != nil {
				return nil, err
			}
			continue
		}
		attempt := manager.loading
		if attempt == nil {
			if manager.loader == nil {
				manager.mu.Unlock()
				return nil, errors.New("ASR resource loader is not configured")
			}
			attempt = &asrLoadAttempt{done: make(chan struct{})}
			manager.loading = attempt
			manager.notifyLocked()
			go manager.runLoad(attempt)
		}
		manager.mu.Unlock()

		select {
		case <-attempt.done:
			if attempt.err != nil {
				return nil, attempt.err
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-manager.ctx.Done():
			return nil, errASRResourceClosed
		}
	}
}

func waitForASRStateChange(callerContext, managerContext context.Context, changed <-chan struct{}) error {
	select {
	case <-changed:
		return nil
	case <-callerContext.Done():
		return callerContext.Err()
	case <-managerContext.Done():
		return errASRResourceClosed
	}
}

func (manager *asrResourceManager) runLoad(attempt *asrLoadAttempt) {
	var result asrResourceLoadResult
	var loadErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				loadErr = fmt.Errorf("ASR resource loader panicked: %v", recovered)
			}
		}()
		result, loadErr = manager.loader(manager.ctx)
	}()
	if loadErr == nil && result.Service == nil {
		loadErr = errors.New("ASR resource loader returned a nil service")
	}
	if result.IdleTimeout <= 0 {
		result.IdleTimeout = defaultASRIdleTimeout
	}

	manager.mu.Lock()
	if manager.loading != attempt {
		manager.mu.Unlock()
		if result.Service != nil {
			result.Service.Close()
		}
		return
	}
	if manager.closing || manager.ctx.Err() != nil {
		attempt.err = errASRResourceClosed
		manager.loading = nil
		if result.Service != nil {
			manager.activeCloses++
		}
		close(attempt.done)
		manager.notifyLocked()
		manager.mu.Unlock()
		if result.Service != nil {
			manager.closeResource(result.Service)
		}
		return
	}
	if loadErr != nil {
		attempt.err = loadErr
		manager.loading = nil
		if result.Service != nil {
			manager.activeCloses++
		}
		close(attempt.done)
		manager.notifyLocked()
		manager.mu.Unlock()
		if result.Service != nil {
			manager.closeResource(result.Service)
		}
		return
	}

	manager.service = result.Service
	manager.config = result.Config
	manager.idleTimeout = result.IdleTimeout
	manager.loading = nil
	close(attempt.done)
	manager.scheduleIdleTimerLocked()
	manager.notifyLocked()
	manager.mu.Unlock()
}

func (manager *asrResourceManager) release(service appASRService) {
	manager.mu.Lock()
	if manager.service != service || manager.active <= 0 {
		manager.mu.Unlock()
		return
	}
	manager.active--
	var detached appASRService
	if manager.active == 0 {
		if manager.closing {
			detached = manager.detachResourceLocked()
		} else {
			manager.scheduleIdleTimerLocked()
		}
	}
	manager.notifyLocked()
	manager.mu.Unlock()
	if detached != nil {
		manager.closeResource(detached)
	}
}

func (manager *asrResourceManager) scheduleIdleTimerLocked() {
	if manager.closing || manager.service == nil || manager.active != 0 || manager.idleTimer != nil {
		return
	}
	manager.generation++
	generation := manager.generation
	service := manager.service
	manager.timerCallbacks++
	manager.idleTimer = manager.clock.AfterFunc(manager.idleTimeout, func() {
		manager.idleTimerFired(generation, service)
	})
}

func (manager *asrResourceManager) stopIdleTimerLocked() {
	if manager.idleTimer == nil {
		return
	}
	manager.generation++
	if manager.idleTimer.Stop() {
		manager.timerCallbacks--
	}
	manager.idleTimer = nil
	manager.notifyLocked()
}

func (manager *asrResourceManager) idleTimerFired(generation uint64, service appASRService) {
	manager.mu.Lock()
	var detached appASRService
	if !manager.closing && manager.generation == generation && manager.service == service && manager.active == 0 {
		manager.idleTimer = nil
		detached = manager.detachResourceLocked()
	}
	manager.notifyLocked()
	manager.mu.Unlock()
	if detached != nil {
		manager.closeResource(detached)
	}
	manager.mu.Lock()
	manager.timerCallbacks--
	manager.notifyLocked()
	manager.mu.Unlock()
}

func (manager *asrResourceManager) detachResourceLocked() appASRService {
	service := manager.service
	if service == nil {
		return nil
	}
	manager.stopIdleTimerLocked()
	manager.service = nil
	manager.config = nil
	manager.generation++
	manager.activeCloses++
	return service
}

func (manager *asrResourceManager) closeResource(service appASRService) {
	defer func() {
		_ = recover()
		manager.mu.Lock()
		if manager.activeCloses > 0 {
			manager.activeCloses--
		}
		manager.notifyLocked()
		manager.mu.Unlock()
	}()
	if service != nil {
		service.Close()
	}
}

func (manager *asrResourceManager) CloseAdmission() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	if manager.closing {
		manager.mu.Unlock()
		return
	}
	manager.closing = true
	manager.cancel()
	manager.stopIdleTimerLocked()
	manager.notifyLocked()
	stopParentCancel := manager.stopParentCancel
	manager.stopParentCancel = nil
	manager.mu.Unlock()
	if stopParentCancel != nil {
		stopParentCancel()
	}
}

func (manager *asrResourceManager) Shutdown(ctx context.Context) bool {
	if manager == nil {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	manager.CloseAdmission()
	for {
		manager.mu.Lock()
		var detached appASRService
		if manager.active == 0 && manager.loading == nil && manager.service != nil {
			detached = manager.detachResourceLocked()
		}
		complete := manager.active == 0 && manager.loading == nil && manager.service == nil &&
			manager.activeCloses == 0 && manager.timerCallbacks == 0
		changed := manager.changed
		manager.mu.Unlock()
		if detached != nil {
			manager.closeResource(detached)
			continue
		}
		if complete {
			return true
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return false
		}
	}
}

func (manager *asrResourceManager) Status() asrResourceStatus {
	if manager == nil {
		return asrResourceStatus{}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return asrResourceStatus{
		Loaded:     manager.service != nil,
		Loading:    manager.loading != nil,
		Active:     manager.active,
		Closing:    manager.closing,
		IdleArmed:  manager.idleTimer != nil,
		Generation: manager.generation,
	}
}

func (manager *asrResourceManager) notifyLocked() {
	close(manager.changed)
	manager.changed = make(chan struct{})
}
