package main

import (
	"bytes"
	"karte/internal/worklog"
	"os"
	"path/filepath"
	"testing"
)

func TestSiteBuildExcludesWorklogAndKeepsOrdinaryContent(t *testing.T) {
	app, root := newEphyTestApp(t)
	if err := worklog.Initialize(root, worklog.Config{Version: worklog.Version, Enabled: true, Actor: "ephy-worklog", Project: "ephy-worklog", Timezone: "Asia/Tokyo", Sensitivity: "internal"}); err != nil {
		t.Fatal(err)
	}
	s, err := worklog.New(root, "model_report")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Start(worklog.StartInput{TaskID: "private-task", EventID: "start", Title: "private worklog sentinel", Objective: "private worklog sentinel"}); err != nil {
		t.Fatal(err)
	}
	if err := app.SaveFile("content/ordinary.md", "---\ndoc_id: ordinary\ntitle: Ordinary\n---\nordinary public document\n"); err != nil {
		t.Fatal(err)
	}
	if err := app.BuildSite(); err != nil {
		t.Fatal(err)
	}
	html, err := os.ReadFile(filepath.Join(app.root, "public/ordinary.html"))
	if err != nil || !bytes.Contains(html, []byte("ordinary public document")) {
		t.Fatalf("ordinary content: %v", err)
	}
	if err := filepath.WalkDir(filepath.Join(app.root, "public"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte("private worklog sentinel")) {
			t.Fatalf("worklog published: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(app.root, ".mdsys/index.json"))
	if err != nil || bytes.Contains(index, []byte("worklog-")) {
		t.Fatalf("site index includes worklog: %v", err)
	}
}
