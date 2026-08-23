package main

import (
	"context"
	"testing"
	"time"
)

func TestAppLifecycleCancelsWaitsAndCanRestart(t *testing.T) {
	var lifecycle appLifecycle
	lifecycle.start(context.Background())

	started := make(chan struct{})
	stopped := make(chan struct{})
	if !lifecycle.goWorker(func(ctx context.Context) {
		close(started)
		<-ctx.Done()
		close(stopped)
	}) {
		t.Fatal("worker was rejected before shutdown")
	}
	<-started

	if !lifecycle.beginShutdown() {
		t.Fatal("first shutdown did not begin")
	}
	if lifecycle.goWorker(func(context.Context) {}) {
		t.Fatal("worker was accepted after shutdown began")
	}
	waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !lifecycle.wait(waitCtx) {
		t.Fatal("worker did not stop after cancellation")
	}
	select {
	case <-stopped:
	default:
		t.Fatal("worker completion was not observed")
	}
	if lifecycle.beginShutdown() {
		t.Fatal("second shutdown unexpectedly began")
	}

	lifecycle.start(context.Background())
	restarted := make(chan struct{})
	if !lifecycle.goWorker(func(context.Context) { close(restarted) }) {
		t.Fatal("worker was rejected after lifecycle restart")
	}
	select {
	case <-restarted:
	case <-time.After(time.Second):
		t.Fatal("restarted worker did not run")
	}
	if !lifecycle.beginShutdown() {
		t.Fatal("shutdown did not begin after restart")
	}
	restartWaitCtx, restartCancel := context.WithTimeout(context.Background(), time.Second)
	defer restartCancel()
	if !lifecycle.wait(restartWaitCtx) {
		t.Fatal("restarted lifecycle did not drain")
	}
}

func TestAppLifecycleRepeatedStartShutdownDrainsEveryWorker(t *testing.T) {
	var lifecycle appLifecycle
	for cycle := 0; cycle < 20; cycle++ {
		lifecycle.start(context.Background())
		stopped := make(chan struct{})
		if !lifecycle.goWorker(func(ctx context.Context) {
			<-ctx.Done()
			close(stopped)
		}) {
			t.Fatalf("cycle %d rejected worker", cycle)
		}
		if !lifecycle.beginShutdown() {
			t.Fatalf("cycle %d did not begin shutdown", cycle)
		}
		waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		if !lifecycle.wait(waitCtx) {
			cancel()
			t.Fatalf("cycle %d left a worker running", cycle)
		}
		cancel()
		select {
		case <-stopped:
		default:
			t.Fatalf("cycle %d worker completion was not observed", cycle)
		}
	}
}

func TestAppShutdownStopsRecordingAndBackgroundWorkers(t *testing.T) {
	app := NewAppWithFileSystem(OSFileSystem{})
	app.recordingStopCh = make(chan struct{})
	app.isRecording = true
	manager := app.getJobManager()
	if manager == nil {
		t.Fatal("managed background workers were not started")
	}
	managedStarted := make(chan struct{})
	managed := manager.Submit(testManagedJob("shutdown-test", "managed", jobPriorityNormal, func(ctx context.Context) error {
		close(managedStarted)
		<-ctx.Done()
		return ctx.Err()
	}))
	requireJobAdmission(t, managed, jobAccepted)
	select {
	case <-managedStarted:
	case <-time.After(time.Second):
		t.Fatal("managed background job did not start")
	}

	recordingStopped := make(chan struct{})
	app.recordingWg.Add(1)
	if !app.lifecycle.goWorker(func(context.Context) {
		defer app.recordingWg.Done()
		<-app.recordingStopCh
		close(recordingStopped)
	}) {
		t.Fatal("recording worker was not started")
	}

	backgroundStopped := make(chan struct{})
	if !app.lifecycle.goWorker(func(ctx context.Context) {
		<-ctx.Done()
		close(backgroundStopped)
	}) {
		t.Fatal("background worker was not started")
	}

	app.shutdown(context.Background())
	app.shutdown(context.Background()) // idempotent

	select {
	case <-recordingStopped:
	default:
		t.Fatal("recording worker was not stopped")
	}
	select {
	case <-backgroundStopped:
	default:
		t.Fatal("background worker was not cancelled")
	}
	if app.isRecording {
		t.Fatal("recording state remained active")
	}
	if app.recordingStopCh != nil {
		t.Fatal("recording stop channel was not released")
	}
	if stats := manager.Stats(); !stats.Closing || stats.Pending != 0 || stats.Running != 0 || stats.LiveWorkers != 0 {
		t.Fatalf("managed job queue did not drain: %+v", stats)
	}
	if app.lifecycle.goWorker(func(context.Context) {}) {
		t.Fatal("worker was accepted after app shutdown")
	}
}

func TestWailsOptionsWireShutdown(t *testing.T) {
	app := NewAppWithFileSystem(OSFileSystem{})
	opts := newWailsAppOptions(app)
	if opts.OnShutdown == nil {
		t.Fatal("Wails OnShutdown is not wired")
	}
}
