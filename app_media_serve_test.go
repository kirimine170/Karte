package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMediaHandlerServesConfinedRangeWithSecurityHeaders(t *testing.T) {
	app := newMediaServeTestApp(t)
	pdf := []byte("%PDF-1.7\n0123456789\n%%EOF\n")
	writeMediaServeFixture(t, app.dataDir, "content/report.pdf", pdf)

	request := httptest.NewRequest(http.MethodGet, "/pdf/content/report.pdf", nil)
	request.Header.Set("Range", "bytes=0-3")
	recorder := httptest.NewRecorder()
	app.createAssetHandler().ServeHTTP(recorder, request)

	response := recorder.Result()
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d，want 206", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "%PDF" {
		t.Fatalf("range body = %q，want %%PDF", body)
	}
	if got := response.Header.Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := response.Header.Get("Accept-Ranges"); got != "bytes" {
		t.Fatalf("Accept-Ranges = %q", got)
	}
	if got := response.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q", got)
	}
}

func TestMediaHandlerRejectsTraversalAbsoluteSVGAndMagicMismatch(t *testing.T) {
	app := newMediaServeTestApp(t)
	writeMediaServeFixture(t, app.dataDir, "content/report.pdf", []byte("%PDF-1.7\n%%EOF\n"))
	writeMediaServeFixture(t, app.dataDir, "content/not-pdf.pdf", []byte("plain text"))
	writeMediaServeFixture(t, app.dataDir, "data/image/active.svg", []byte(`<svg onload="alert(1)"></svg>`))
	writeMediaServeFixture(t, app.dataDir, "secret.pdf", []byte("%PDF-1.7\nsecret\n"))

	tests := []string{
		"/pdf//etc/passwd",
		"/pdf/content/../secret.pdf",
		"/pdf/content/%2e%2e/secret.pdf",
		"/pdf/content/%252e%252e/secret.pdf",
		"/pdf/content%2f..%2fsecret.pdf",
		"/pdf/content%252f..%252fsecret.pdf",
		"/pdf%252fcontent%252f..%252fsecret.pdf",
		"/pdf/content/%5c..%5csecret.pdf",
		"/pdf/content/not-pdf.pdf",
		"/image/data/image/active.svg",
		"/audio/content/report.pdf",
	}
	for _, target := range tests {
		t.Run(strings.ReplaceAll(target, "/", "_"), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, target, nil)
			recorder := httptest.NewRecorder()
			app.createAssetHandler().ServeHTTP(recorder, request)
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("%s status = %d，want 404，body=%q", target, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestMediaHandlerRejectsSymlinkEscapeAndNonRegularFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows hosts")
	}
	app := newMediaServeTestApp(t)
	outside := filepath.Join(t.TempDir(), "outside.pdf")
	if err := os.WriteFile(outside, []byte("%PDF-1.7\noutside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(app.dataDir, "content", "escape.pdf")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(app.dataDir, "content", "directory.pdf"), 0o700); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{"/pdf/content/escape.pdf", "/pdf/content/directory.pdf"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		recorder := httptest.NewRecorder()
		app.createAssetHandler().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d，want 404", target, recorder.Code)
		}
	}
}

func TestMediaHandlerRejectsAncestorDirectorySymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows hosts")
	}
	app := newMediaServeTestApp(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "report.pdf"), []byte("%PDF-1.7\noutside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(app.dataDir, "content", "escaped-directory")); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/pdf/content/escaped-directory/report.pdf", nil)
	recorder := httptest.NewRecorder()
	app.createAssetHandler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d，want 404", recorder.Code)
	}
}

func TestOpenConfinedMediaFileRejectsPathSwapAfterInspection(t *testing.T) {
	app := newMediaServeTestApp(t)
	writeMediaServeFixture(t, app.dataDir, "content/report.pdf", []byte("%PDF-1.7\noriginal\n"))

	_, _, err := openConfinedMediaFileWithHooks(app.dataDir, "content/report.pdf", mediaFileOpenHooks{
		afterInitialLstat: func(root *os.Root, path string, _ os.FileInfo) error {
			if err := root.Rename(path, "content/inspected.pdf"); err != nil {
				return err
			}
			return root.WriteFile(path, []byte("%PDF-1.7\nreplacement\n"), 0o600)
		},
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("path replacement error = %v，want SameFile mismatch", err)
	}
}

func TestMediaFileURLsRequireAllowedConfinedFilesAndEscapeNames(t *testing.T) {
	app := newMediaServeTestApp(t)
	writeMediaServeFixture(t, app.dataDir, "content/日本 語.pdf", []byte("%PDF-1.7\n%%EOF\n"))
	writeMediaServeFixture(t, app.dataDir, "data/image/photo.png", []byte("\x89PNG\r\n\x1a\nfixture"))
	writeMediaServeFixture(t, app.dataDir, "data/audio/sample.mp3", []byte("ID3fixture"))

	pdfURL, err := app.GetPdfFileURL("content/日本 語.pdf")
	if err != nil {
		t.Fatal(err)
	}
	if pdfURL != "/pdf/content/%E6%97%A5%E6%9C%AC%20%E8%AA%9E.pdf" {
		t.Fatalf("PDF URL = %q", pdfURL)
	}
	if _, err := app.GetImageFileURL("data/image/photo.png"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetAudioFileURL("data/audio/sample.mp3"); err != nil {
		t.Fatal(err)
	}

	invalid := []func() error{
		func() error {
			_, err := app.GetPdfFileURL(filepath.Join(app.dataDir, "content", "日本 語.pdf"))
			return err
		},
		func() error { _, err := app.GetPdfFileURL("content/../secret.pdf"); return err },
		func() error { _, err := app.GetPdfFileURL("content/%252e%252e/secret.pdf"); return err },
		func() error { _, err := app.GetImageFileURL("content/日本 語.pdf"); return err },
		func() error { _, err := app.GetImageFileURL("data/image/active.svg"); return err },
	}
	for index, call := range invalid {
		if err := call(); err == nil {
			t.Fatalf("invalid URL case %d succeeded", index)
		}
	}
}

func newMediaServeTestApp(t *testing.T) *App {
	t.Helper()
	dataDirectory := t.TempDir()
	for _, directory := range []string{"content", "data/audio", "data/image", "content/clips/assets"} {
		if err := os.MkdirAll(filepath.Join(dataDirectory, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &App{dataDir: dataDirectory}
}

func writeMediaServeFixture(t *testing.T, dataDirectory, relativePath string, data []byte) {
	t.Helper()
	absolutePath := filepath.Join(dataDirectory, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
