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
