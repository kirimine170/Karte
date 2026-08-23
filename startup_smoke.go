package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const (
	startupSmokeReadyFileEnv = "KARTE_STARTUP_SMOKE_READY_FILE"
	startupSmokeReadyPayload = "karte-dom-ready-v1\n"
)

// startupSmokeState joins the two independent readiness signals emitted by
// Wails.  A marker is only written after both backend startup and frontend DOM
// readiness complete successfully.
type startupSmokeState struct {
	mu             sync.Mutex
	enabled        bool
	markerPath     string
	configuration  error
	startupDone    bool
	startupFailure error
	domReady       bool
	finalizing     bool
	completed      bool
	outcome        error
}

type startupSmokeAction struct {
	markerPath string
	failure    error
}

func newStartupSmokeState(markerPath string) *startupSmokeState {
	state := &startupSmokeState{}
	if markerPath == "" {
		return state
	}
	state.enabled = true
	state.markerPath = markerPath
	state.configuration = validateStartupSmokeMarkerPath(markerPath)
	return state
}

func validateStartupSmokeMarkerPath(markerPath string) error {
	if !filepath.IsAbs(markerPath) {
		return fmt.Errorf("%s must be an absolute path: %q", startupSmokeReadyFileEnv, markerPath)
	}
	if filepath.Clean(markerPath) != markerPath {
		return fmt.Errorf("%s must be a clean path: %q", startupSmokeReadyFileEnv, markerPath)
	}
	return nil
}

func (s *startupSmokeState) startupFinished(startupErr error) (startupSmokeAction, bool) {
	if s == nil {
		return startupSmokeAction{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.startupDone {
		return startupSmokeAction{}, false
	}
	s.startupDone = true
	s.startupFailure = startupErr
	return s.actionLocked()
}

func (s *startupSmokeState) frontendReady() (startupSmokeAction, bool) {
	if s == nil {
		return startupSmokeAction{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled || s.domReady {
		return startupSmokeAction{}, false
	}
	s.domReady = true
	return s.actionLocked()
}

func (s *startupSmokeState) actionLocked() (startupSmokeAction, bool) {
	if s.finalizing || !s.startupDone || !s.domReady {
		return startupSmokeAction{}, false
	}
	s.finalizing = true
	failure := s.configuration
	if failure == nil && s.startupFailure != nil {
		failure = fmt.Errorf("application startup failed: %w", s.startupFailure)
	}
	return startupSmokeAction{markerPath: s.markerPath, failure: failure}, true
}

func (s *startupSmokeState) finish(outcome error) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = true
	s.outcome = outcome
}

func (s *startupSmokeState) isEnabled() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

func (s *startupSmokeState) result() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.enabled {
		return nil
	}
	if !s.completed {
		return errors.New("startup smoke exited before startup and DOM readiness completed")
	}
	return s.outcome
}

func (a *App) finishStartup(ctx context.Context, startupErr error) {
	if a == nil {
		return
	}
	action, ready := a.startupSmoke.startupFinished(startupErr)
	a.finalizeStartupSmoke(ctx, action, ready, runtime.Quit)
}

func (a *App) domReady(ctx context.Context) {
	if a == nil {
		return
	}
	action, ready := a.startupSmoke.frontendReady()
	a.finalizeStartupSmoke(ctx, action, ready, runtime.Quit)
}

func (a *App) finalizeStartupSmoke(ctx context.Context, action startupSmokeAction, ready bool, quit func(context.Context)) {
	if !ready {
		return
	}
	outcome := action.failure
	if outcome == nil {
		outcome = writeStartupSmokeMarker(action.markerPath)
	}
	a.startupSmoke.finish(outcome)

	// The existing close handler delegates normal close attempts to the
	// frontend.  Smoke mode has no unsaved document，so arm exactly one close
	// attempt before requesting the same Wails shutdown path used by AllowClose.
	a.allowCloseMu.Lock()
	a.allowCloseFlag = true
	a.allowCloseMu.Unlock()
	quit(ctx)
}

func writeStartupSmokeMarker(markerPath string) (result error) {
	if err := validateStartupSmokeMarkerPath(markerPath); err != nil {
		return err
	}
	parent := filepath.Dir(markerPath)
	if err := validateStartupSmokeMarkerParent(parent); err != nil {
		return err
	}
	if _, err := os.Lstat(markerPath); err == nil {
		return fmt.Errorf("startup smoke marker already exists: %s", markerPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect startup smoke marker: %w", err)
	}

	temporary, err := os.CreateTemp(parent, ".karte-startup-ready-*")
	if err != nil {
		return fmt.Errorf("create startup smoke marker temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	installed := false
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
		if result != nil && installed {
			_ = os.Remove(markerPath)
		}
	}()

	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("set startup smoke marker permissions: %w", err)
	}
	if _, err := temporary.WriteString(startupSmokeReadyPayload); err != nil {
		return fmt.Errorf("write startup smoke marker: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync startup smoke marker: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close startup smoke marker: %w", err)
	}
	// Revalidate immediately before the no-replace hard-link install.  The
	// temporary file and marker share a directory，so the install is atomic.
	if err := validateStartupSmokeMarkerParent(parent); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, markerPath); err != nil {
		return fmt.Errorf("install startup smoke marker without replacement: %w", err)
	}
	installed = true
	if err := syncStartupSmokeDirectory(parent); err != nil {
		return fmt.Errorf("sync startup smoke marker directory: %w", err)
	}
	return nil
}

func validateStartupSmokeMarkerParent(parent string) error {
	info, err := os.Lstat(parent)
	if err != nil {
		return fmt.Errorf("inspect startup smoke marker directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("startup smoke marker parent is not a real directory: %s", parent)
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return fmt.Errorf("resolve startup smoke marker directory: %w", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(parent) {
		return fmt.Errorf("startup smoke marker directory contains a symlink: %s", parent)
	}
	return nil
}

func startupSmokeExitCode(smokeEnabled bool, wailsErr, smokeErr error) int {
	if smokeEnabled && (wailsErr != nil || smokeErr != nil) {
		return 1
	}
	return 0
}
