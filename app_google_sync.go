package main

import (
	"context"
	"time"

	"karte/internal/googlesync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) GoogleAccountStatus() (googlesync.Status, error) {
	return a.googleSession.Status()
}

func (a *App) SignInGoogle() (googlesync.Status, error) {
	return a.googleSession.SignIn(a.ctx, func(url string) {
		runtime.BrowserOpenURL(a.ctx, url)
	})
}

func (a *App) SignOutGoogle() error {
	a.googleDriveMu.Lock()
	defer a.googleDriveMu.Unlock()
	return a.googleSession.SignOut()
}

func (a *App) GoogleDecisionSyncStatus() (googlesync.SyncStatus, error) {
	return googlesync.WorkspaceSyncStatus(a.dataDir)
}

func (a *App) SetGoogleDecisionSync(name string, enabled bool) (googlesync.SyncStatus, error) {
	a.googleDriveMu.Lock()
	defer a.googleDriveMu.Unlock()
	return a.googleSession.SetWorkspaceSync(a.dataDir, name, enabled)
}

func (a *App) SyncGoogleDecisions() (googlesync.SyncStatus, error) {
	a.googleDriveMu.Lock()
	defer a.googleDriveMu.Unlock()
	return a.syncGoogleDecisions(a.ctx)
}

func (a *App) syncGoogleDecisions(ctx context.Context) (googlesync.SyncStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	return a.googleSession.SyncWorkspace(ctx, a.dataDir)
}

func (a *App) runGoogleDecisionSync(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if a.googleDriveMu.TryLock() {
			status, err := a.syncGoogleDecisions(ctx)
			a.googleDriveMu.Unlock()
			if err == nil && status.Enabled && ctx.Err() == nil {
				runtime.EventsEmit(ctx, "google-decision-sync", status)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
