package site

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	htmlr "github.com/yuin/goldmark/renderer/html"
	"gopkg.in/yaml.v3"
)

type FrontMatter struct {
	Title   string         `yaml:"title"`
	Layout  string         `yaml:"layout"`
	Owners  []string       `yaml:"owners"`
	Viewers []string       `yaml:"viewers"`
	Extra   map[string]any `yaml:",inline"`
}

// Build a goldmark instance per render to support options like hardwraps
func newMarkdown(hardwrap bool) goldmark.Markdown {
	opts := []goldmark.Option{
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(htmlr.WithUnsafe()), // allow raw HTML
	}
	if hardwrap {
		opts = append(opts, goldmark.WithRendererOptions(htmlr.WithHardWraps()))
	}
	return goldmark.New(opts...)
}

var fmRe = regexp.MustCompile(`(?s)^---\n(.*?)\n---\n`)
var impRe = regexp.MustCompile(`(?m)^@import\((.*?)\)\s*$`)

const forcePageBreakHTML = `<div class="karte-force-page-break" aria-hidden="true"></div>`

const (
	mermaidCDNURL = "https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"
	katexCSSURL   = "https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.css"
	katexJSURL    = "https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.js"
)

func injectManualPageBreakMarkers(body []byte) []byte {
	if len(body) == 0 {
		return body
	}
	text := strings.ReplaceAll(string(body), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if line == "===" {
			out = append(out, forcePageBreakHTML)
			continue
		}
		if line == "---" && i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "---" {
			out = append(out, forcePageBreakHTML)
			i++
			continue
		}
		out = append(out, lines[i])
	}
	return []byte(strings.Join(out, "\n"))
}

// FileSystem abstracts file access for rendering.
type FileSystem interface {
	ReadFile(name string) ([]byte, error)
	Stat(name string) (fs.FileInfo, error)
	Open(name string) (io.ReadCloser, error)
}

// OSFileSystem implements FileSystem using the os package.
type OSFileSystem struct{}

func (OSFileSystem) ReadFile(name string) ([]byte, error)    { return os.ReadFile(name) }
func (OSFileSystem) Stat(name string) (fs.FileInfo, error)   { return os.Stat(name) }
func (OSFileSystem) Open(name string) (io.ReadCloser, error) { return os.Open(name) }

// Renderer renders Markdown content using the provided FileSystem.
type Renderer struct {
	fs FileSystem
}

// NewRenderer constructs a Renderer.
func NewRenderer(fs FileSystem) *Renderer {
	if fs == nil {
		fs = OSFileSystem{}
	}
	return &Renderer{fs: fs}
}

var defaultRenderer = NewRenderer(OSFileSystem{})

func RenderMarkdown(root, path string) (string, *FrontMatter, error) {
	return defaultRenderer.RenderMarkdownWithOptions(root, path, false)
}

func RenderMarkdownWithOptions(root, path string, hardwrap bool) (string, *FrontMatter, error) {
	return defaultRenderer.RenderMarkdownWithOptions(root, path, hardwrap)
}

// RenderMarkdownWithOptions renders markdown content using the configured FileSystem.
func (r *Renderer) RenderMarkdownWithOptions(root, path string, hardwrap bool) (string, *FrontMatter, error) {
	b, err := r.fs.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	fm := &FrontMatter{}
	body := b
	if m := fmRe.FindSubmatch(b); m != nil {
		if err := yaml.Unmarshal(m[1], fm); err != nil {
			// If YAML parsing fails, treat as if there's no frontmatter
			// This allows rendering markdown even with malformed frontmatter
			fm = &FrontMatter{}
			body = b
		} else {
			body = b[len(m[0]):]
		}
	}
	expanded := impRe.ReplaceAllFunc(body, func(line []byte) []byte {
		argStr := string(line)
		args := parseArgs(argStr)
		typ := args["type"]
		p := args["path"]
		switch typ {
		case "csv":
			abs := filepath.Join(root, p)
			html := r.renderCSV(abs, args)
			return []byte(html)
		case "md":
			abs := filepath.Join(root, p)
			child, _, err := r.RenderMarkdownWithOptions(root, abs, hardwrap)
			if err != nil {
				return []byte(fmt.Sprintf("<p>Error include: %v</p>", err))
			}
			return []byte(child)
		default:
			return []byte(fmt.Sprintf("<p>unknown import type: %s</p>", html.EscapeString(typ)))
		}
	})
	expanded = injectManualPageBreakMarkers(expanded)
	var buf bytes.Buffer
	md := newMarkdown(hardwrap)
	if err := md.Convert(expanded, &buf); err != nil {
		return "", nil, err
	}
	htmlContent := buf.String()
	htmlContent = processMermaidBlocks(htmlContent)
	// Process KaTeX math expressions
	htmlContent = processKaTeX(htmlContent)
	htmlOut := r.wrapWithLayout(root, fm, htmlContent)
	return htmlOut, fm, nil
}

func processMermaidBlocks(htmlContent string) string {
	re := regexp.MustCompile(`(?is)<pre>\s*<code class="(?:language-mermaid|lang-mermaid)">([\s\S]*?)</code>\s*</pre>`)
	return re.ReplaceAllStringFunc(htmlContent, func(match string) string {
		sub := re.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		source := html.UnescapeString(strings.TrimSpace(sub[1]))
		if source == "" {
			return match
		}
		return `<div class="mermaid">` + source + `</div>`
	})
}

func (r *Renderer) wrapWithLayout(root string, fm *FrontMatter, inner string) string {
	// Check if this is a preview request by looking for preview layout
	previewLayoutPath := filepath.Join(root, "themes", "default", "preview.html")
	layoutPath := filepath.Join(root, "themes", "default", "layout.html")

	var b []byte
	var err error

	// Try preview layout first, fallback to regular layout
	if _, statErr := r.fs.Stat(previewLayoutPath); statErr == nil {
		b, err = r.fs.ReadFile(previewLayoutPath)
	} else {
		b, err = r.fs.ReadFile(layoutPath)
	}

	if err != nil {
		return fmt.Sprintf(`<!doctype html>
<html>
<meta charset="utf-8"><title>%s</title>
            <script src="https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"></script>
            <script>mermaid.initialize({startOnLoad:true});</script>
            <body><main class="container">%s</main></body>
</html>`, html.EscapeString(fm.Title), inner)
	}
	s := string(b)
	s = strings.ReplaceAll(s, "{{TITLE}}", html.EscapeString(fm.Title))
	s = strings.ReplaceAll(s, "{{CONTENT}}", inner)
	s = injectRenderAssets(s)
	s = injectRenderHelpers(s)
	return s
}

func injectRenderAssets(htmlContent string) string {
	var inserts []string
	if !strings.Contains(htmlContent, mermaidCDNURL) {
		inserts = append(inserts, `<script src="`+mermaidCDNURL+`"></script>`)
	}
	if !strings.Contains(htmlContent, katexCSSURL) {
		inserts = append(inserts, `<link rel="stylesheet" href="`+katexCSSURL+`">`)
	}
	if !strings.Contains(htmlContent, katexJSURL) {
		inserts = append(inserts, `<script src="`+katexJSURL+`"></script>`)
	}
	if len(inserts) == 0 {
		return htmlContent
	}

	injection := strings.Join(inserts, "\n")
	if strings.Contains(htmlContent, "</head>") {
		return strings.Replace(htmlContent, "</head>", injection+"\n</head>", 1)
	}
	if strings.Contains(htmlContent, "<head>") {
		return strings.Replace(htmlContent, "<head>", "<head>\n"+injection+"\n", 1)
	}
	if strings.Contains(strings.ToLower(htmlContent), "<html") {
		re := regexp.MustCompile(`(?i)<html([^>]*)>`)
		return re.ReplaceAllString(htmlContent, `<html$1><head>`+injection+`</head>`)
	}
	if strings.Contains(strings.ToLower(htmlContent), "<!doctype html>") {
		re := regexp.MustCompile(`(?i)<!doctype html>`)
		return re.ReplaceAllString(htmlContent, "<!doctype html>\n<head>\n"+injection+"\n</head>")
	}
	return injection + "\n" + htmlContent
}

func injectRenderHelpers(htmlContent string) string {
	if strings.Contains(htmlContent, "karte-render-enhancers") {
		return htmlContent
	}
	script := `<script id="karte-render-enhancers">
(function() {
  function decodeHtmlEntities(text) {
    var textarea = document.createElement('textarea');
    textarea.innerHTML = text;
    return textarea.value;
  }
  function renderKaTeX() {
    if (typeof katex === 'undefined') return false;
    document.querySelectorAll('.katex-inline').forEach(function(el) {
      if (el.querySelector('.katex')) return;
      var raw = (el.textContent || '').trim();
      if (!raw) return;
      try { katex.render(decodeHtmlEntities(raw), el, { throwOnError: false, displayMode: false }); } catch (e) {}
    });
    document.querySelectorAll('.katex-block').forEach(function(el) {
      if (el.querySelector('.katex')) return;
      var raw = (el.textContent || '').trim();
      if (!raw) return;
      try { katex.render(decodeHtmlEntities(raw), el, { throwOnError: false, displayMode: true }); } catch (e) {}
    });
    window.__karteKaTeXReady = true;
    return true;
  }
  function convertMermaidCodeBlocks() {
    document.querySelectorAll('pre > code.language-mermaid, pre > code.lang-mermaid').forEach(function(code) {
      var pre = code.parentElement;
      if (!pre) return;
      var container = document.createElement('div');
      container.className = 'mermaid';
      container.textContent = code.textContent || '';
      pre.replaceWith(container);
    });
  }
  function renderMermaid() {
    convertMermaidCodeBlocks();
    if (typeof mermaid === 'undefined') return false;
    var nodes = document.querySelectorAll('.mermaid:not([data-processed])');
    if (nodes.length === 0) {
      window.__karteMermaidReady = true;
      return true;
    }
    try {
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'loose',
        htmlLabels: true,
        flowchart: { htmlLabels: true },
        sequence: { htmlLabels: true }
      });
    } catch (e) {}
    try {
      var result = mermaid.run({ nodes: nodes });
      Promise.resolve(result).finally(function() {
        window.__karteMermaidReady = document.querySelectorAll('.mermaid:not([data-processed])').length === 0;
        renderKaTeX();
      });
    } catch (e) {}
    return true;
  }
  function runAll() {
    renderKaTeX();
    renderMermaid();
  }
  window.__karteRunRenderEnhancers = runAll;
  function schedule() {
    var attempts = 0;
    var timer = setInterval(function() {
      attempts++;
      runAll();
      if (attempts > 200) clearInterval(timer);
      if (typeof katex !== 'undefined' && typeof mermaid !== 'undefined') clearInterval(timer);
    }, 50);
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', schedule);
  } else {
    schedule();
  }
})();
</script>`
	if strings.Contains(htmlContent, "</body>") {
		return strings.Replace(htmlContent, "</body>", script+"</body>", 1)
	}
	if strings.Contains(htmlContent, "</html>") {
		return strings.Replace(htmlContent, "</html>", script+"</html>", 1)
	}
	return htmlContent + script
}

var argKV = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*"([^"]*)"`)

func parseArgs(s string) map[string]string {
	m := map[string]string{}
	ms := argKV.FindAllStringSubmatch(s, -1)
	for _, a := range ms {
		m[a[1]] = a[2]
	}
	return m
}

func (r *Renderer) renderCSV(abs string, args map[string]string) string {
	f, err := r.fs.Open(abs)
	if err != nil {
		return fmt.Sprintf("<p>csv open error: %v</p>", err)
	}
	defer f.Close()
	reader := csv.NewReader(f)
	rec, err := reader.ReadAll()
	if err != nil {
		return fmt.Sprintf("<p>csv read error: %v</p>", err)
	}
	if len(rec) == 0 {
		return "<p>(no data)</p>"
	}
	selectCols := parseCSVList(args["select"])
	header := rec[0]
	idx := make([]int, 0)
	if len(selectCols) == 0 {
		for i := range header {
			idx = append(idx, i)
		}
	} else {
		for _, name := range selectCols {
			for i, h := range header {
				if h == name {
					idx = append(idx, i)
					break
				}
			}
		}
	}
	var b strings.Builder
	b.WriteString("<table><thead><tr>")
	for _, i := range idx {
		b.WriteString("<th>")
		b.WriteString(html.EscapeString(header[i]))
		b.WriteString("</th>")
	}
	b.WriteString("</tr></thead><tbody>")
	for _, row := range rec[1:] {
		b.WriteString("<tr>")
		for _, i := range idx {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			b.WriteString("<td>")
			b.WriteString(html.EscapeString(cell))
			b.WriteString("</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

func parseCSVList(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	p := strings.Split(s, ",")
	for i := range p {
		p[i] = strings.TrimSpace(p[i])
	}
	return p
}

// processKaTeX processes KaTeX math expressions in HTML
// Converts $...$ to inline math and $$$...$$$ to block math
// Excludes code blocks (<pre><code>...</code></pre> and <code>...</code>)
func processKaTeX(htmlContent string) string {
	// First, protect code blocks by replacing them with placeholders
	type codeBlock struct {
		placeholder string
		content     string
	}
	var codeBlocks []codeBlock
	codeBlockIndex := 0

	// Protect <pre><code>...</code></pre> blocks
	preCodeRegex := regexp.MustCompile(`<pre><code[^>]*>[\s\S]*?</code></pre>`)
	htmlContent = preCodeRegex.ReplaceAllStringFunc(htmlContent, func(match string) string {
		placeholder := fmt.Sprintf("__CODE_BLOCK_PLACEHOLDER_%d__", codeBlockIndex)
		codeBlocks = append(codeBlocks, codeBlock{
			placeholder: placeholder,
			content:     match,
		})
		codeBlockIndex++
		return placeholder
	})

	// Protect inline <code>...</code> blocks
	inlineCodeRegex := regexp.MustCompile(`<code[^>]*>[^<]*</code>`)
	htmlContent = inlineCodeRegex.ReplaceAllStringFunc(htmlContent, func(match string) string {
		placeholder := fmt.Sprintf("__INLINE_CODE_PLACEHOLDER_%d__", codeBlockIndex)
		codeBlocks = append(codeBlocks, codeBlock{
			placeholder: placeholder,
			content:     match,
		})
		codeBlockIndex++
		return placeholder
	})

	// Process block math: $$$...$$$ (can span multiple lines)
	blockMathRegex := regexp.MustCompile(`\$\$\$([\s\S]*?)\$\$\$`)
	htmlContent = blockMathRegex.ReplaceAllStringFunc(htmlContent, func(match string) string {
		mathContent := blockMathRegex.FindStringSubmatch(match)[1]
		// Decode HTML entities that goldmark may have created (e.g., &amp;, &lt;, &gt;)
		mathContent = html.UnescapeString(mathContent)
		// Trim whitespace
		mathContent = strings.TrimSpace(mathContent)
		return fmt.Sprintf(`<div class="katex-block">%s</div>`, mathContent)
	})

	// Process inline math: $...$ (single line, no newlines)
	inlineMathRegex := regexp.MustCompile(`\$([^$\n]+?)\$`)
	htmlContent = inlineMathRegex.ReplaceAllStringFunc(htmlContent, func(match string) string {
		mathContent := inlineMathRegex.FindStringSubmatch(match)[1]
		// Decode HTML entities that goldmark may have created (e.g., &amp;, &lt;, &gt;)
		mathContent = html.UnescapeString(mathContent)
		return fmt.Sprintf(`<span class="katex-inline">%s</span>`, mathContent)
	})

	// Restore code blocks
	for _, cb := range codeBlocks {
		htmlContent = strings.ReplaceAll(htmlContent, cb.placeholder, cb.content)
	}

	return htmlContent
}
