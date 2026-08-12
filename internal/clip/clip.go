package clip

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
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
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

const (
	ImageModeDownload = "download"
	ImageModeLink     = "link"
	ImageModeNone     = "none"

	ImageManifestFile   = ".webclip-images.json"
	ImageManifestSchema = "karte.web_clip.images.v1"

	maxPageBytes  = int64(12 * 1024 * 1024)
	maxImageBytes = int64(20 * 1024 * 1024)
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
	DataDir     string
	Client      *http.Client
	Now         func() time.Time
	ResolveHost func(context.Context, string) ([]net.IP, error)
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

var (
	nonSlugChar           = regexp.MustCompile(`[^a-z0-9]+`)
	blockedRemoteNetworks = parseIPNetworks([]string{
		"0.0.0.0/8",
		"100.64.0.0/10",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"198.18.0.0/15",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"240.0.0.0/4",
		"2001:db8::/32",
	})
)

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
	sanitizedHTML, removed, err := sanitizeHTMLForMarkdown(doc.ContentHTML, page.URL, s.externalURLValidator(ctx))
	if err != nil {
		return ClipResult{}, fmt.Errorf("sanitize article html: %w", err)
	}
	doc.ContentHTML = sanitizedHTML
	if removed > 0 {
		doc.Warnings = append(doc.Warnings, fmt.Sprintf("安全でないHTML要素またはURL属性を%d件除去しました", removed))
	}

	now := s.now()
	slugBase := makeSlug(doc.Title)
	if slugBase == "" {
		slugBase = makeSlug(page.URL.Hostname())
	}
	if slugBase == "" {
		slugBase = "web-clip"
	}

	contentRoot := filepath.Join(s.DataDir, "content", "clips")
	if err := os.MkdirAll(s.DataDir, 0o755); err != nil {
		return ClipResult{}, fmt.Errorf("prepare data directory: %w", err)
	}
	if err := ensurePathInsideRoot(s.DataDir, contentRoot); err != nil {
		return ClipResult{}, fmt.Errorf("validate clips directory: %w", err)
	}
	if err := os.MkdirAll(contentRoot, 0o755); err != nil {
		return ClipResult{}, fmt.Errorf("prepare clips directory: %w", err)
	}
	if err := ensurePathInsideRoot(s.DataDir, contentRoot); err != nil {
		return ClipResult{}, fmt.Errorf("validate clips directory: %w", err)
	}

	markdownAbs, slug, err := uniqueMarkdownPath(contentRoot, slugBase)
	if err != nil {
		return ClipResult{}, err
	}

	assetRel := filepath.ToSlash(filepath.Join("assets", slug))
	assetAbs := filepath.Join(contentRoot, "assets", slug)
	if err := ensurePathInsideRoot(s.DataDir, markdownAbs); err != nil {
		return ClipResult{}, fmt.Errorf("validate markdown path: %w", err)
	}
	if err := ensurePathInsideRoot(s.DataDir, assetAbs); err != nil {
		return ClipResult{}, fmt.Errorf("validate asset path: %w", err)
	}
	imageMap := map[string]string{}
	if req.ImageMode == ImageModeDownload {
		replacements, warnings, failedImages := s.downloadImages(ctx, doc, page.URL, now, assetAbs, assetRel)
		doc.Warnings = append(doc.Warnings, warnings...)
		for _, replacement := range replacements {
			imageMap[replacement.Source] = replacement.Local
		}
		if len(failedImages) > 0 {
			doc.ContentHTML, err = removeHTMLImages(doc.ContentHTML, page.URL, failedImages)
			if err != nil {
				return ClipResult{}, fmt.Errorf("remove unsafe or unavailable images: %w", err)
			}
		}
	}

	articleMarkdown, err := htmlToMarkdown(doc.ContentHTML, page.URL)
	if err != nil {
		return ClipResult{}, fmt.Errorf("convert html to markdown: %w", err)
	}
	articleMarkdown = strings.TrimSpace(rewriteMarkdownImages(articleMarkdown, imageMap, req.ImageMode))

	markdown := buildMarkdown(doc, page.URL.String(), now, assetRel, articleMarkdown)
	if err := ensurePathInsideRoot(s.DataDir, markdownAbs); err != nil {
		return ClipResult{}, fmt.Errorf("validate markdown path before write: %w", err)
	}
	if err := writeFileExclusive(markdownAbs, []byte(markdown), 0o644); err != nil {
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
	validateURL := s.externalURLValidator(ctx)
	if err := validateURL(parsed); err != nil {
		return fetchedPage{}, fmt.Errorf("invalid url: %w", err)
	}

	client := s.httpClient()
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
	if resp.Request == nil || resp.Request.URL == nil {
		return fetchedPage{}, errors.New("fetch url: response is missing the final request url")
	}
	if err := validateURL(resp.Request.URL); err != nil {
		return fetchedPage{}, fmt.Errorf("fetch url: unsafe redirect target: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fetchedPage{}, fmt.Errorf("fetch url: unexpected status %d", resp.StatusCode)
	}
	reader, err := charset.NewReader(resp.Body, resp.Header.Get("Content-Type"))
	warnings := []string{}
	if err != nil {
		reader = resp.Body
		warnings = append(warnings, "charset判定に失敗したためUTF-8として処理しました")
	}
	body, err := readAllLimited(reader, maxPageBytes)
	if err != nil {
		return fetchedPage{}, fmt.Errorf("read response body: %w", err)
	}
	return fetchedPage{Body: body, URL: resp.Request.URL, Warnings: warnings}, nil
}

func (s Service) downloadImages(ctx context.Context, doc clipDocument, pageURL *url.URL, capturedAt time.Time, assetAbs, assetRel string) ([]imageReplacement, []string, map[string]struct{}) {
	imageCandidates := extractImageCandidates(doc.ContentHTML, pageURL)
	if len(imageCandidates) == 0 {
		return nil, nil, nil
	}
	if err := os.MkdirAll(assetAbs, 0o755); err != nil {
		return nil, []string{fmt.Sprintf("画像保存ディレクトリを作成できませんでした: %v", err)}, imageCandidateURLs(imageCandidates)
	}
	if err := ensurePathInsideRoot(s.DataDir, assetAbs); err != nil {
		return nil, []string{fmt.Sprintf("画像保存ディレクトリがdata directory外を指しています: %v", err)}, imageCandidateURLs(imageCandidates)
	}

	client := s.httpClient()
	validateURL := s.externalURLValidator(ctx)
	replacements := make([]imageReplacement, 0, len(imageCandidates))
	manifest := ImageManifest{Schema: ImageManifestSchema}
	warnings := []string{}
	failedImages := map[string]struct{}{}
	for i, candidate := range imageCandidates {
		urlExt := imageExt(candidate.ResolvedURL)
		localName := fmt.Sprintf("image-%03d%s", i+1, urlExt)
		localAbs := filepath.Join(assetAbs, localName)
		capture, err := downloadImage(ctx, client, candidate.ResolvedURL, localAbs, validateURL)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("画像のダウンロードに失敗しました: %s (%v)", candidate.ResolvedURL, err))
			failedImages[candidate.ResolvedURL] = struct{}{}
			continue
		}
		responseExt := imageExtFromContentType(capture.ContentType)
		if responseExt != urlExt {
			finalName := fmt.Sprintf("image-%03d%s", i+1, responseExt)
			finalAbs := filepath.Join(assetAbs, finalName)
			if err := moveFileExclusive(localAbs, finalAbs); err != nil {
				_ = os.Remove(localAbs)
				warnings = append(warnings, fmt.Sprintf("画像の拡張子を確定できませんでした: %s (%v)", candidate.ResolvedURL, err))
				failedImages[candidate.ResolvedURL] = struct{}{}
				continue
			}
			localName = finalName
		}
		localRel := filepath.ToSlash(filepath.Join(assetRel, localName))
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
	return replacements, warnings, failedImages
}

func imageCandidateURLs(candidates []imageCandidate) map[string]struct{} {
	urls := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		urls[candidate.ResolvedURL] = struct{}{}
	}
	return urls
}

func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s Service) httpClient() *http.Client {
	if s.Client == nil {
		return newSafeHTTPClient()
	}
	client := *s.Client
	if client.Timeout == 0 {
		client.Timeout = 20 * time.Second
	}
	previousRedirectPolicy := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := s.externalURLValidator(req.Context())(req.URL); err != nil {
			return fmt.Errorf("unsafe redirect target: %w", err)
		}
		if previousRedirectPolicy != nil {
			return previousRedirectPolicy(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &client
}

func newSafeHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("parse remote address: %w", err)
		}
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil {
			return nil, fmt.Errorf("resolve remote host: %w", err)
		}
		if len(ips) == 0 {
			return nil, errors.New("remote host resolved to no addresses")
		}
		for _, ip := range ips {
			if isUnsafeRemoteIP(ip) {
				return nil, fmt.Errorf("remote host resolves to a non-public address: %s", ip)
			}
		}
		var lastErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
	return &http.Client{
		Transport: transport,
		Timeout:   20 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if err := validateExternalHTTPURL(req.URL); err != nil {
				return fmt.Errorf("unsafe redirect target: %w", err)
			}
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			return nil
		},
	}
}

func validateExternalHTTPURL(parsed *url.URL) error {
	if parsed == nil {
		return errors.New("url is required")
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errors.New("url must use http or https")
	}
	if parsed.User != nil {
		return errors.New("url credentials are not allowed")
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" {
		return errors.New("url host is required")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".localdomain") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".home.arpa") {
		return fmt.Errorf("non-public host is not allowed: %s", host)
	}
	if ip := net.ParseIP(host); ip != nil && isUnsafeRemoteIP(ip) {
		return fmt.Errorf("non-public address is not allowed: %s", ip)
	}
	return nil
}

func (s Service) externalURLValidator(ctx context.Context) func(*url.URL) error {
	cache := map[string]error{}
	return func(parsed *url.URL) error {
		if err := validateExternalHTTPURL(parsed); err != nil {
			return err
		}
		host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if net.ParseIP(host) != nil {
			return nil
		}
		if cached, ok := cache[host]; ok {
			return cached
		}
		lookupContext, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		var (
			ips []net.IP
			err error
		)
		if s.ResolveHost != nil {
			ips, err = s.ResolveHost(lookupContext, host)
		} else {
			ips, err = net.DefaultResolver.LookupIP(lookupContext, "ip", host)
		}
		if err != nil {
			err = fmt.Errorf("resolve external host %s: %w", host, err)
			cache[host] = err
			return err
		}
		if len(ips) == 0 {
			err = fmt.Errorf("external host resolved to no addresses: %s", host)
			cache[host] = err
			return err
		}
		for _, ip := range ips {
			if isUnsafeRemoteIP(ip) {
				err = fmt.Errorf("external host resolves to a non-public address: %s (%s)", host, ip)
				cache[host] = err
				return err
			}
		}
		cache[host] = nil
		return nil
	}
}

func isUnsafeRemoteIP(ip net.IP) bool {
	if ip == nil || ip.IsUnspecified() || ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return true
	}
	for _, network := range blockedRemoteNetworks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseIPNetworks(rawCIDRs []string) []*net.IPNet {
	networks := make([]*net.IPNet, 0, len(rawCIDRs))
	for _, rawCIDR := range rawCIDRs {
		_, network, err := net.ParseCIDR(rawCIDR)
		if err != nil {
			panic(fmt.Sprintf("invalid built-in CIDR %q: %v", rawCIDR, err))
		}
		networks = append(networks, network)
	}
	return networks
}

func readAllLimited(reader io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("response exceeds %d byte limit", limit)
	}
	return body, nil
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

func sanitizeHTMLForMarkdown(rawHTML string, pageURL *url.URL, validateURL func(*url.URL) error) (string, int, error) {
	contextNode := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(rawHTML), contextNode)
	if err != nil {
		return "", 0, err
	}
	container := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		container.AppendChild(node)
	}
	removed := sanitizeHTMLChildren(container, pageURL, validateURL)
	var rendered strings.Builder
	for child := container.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&rendered, child); err != nil {
			return "", removed, err
		}
	}
	return rendered.String(), removed, nil
}

func removeHTMLImages(rawHTML string, pageURL *url.URL, blocked map[string]struct{}) (string, error) {
	contextNode := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(rawHTML), contextNode)
	if err != nil {
		return "", err
	}
	container := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		container.AppendChild(node)
	}
	removeBlockedImageNodes(container, pageURL, blocked)
	var rendered strings.Builder
	for child := container.FirstChild; child != nil; child = child.NextSibling {
		if err := html.Render(&rendered, child); err != nil {
			return "", err
		}
	}
	return rendered.String(), nil
}

func removeBlockedImageNodes(parent *html.Node, pageURL *url.URL, blocked map[string]struct{}) {
	for child := parent.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == html.ElementNode && child.Data == "img" {
			source := firstAttr(child, "src")
			if source == "" {
				source = firstAttr(child, "data-src")
			}
			if _, shouldRemove := blocked[resolveURL(pageURL, source)]; shouldRemove {
				parent.RemoveChild(child)
				child = next
				continue
			}
		}
		removeBlockedImageNodes(child, pageURL, blocked)
		child = next
	}
}

func sanitizeHTMLChildren(parent *html.Node, pageURL *url.URL, validateURL func(*url.URL) error) int {
	removed := 0
	for child := parent.FirstChild; child != nil; {
		next := child.NextSibling
		if child.Type == html.ElementNode && isActiveHTMLElement(child.Data) {
			parent.RemoveChild(child)
			removed++
			child = next
			continue
		}
		if child.Type == html.ElementNode {
			attributes := child.Attr[:0]
			for _, attr := range child.Attr {
				name := strings.ToLower(attr.Key)
				if strings.HasPrefix(name, "on") || name == "style" || name == "srcdoc" || name == "srcset" {
					removed++
					continue
				}
				if isHTMLURLAttribute(name) && !isSafeHTMLURL(pageURL, attr.Val, name == "href" || name == "xlink:href", validateURL) {
					removed++
					continue
				}
				attributes = append(attributes, attr)
			}
			child.Attr = attributes
		}
		removed += sanitizeHTMLChildren(child, pageURL, validateURL)
		child = next
	}
	return removed
}

func isActiveHTMLElement(name string) bool {
	switch strings.ToLower(name) {
	case "script", "style", "noscript", "iframe", "frame", "frameset", "object", "embed", "applet", "form", "input", "button", "select", "option", "textarea", "template", "svg", "math", "link", "meta", "base":
		return true
	default:
		return false
	}
}

func isHTMLURLAttribute(name string) bool {
	switch name {
	case "href", "src", "data-src", "poster", "action", "formaction", "xlink:href":
		return true
	default:
		return false
	}
}

func isSafeHTMLURL(pageURL *url.URL, raw string, allowMailto bool, validateURL func(*url.URL) error) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return true
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	if allowMailto && strings.HasPrefix(raw, "#") {
		return true
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if allowMailto && strings.EqualFold(parsed.Scheme, "mailto") {
		return true
	}
	if pageURL == nil {
		return false
	}
	resolved := pageURL.ResolveReference(parsed)
	return validateURL != nil && validateURL(resolved) == nil
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

func downloadImage(ctx context.Context, client *http.Client, imageURL, dest string, validateURL func(*url.URL) error) (imageCapture, error) {
	parsed, err := url.Parse(strings.TrimSpace(imageURL))
	if err != nil {
		return imageCapture{}, fmt.Errorf("invalid image url: %w", err)
	}
	if validateURL == nil {
		validateURL = validateExternalHTTPURL
	}
	if err := validateURL(parsed); err != nil {
		return imageCapture{}, fmt.Errorf("unsafe image url: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return imageCapture{}, err
	}
	req.Header.Set("User-Agent", "Karte WebClip/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return imageCapture{}, err
	}
	defer resp.Body.Close()
	if resp.Request == nil || resp.Request.URL == nil {
		return imageCapture{}, errors.New("image response is missing the final request url")
	}
	if err := validateURL(resp.Request.URL); err != nil {
		return imageCapture{}, fmt.Errorf("unsafe image redirect target: %w", err)
	}
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
	responseExt := imageExtFromContentType(resp.Header.Get("Content-Type"))
	if responseExt == ".img" {
		return capture, fmt.Errorf("unexpected content type %q", resp.Header.Get("Content-Type"))
	}
	if resp.ContentLength > maxImageBytes {
		return capture, fmt.Errorf("image exceeds %d byte limit", maxImageBytes)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return capture, err
	}
	file, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return capture, err
	}
	written, copyErr := io.Copy(file, io.LimitReader(resp.Body, maxImageBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(dest)
		return capture, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(dest)
		return capture, closeErr
	}
	if written > maxImageBytes {
		_ = os.Remove(dest)
		return capture, fmt.Errorf("image exceeds %d byte limit", maxImageBytes)
	}
	return capture, nil
}

func writeImageManifest(assetAbs string, manifest ImageManifest) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal image manifest: %w", err)
	}
	data = append(data, '\n')
	return writeFileExclusive(filepath.Join(assetAbs, ImageManifestFile), data, 0o644)
}

func writeFileExclusive(path string, data []byte, perm os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func moveFileExclusive(source, destination string) error {
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}
	destinationFile, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, sourceInfo.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(destinationFile, sourceFile); err != nil {
		_ = destinationFile.Close()
		_ = os.Remove(destination)
		return err
	}
	if err := destinationFile.Close(); err != nil {
		_ = os.Remove(destination)
		return err
	}
	if err := os.Remove(source); err != nil {
		_ = os.Remove(destination)
		return err
	}
	return nil
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
		case ".jpg", ".jpeg", ".png", ".gif", ".webp":
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

func imageExtFromContentType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ".img"
	}
	switch strings.ToLower(mediaType) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	default:
		return ".img"
	}
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

func ensurePathInsideRoot(root, candidate string) error {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	candidateAbs, err := filepath.Abs(candidate)
	if err != nil {
		return err
	}
	lexicalRel, err := filepath.Rel(rootAbs, candidateAbs)
	if err != nil || pathEscapesRoot(lexicalRel) {
		return fmt.Errorf("path escapes data directory: %s", candidate)
	}
	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return fmt.Errorf("resolve data directory: %w", err)
	}
	current := rootAbs
	components := strings.Split(filepath.Clean(lexicalRel), string(filepath.Separator))
	for _, component := range components {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		if _, err := os.Lstat(current); err != nil {
			if os.IsNotExist(err) {
				break
			}
			return err
		}
		resolvedCurrent, err := filepath.EvalSymlinks(current)
		if err != nil {
			return err
		}
		resolvedRel, err := filepath.Rel(resolvedRoot, resolvedCurrent)
		if err != nil || pathEscapesRoot(resolvedRel) {
			return fmt.Errorf("path escapes data directory through symlink: %s", candidate)
		}
	}
	return nil
}

func pathEscapesRoot(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative)
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
	escaped = strings.ReplaceAll(escaped, "\r", `\r`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)
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
