package main

import (
	"context"
	"errors"
	"time"

	"karte/internal/googledrive"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

const driveFolderSyncInterval = 30 * time.Second

type GoogleDriveFolderSyncStatus struct {
	googledrive.FolderSyncReport
	Enabled   bool   `json:"enabled"`
	FolderID  string `json:"folderId"`
	UpdatedAt string `json:"updatedAt"`
}

func (a *App) GetGoogleDriveFolderSyncStatus() GoogleDriveFolderSyncStatus {
	a.driveFolderSyncMu.Lock()
	defer a.driveFolderSyncMu.Unlock()
	return a.driveFolderSync
}

func (a *App) StartGoogleDriveFolderSync(folderID string) error {
	a.driveFolderSyncMu.Lock()
	defer a.driveFolderSyncMu.Unlock()
	if a.driveFolderSyncStop != nil {
		if a.driveFolderSync.Enabled && a.driveFolderSync.FolderID == folderID {
			return nil
		}
		return errors.New("実行中のフォルダ同期を停止してから開始してください")
	}
	initial, done := context.WithTimeout(a.ctx, 30*time.Second)
	defer done()
	folder, err := googledrive.NewFolderSync(initial, a.dataDir, folderID, a.driveRelationService(), a.googleDriveService())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.driveFolderSyncStop = cancel
	a.driveFolderSync = GoogleDriveFolderSyncStatus{Enabled: true, FolderID: folderID}
	go a.runDriveFolderSync(ctx, folder)
	return nil
}

func (a *App) StopGoogleDriveFolderSync() {
	a.stopDriveFolderSync()
}

func (a *App) stopDriveFolderSync() {
	a.driveFolderSyncMu.Lock()
	defer a.driveFolderSyncMu.Unlock()
	if a.driveFolderSyncStop != nil {
		a.driveFolderSyncStop()
		a.driveFolderSync.Enabled = false
	}
}

func (a *App) runDriveFolderSync(ctx context.Context, folder *googledrive.FolderSync) {
	defer func() {
		_ = folder.Close()
		a.driveFolderSyncMu.Lock()
		a.driveFolderSyncStop = nil
		a.driveFolderSync.Enabled = false
		a.driveFolderSyncMu.Unlock()
	}()
	for ctx.Err() == nil {
		cycle, cancel := context.WithTimeout(ctx, 3*time.Minute)
		report, err := folder.Run(cycle)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			report.Issues++
			report.Message = err.Error()
		}
		a.driveFolderSyncMu.Lock()
		a.driveFolderSync.FolderSyncReport = report
		a.driveFolderSync.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		status := a.driveFolderSync
		a.driveFolderSyncMu.Unlock()
		runtime.EventsEmit(a.ctx, "drive-folder-sync", status)
		timer := time.NewTimer(driveFolderSyncInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}
