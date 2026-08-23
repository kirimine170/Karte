package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestStartupSmokeWaitsForStartupAndDOMReady(t *testing.T) {
	markerPath := startupSmokeTestMarkerPath(t)
	app := &App{startupSmoke: newStartupSmokeState(markerPath)}
	quitCalls := 0
	quit := func(context.Context) { quitCalls++ }

	action, ready := app.startupSmoke.frontendReady()
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)
	if quitCalls != 0 {
		t.Fatalf("quit calls before startup = %d，want 0", quitCalls)
	}
	if _, err := os.Lstat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("marker must not exist before startup succeeds: %v", err)
	}

	action, ready = app.startupSmoke.startupFinished(nil)
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)
	if quitCalls != 1 {
		t.Fatalf("quit calls = %d，want 1", quitCalls)
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != startupSmokeReadyPayload {
		t.Fatalf("marker = %q，want %q", marker, startupSmokeReadyPayload)
	}
	if info, err := os.Stat(markerPath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("marker permissions = %o，want owner-only", info.Mode().Perm())
	}
	if err := app.startupSmoke.result(); err != nil {
		t.Fatalf("smoke result = %v，want success", err)
	}
	if !app.allowCloseFlag {
		t.Fatal("startup smoke must arm one safe close attempt")
	}
}

func TestStartupSmokeFailureNeverCreatesReadyMarker(t *testing.T) {
	markerPath := startupSmokeTestMarkerPath(t)
	app := &App{startupSmoke: newStartupSmokeState(markerPath)}
	quitCalls := 0
	quit := func(context.Context) { quitCalls++ }

	action, ready := app.startupSmoke.startupFinished(errors.New("data initialisation failed"))
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)
	if quitCalls != 0 {
		t.Fatalf("quit calls before DOM ready = %d，want 0", quitCalls)
	}
	action, ready = app.startupSmoke.frontendReady()
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)

	if quitCalls != 1 {
		t.Fatalf("quit calls = %d，want 1", quitCalls)
	}
	if _, err := os.Lstat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("failed startup must not create a marker: %v", err)
	}
	if err := app.startupSmoke.result(); err == nil || !strings.Contains(err.Error(), "data initialisation failed") {
		t.Fatalf("smoke result = %v，want startup failure", err)
	}
}

func TestStartupSmokeConcurrentSignalsFinalizeExactlyOnce(t *testing.T) {
	markerPath := startupSmokeTestMarkerPath(t)
	app := &App{startupSmoke: newStartupSmokeState(markerPath)}
	var quitCalls atomic.Int32
	quit := func(context.Context) { quitCalls.Add(1) }
	var signals sync.WaitGroup
	signals.Add(2)
	go func() {
		defer signals.Done()
		action, ready := app.startupSmoke.startupFinished(nil)
		app.finalizeStartupSmoke(context.Background(), action, ready, quit)
	}()
	go func() {
		defer signals.Done()
		action, ready := app.startupSmoke.frontendReady()
		app.finalizeStartupSmoke(context.Background(), action, ready, quit)
	}()
	signals.Wait()

	if calls := quitCalls.Load(); calls != 1 {
		t.Fatalf("quit calls = %d，want 1", calls)
	}
	if marker, err := os.ReadFile(markerPath); err != nil {
		t.Fatal(err)
	} else if string(marker) != startupSmokeReadyPayload {
		t.Fatalf("marker = %q", marker)
	}
}

func TestWriteStartupSmokeMarkerConcurrentInstallDoesNotReplace(t *testing.T) {
	markerPath := startupSmokeTestMarkerPath(t)
	results := make(chan error, 2)
	var writers sync.WaitGroup
	writers.Add(2)
	for range 2 {
		go func() {
			defer writers.Done()
			results <- writeStartupSmokeMarker(markerPath)
		}()
	}
	writers.Wait()
	close(results)
	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent install successes=%d failures=%d，want 1 and 1", successes, failures)
	}
	if marker, err := os.ReadFile(markerPath); err != nil {
		t.Fatal(err)
	} else if string(marker) != startupSmokeReadyPayload {
		t.Fatalf("installed marker = %q", marker)
	}
}

func TestStartupSmokeInvalidConfigurationQuitsAndFails(t *testing.T) {
	app := &App{startupSmoke: newStartupSmokeState("relative/ready.marker")}
	quitCalls := 0
	quit := func(context.Context) { quitCalls++ }
	action, ready := app.startupSmoke.startupFinished(nil)
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)
	action, ready = app.startupSmoke.frontendReady()
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)

	if quitCalls != 1 {
		t.Fatalf("quit calls = %d，want 1", quitCalls)
	}
	if err := app.startupSmoke.result(); err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("smoke result = %v，want absolute path failure", err)
	}
}

func TestStartupSmokeMarkerInstallFailureQuitsAndPropagates(t *testing.T) {
	markerPath := startupSmokeTestMarkerPath(t)
	if err := os.WriteFile(markerPath, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := &App{startupSmoke: newStartupSmokeState(markerPath)}
	quitCalls := 0
	quit := func(context.Context) { quitCalls++ }

	action, ready := app.startupSmoke.startupFinished(nil)
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)
	action, ready = app.startupSmoke.frontendReady()
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)

	if quitCalls != 1 {
		t.Fatalf("quit calls = %d，want 1", quitCalls)
	}
	if err := app.startupSmoke.result(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("smoke result = %v，want existing marker failure", err)
	}
	if code := startupSmokeExitCode(true, nil, app.startupSmoke.result()); code != 1 {
		t.Fatalf("marker install failure exit code = %d，want 1", code)
	}
	if contents, err := os.ReadFile(markerPath); err != nil {
		t.Fatal(err)
	} else if string(contents) != "do not replace" {
		t.Fatalf("existing marker was replaced: %q", contents)
	}
}

func TestWriteStartupSmokeMarkerRejectsExistingFileAndSymlink(t *testing.T) {
	markerPath := startupSmokeTestMarkerPath(t)
	if err := os.WriteFile(markerPath, []byte("do not replace"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeStartupSmokeMarker(markerPath); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing marker error = %v", err)
	}
	contents, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "do not replace" {
		t.Fatalf("existing marker was replaced: %q", contents)
	}

	if runtime.GOOS == "windows" {
		return
	}
	symlinkPath := markerPath + ".link"
	if err := os.Symlink(markerPath, symlinkPath); err != nil {
		t.Fatal(err)
	}
	if err := writeStartupSmokeMarker(symlinkPath); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("symlink marker error = %v", err)
	}
}

func TestWriteStartupSmokeMarkerRejectsSymlinkedParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	realRoot := t.TempDir()
	realParent := filepath.Join(realRoot, "marker parent")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	linkedRoot := filepath.Join(t.TempDir(), "linked root")
	if err := os.Symlink(realRoot, linkedRoot); err != nil {
		t.Fatal(err)
	}
	markerPath := filepath.Join(linkedRoot, "marker parent", "ready.marker")
	if err := writeStartupSmokeMarker(markerPath); err == nil || !strings.Contains(err.Error(), "contains a symlink") {
		t.Fatalf("symlinked parent error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(realParent, "ready.marker")); !os.IsNotExist(err) {
		t.Fatalf("marker was written through a symlinked parent: %v", err)
	}
}

func TestStartupSmokeDisabledLeavesProductionLifecycleUnchanged(t *testing.T) {
	app := &App{startupSmoke: newStartupSmokeState("")}
	quitCalls := 0
	quit := func(context.Context) { quitCalls++ }
	action, ready := app.startupSmoke.startupFinished(nil)
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)
	action, ready = app.startupSmoke.frontendReady()
	app.finalizeStartupSmoke(context.Background(), action, ready, quit)

	if app.startupSmoke.isEnabled() {
		t.Fatal("startup smoke unexpectedly enabled")
	}
	if quitCalls != 0 || app.allowCloseFlag {
		t.Fatalf("disabled smoke changed close lifecycle: quits=%d allowClose=%v", quitCalls, app.allowCloseFlag)
	}
	if err := app.startupSmoke.result(); err != nil {
		t.Fatalf("disabled smoke result = %v", err)
	}
	if code := startupSmokeExitCode(false, errors.New("normal Wails error"), nil); code != 0 {
		t.Fatalf("normal production exit code changed to %d", code)
	}
}

func TestStartupSmokeExitCodeFailsClosed(t *testing.T) {
	if code := startupSmokeExitCode(true, errors.New("Wails failed"), nil); code != 1 {
		t.Fatalf("Wails failure exit code = %d，want 1", code)
	}
	if code := startupSmokeExitCode(true, nil, errors.New("marker failed")); code != 1 {
		t.Fatalf("marker failure exit code = %d，want 1", code)
	}
	state := newStartupSmokeState(startupSmokeTestMarkerPath(t))
	if err := state.result(); err == nil || !strings.Contains(err.Error(), "before startup and DOM readiness") {
		t.Fatalf("incomplete smoke result = %v", err)
	}
}

func TestWailsOptionsConnectStartupSmokeToDOMReady(t *testing.T) {
	app := &App{}
	options := newWailsAppOptions(app)
	if options.OnDomReady == nil {
		t.Fatal("OnDomReady is not connected")
	}
}

func TestStartupSmokeArmsExactlyOneSafeBeforeClose(t *testing.T) {
	app := &App{allowCloseFlag: true}
	options := newWailsAppOptions(app)
	if prevent := options.OnBeforeClose(context.Background()); prevent {
		t.Fatal("startup smoke close was unexpectedly prevented")
	}
	app.allowCloseMu.Lock()
	defer app.allowCloseMu.Unlock()
	if app.allowCloseFlag {
		t.Fatal("startup smoke close permission was not consumed")
	}
}

func startupSmokeTestMarkerPath(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(directory, "DOM ready.marker")
}
