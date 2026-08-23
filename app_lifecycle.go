package main

import (
	"context"
	"sync"
)

// appLifecycle owns background work started by App.  It prevents new workers
// from being registered after shutdown starts and gives shutdown one place to
// cancel and wait for existing work.
type appLifecycle struct {
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
	closing bool
}

func (l *appLifecycle) start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
	l.ctx, l.cancel = context.WithCancel(parent)
	l.closing = false
}

func (l *appLifecycle) context() context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ctx == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
	}
	return l.ctx
}

func (l *appLifecycle) goWorker(worker func(context.Context)) bool {
	if worker == nil {
		return false
	}

	l.mu.Lock()
	if l.closing {
		l.mu.Unlock()
		return false
	}
	if l.ctx == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
	}
	ctx := l.ctx
	l.workers.Add(1)
	l.mu.Unlock()

	go func() {
		defer l.workers.Done()
		worker(ctx)
	}()
	return true
}

// beginShutdown atomically closes admission for new workers and broadcasts
// cancellation.  It returns false when shutdown is already in progress.
func (l *appLifecycle) beginShutdown() bool {
	if !l.beginShutdownDrain() {
		return false
	}
	l.cancelShutdownWorkers()
	return true
}

// beginShutdownDrain closes worker admission without canceling work that must
// finish a consistency boundary first，such as an active recording session．
func (l *appLifecycle) beginShutdownDrain() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return false
	}
	l.closing = true
	return true
}

func (l *appLifecycle) cancelShutdownWorkers() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cancel != nil {
		l.cancel()
	}
}

func (l *appLifecycle) wait(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		l.workers.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (l *appLifecycle) isClosing() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closing
}
