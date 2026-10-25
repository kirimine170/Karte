package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"

	"github.com/kirimine170/KarteRenderer/instagram"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

var postLabSaveDialog = runtime.SaveFileDialog

// SaveInstagramDraft persists an explicit user export，separate from canonical Markdown．
func (a *App) SaveInstagramDraft(draftJSON string) (string, error) {
	if _, err := instagram.ParseDraft([]byte(draftJSON)); err != nil {
		return "", err
	}
	return a.savePostLabFile("karte-instagram-draft.json", "Instagram draft", "*.json", []byte(draftJSON))
}

// SaveInstagramImage saves renderer-produced pixels through the native save dialog．
func (a *App) SaveInstagramImage(filename, encoded string) (string, error) {
	if len(encoded) > 24*1024*1024 {
		return "", fmt.Errorf("JPEG export exceeds the application limit")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("invalid JPEG data: %w", err)
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 1080 || config.Height < 565 || config.Height > 1440 {
		return "", fmt.Errorf("expected a 1080px-wide Instagram JPEG")
	}
	filename = filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	if filename == "." || filename == "" {
		filename = "instagram.jpg"
	}
	filename = strings.TrimSuffix(filename, filepath.Ext(filename)) + ".jpg"
	return a.savePostLabFile(filename, "JPEG image", "*.jpg", data)
}

func (a *App) savePostLabFile(filename, label, pattern string, data []byte) (string, error) {
	ctx := a.ctx
	if ctx == nil {
		return "", fmt.Errorf("application runtime is not ready")
	}
	return savePostLabExport(ctx, filename, label, pattern, data)
}

func savePostLabExport(ctx context.Context, filename, label, pattern string, data []byte) (string, error) {
	path, err := postLabSaveDialog(ctx, runtime.SaveDialogOptions{Title: label + "を保存", DefaultFilename: filename, Filters: []runtime.FileFilter{{DisplayName: label, Pattern: pattern}}})
	if err != nil || path == "" {
		return "", err
	}
	// Only the native dialog selects a destination．The embedded editor supplies no path．
	f, err := os.CreateTemp(filepath.Dir(path), ".karte-postlab-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
