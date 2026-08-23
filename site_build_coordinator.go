package main

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	defaultSiteBuildDebounce = 200 * time.Millisecond
	defaultSiteBuildMaxDirty = 1024
)

type siteBuildRequest struct {
	Dirty  []string
	Rescan bool
}

type siteBuildRun func(context.Context, siteBuildRequest) error

type siteBuildTimer interface {
	Chan() <-chan time.Time
	Stop() bool
	Reset(time.Duration) bool
}

type siteBuildClock interface {
	NewTimer(time.Duration) siteBuildTimer
}

type realSiteBuildClock struct{}

type realSiteBuildTimer struct {
	timer *time.Timer
}

func (realSiteBuildClock) NewTimer(delay time.Duration) siteBuildTimer {
	return &realSiteBuildTimer{timer: time.NewTimer(delay)}
}

func (timer *realSiteBuildTimer) Chan() <-chan time.Time {
	return timer.timer.C
}

func (timer *realSiteBuildTimer) Stop() bool {
	return timer.timer.Stop()
}

func (timer *realSiteBuildTimer) Reset(delay time.Duration) bool {
	return timer.timer.Reset(delay)
}

type siteBuildCoordinator struct {
	ctx      context.Context
	run      siteBuildRun
	onError  func(error)
	clock    siteBuildClock
	debounce time.Duration
	maxDirty int
	wake     chan struct{}

	mu      sync.Mutex
	pending map[string]struct{}
	rescan  bool
}

func newSiteBuildCoordinator(
	ctx context.Context,
	run siteBuildRun,
	onError func(error),
	clock siteBuildClock,
	debounce time.Duration,
	maxDirty int,
) *siteBuildCoordinator {
	if ctx == nil {
		ctx = context.Background()
	}
	if clock == nil {
		clock = realSiteBuildClock{}
	}
	if debounce <= 0 {
		debounce = defaultSiteBuildDebounce
	}
	if maxDirty <= 0 {
		maxDirty = defaultSiteBuildMaxDirty
	}
	return &siteBuildCoordinator{
		ctx:      ctx,
		run:      run,
		onError:  onError,
		clock:    clock,
		debounce: debounce,
		maxDirty: maxDirty,
		wake:     make(chan struct{}, 1),
		pending:  make(map[string]struct{}),
	}
}

func (coordinator *siteBuildCoordinator) Schedule(paths ...string) bool {
	if coordinator == nil || coordinator.run == nil || coordinator.ctx.Err() != nil {
		return false
	}

	coordinator.mu.Lock()
	for _, inputPath := range paths {
		path, ok := normalizeSiteBuildDirtyPath(inputPath)
		if !ok {
			continue
		}
		if _, exists := coordinator.pending[path]; exists {
			continue
		}
		if len(coordinator.pending) >= coordinator.maxDirty {
			coordinator.rescan = true
			continue
		}
		coordinator.pending[path] = struct{}{}
	}
	if len(paths) == 0 {
		coordinator.rescan = true
	}
	hasWork := len(coordinator.pending) > 0 || coordinator.rescan
	coordinator.mu.Unlock()
	if !hasWork {
		return false
	}

	select {
	case coordinator.wake <- struct{}{}:
	default:
	}
	return true
}

func (coordinator *siteBuildCoordinator) Run(ctx context.Context) {
	if coordinator == nil || coordinator.run == nil {
		return
	}
	if ctx == nil {
		ctx = coordinator.ctx
	}

	var timer siteBuildTimer
	var timerChannel <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-coordinator.ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case <-coordinator.wake:
			if timer == nil {
				timer = coordinator.clock.NewTimer(coordinator.debounce)
				timerChannel = timer.Chan()
				continue
			}
			stopAndDrainSiteBuildTimer(timer)
			timer.Reset(coordinator.debounce)
			timerChannel = timer.Chan()
		case <-timerChannel:
			timer = nil
			timerChannel = nil
			request, ok := coordinator.takePending()
			if !ok || ctx.Err() != nil || coordinator.ctx.Err() != nil {
				continue
			}
			if err := coordinator.run(ctx, request); err != nil && ctx.Err() == nil && coordinator.ctx.Err() == nil {
				if errors.Is(err, errJobQueueFull) {
					// The manager applies nonblocking backpressure．Restore the dirty
					// request and retry after debounce instead of losing a mutation．
					coordinator.restorePending(request)
				}
				if coordinator.onError != nil {
					coordinator.onError(err)
				}
			}
		}
	}
}

func (coordinator *siteBuildCoordinator) restorePending(request siteBuildRequest) {
	coordinator.mu.Lock()
	if request.Rescan {
		coordinator.rescan = true
	}
	for _, path := range request.Dirty {
		if _, exists := coordinator.pending[path]; exists {
			continue
		}
		if len(coordinator.pending) >= coordinator.maxDirty {
			coordinator.rescan = true
			break
		}
		coordinator.pending[path] = struct{}{}
	}
	coordinator.mu.Unlock()
	select {
	case coordinator.wake <- struct{}{}:
	default:
	}
}

func (coordinator *siteBuildCoordinator) takePending() (siteBuildRequest, bool) {
	coordinator.mu.Lock()
	defer coordinator.mu.Unlock()
	if len(coordinator.pending) == 0 && !coordinator.rescan {
		return siteBuildRequest{}, false
	}
	request := siteBuildRequest{
		Dirty:  make([]string, 0, len(coordinator.pending)),
		Rescan: coordinator.rescan,
	}
	for path := range coordinator.pending {
		request.Dirty = append(request.Dirty, path)
	}
	sort.Strings(request.Dirty)
	coordinator.pending = make(map[string]struct{})
	coordinator.rescan = false
	return request, true
}

func stopAndDrainSiteBuildTimer(timer siteBuildTimer) {
	if timer == nil || timer.Stop() {
		return
	}
	select {
	case <-timer.Chan():
	default:
	}
}

func normalizeSiteBuildDirtyPath(inputPath string) (string, bool) {
	path := filepath.ToSlash(filepath.Clean(strings.TrimSpace(inputPath)))
	path = strings.TrimPrefix(path, "./")
	path = strings.TrimPrefix(path, "/")
	if !strings.HasPrefix(path, "content/") || !strings.EqualFold(filepath.Ext(path), ".md") {
		return "", false
	}
	if path == "content/." || strings.Contains(path, "\\") || strings.HasPrefix(path, "content/../") || strings.Contains(path, "/../") {
		return "", false
	}
	return path, true
}
