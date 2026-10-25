package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/jpeg"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/kirimine170/KarteRenderer/instagram"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func TestPostLabRendererCacheMatchesPinnedDependency(t *testing.T) {
	for _, name := range []string{"renderer.mjs", "preview.css"} {
		want, err := fs.ReadFile(instagram.WebAssets(), name)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join("frontend", "public", "postlab-renderer", name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("stale renderer asset %s: run go run ./cmd/postlab-assets", name)
		}
	}
}

func TestPostLabExportsUseNativeDialogAndPreserveCancellation(t *testing.T) {
	original := postLabSaveDialog
	t.Cleanup(func() { postLabSaveDialog = original })
	root := t.TempDir()
	destination := filepath.Join(root, "draft.json")
	calls := 0
	postLabSaveDialog = func(_ context.Context, opts runtime.SaveDialogOptions) (string, error) {
		calls++
		if opts.DefaultFilename != "karte-instagram-draft.json" {
			t.Fatal(opts.DefaultFilename)
		}
		return destination, nil
	}
	app := &App{ctx: context.Background()}
	draft := `{"schema":"karte.instagram-draft","version":1,"mode":"app","aspect":"0.8","images":[],"caption":"test"}`
	path, err := app.SaveInstagramDraft(draft)
	if err != nil || path != destination {
		t.Fatalf("%s %v", path, err)
	}
	got, _ := os.ReadFile(destination)
	if string(got) != draft {
		t.Fatal("draft changed")
	}
	if _, err := app.SaveInstagramDraft(`{"schema":"other"}`); err == nil || calls != 1 {
		t.Fatal("invalid draft reached file dialog")
	}
	postLabSaveDialog = func(context.Context, runtime.SaveDialogOptions) (string, error) { return "", nil }
	if path, err := app.SaveInstagramDraft(draft); err != nil || path != "" {
		t.Fatal("cancel is not a clean no-op")
	}
	files, _ := os.ReadDir(root)
	if len(files) != 1 {
		t.Fatal("cancel created files")
	}
}

func TestPostLabImageExportValidatesPixelsAndSanitizesFilename(t *testing.T) {
	original := postLabSaveDialog
	t.Cleanup(func() { postLabSaveDialog = original })
	destination := filepath.Join(t.TempDir(), "image.jpg")
	postLabSaveDialog = func(_ context.Context, opts runtime.SaveDialogOptions) (string, error) {
		if opts.DefaultFilename != "image.jpg" {
			t.Fatal(opts.DefaultFilename)
		}
		return destination, nil
	}
	app := &App{ctx: context.Background()}
	if _, err := app.SaveInstagramImage("image.jpg", "not base64"); err == nil {
		t.Fatal("accepted invalid base64")
	}
	var b bytes.Buffer
	jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 1080, 1350)), &jpeg.Options{Quality: 92})
	path, err := app.SaveInstagramImage("../../image.png", base64.StdEncoding.EncodeToString(b.Bytes()))
	if err != nil || path != destination {
		t.Fatalf("%s %v", path, err)
	}
	b.Reset()
	jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 20, 20)), nil)
	if _, err := app.SaveInstagramImage("image.jpg", base64.StdEncoding.EncodeToString(b.Bytes())); err == nil {
		t.Fatal("accepted non-post dimensions")
	}
}
