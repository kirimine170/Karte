package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	karterenderer "github.com/kirimine170/KarteRenderer"
)

func TestExportPDFImageConversionLogSeverity(t *testing.T) {
	originalExport := exportRendererHTMLPDF
	originalEmit := emitPDFExportEvent
	t.Cleanup(func() {
		exportRendererHTMLPDF = originalExport
		emitPDFExportEvent = originalEmit
	})

	var renderedHTML string
	exportRendererHTMLPDF = func(_ context.Context, htmlPath, outputPath string, _ karterenderer.PDFOptions) error {
		content, err := os.ReadFile(htmlPath)
		if err != nil {
			return err
		}
		renderedHTML = string(content)
		return os.WriteFile(outputPath, []byte("%PDF-1.4\n"), 0o644)
	}
	emitPDFExportEvent = func(context.Context, string, ...interface{}) {}

	t.Run("no images is informational", func(t *testing.T) {
		app := newPDFLoggingTestApp(t)
		renderedHTML = ""

		if _, err := app.exportPDFInternal(`<html><body><p>No images</p></body></html>`); err != nil {
			t.Fatalf("export PDF without images: %v", err)
		}

		logs := readPDFExportLog(t, app)
		if strings.Contains(logs, " [ERROR] ") {
			t.Fatalf("normal image-free export recorded an error:\n%s", logs)
		}
		if !strings.Contains(logs, "[INFO] PDF export: DEBUG - No data:image found in converted HTML!") {
			t.Fatalf("image-free diagnostic was not informational:\n%s", logs)
		}
		if renderedHTML == "" {
			t.Fatal("renderer did not receive the image-free HTML")
		}
	})

	t.Run("image conversion succeeds without error", func(t *testing.T) {
		app := newPDFLoggingTestApp(t)
		imageDir := filepath.Join(app.dataDir, "data", "image")
		if err := os.MkdirAll(imageDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(imageDir, "ok.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"></svg>`), 0o644); err != nil {
			t.Fatal(err)
		}
		renderedHTML = ""

		if _, err := app.exportPDFInternal(`<html><body><img src="data/image/ok.svg"></body></html>`); err != nil {
			t.Fatalf("export PDF with image: %v", err)
		}

		logs := readPDFExportLog(t, app)
		if strings.Contains(logs, " [ERROR] ") {
			t.Fatalf("normal image conversion recorded an error:\n%s", logs)
		}
		if strings.Contains(logs, "No data:image found") {
			t.Fatalf("converted image was reported as absent:\n%s", logs)
		}
		if !strings.Contains(renderedHTML, "data:image/svg+xml;base64,") {
			t.Fatalf("renderer did not receive the converted image:\n%s", renderedHTML)
		}
	})

	t.Run("image conversion failure is an error", func(t *testing.T) {
		app := newPDFLoggingTestApp(t)
		imageDir := filepath.Join(app.dataDir, "data", "image")
		if err := os.MkdirAll(imageDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(imageDir, "broken.png"), []byte("not a PNG"), 0o644); err != nil {
			t.Fatal(err)
		}
		renderedHTML = ""

		if _, err := app.exportPDFInternal(`<html><body><img src="data/image/broken.png"></body></html>`); err != nil {
			t.Fatalf("PDF renderer should still receive HTML after an image conversion failure: %v", err)
		}

		logs := readPDFExportLog(t, app)
		if !strings.Contains(logs, "[ERROR] Failed to decode image for PDF export:") {
			t.Fatalf("actual image conversion failure was not recorded as an error:\n%s", logs)
		}
		if got := strings.Count(logs, " [ERROR] "); got != 1 {
			t.Fatalf("image conversion failure recorded %d error lines, want 1:\n%s", got, logs)
		}
		if !strings.Contains(logs, "[INFO] PDF export: DEBUG - No data:image found in converted HTML!") {
			t.Fatalf("post-conversion absence diagnostic was not informational:\n%s", logs)
		}
	})
}

func newPDFLoggingTestApp(t *testing.T) *App {
	t.Helper()
	dataDir := t.TempDir()
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return &App{
		dataDir:     dataDir,
		logFilePath: filepath.Join(logDir, "app.log"),
	}
}

func readPDFExportLog(t *testing.T, app *App) string {
	t.Helper()
	content, err := os.ReadFile(app.logFilePath)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
