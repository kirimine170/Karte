package main

import (
	"context"
	"encoding/base64"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"karte/internal/contextcore"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func researchTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"content", "data/audio", "data/image"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return &App{root: root, dataDir: root}
}
func TestResearchResourcesResolveOnlyManagedFiles(t *testing.T) {
	app := researchTestApp(t)
	for path, data := range map[string]string{"content/a.md": "---\ntitle: 資料\ndoc_id: doc:test\n---\n現地スケッチを参照．", "content/paper.pdf": "%PDF-1.7\n", "data/audio/録音 #1.wav": "RIFF", "data/image/sketch.webp": "RIFF"} {
		if err := os.WriteFile(filepath.Join(app.dataDir, filepath.FromSlash(path)), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := app.LoadResearchResource("content/a.md")
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "markdown" || r.EditPath != "content/a.md" || !strings.Contains(r.Body, "現地スケッチ") {
		t.Fatalf("bad document: %#v", r)
	}
	r, err = app.LoadResearchResource("data/audio/録音 #1.wav")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(r.URL, "#") || strings.Contains(r.URL, " ") {
		t.Fatalf("unescaped media URL: %s", r.URL)
	}
	for _, path := range []string{"../secret.md", "content/../secret.md", "/etc/passwd", "data/audio/../../secret.wav", "content\\a.md", "content/a.board.md"} {
		if _, err = app.LoadResearchResource(path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.md")
	if err = os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(app.dataDir, "content", "escape.md")); err == nil {
		if _, err = app.LoadResearchResource("content/escape.md"); err == nil {
			t.Fatal("followed symlink outside managed content")
		}
	}
}
func TestResearchTranscriptUsesCanonicalDocument(t *testing.T) {
	app := researchTestApp(t)
	audio := "data/audio/test.wav"
	if err := os.WriteFile(filepath.Join(app.dataDir, filepath.FromSlash(audio)), []byte("RIFF"), 0644); err != nil {
		t.Fatal(err)
	}
	text := "---\ntitle: 聞き取り\ndoc_id: doc:transcript\naudio_path: data/audio/test.wav\n---\n確認した内容\n"
	if err := os.WriteFile(filepath.Join(app.dataDir, "content", "transcript.md"), []byte(text), 0644); err != nil {
		t.Fatal(err)
	}
	edit, err := app.EditResearchResource(audio)
	if err != nil {
		t.Fatal(err)
	}
	if edit != "content/transcript.md" {
		t.Fatalf("lost associated transcript: %s", edit)
	}
	r, err := app.LoadResearchResource(audio)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Body, "確認した内容") {
		t.Fatalf("missing canonical transcript: %#v", r)
	}
}
func TestResearchResourceReadHonorsHumanPolicy(t *testing.T) {
	app, s, _, raw := newRuntimeRecordTestApp(t)
	receipt, err := s.Apply(raw)
	if err != nil {
		t.Fatal(err)
	}
	path := "content/projects/synthetic-diary/note/2026-09/ephy-v2-" + receipt.Applied.DocID + ".md"
	if _, err = app.LoadResearchResource(path); err != nil {
		t.Fatal(err)
	}
	policy := contextcore.DefaultPolicy()
	human := policy.Actors["human"]
	human.SensitivityCeiling = "public"
	policy.Actors["human"] = human
	if err = s.SetPrivacyPolicy(policy); err != nil {
		t.Fatal(err)
	}
	if _, err = app.LoadResearchResource(path); err == nil {
		t.Fatal("research resource bypassed human policy")
	}
	for _, r := range app.ListResearchResources() {
		if r.Path == path {
			t.Fatal("research library exposed denied record")
		}
	}
}

func TestResearchBoardExportUsesNativeDialogAndCancellation(t *testing.T) {
	original := postLabSaveDialog
	t.Cleanup(func() { postLabSaveDialog = original })
	path := filepath.Join(t.TempDir(), "research.wall.json")
	calls := 0
	postLabSaveDialog = func(_ context.Context, options runtime.SaveDialogOptions) (string, error) {
		calls++
		if options.DefaultFilename != "research.wall.json" {
			t.Fatal(options.DefaultFilename)
		}
		return path, nil
	}
	app := &App{ctx: context.Background()}
	content := `{"schema":"karte-research-wall/1","title":"研究","cards":[],"edges":[]}`
	saved, err := app.SaveResearchBoardExport("../../research.wall.json", content)
	if err != nil || saved != path {
		t.Fatalf("%s %v", saved, err)
	}
	bytes, err := os.ReadFile(path)
	if err != nil || string(bytes) != content {
		t.Fatal("portable export changed")
	}
	if _, err = app.SaveResearchBoardExport("research.wall.json", `{"schema":"other"}`); err == nil || calls != 1 {
		t.Fatal("invalid JSON reached file dialog")
	}
	postLabSaveDialog = func(context.Context, runtime.SaveDialogOptions) (string, error) { return "", nil }
	if saved, err = app.SaveResearchBoardExport("research.wall.json", content); err != nil || saved != "" {
		t.Fatal("cancellation is not a no-op")
	}
}

func TestResearchMarkdownImportPreservesMetadataWithNewIdentity(t *testing.T) {
	app := researchTestApp(t)
	text := "---\ndoc_id: original-document\ntitle: 研究メモ\ntags: [研究]\n---\n\n# メモ\n\n現地スケッチを参照．\n"
	resource, err := app.ImportResearchResource("notes.md", base64.StdEncoding.EncodeToString([]byte(text)))
	if err != nil {
		t.Fatal(err)
	}
	if resource.Path == "" || resource.EditPath != resource.Path {
		t.Fatal("import is not managed")
	}
	if strings.Contains(resource.Body, "original-document") || !strings.Contains(resource.Body, "doc_id:") {
		t.Fatalf("canonical identity was reused: %s", resource.Body)
	}
	for _, want := range []string{"研究メモ", "研究", "現地スケッチを参照．"} {
		if !strings.Contains(resource.Body, want) {
			t.Fatalf("lost imported metadata or body: %s", resource.Body)
		}
	}
}
