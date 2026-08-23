package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAppBuildSiteRemainsSynchronousAndFull(t *testing.T) {
	dataDir := newSiteBuildTestRoot(t, map[string]string{
		"a.md": "alpha",
		"b.md": "beta",
	})
	var rendered []string
	app := &App{root: filepath.Join(dataDir, "unused-root"), dataDir: dataDir}
	app.siteBuild.builder = newTestSiteBuilder(&rendered)

	if err := app.BuildSite(); err != nil {
		t.Fatal(err)
	}
	assertSiteBuildPaths(t, rendered, "content/a.md", "content/b.md")
	if _, err := os.Stat(filepath.Join(dataDir, "public", "a.html")); err != nil {
		t.Fatalf("BuildSite did not publish synchronously: %v", err)
	}

	rendered = nil
	if err := app.BuildSite(); err != nil {
		t.Fatal(err)
	}
	assertSiteBuildPaths(t, rendered, "content/a.md", "content/b.md")
}

func TestSaveFileDoesNotWaitForRunningSiteBuild(t *testing.T) {
	dataDir := newSiteBuildTestRoot(t, map[string]string{
		"note.md": "---\ndoc_id: doc:save-latency\n---\nold\n",
	})
	if err := os.MkdirAll(filepath.Join(dataDir, documentMapDirectoryName), 0o755); err != nil {
		t.Fatal(err)
	}
	app := &App{root: dataDir, dataDir: dataDir}
	app.lifecycle.start(context.Background())
	clock := newManualSiteBuildClock()
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	app.siteBuild.clock = clock
	app.siteBuild.debounce = time.Second
	app.siteBuild.run = func(ctx context.Context, _ siteBuildRequest) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	defer func() {
		releaseOnce.Do(func() { close(release) })
		app.lifecycle.beginShutdown()
		waitCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if !app.lifecycle.wait(waitCtx) {
			t.Error("site build worker did not stop")
		}
	}()

	if !app.scheduleSiteBuild("content/note.md") {
		t.Fatal("failed to queue blocking build")
	}
	clock.waitForTimer(t, 0).Fire()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocking build did not start")
	}

	saveDone := make(chan error, 1)
	go func() {
		saveDone <- app.SaveFile("content/note.md", "---\ndoc_id: doc:save-latency\n---\nnew\n")
	}()
	select {
	case err := <-saveDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("SaveFile waited for the running site build")
	}
	if got := readSiteBuildTestFile(t, filepath.Join(dataDir, "content", "note.md")); !strings.HasSuffix(got, "new\n") {
		t.Fatalf("SaveFile did not persist content before returning: %q", got)
	}
	releaseOnce.Do(func() { close(release) })
}
