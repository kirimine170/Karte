package clip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	readability "github.com/go-shiori/go-readability"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	ImageModeDownload = "download"
	ImageModeLink     = "link"
	ImageModeNone     = "none"

	ImageManifestFile   = ".webclip-images.json"
	ImageManifestSchema = "karte.web_clip.images.v1"
)

type ClipRequest struct {
	URL       string `json:"url"`
	Mode      string `json:"mode"`
	ImageMode string `json:"imageMode"`
	OutputDir string `json:"outputDir"`
}

type ClipResult struct {
	MarkdownPath string   `json:"markdownPath"`
	AssetDir     string   `json:"assetDir"`
	Title        string   `json:"title"`
	SourceURL    string   `json:"sourceUrl"`
	Warnings     []string `json:"warnings"`
}

type Service struct {
	DataDir string
	Client  *http.Client
	Now     func() time.Time
}

type fetchedPage struct {
	Body     []byte
	URL      *url.URL
	Warnings []string
}

type clipDocument struct {
	Title       string
	SiteName    string
	Author      string
	PublishedAt string
	ContentHTML string
	Warnings    []string
}

type imageReplacement struct {
	Source string
	Local  string
}

type ImageManifest struct {
	Schema string              `json:"schema"`
	Images []ImageManifestItem `json:"images"`
}

type ImageManifestItem struct {
	LocalPath         string `json:"local_path"`
	MarkdownReference string `json:"markdown_reference"`
	ImageURL          string `json:"image_url"`
	ResolvedImageURL  string `json:"resolved_image_url"`
	PageURL           string `json:"page_url"`
	SiteName          string `json:"site_name,omitempty"`
	PageTitle         string `json:"page_title,omitempty"`
	HTMLAlt           string `json:"html_alt,omitempty"`
	HTMLCaption       string `json:"html_caption,omitempty"`
	CapturedAt        string `json:"captured_at"`
	HTTPStatus        int    `json:"http_status"`
	ContentType       string `json:"content_type,omitempty"`
	ETag              string `json:"etag,omitempty"`
	LastModified      string `json:"last_modified,omitempty"`
	OriginalFormat    string `json:"original_format,omitempty"`
}

type imageCandidate struct {
	Source      string
	ResolvedURL string
	Alt         string
	Caption     string
}

type imageCapture struct {
	HTTPStatus     int
	ContentType    string
	ETag           string
	LastModified   string
	OriginalFormat string
}

var nonSlugChar = regexp.MustCompile(`[^a-z0-9]+`)

func (s Service) ClipURL(ctx context.Context, req ClipRequest) (ClipResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(s.DataDir) == "" {
		return ClipResult{}, errors.New("dataDir is required")
	}
	if req.ImageMode == "" {
		req.ImageMode = ImageModeDownload
	}
	if req.Mode == "" {
		req.Mode = "article"
	}
	if req.Mode != "article" {
		return ClipResult{}, fmt.Errorf("unsupported clip mode: %s", req.Mode)
	}
	if req.ImageMode != ImageModeDownload && req.ImageMode != ImageModeLink && req.ImageMode != ImageModeNone {
		return ClipResult{}, fmt.Errorf("unsupported image mode: %s", req.ImageMode)
	}

	page, err := s.fetchPage(ctx, req.URL)
	if err != nil {
		return ClipResult{}, err
	}

	doc, err := extractDocument(page.Body, page.URL)
	if err != nil {
		return ClipResult{}, err
	}
	doc.Warnings = append(page.Warnings, doc.Warnings...)

	now := s.now()
	slugBase := makeSlug(doc.Title)
	if slugBase == "" {
		slugBase = makeSlug(page.URL.Hostname())
	}
	if slugBase == "" {
		slugBase = "web-clip"
	}

	contentRoot := filepath.Join(s.DataDir, "content", "clips")
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		return ClipResult{}, fmt.Errorf("prepare clips directory: %w", err)
	}

	markdownAbs, slug, err := uniqueMarkdownPath(contentRoot, slugBase)
	if err != nil {
		return ClipResult{}, err
	}

	assetRel := filepath.ToSlash(filepath.Join("assets", slug))
	assetAbs := filepath.Join(contentRoot, "assets", slug)
	imageMap := map[string]string{}
	if req.ImageMode == ImageModeDownload {
		replacements, warnings := s.downloadImages(ctx, doc, page.URL, now, assetAbs, assetRel)
		doc.Warnings = append(doc.Warnings, warnings...)
		for _, replacement := range replacements {
			imageMap[replacement.Source] = replacement.Local
		}
	}

	articleMarkdown, err := htmlToMarkdown(doc.ContentHTML, page.URL)
	if err != nil {
		return ClipResult{}, fmt.Errorf("convert html to markdown: %w", err)
	}
	articleMarkdown = strings.TrimSpace(rewriteMarkdownImages(articleMarkdown, imageMap, req.ImageMode))

	markdown := buildMarkdown(doc, page.URL.String(), now, assetRel, articleMarkdown)
	if err := os.WriteFile(markdownAbs, []byte(markdown), 0o644); err != nil {
		return ClipResult{}, fmt.Errorf("write markdown: %w", err)
	}

	markdownRel, err := filepath.Rel(s.DataDir, markdownAbs)
	if err != nil {
		return ClipResult{}, fmt.Errorf("resolve markdown path: %w", err)
	}
	assetPath := ""
	if req.ImageMode == ImageModeDownload {
		assetPath = filepath.ToSlash(filepath.Join("content", "clips", assetRel))
	}

	return ClipResult{
		MarkdownPath: filepath.ToSlash(markdownRel),
		AssetDir:     assetPath,
		Title:        doc.Title,
		SourceURL:    page.URL.String(),
		Warnings:     doc.Warnings,
	}, nil
}

func (s Service) fetchPage(ctx context.Context, rawURL string) (fetchedPage, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return fetchedPage{}, fmt.Errorf("invalid url: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fetchedPage{}, fmt.Errorf("url must use http or https")
	}

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return fetchedPage{}, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("User-Agent", "Karte WebClip/1.0")

	resp, err := client.Do(httpReq)
	if err != nil {
		return fetchedPage{}, fmt.Errorf("fetch url: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fetchedPage{}, fmt.Errorf("fetch url: unexpected status %d", resp.StatusCode)
	}
	reader, err := charset.NewReader(resp.Body, resp.Header.Get("Content-Type"))
	warnings := []string{}
	if err != nil {
		reader = resp.Body
		warnings = append(warnings, "charset判定に失敗したためUTF-8として処理しました")
	}
	body, err := io.ReadAll(io.LimitReader(reader, 12*1024*1024))
	if err != nil {
		return fetchedPage{}, fmt.Errorf("read response body: %w", err)
	}
	return fetchedPage{Body: body, URL: resp.Request.URL, Warnings: warnings}, nil
}

func (s Service) downloadImages(ctx context.Context, doc clipDocument, pageURL *url.URL, capturedAt time.Time, assetAbs, assetRel string) ([]imageReplacement, []string) {
	imageCandidates := extractImageCandidates(doc.ContentHTML, pageURL)
	if len(imageCandidates) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(assetAbs, 0o755); err != nil {
		return nil, []string{fmt.Sprintf("画像保存ディレクトリを作成できませんでした: %v", err)}
	}

	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	replacements := make([]imageReplacement, 0, len(imageCandidates))
	manifest := ImageManifest{Schema: ImageManifestSchema}
	warnings := []string{}
	for i, candidate := range imageCandidates {
		localName := fmt.Sprintf("image-%03d%s", i+1, imageExt(candidate.ResolvedURL))
		localAbs := filepath.Join(assetAbs, localName)
		localRel := filepath.ToSlash(filepath.Join(assetRel, localName))
		capture, err := downloadImage(ctx, client, candidate.ResolvedURL, localAbs)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("画像のダウンロードに失敗しました: %s (%v)", candidate.ResolvedURL, err))
			continue
		}
		replacements = append(replacements, imageReplacement{Source: candidate.ResolvedURL, Local: localRel})
		manifest.Images = append(manifest.Images, ImageManifestItem{
			LocalPath:         filepath.ToSlash(filepath.Join("content", "clips", localRel)),
			MarkdownReference: localRel,
			ImageURL:          candidate.Source,
			ResolvedImageURL:  candidate.ResolvedURL,
			PageURL:           pageURL.String(),
			SiteName:          doc.SiteName,
			PageTitle:         doc.Title,
			HTMLAlt:           candidate.Alt,
			HTMLCaption:       candidate.Caption,
			CapturedAt:        capturedAt.Format(time.RFC3339),
			HTTPStatus:        capture.HTTPStatus,
			ContentType:       capture.ContentType,
			ETag:              capture.ETag,
			LastModified:      capture.LastModified,
			OriginalFormat:    capture.OriginalFormat,
		})
	}
	if len(manifest.Images) > 0 {
		if err := writeImageManifest(assetAbs, manifest); err != nil {
			warnings = append(warnings, fmt.Sprintf("画像メタデータmanifestを保存できませんでした: %v", err))
		}
	}
	return replacements, warnings
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func extractDocument(body []byte, pageURL *url.URL) (clipDocument, error) {
	fullMeta := map[string]string{}
	if root, err := html.Parse(bytes.NewReader(body)); err == nil {
		fullMeta = extractMeta(root)
	}
	article, err := readability.FromReader(bytes.NewReader(body), pageURL)
	warnings := []string{}
	if err != nil || strings.TrimSpace(article.Content) == "" {
		warnings = append(warnings, "記事本文を抽出できなかったため，body全体をMarkdown化しました")
		fallback, meta, fallbackErr := fallbackBodyHTML(body)
		if fallbackErr != nil {
			if err != nil {
				return clipDocument{}, fmt.Errorf("extract article: %w", err)
			}
			return clipDocument{}, fallbackErr
		}
		return clipDocument{
			Title:       meta["title"],
			SiteName:    meta["site_name"],
			Author:      meta["author"],
			PublishedAt: meta["published_at"],
			ContentHTML: fallback,
			Warnings:    warnings,
		}, nil
	}

	publishedAt := ""
	if article.PublishedTime != nil {
		publishedAt = article.PublishedTime.Format(time.RFC3339)
	}
	title := strings.TrimSpace(article.Title)
	if heading := firstHeading(article.Content); heading != "" {
		title = heading
	}
	if fullMeta["title"] != "" {
		title = fullMeta["title"]
	}
	siteName := strings.TrimSpace(article.SiteName)
	if siteName == "" {
		siteName = fullMeta["site_name"]
	}
	author := strings.TrimSpace(article.Byline)
	if author == "" {
		author = fullMeta["author"]
	}
	if publishedAt == "" {
		publishedAt = fullMeta["published_at"]
	}
	return clipDocument{
		Title:       title,
		SiteName:    siteName,
		Author:      author,
		PublishedAt: publishedAt,
		ContentHTML: article.Content,
		Warnings:    warnings,
	}, nil
}

func firstHeading(rawHTML string) string {
	root, err := html.Parse(strings.NewReader(rawHTML))
	if err != nil {
		return ""
	}
	return textContent(findElement(root, "h1"))
}

func fallbackBodyHTML(body []byte) (string, map[string]string, error) {
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return "", nil, fmt.Errorf("parse html: %w", err)
	}
	meta := extractMeta(root)
	bodyNode := findElement(root, "body")
	if bodyNode == nil {
		bodyNode = root
	}
	var b strings.Builder
	for child := bodyNode.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&b, child); err != nil {
			return "", nil, fmt.Errorf("render body html: %w", err)
		}
	}
	return b.String(), meta, nil
}

func extractMeta(root *html.Node) map[string]string {
	meta := map[string]string{}
	if title := textContent(findElement(root, "title")); title != "" {
		meta["title"] = title
	}
	if heading := textContent(findElement(root, "h1")); heading != "" {
		meta["title"] = heading
	}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "meta" {
			key := firstAttr(n, "property")
			if key == "" {
				key = firstAttr(n, "name")
			}
			value := firstAttr(n, "content")
			switch strings.ToLower(key) {
			case "og:title":
				meta["title"] = value
			case "og:site_name":
				meta["site_name"] = value
			case "author":
				meta["author"] = value
			case "article:published_time":
				meta["published_at"] = value
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return meta
}

func htmlToMarkdown(articleHTML string, pageURL *url.URL) (string, error) {
	return htmltomarkdown.ConvertString(articleHTML, converter.WithDomain(pageURL.Scheme+"://"+pageURL.Host))
}

func extractImageURLs(articleHTML string, pageURL *url.URL) []string {
	candidates := extractImageCandidates(articleHTML, pageURL)
	urls := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		urls = append(urls, candidate.ResolvedURL)
	}
	return urls
}

func extractImageCandidates(articleHTML string, pageURL *url.URL) []imageCandidate {
	root, err := html.Parse(strings.NewReader(articleHTML))
	if err != nil {
		return nil
	}
	seen := map[string]struct{}{}
	var images []imageCandidate
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n == nil {
			return
		}
		if n.Type == html.ElementNode && n.Data == "img" {
			src := firstAttr(n, "src")
			if src == "" {
				src = firstAttr(n, "data-src")
			}
			resolved := resolveURL(pageURL, src)
			if resolved != "" {
				if _, ok := seen[resolved]; !ok {
					seen[resolved] = struct{}{}
					images = append(images, imageCandidate{
						Source:      strings.TrimSpace(src),
						ResolvedURL: resolved,
						Alt:         firstAttr(n, "alt"),
						Caption:     imageCaption(n),
					})
				}
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return images
}

func imageCaption(img *html.Node) string {
	for n := img.Parent; n != nil; n = n.Parent {
		if n.Type == html.ElementNode && n.Data == "figure" {
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				if child.Type == html.ElementNode && child.Data == "figcaption" {
					return textContent(child)
				}
			}
			return ""
		}
	}
	return ""
}

func downloadImage(ctx context.Context, client *http.Client, imageURL, dest string) (imageCapture, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageURL, nil)
	if err != nil {
		return imageCapture{}, err
	}
	req.Header.Set("User-Agent", "Karte WebClip/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return imageCapture{}, err
	}
	defer resp.Body.Close()
	capture := imageCapture{
		HTTPStatus:     resp.StatusCode,
		ContentType:    resp.Header.Get("Content-Type"),
		ETag:           resp.Header.Get("ETag"),
		LastModified:   resp.Header.Get("Last-Modified"),
		OriginalFormat: normalizedImageContentType(resp.Header.Get("Content-Type"), imageURL),
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return capture, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return capture, err
	}
	file, err := os.Create(dest)
	if err != nil {
		return capture, err
	}
	defer file.Close()
	_, err = io.Copy(file, io.LimitReader(resp.Body, 20*1024*1024))
	return capture, err
}

func writeImageManifest(assetAbs string, manifest ImageManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal image manifest: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(assetAbs, ImageManifestFile), data, 0o644)
}

func normalizedImageContentType(contentType, imageURL string) string {
	if mediaType, _, err := mime.ParseMediaType(contentType); err == nil && strings.HasPrefix(mediaType, "image/") {
		return mediaType
	}
	switch imageExt(imageURL) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func imageExt(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err == nil {
		ext := strings.ToLower(path.Ext(parsed.Path))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg":
			return ext
		}
	}
	if contentType := mime.TypeByExtension(path.Ext(rawURL)); contentType != "" {
		if exts, err := mime.ExtensionsByType(contentType); err == nil && len(exts) > 0 {
			return exts[0]
		}
	}
	return ".img"
}

func rewriteMarkdownImages(markdown string, images map[string]string, mode string) string {
	if mode == ImageModeNone {
		lines := strings.Split(markdown, "\n")
		filtered := lines[:0]
		for _, line := range lines {
			if strings.Contains(line, "![") {
				continue
			}
			filtered = append(filtered, line)
		}
		return strings.Join(filtered, "\n")
	}
	if len(images) == 0 {
		return markdown
	}
	sources := make([]string, 0, len(images))
	for source := range images {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return len(sources[i]) > len(sources[j]) })
	for _, source := range sources {
		markdown = strings.ReplaceAll(markdown, source, images[source])
	}
	return markdown
}

func buildMarkdown(doc clipDocument, sourceURL string, clippedAt time.Time, assetRel, body string) string {
	title := doc.Title
	if strings.TrimSpace(title) == "" {
		title = "Untitled Web Clip"
	}
	fields := []struct {
		key   string
		value string
	}{
		{"title", title},
		{"source_url", sourceURL},
		{"site_name", doc.SiteName},
		{"author", doc.Author},
		{"published_at", doc.PublishedAt},
		{"clipped_at", clippedAt.Format(time.RFC3339)},
		{"clip_type", "web_article"},
		{"assets_dir", "./" + assetRel},
	}

	var b strings.Builder
	b.WriteString("---\n")
	for _, field := range fields {
		b.WriteString(field.key)
		b.WriteString(": ")
		b.WriteString(yamlQuote(field.value))
		b.WriteString("\n")
	}
	b.WriteString("---\n\n")
	if !hasLeadingHeading(body, title) {
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
	}
	b.WriteString(body)
	b.WriteString("\n")
	return b.String()
}

func uniqueMarkdownPath(dir, base string) (string, string, error) {
	for i := 0; i < 1000; i++ {
		slug := base
		if i > 0 {
			slug = fmt.Sprintf("%s-%d", base, i+1)
		}
		candidate := filepath.Join(dir, slug+".md")
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, slug, nil
		} else if err != nil {
			return "", "", fmt.Errorf("check markdown path: %w", err)
		}
	}
	return "", "", fmt.Errorf("could not find available filename for %s", base)
}

func makeSlug(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	var b strings.Builder
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune('-')
		default:
			b.WriteRune(r)
		}
	}
	slug := nonSlugChar.ReplaceAllString(b.String(), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 80 {
		slug = strings.Trim(slug[:80], "-")
	}
	return slug
}

func resolveURL(base *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "data:") || strings.HasPrefix(raw, "blob:") {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	resolved := base.ResolveReference(parsed)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return ""
	}
	return resolved.String()
}

func firstAttr(n *html.Node, name string) string {
	for _, attr := range n.Attr {
		if strings.EqualFold(attr.Key, name) {
			return strings.TrimSpace(attr.Val)
		}
	}
	return ""
}

func findElement(n *html.Node, name string) *html.Node {
	if n == nil {
		return nil
	}
	if n.Type == html.ElementNode && n.Data == name {
		return n
	}
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if found := findElement(child, name); found != nil {
			return found
		}
	}
	return nil
}

func textContent(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return strings.TrimSpace(n.Data)
	}
	var parts []string
	for child := n.FirstChild; child != nil; child = child.NextSibling {
		if text := textContent(child); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " ")
}

func yamlQuote(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

func hasLeadingHeading(body, title string) bool {
	body = strings.TrimSpace(body)
	if body == "" {
		return false
	}
	firstLine := strings.TrimSpace(strings.SplitN(body, "\n", 2)[0])
	return strings.HasPrefix(firstLine, "# ") && strings.EqualFold(strings.TrimSpace(strings.TrimPrefix(firstLine, "# ")), strings.TrimSpace(title))
}
