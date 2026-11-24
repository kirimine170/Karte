package main

import (
	"context"
	"time"

	"karte/internal/googledrive"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Google Drive is an isolated prototype accessed from its own dialog.
func (a *App) GoogleDriveAccount() googledrive.Account {
	return a.googleDriveService().Account()
}

func (a *App) ConnectGoogleDrive() (googledrive.Account, error) {
	account, _, err := a.authorizeGoogleDrive(false)
	return account, err
}

func (a *App) PickGoogleDriveMarkdown() (GoogleDriveSelection, error) {
	account, id, err := a.authorizeGoogleDrive(true)
	return GoogleDriveSelection{Account: account, ID: id}, err
}

type GoogleDriveSelection struct {
	Account googledrive.Account `json:"account"`
	ID      string              `json:"id"`
}

func (a *App) authorizeGoogleDrive(pick bool) (googledrive.Account, string, error) {
	return a.googleDriveService().Authorize(a.ctx, func(url string) {
		runtime.BrowserOpenURL(a.ctx, url)
	}, pick)
}

func (a *App) CancelGoogleDriveAuthorization() {
	a.googleDriveService().CancelAuthorization()
}

func (a *App) DisconnectGoogleDrive() {
	a.googleDriveService().Disconnect()
}

func (a *App) ListGoogleDriveMarkdown() ([]googledrive.File, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().List(ctx)
}

func (a *App) LoadGoogleDriveMarkdown(id string) (googledrive.Document, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().Load(ctx, id)
}

func (a *App) CreateGoogleDriveMarkdown(name, content string) (googledrive.Document, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().Create(ctx, name, content)
}

func (a *App) SaveGoogleDriveMarkdown(id, checksum, content string) (googledrive.Document, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().Save(ctx, id, checksum, content)
}

func (a *App) googleDriveService() *googledrive.Service {
	a.googleDriveOnce.Do(func() { a.googleDrive = googledrive.New() })
	return a.googleDrive
}
