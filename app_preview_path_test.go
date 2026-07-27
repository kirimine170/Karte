package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"karte/internal/clip"
	"karte/internal/webpchunk"
)

func TestPreviewMarkdownForPathResolvesDocumentRelativeImages(t *testing.T) {
	dataDir := t.TempDir()
	imagePath := filepath.Join(dataDir, "content", "clips", "assets", "example", "image-001.png")
	if err := os.MkdirAll(filepath.Dir(imagePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{dataDir: dataDir}
	html, err := app.PreviewMarkdownForPath("content/clips/example.md", `# Example

![diagram](assets/example/image-001.png)
`)
	if err != nil {
		t.Fatalf("PreviewMarkdownForPath returned error: %v", err)
	}
	if !strings.Contains(html, `src="/image/content/clips/assets/example/image-001.png"`) {
		t.Fatalf("expected resolved image URL, got:\n%s", html)
	}
}

func TestPreviewMarkdownForPathKeepsMissingRelativeImagesUnchanged(t *testing.T) {
	app := &App{dataDir: t.TempDir()}
	html, err := app.PreviewMarkdownForPath("content/clips/example.md", `![diagram](assets/example/missing.png)`)
	if err != nil {
		t.Fatalf("PreviewMarkdownForPath returned error: %v", err)
	}
	if strings.Contains(html, `/image/content/clips/assets/example/missing.png`) {
		t.Fatalf("missing image should not be rewritten, got:\n%s", html)
	}
}

func TestEnableMarpInFrontMatterPreservesPresentationFields(t *testing.T) {
	content := "---\ntitle: Quarterly Review\nheader: Team update\nfooter: Confidential\npaginate: true\naspectRatio: 16:9\nmarpTheme: uncover\ncustom: retained\n---\n# Results\n"
	want := "---\nmarp: true\ntitle: Quarterly Review\nheader: Team update\nfooter: Confidential\npaginate: true\naspectRatio: 16:9\nmarpTheme: uncover\ncustom: retained\n---\n# Results\n"

	if got := enableMarpInFrontMatter(content); got != want {
		t.Fatalf("front matter was not preserved when enabling Marp:\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestGetImageListIncludesWebClipAssets(t *testing.T) {
	dataDir := t.TempDir()
	assetPath := filepath.Join(dataDir, "content", "clips", "assets", "example", "image-001.png")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{dataDir: dataDir}
	images := app.GetImageList()
	if len(images) != 1 {
		t.Fatalf("expected one image, got %d: %#v", len(images), images)
	}
	if images[0].Path != "content/clips/assets/example/image-001.png" {
		t.Fatalf("unexpected image path: %s", images[0].Path)
	}
}

func TestGetImageListPrefersWebPForWebClipAssets(t *testing.T) {
	dataDir := t.TempDir()
	assetDir := filepath.Join(dataDir, "content", "clips", "assets", "example")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "image-001.png"), []byte("png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "image-001.webp"), []byte("webp"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{dataDir: dataDir}
	images := app.GetImageList()
	if len(images) != 1 {
		t.Fatalf("expected one deduped image, got %d: %#v", len(images), images)
	}
	if images[0].Path != "content/clips/assets/example/image-001.webp" {
		t.Fatalf("expected webp to be preferred, got %s", images[0].Path)
	}
	if images[0].OriginalPath != "content/clips/assets/example/image-001.png" {
		t.Fatalf("expected original path to point to png, got %s", images[0].OriginalPath)
	}
}

func TestWebClipConversionUpdatesMarkdownToWebP(t *testing.T) {
	dataDir := t.TempDir()
	assetDir := filepath.Join(dataDir, "content", "clips", "assets", "example")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTestPNG(filepath.Join(assetDir, "image-001.png")); err != nil {
		t.Fatal(err)
	}
	markdownPath := filepath.Join(dataDir, "content", "clips", "example.md")
	if err := os.WriteFile(markdownPath, []byte("![diagram](assets/example/image-001.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{dataDir: dataDir}
	err := app.processWebClipConversionJob(webClipConversionJob{
		MarkdownPath: "content/clips/example.md",
		AssetDir:     "content/clips/assets/example",
	}, 0)
	if err != nil {
		t.Fatalf("conversion job returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(assetDir, "image-001.webp")); err != nil {
		t.Fatalf("expected webp file: %v", err)
	}
	content, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "assets/example/image-001.webp") {
		t.Fatalf("expected markdown to reference webp, got:\n%s", content)
	}
	if strings.Contains(string(content), "assets/example/image-001.png") {
		t.Fatalf("expected markdown png reference to be replaced, got:\n%s", content)
	}
}

func TestWebClipConversionEmbedsMetadataChunk(t *testing.T) {
	dataDir := t.TempDir()
	assetDir := filepath.Join(dataDir, "content", "clips", "assets", "example")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTestPNG(filepath.Join(assetDir, "image-001.png")); err != nil {
		t.Fatal(err)
	}
	manifest := clip.ImageManifest{
		Schema: clip.ImageManifestSchema,
		Images: []clip.ImageManifestItem{{
			LocalPath:         "content/clips/assets/example/image-001.png",
			MarkdownReference: "assets/example/image-001.png",
			ImageURL:          "/images/diagram.png",
			ResolvedImageURL:  "https://example.test/images/diagram.png",
			PageURL:           "https://example.test/article",
			SiteName:          "Example Site",
			PageTitle:         "Example Article",
			HTMLAlt:           "diagram",
			HTMLCaption:       "Figure 1",
			CapturedAt:        "2026-06-05T10:11:12+09:00",
			HTTPStatus:        200,
			ContentType:       "image/png",
			ETag:              `"abc"`,
			LastModified:      "Fri, 05 Jun 2026 01:11:12 GMT",
			OriginalFormat:    "image/png",
		}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, clip.ImageManifestFile), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	markdownPath := filepath.Join(dataDir, "content", "clips", "example.md")
	if err := os.WriteFile(markdownPath, []byte("![diagram](assets/example/image-001.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{dataDir: dataDir}
	err = app.processWebClipConversionJob(webClipConversionJob{
		MarkdownPath: "content/clips/example.md",
		AssetDir:     "content/clips/assets/example",
	}, 0)
	if err != nil {
		t.Fatalf("conversion job returned error: %v", err)
	}

	metadataBytes, err := webpchunk.ReadMetadataFromWebP(filepath.Join(assetDir, "image-001.webp"))
	if err != nil {
		t.Fatalf("read webp metadata: %v", err)
	}
	if len(metadataBytes) == 0 {
		t.Fatal("expected webp metadata chunk")
	}
	var metadata webClipImageMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		t.Fatalf("parse webp metadata: %v\n%s", err, metadataBytes)
	}
	if metadata.Schema != "karte.image.metadata.v1" {
		t.Fatalf("unexpected schema: %s", metadata.Schema)
	}
	if metadata.Source.Kind != "web_clip" || metadata.Source.PageURL != "https://example.test/article" {
		t.Fatalf("unexpected source metadata: %#v", metadata.Source)
	}
	if metadata.Source.HTMLAlt != "diagram" || metadata.Source.HTMLCaption != "Figure 1" {
		t.Fatalf("unexpected html metadata: %#v", metadata.Source)
	}
	if metadata.Capture.Method != "url_clip" || metadata.Capture.HTTPStatus != 200 || metadata.Capture.ETag != `"abc"` {
		t.Fatalf("unexpected capture metadata: %#v", metadata.Capture)
	}
	if metadata.Relations.DocumentPath != "content/clips/example.md" {
		t.Fatalf("unexpected document path: %s", metadata.Relations.DocumentPath)
	}
	if metadata.Relations.MarkdownReference != "assets/example/image-001.webp" {
		t.Fatalf("unexpected markdown reference: %s", metadata.Relations.MarkdownReference)
	}
	if metadata.Processing.OriginalFormat != "image/png" || metadata.Processing.ConvertedTo != "image/webp" {
		t.Fatalf("unexpected processing metadata: %#v", metadata.Processing)
	}

	userMetadata, err := app.GetImageMetadata("content/clips/assets/example/image-001.webp")
	if err != nil {
		t.Fatalf("GetImageMetadata returned error: %v", err)
	}
	if strings.Contains(userMetadata, "web_clip:") {
		t.Fatalf("user metadata should not include KMTD web_clip section, got:\n%s", userMetadata)
	}

	systemMetadata, err := app.GetImageSystemMetadata("content/clips/assets/example/image-001.webp")
	if err != nil {
		t.Fatalf("GetImageSystemMetadata returned error: %v", err)
	}
	if !strings.Contains(systemMetadata, "schema: karte.image.metadata.v1") {
		t.Fatalf("expected system metadata schema, got:\n%s", systemMetadata)
	}
	if !strings.Contains(systemMetadata, "page_url: https://example.test/article") {
		t.Fatalf("expected system metadata to include page url, got:\n%s", systemMetadata)
	}
	if !strings.Contains(systemMetadata, "markdown_reference: assets/example/image-001.webp") {
		t.Fatalf("expected system metadata to include markdown reference, got:\n%s", systemMetadata)
	}
}

func TestSaveImageSystemMetadataUpdatesKMTDChunk(t *testing.T) {
	dataDir := t.TempDir()
	assetDir := filepath.Join(dataDir, "content", "clips", "assets", "example")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTestPNG(filepath.Join(assetDir, "image-001.png")); err != nil {
		t.Fatal(err)
	}
	app := &App{dataDir: dataDir}
	if err := app.convertImageFileToWebP(filepath.Join(assetDir, "image-001.png"), filepath.Join(assetDir, "image-001.webp"), ".png"); err != nil {
		t.Fatal(err)
	}

	ok, err := app.SaveImageSystemMetadata("content/clips/assets/example/image-001.webp", `schema: karte.image.metadata.v1
source:
  kind: web_clip
  page_url: https://example.test/updated
`)
	if err != nil {
		t.Fatalf("SaveImageSystemMetadata returned error: %v", err)
	}
	if !ok {
		t.Fatal("SaveImageSystemMetadata returned false")
	}

	metadataBytes, err := webpchunk.ReadMetadataFromWebP(filepath.Join(assetDir, "image-001.webp"))
	if err != nil {
		t.Fatalf("read webp metadata: %v", err)
	}
	if !strings.Contains(string(metadataBytes), "https://example.test/updated") {
		t.Fatalf("expected updated metadata chunk, got:\n%s", metadataBytes)
	}
}

func TestWebClipConversionSkipsExistingWebP(t *testing.T) {
	dataDir := t.TempDir()
	assetDir := filepath.Join(dataDir, "content", "clips", "assets", "example")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeTestPNG(filepath.Join(assetDir, "image-001.png")); err != nil {
		t.Fatal(err)
	}
	webpPath := filepath.Join(assetDir, "image-001.webp")
	if err := os.WriteFile(webpPath, []byte("existing-webp"), 0o644); err != nil {
		t.Fatal(err)
	}
	markdownPath := filepath.Join(dataDir, "content", "clips", "example.md")
	if err := os.WriteFile(markdownPath, []byte("![diagram](assets/example/image-001.png)\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{dataDir: dataDir}
	err := app.processWebClipConversionJob(webClipConversionJob{
		MarkdownPath: "content/clips/example.md",
		AssetDir:     "content/clips/assets/example",
	}, 0)
	if err != nil {
		t.Fatalf("conversion job returned error: %v", err)
	}
	webpContent, err := os.ReadFile(webpPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(webpContent) != "existing-webp" {
		t.Fatalf("existing webp should not be overwritten, got %q", webpContent)
	}
	markdownContent, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(markdownContent), "assets/example/image-001.png") {
		t.Fatalf("markdown should stay unchanged when conversion is skipped, got:\n%s", markdownContent)
	}
}

func TestWebClipConversionKeepsOriginalOnFailure(t *testing.T) {
	dataDir := t.TempDir()
	assetDir := filepath.Join(dataDir, "content", "clips", "assets", "example")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "image-001.png"), []byte("not a png"), 0o644); err != nil {
		t.Fatal(err)
	}
	markdownPath := filepath.Join(dataDir, "content", "clips", "example.md")
	originalMarkdown := "![diagram](assets/example/image-001.png)\n"
	if err := os.WriteFile(markdownPath, []byte(originalMarkdown), 0o644); err != nil {
		t.Fatal(err)
	}

	app := &App{dataDir: dataDir}
	err := app.processWebClipConversionJob(webClipConversionJob{
		MarkdownPath: "content/clips/example.md",
		AssetDir:     "content/clips/assets/example",
	}, 0)
	if err != nil {
		t.Fatalf("conversion job returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(assetDir, "image-001.webp")); !os.IsNotExist(err) {
		t.Fatalf("webp should not be created on conversion failure, stat err=%v", err)
	}
	content, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != originalMarkdown {
		t.Fatalf("markdown should remain unchanged, got:\n%s", content)
	}
}

func writeTestPNG(path string) error {
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}
