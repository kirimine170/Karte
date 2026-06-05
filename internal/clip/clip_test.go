package clip

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestClipURLExtractsArticleAndDownloadsImages(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		switch r.URL.Path {
		case "/article":
			return htmlResponse(r, `<!doctype html>
<html>
<head>
  <title>Fallback title</title>
  <meta property="og:site_name" content="Example Site">
  <meta name="author" content="Jane Writer">
</head>
<body>
  <nav>Navigation should be ignored</nav>
  <article>
    <h1>Example Article</h1>
    <p>This is the main article body.</p>
    <img alt="diagram" src="/images/diagram.png">
  </article>
</body>
</html>`)
		case "/images/diagram.png":
			return binaryResponse(r, http.StatusOK, "image/png", []byte("png bytes"))
		default:
			return binaryResponse(r, http.StatusNotFound, "text/plain", []byte("not found"))
		}
	})

	dataDir := t.TempDir()
	service := Service{
		DataDir: dataDir,
		Client:  client,
		Now: func() time.Time {
			return time.Date(2026, 6, 5, 10, 11, 12, 0, time.FixedZone("JST", 9*60*60))
		},
	}

	result, err := service.ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		Mode:      "article",
		ImageMode: ImageModeDownload,
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}

	if result.MarkdownPath != "content/clips/example-article.md" {
		t.Fatalf("unexpected markdown path: %s", result.MarkdownPath)
	}
	if result.AssetDir != "content/clips/assets/example-article" {
		t.Fatalf("unexpected asset dir: %s", result.AssetDir)
	}
	if result.Title != "Example Article" {
		t.Fatalf("unexpected title: %s", result.Title)
	}

	markdownBytes, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(result.MarkdownPath)))
	if err != nil {
		t.Fatalf("read markdown: %v", err)
	}
	markdown := string(markdownBytes)
	assertContains(t, markdown, `source_url: "https://example.test/article"`)
	assertContains(t, markdown, `site_name: "Example Site"`)
	assertContains(t, markdown, `clip_type: "web_article"`)
	assertContains(t, markdown, "# Example Article")
	assertContains(t, markdown, "This is the main article body.")
	assertContains(t, markdown, "assets/example-article/image-001.png")

	imagePath := filepath.Join(dataDir, "content", "clips", "assets", "example-article", "image-001.png")
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("expected downloaded image at %s: %v", imagePath, err)
	}

	manifestBytes, err := os.ReadFile(filepath.Join(dataDir, "content", "clips", "assets", "example-article", ImageManifestFile))
	if err != nil {
		t.Fatalf("expected image manifest: %v", err)
	}
	var manifest ImageManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("parse image manifest: %v", err)
	}
	if manifest.Schema != ImageManifestSchema {
		t.Fatalf("unexpected manifest schema: %s", manifest.Schema)
	}
	if len(manifest.Images) != 1 {
		t.Fatalf("expected one manifest image, got %#v", manifest.Images)
	}
	item := manifest.Images[0]
	if item.PageURL != "https://example.test/article" {
		t.Fatalf("unexpected page url: %s", item.PageURL)
	}
	if item.ImageURL != "https://example.test/images/diagram.png" || item.ResolvedImageURL != "https://example.test/images/diagram.png" {
		t.Fatalf("unexpected image urls: %#v", item)
	}
	if item.HTMLAlt != "diagram" {
		t.Fatalf("unexpected alt: %s", item.HTMLAlt)
	}
	if item.ContentType != "image/png" || item.OriginalFormat != "image/png" || item.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected capture metadata: %#v", item)
	}
}

func TestClipURLUsesUniqueSlug(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		return htmlResponse(r, `<html><body><article><h1>Repeated Title</h1><p>Body content with enough words for readability.</p></article></body></html>`)
	})

	dataDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dataDir, "content", "clips"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "content", "clips", "repeated-title.md"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := (Service{DataDir: dataDir, Client: client}).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeLink,
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}
	if result.MarkdownPath != "content/clips/repeated-title-2.md" {
		t.Fatalf("unexpected markdown path: %s", result.MarkdownPath)
	}
}

func TestClipURLKeepsMarkdownWhenImageDownloadFails(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		if r.URL.Path == "/missing.png" {
			return binaryResponse(r, http.StatusNotFound, "text/plain", []byte("not found"))
		}
		return htmlResponse(r, `<html><body><article><h1>Broken Image</h1><p>Article body.</p><img src="/missing.png"></article></body></html>`)
	})

	dataDir := t.TempDir()
	result, err := (Service{DataDir: dataDir, Client: client}).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeDownload,
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected image warning")
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "画像のダウンロードに失敗しました") {
		t.Fatalf("unexpected warnings: %v", result.Warnings)
	}
	if _, err := os.Stat(filepath.Join(dataDir, filepath.FromSlash(result.MarkdownPath))); err != nil {
		t.Fatalf("markdown should still be written: %v", err)
	}
}

func assertContains(t *testing.T, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q to contain %q", haystack, needle)
	}
}

type roundTripFunc func(*http.Request) *http.Response

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r), nil
}

func fakeClient(fn func(*http.Request) *http.Response) *http.Client {
	return &http.Client{Transport: roundTripFunc(fn)}
}

func htmlResponse(r *http.Request, body string) *http.Response {
	return binaryResponse(r, http.StatusOK, "text/html; charset=utf-8", []byte(body))
}

func binaryResponse(r *http.Request, status int, contentType string, body []byte) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		Request:    r,
	}
}
