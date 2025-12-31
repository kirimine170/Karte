package site

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path/filepath"
	"testing"
	"time"
)

type mockFileInfo struct {
	name string
	size int64
}

func (m mockFileInfo) Name() string       { return m.name }
func (m mockFileInfo) Size() int64        { return m.size }
func (m mockFileInfo) Mode() fs.FileMode  { return 0644 }
func (m mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m mockFileInfo) IsDir() bool        { return false }
func (m mockFileInfo) Sys() any           { return nil }

type mockFileSystem struct {
	files   map[string][]byte
	statErr map[string]error
	openErr map[string]error
}

func (m *mockFileSystem) ReadFile(name string) ([]byte, error) {
	data, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return data, nil
}

func (m *mockFileSystem) Stat(name string) (fs.FileInfo, error) {
	if err, ok := m.statErr[name]; ok {
		return nil, err
	}
	if data, ok := m.files[name]; ok {
		return mockFileInfo{name: filepath.Base(name), size: int64(len(data))}, nil
	}
	return nil, fs.ErrNotExist
}

func (m *mockFileSystem) Open(name string) (io.ReadCloser, error) {
	if err, ok := m.openErr[name]; ok {
		return nil, err
	}
	data, ok := m.files[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	return mockReadCloser{bytes.NewReader(data)}, nil
}

type mockReadCloser struct{ *bytes.Reader }

func (mockReadCloser) Close() error { return nil }

func TestRenderMarkdownWithMockFS(t *testing.T) {
	root := "./root"
	mdPath := filepath.Join(root, "content", "test.md")
	layoutPath := filepath.Join(root, "themes", "default", "layout.html")
	fsys := &mockFileSystem{
		files: map[string][]byte{
			mdPath:     []byte("---\ntitle: Title\n---\n\nHello"),
			layoutPath: []byte("<html><head><title>{{TITLE}}</title></head><body>{{CONTENT}}</body></html>"),
		},
	}

	renderer := NewRenderer(fsys)
	html, fm, err := renderer.RenderMarkdownWithOptions(root, mdPath, false)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if fm == nil || fm.Title != "Title" {
		t.Fatalf("unexpected frontmatter: %+v", fm)
	}
	if !bytes.Contains([]byte(html), []byte("Hello")) {
		t.Fatalf("rendered HTML missing content: %s", html)
	}
}

func TestRenderMarkdownMissingFile(t *testing.T) {
	renderer := NewRenderer(&mockFileSystem{files: map[string][]byte{}})
	_, _, err := renderer.RenderMarkdownWithOptions("/tmp", "/tmp/missing.md", false)
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected not exist error, got %v", err)
	}
}

func TestRenderCSVSelectAndEmpty(t *testing.T) {
	root := "./root"
	csvPath := filepath.Join(root, "data", "sample.csv")
	emptyPath := filepath.Join(root, "data", "empty.csv")
	fsys := &mockFileSystem{files: map[string][]byte{
		csvPath:   []byte("col1,col2,col3\na1,a2,a3\nb1,b2,b3"),
		emptyPath: []byte(""),
	}}

	renderer := NewRenderer(fsys)

	html := renderer.renderCSV(csvPath, map[string]string{"select": "col1,col3"})
	expected := "<table><thead><tr><th>col1</th><th>col3</th></tr></thead><tbody><tr><td>a1</td><td>a3</td></tr><tr><td>b1</td><td>b3</td></tr></tbody></table>"
	if html != expected {
		t.Fatalf("unexpected csv render: %s", html)
	}

	emptyHTML := renderer.renderCSV(emptyPath, map[string]string{})
	if emptyHTML != "<p>(no data)</p>" {
		t.Fatalf("expected no data message, got %s", emptyHTML)
	}
}

func TestRenderMarkdownWithOptionsImportErrors(t *testing.T) {
	root := "./root"
	mdPath := filepath.Join(root, "content", "test.md")
	layoutPath := filepath.Join(root, "themes", "default", "layout.html")
	fsys := &mockFileSystem{files: map[string][]byte{
		mdPath:     []byte("@import(type=\"unknown\" path=\"data.csv\")\n@import(type=\"md\" path=\"content/missing.md\")"),
		layoutPath: []byte("<html><head><title>{{TITLE}}</title></head><body>{{CONTENT}}</body></html>"),
	}}

	renderer := NewRenderer(fsys)
	html, _, err := renderer.RenderMarkdownWithOptions(root, mdPath, false)
	if err != nil {
		t.Fatalf("render failed: %v", err)
	}

	if !bytes.Contains([]byte(html), []byte("unknown import type: unknown")) {
		t.Fatalf("expected unknown import type message, got %s", html)
	}
	if !bytes.Contains([]byte(html), []byte("Error include: ")) {
		t.Fatalf("expected include error message, got %s", html)
	}
}

func TestParseArgs(t *testing.T) {
	args := parseArgs(`@import(  type = "csv"   path="data.csv" select = " col1 , col2 "  )`)
	expected := map[string]string{
		"type":   "csv",
		"path":   "data.csv",
		"select": " col1 , col2 ",
	}

	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d", len(expected), len(args))
	}

	for k, v := range expected {
		if args[k] != v {
			t.Fatalf("unexpected value for %s: %s", k, args[k])
		}
func TestProcessKaTeX(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "inline math replaced and entities decoded",
			input:    "<p>$a &amp; b$ inside text</p>",
			expected: "<p><span class=\"katex-inline\">a & b</span> inside text</p>",
		},
		{
			name:     "block math with newlines and entities decoded",
			input:    "<section>$$$x &amp; y\n+ z$$$</section>",
			expected: "<section><div class=\"katex-block\">x & y\n+ z</div></section>",
		},
		{
			name:     "inline code preserved",
			input:    "<p><code>$x$ &amp; y</code> text</p>",
			expected: "<p><code>$x$ &amp; y</code> text</p>",
		},
		{
			name:     "pre code preserved while math converts",
			input:    "<pre><code class=\"language-go\">$foo$ &amp; bar\nreturn</code></pre><p>$1+1$</p>",
			expected: "<pre><code class=\"language-go\">$foo$ &amp; bar\nreturn</code></pre><p><span class=\"katex-inline\">1+1</span></p>",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := processKaTeX(tt.input)
			if got != tt.expected {
				t.Fatalf("unexpected output:\ninput: %s\nwant:  %s\ngot:   %s", tt.input, tt.expected, got)
			}
		})
	}
}
