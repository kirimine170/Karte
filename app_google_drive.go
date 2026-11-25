package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"karte/internal/googledrive"

	"github.com/wailsapp/wails/v2/pkg/runtime"
	"gopkg.in/yaml.v3"
)

// Google Drive is an isolated prototype accessed from its own dialog.
func (a *App) GoogleDriveAccount() googledrive.Account {
	return a.googleDriveService().Account()
}

func (a *App) ConnectGoogleDrive() (googledrive.Account, error) {
	account, _, err := a.authorizeGoogleDrive(false)
	return account, err
}

func (a *App) PickGoogleDriveFile() (GoogleDriveSelection, error) {
	account, id, err := a.authorizeGoogleDrive(true)
	return GoogleDriveSelection{Account: account, ID: id}, err
}

type GoogleDriveSelection struct {
	Account googledrive.Account `json:"account"`
	ID      string              `json:"id"`
}

func (a *App) authorizeGoogleDrive(pick bool) (googledrive.Account, string, error) {
	account, id, err := a.googleDriveService().Authorize(a.ctx, func(url string) {
		runtime.BrowserOpenURL(a.ctx, url)
	}, pick)
	if err == nil {
		relations := a.driveRelationService()
		if reader := relations.Account(); reader.Email != "" && !reader.SameUser(account) {
			a.stopDriveFolderSync()
			relations.Disconnect()
		}
	}
	return account, id, err
}

func (a *App) CancelGoogleDriveAuthorization() {
	a.googleDriveService().CancelAuthorization()
}

func (a *App) DisconnectGoogleDrive() {
	a.stopDriveFolderSync()
	a.googleDriveService().Disconnect()
	a.driveRelationService().Disconnect()
}

func (a *App) ListGoogleDriveFiles() ([]googledrive.File, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().List(ctx)
}

func (a *App) OpenGoogleDriveFolder() error {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	id, err := a.googleDriveService().Folder(ctx)
	if err != nil {
		return err
	}
	runtime.BrowserOpenURL(a.ctx, "https://drive.google.com/drive/folders/"+id)
	return nil
}

func (a *App) LoadGoogleDriveFile(id string) (googledrive.Document, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().Load(ctx, id)
}

func (a *App) CreateGoogleDriveFile(kind, name, content string) (googledrive.Document, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().Create(ctx, googledrive.Kind(kind), name, content)
}

func (a *App) SaveGoogleDriveFile(id, checksum, content string) (googledrive.Document, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().Save(ctx, id, checksum, content)
}

func (a *App) googleDriveService() *googledrive.Service {
	a.googleDriveOnce.Do(func() { a.googleDrive = googledrive.New() })
	return a.googleDrive
}

func (a *App) driveRelationService() *googledrive.RelationIndex {
	a.driveRelationsOnce.Do(func() { a.driveRelations = googledrive.NewRelationIndex() })
	return a.driveRelations
}

func (a *App) GoogleDriveSearchAccount() googledrive.Account {
	return a.driveRelationService().Account()
}

func (a *App) ConnectGoogleDriveSearch() (googledrive.Account, error) {
	return a.driveRelationService().Authorize(a.ctx, func(url string) {
		runtime.BrowserOpenURL(a.ctx, url)
	}, a.googleDriveService().Account())
}

func (a *App) CancelGoogleDriveSearchAuthorization() {
	a.driveRelationService().CancelAuthorization()
}

func (a *App) DisconnectGoogleDriveSearch() {
	a.stopDriveFolderSync()
	a.driveRelationService().Disconnect()
}

func (a *App) ListGoogleDriveSearchFolders() ([]googledrive.RelationFolder, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
	defer cancel()
	return a.driveRelationService().Folders(ctx)
}

func (a *App) RefreshGoogleDriveRelations(folderID string) (googledrive.RelationSnapshot, error) {
	ctx, cancel := context.WithTimeout(a.ctx, 90*time.Second)
	defer cancel()
	return a.driveRelationService().Refresh(ctx, folderID)
}

// Copy a user-selected library image along with its Karte sidecar tags.
func (a *App) CopyKarteImageToDrive(imagePath string) (googledrive.Document, error) {
	rel := filepath.FromSlash(imagePath)
	_, supported := supportedImageExt[strings.ToLower(filepath.Ext(rel))]
	if !supported || !filepath.IsLocal(rel) || filepath.Clean(rel) != rel {
		return googledrive.Document{}, errors.New("Karteの画像を選んでください")
	}
	inLibrary := false
	for _, item := range a.GetImageList() {
		if item.Path == filepath.ToSlash(rel) {
			inLibrary = true
			break
		}
	}
	if !inLibrary {
		return googledrive.Document{}, errors.New("Karteの画像が見つかりません")
	}
	root, err := os.OpenRoot(a.dataDir)
	if err != nil {
		return googledrive.Document{}, err
	}
	defer root.Close()
	file, err := root.Open(rel)
	if err != nil {
		return googledrive.Document{}, err
	}
	defer file.Close()
	const maxPhotoBytes = 5 << 20
	content, err := io.ReadAll(io.LimitReader(file, maxPhotoBytes+1))
	if err != nil || len(content) > maxPhotoBytes {
		return googledrive.Document{}, errors.New("5 MiB以下の写真を選んでください")
	}
	var tags []string
	sidecar, err := root.Open(filepath.FromSlash(metadataPathFromImage(filepath.ToSlash(rel))))
	switch {
	case err == nil:
		defer sidecar.Close()
		const maxSidecarBytes = 1 << 20
		data, readErr := io.ReadAll(io.LimitReader(sidecar, maxSidecarBytes+1))
		var metadata map[string]interface{}
		if readErr != nil || len(data) > maxSidecarBytes || yaml.Unmarshal(data, &metadata) != nil {
			return googledrive.Document{}, errors.New("写真の既存メタデータを読み取れません")
		}
		tags = a.extractTagsFromMetadata(metadata)
	case !errors.Is(err, os.ErrNotExist):
		return googledrive.Document{}, errors.New("写真の既存メタデータを読み取れません")
	}
	ctx, cancel := context.WithTimeout(a.ctx, 45*time.Second)
	defer cancel()
	return a.googleDriveService().CreateWithTags(ctx, googledrive.Photo, filepath.Base(rel), base64.StdEncoding.EncodeToString(content), tags)
}
