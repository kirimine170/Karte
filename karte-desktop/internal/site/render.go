package site

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"html"
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

func RenderMarkdown(root, path string) (string, *FrontMatter, error) {
	return RenderMarkdownWithOptions(root, path, false)
}

func RenderMarkdownWithOptions(root, path string, hardwrap bool) (string, *FrontMatter, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, err
	}
	fm := &FrontMatter{}
	body := b
	if m := fmRe.FindSubmatch(b); m != nil {
		if err := yaml.Unmarshal(m[1], fm); err != nil {
			return "", nil, err
		}
		body = b[len(m[0]):]
	}
	expanded := impRe.ReplaceAllFunc(body, func(line []byte) []byte {
		argStr := string(line)
		args := parseArgs(argStr)
		typ := args["type"]
		p := args["path"]
		switch typ {
		case "csv":
			abs := filepath.Join(root, p)
			html := renderCSV(abs, args)
			return []byte(html)
		case "md":
			abs := filepath.Join(root, p)
			child, _, err := RenderMarkdown(root, abs)
			if err != nil {
				return []byte(fmt.Sprintf("<p>Error include: %v</p>", err))
			}
			return []byte(child)
		default:
			return []byte(fmt.Sprintf("<p>unknown import type: %s</p>", html.EscapeString(typ)))
		}
	})
	var buf bytes.Buffer
	md := newMarkdown(hardwrap)
	if err := md.Convert(expanded, &buf); err != nil {
		return "", nil, err
	}
	htmlOut := wrapWithLayout(root, fm, buf.String())
	return htmlOut, fm, nil
}

func wrapWithLayout(root string, fm *FrontMatter, inner string) string {
	// Check if this is a preview request by looking for preview layout
	previewLayoutPath := filepath.Join(root, "themes", "default", "preview.html")
	layoutPath := filepath.Join(root, "themes", "default", "layout.html")

	var b []byte
	var err error

	// Try preview layout first, fallback to regular layout
	if _, statErr := os.Stat(previewLayoutPath); statErr == nil {
		b, err = os.ReadFile(previewLayoutPath)
	} else {
		b, err = os.ReadFile(layoutPath)
	}

	if err != nil {
		return fmt.Sprintf(`<!doctype html><meta charset="utf-8"><title>%s</title>
            <script src="https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"></script>
            <script>mermaid.initialize({startOnLoad:true});</script>
            <body><main class="container">%s</main></body>`, html.EscapeString(fm.Title), inner)
	}
	s := string(b)
	s = strings.ReplaceAll(s, "{{TITLE}}", html.EscapeString(fm.Title))
	s = strings.ReplaceAll(s, "{{CONTENT}}", inner)
	return s
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

func renderCSV(abs string, args map[string]string) string {
	f, err := os.Open(abs)
	if err != nil {
		return fmt.Sprintf("<p>csv open error: %v</p>", err)
	}
	defer f.Close()
	r := csv.NewReader(f)
	rec, err := r.ReadAll()
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
