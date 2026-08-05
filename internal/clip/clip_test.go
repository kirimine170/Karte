package clip

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
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
		DataDir:     dataDir,
		Client:      client,
		ResolveHost: publicTestResolver,
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

	result, err := testService(dataDir, client).ClipURL(context.Background(), ClipRequest{
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
	result, err := testService(dataDir, client).ClipURL(context.Background(), ClipRequest{
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

func TestClipURLSanitizesActiveContentAndUnsafeURLs(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		return htmlResponse(r, `<html><body><article>
<h1>Security Boundaries</h1>
<p><a href="javascript:alert(1)" onclick="alert(2)">unsafe link</a></p>
<p><a href="https://public.example/path">safe link</a></p>
<script>window.exfiltrate(document.cookie)</script>
<iframe src="https://public.example/frame"></iframe>
<img alt="private" src="http://127.0.0.1/private.png" onerror="alert(3)">
<img alt="public" src="https://cdn.example.test/public.png">
</article></body></html>`)
	})

	dataDir := t.TempDir()
	result, err := testService(dataDir, client).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeLink,
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}
	markdownBytes, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(result.MarkdownPath)))
	if err != nil {
		t.Fatal(err)
	}
	markdown := strings.ToLower(string(markdownBytes))
	for _, forbidden := range []string{"javascript:", "window.exfiltrate", "onclick", "onerror", "<script", "<iframe", "127.0.0.1"} {
		if strings.Contains(markdown, forbidden) {
			t.Errorf("markdown contains unsafe content %q:\n%s", forbidden, markdown)
		}
	}
	assertContains(t, markdown, "https://public.example/path")
	assertContains(t, markdown, "https://cdn.example.test/public.png")
}

func TestClipURLStripsHostnamesThatResolveToPrivateAddresses(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		return htmlResponse(r, `<html><body><article><h1>DNS Boundary</h1>
<a href="https://rebind.example.test/admin">private link</a>
<img src="https://rebind.example.test/private.png">
<img src="https://cdn.example.test/public.png">
</article></body></html>`)
	})
	service := testService(t.TempDir(), client)
	service.ResolveHost = func(_ context.Context, host string) ([]net.IP, error) {
		if host == "rebind.example.test" {
			return []net.IP{net.ParseIP("127.0.0.1")}, nil
		}
		return publicTestResolver(context.Background(), host)
	}
	result, err := service.ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeLink,
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}
	markdownBytes, err := os.ReadFile(filepath.Join(service.DataDir, filepath.FromSlash(result.MarkdownPath)))
	if err != nil {
		t.Fatal(err)
	}
	markdown := string(markdownBytes)
	if strings.Contains(markdown, "rebind.example.test") {
		t.Fatalf("private DNS target survived sanitization:\n%s", markdown)
	}
	assertContains(t, markdown, "https://cdn.example.test/public.png")
}

func TestClipURLRejectsPrivatePageURLsBeforeRequest(t *testing.T) {
	requests := 0
	client := fakeClient(func(r *http.Request) *http.Response {
		requests++
		return htmlResponse(r, `<html><body><article><h1>Unexpected</h1></article></body></html>`)
	})
	unsafeURLs := []string{
		"file:///etc/passwd",
		"http://localhost/admin",
		"http://127.0.0.1/admin",
		"http://[::1]/admin",
		"http://10.0.0.8/admin",
		"https://user:password@example.test/article",
	}
	for _, rawURL := range unsafeURLs {
		t.Run(rawURL, func(t *testing.T) {
			_, err := testService(t.TempDir(), client).ClipURL(context.Background(), ClipRequest{
				URL:       rawURL,
				ImageMode: ImageModeNone,
			})
			if err == nil {
				t.Fatalf("expected %s to be rejected", rawURL)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("unsafe page URLs reached the HTTP client %d times", requests)
	}
}

func TestClipURLRejectsPrivateRedirectTarget(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		resp := htmlResponse(r, `<html><body><article><h1>Redirected</h1></article></body></html>`)
		finalRequest, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/private", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Request = finalRequest
		return resp
	})
	_, err := testService(t.TempDir(), client).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeNone,
	})
	if err == nil {
		t.Fatal("expected redirect to a private address to be rejected")
	}
}

func TestClipURLDownloadsPublicAssetsButRejectsPrivateAssets(t *testing.T) {
	var requested []string
	client := fakeClient(func(r *http.Request) *http.Response {
		requested = append(requested, r.URL.String())
		switch r.URL.String() {
		case "https://example.test/article":
			return htmlResponse(r, `<html><body><article><h1>Asset Boundary</h1>
<img src="http://127.0.0.1/secret.png">
<img src="https://cdn.example.test/public.png">
</article></body></html>`)
		case "https://cdn.example.test/public.png":
			return binaryResponse(r, http.StatusOK, "image/png", []byte("public image"))
		default:
			return binaryResponse(r, http.StatusNotFound, "text/plain", []byte("not found"))
		}
	})

	dataDir := t.TempDir()
	result, err := testService(dataDir, client).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeDownload,
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}
	for _, rawURL := range requested {
		if strings.Contains(rawURL, "127.0.0.1") {
			t.Fatalf("private asset reached the HTTP client: %s", rawURL)
		}
	}
	if len(requested) != 2 {
		t.Fatalf("expected page and one public asset request, got %v", requested)
	}
	markdownBytes, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(result.MarkdownPath)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(markdownBytes), "127.0.0.1") {
		t.Fatalf("private asset URL survived sanitization:\n%s", markdownBytes)
	}
	assertContains(t, string(markdownBytes), "assets/asset-boundary/image-001.png")
}

func TestClipURLUsesImageContentTypeForExtension(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		if r.URL.Path == "/image" {
			return binaryResponse(r, http.StatusOK, "image/jpeg; charset=binary", []byte("jpeg bytes"))
		}
		return htmlResponse(r, `<html><body><article><h1>Extensionless Image</h1><img src="/image"></article></body></html>`)
	})

	dataDir := t.TempDir()
	result, err := testService(dataDir, client).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeDownload,
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}
	markdownBytes, err := os.ReadFile(filepath.Join(dataDir, filepath.FromSlash(result.MarkdownPath)))
	if err != nil {
		t.Fatal(err)
	}
	assertContains(t, string(markdownBytes), "assets/extensionless-image/image-001.jpg")
	imagePath := filepath.Join(dataDir, "content", "clips", "assets", "extensionless-image", "image-001.jpg")
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("expected JPEG extension derived from response content type: %v", err)
	}
}

func TestClipURLDoesNotOverwriteExistingAssetDuringExtensionCorrection(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		if r.URL.Path == "/image" {
			return binaryResponse(r, http.StatusOK, "image/jpeg", []byte("new jpeg"))
		}
		return htmlResponse(r, `<html><body><article><h1>Collision</h1><img src="/image"></article></body></html>`)
	})
	dataDir := t.TempDir()
	assetDir := filepath.Join(dataDir, "content", "clips", "assets", "collision")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	existingPath := filepath.Join(assetDir, "image-001.jpg")
	if err := os.WriteFile(existingPath, []byte("existing jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := testService(dataDir, client).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeDownload,
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected an asset collision warning")
	}
	existing, err := os.ReadFile(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(existing) != "existing jpeg" {
		t.Fatalf("existing asset was overwritten: %q", existing)
	}
	if _, err := os.Stat(filepath.Join(assetDir, "image-001.img")); !os.IsNotExist(err) {
		t.Fatalf("temporary image remained after collision: %v", err)
	}
}

func TestDownloadImageRejectsOversizedAndNonImageResponses(t *testing.T) {
	t.Run("oversized", func(t *testing.T) {
		body := bytes.Repeat([]byte{'x'}, int(maxImageBytes)+1)
		client := fakeClient(func(r *http.Request) *http.Response {
			return binaryResponse(r, http.StatusOK, "image/png", body)
		})
		dest := filepath.Join(t.TempDir(), "oversized.png")
		if _, err := downloadImage(context.Background(), client, "https://images.example.test/oversized", dest); err == nil {
			t.Fatal("expected oversized image to be rejected")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatalf("oversized image left a partial file: %v", err)
		}
	})

	t.Run("non-image", func(t *testing.T) {
		client := fakeClient(func(r *http.Request) *http.Response {
			return binaryResponse(r, http.StatusOK, "text/html", []byte("<script>alert(1)</script>"))
		})
		dest := filepath.Join(t.TempDir(), "not-image.img")
		if _, err := downloadImage(context.Background(), client, "https://images.example.test/not-image", dest); err == nil {
			t.Fatal("expected non-image response to be rejected")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatalf("non-image response left a file: %v", err)
		}
	})

	t.Run("active SVG", func(t *testing.T) {
		client := fakeClient(func(r *http.Request) *http.Response {
			return binaryResponse(r, http.StatusOK, "image/svg+xml", []byte(`<svg onload="alert(1)"></svg>`))
		})
		dest := filepath.Join(t.TempDir(), "active.svg")
		if _, err := downloadImage(context.Background(), client, "https://images.example.test/active.svg", dest); err == nil {
			t.Fatal("expected active SVG response to be rejected")
		}
		if _, err := os.Stat(dest); !os.IsNotExist(err) {
			t.Fatalf("active SVG response left a file: %v", err)
		}
	})
}

func TestClipURLContainsGeneratedPathsWithinDataDir(t *testing.T) {
	client := fakeClient(func(r *http.Request) *http.Response {
		return htmlResponse(r, `<html><body><article><h1>../../outside</h1><p>Safe body.</p></article></body></html>`)
	})
	dataDir := t.TempDir()
	result, err := testService(dataDir, client).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeNone,
		OutputDir: "../../outside",
	})
	if err != nil {
		t.Fatalf("ClipURL returned error: %v", err)
	}
	generated := filepath.Join(dataDir, filepath.FromSlash(result.MarkdownPath))
	relative, err := filepath.Rel(dataDir, generated)
	if err != nil || pathEscapesRoot(relative) {
		t.Fatalf("generated path escaped data directory: %s", generated)
	}
	if strings.Contains(result.MarkdownPath, "..") {
		t.Fatalf("generated relative path contains traversal: %s", result.MarkdownPath)
	}
}

func TestClipURLRejectsSymlinkEscape(t *testing.T) {
	dataDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataDir, "content")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	client := fakeClient(func(r *http.Request) *http.Response {
		return htmlResponse(r, `<html><body><article><h1>Symlink Escape</h1><p>Safe body.</p></article></body></html>`)
	})
	_, err := testService(dataDir, client).ClipURL(context.Background(), ClipRequest{
		URL:       "https://example.test/article",
		ImageMode: ImageModeNone,
	})
	if err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "clips")); !os.IsNotExist(statErr) {
		t.Fatalf("symlink escape created content outside data directory: %v", statErr)
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

func testService(dataDir string, client *http.Client) Service {
	return Service{DataDir: dataDir, Client: client, ResolveHost: publicTestResolver}
}

func publicTestResolver(context.Context, string) ([]net.IP, error) {
	return []net.IP{net.ParseIP("93.184.216.34")}, nil
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
