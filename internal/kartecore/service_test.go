package kartecore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteRejectsPathEscape(t *testing.T) {
	tmp := t.TempDir()
	svc := New(tmp, nil)
	if err := svc.Init(); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	err := svc.Write("content/../evil.md", "# bad", true)
	if err == nil {
		t.Fatal("expected error")
	}
	coreErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}
	if coreErr.Code != ErrCodeInvalidInput {
		t.Fatalf("unexpected code: %s", coreErr.Code)
	}
}

func TestNormalizeDocumentPathAddsContentPrefix(t *testing.T) {
	got, err := normalizeDocumentPath("notes/a.md")
	if err != nil {
		t.Fatalf("normalize failed: %v", err)
	}
	if got != "content/notes/a.md" {
		t.Fatalf("unexpected normalized path: %s", got)
	}
}

func TestWriteNormalizesFrontMatterAndDocID(t *testing.T) {
	tmp := t.TempDir()
	svc := New(tmp, nil)
	if err := svc.Init(); err != nil {
		t.Fatalf("init failed: %v", err)
	}
	content := "---\ntitle:\"A\"\ntags:\"x,  y\"\n---\n\nbody"
	if err := svc.Write("a.md", content, true); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	read, err := svc.Read("a.md")
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(read, `title: "A"`) {
		t.Fatalf("title was not normalized: %s", read)
	}
	if !strings.Contains(read, `tags: "x, y"`) {
		t.Fatalf("tags were not normalized: %s", read)
	}
	if !strings.Contains(read, `doc_id: "`) {
		t.Fatalf("doc_id missing: %s", read)
	}
}

func TestInitToGraphFlow(t *testing.T) {
	tmp := t.TempDir()
	svc := New(tmp, nil)
	if err := svc.Init(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	if _, err := svc.Create("a.md", "Alpha"); err != nil {
		t.Fatalf("create a failed: %v", err)
	}
	if err := svc.Write("b.md", "# Beta\n\n[[a]]\n", true); err != nil {
		t.Fatalf("write b failed: %v", err)
	}
	items, err := svc.ListFiles()
	if err != nil {
		t.Fatalf("list failed: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected at least 2 files, got %d", len(items))
	}

	content, err := svc.Read("b.md")
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(content, "[[a]]") {
		t.Fatalf("unexpected content: %s", content)
	}

	if err := svc.Build(); err != nil {
		t.Fatalf("build failed: %v", err)
	}
	publicA := filepath.Join(svc.DataDir, "public", "a.html")
	if _, err := os.Stat(publicA); err != nil {
		t.Fatalf("expected built file %s: %v", publicA, err)
	}

	graph, err := svc.Graph()
	if err != nil {
		t.Fatalf("graph failed: %v", err)
	}
	if len(graph.Nodes) == 0 {
		t.Fatal("graph nodes should not be empty")
	}
	if len(graph.Edges) == 0 {
		t.Fatal("graph edges should not be empty")
	}
}
