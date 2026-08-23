package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestSiteBuilderUsesChecksumManifestForIncrementalBuilds(t *testing.T) {
	root := newSiteBuildTestRoot(t, map[string]string{
		"a.md":        "alpha-v1",
		"nested/b.md": "beta-v1",
	})
	var rendered []string
	builder := newTestSiteBuilder(&rendered)

	initial, err := builder.BuildIncremental(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	assertSiteBuildPaths(t, initial.Rendered, "content/a.md", "content/nested/b.md")
	if !initial.Full {
		t.Fatal("missing baseline must trigger a full build")
	}
	previousB := readSiteBuildTestFile(t, filepath.Join(root, "public", "nested", "b.html"))

	rendered = nil
	writeSiteBuildTestFile(t, filepath.Join(root, "content", "a.md"), "alpha-v2")
	incremental, err := builder.BuildIncremental(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if incremental.Full {
		t.Fatal("valid baseline unexpectedly triggered a full build")
	}
	assertSiteBuildPaths(t, incremental.Rendered, "content/a.md")
	assertSiteBuildPaths(t, rendered, "content/a.md")
	if got := readSiteBuildTestFile(t, filepath.Join(root, "public", "nested", "b.html")); got != previousB {
		t.Fatalf("unchanged output was replaced: got %q want %q", got, previousB)
	}

	rendered = nil
	unchanged, err := builder.BuildIncremental(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Full || len(unchanged.Rendered) != 0 || len(rendered) != 0 {
		t.Fatalf("unchanged incremental build rendered files: result=%+v hook=%v", unchanged, rendered)
	}

	rendered = nil
	explicitFull, err := builder.BuildFull(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !explicitFull.Full {
		t.Fatal("explicit full build lost synchronous full-build semantics")
	}
	assertSiteBuildPaths(t, explicitFull.Rendered, "content/a.md", "content/nested/b.md")
}

func TestSiteBuilderDefaultRendererPublishesMarkdown(t *testing.T) {
	root := newSiteBuildTestRoot(t, map[string]string{"note.md": "# Rendered note\n"})
	result, err := newSiteBuilder(siteBuildHooks{}).BuildFull(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	assertSiteBuildPaths(t, result.Rendered, "content/note.md")
	if html := readSiteBuildTestFile(t, filepath.Join(root, "public", "note.html")); !strings.Contains(html, "Rendered note") {
		t.Fatalf("default renderer output is missing Markdown content: %s", html)
	}
}

func TestSiteBuilderHandlesRenameDeleteAndMultipleDirtySources(t *testing.T) {
	root := newSiteBuildTestRoot(t, map[string]string{
		"a.md": "alpha",
		"b.md": "beta",
	})
	var rendered []string
	builder := newTestSiteBuilder(&rendered)
	if _, err := builder.BuildIncremental(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	writeSiteBuildTestFile(t, filepath.Join(root, "public", "kept.asset"), "keep")

	if err := os.Rename(filepath.Join(root, "content", "a.md"), filepath.Join(root, "content", "renamed.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "content", "b.md")); err != nil {
		t.Fatal(err)
	}
	writeSiteBuildTestFile(t, filepath.Join(root, "content", "c.md"), "charlie")
	writeSiteBuildTestFile(t, filepath.Join(root, "content", "nested", "d.md"), "delta")
	rendered = nil

	result, err := builder.BuildIncremental(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Full {
		t.Fatal("rename/delete with a valid baseline triggered a full build")
	}
	assertSiteBuildPaths(t, result.Rendered, "content/c.md", "content/nested/d.md", "content/renamed.md")
	assertSiteBuildPaths(t, result.Deleted, "a.html", "b.html")
	for _, removed := range []string{"a.html", "b.html"} {
		if _, err := os.Stat(filepath.Join(root, "public", removed)); !os.IsNotExist(err) {
			t.Fatalf("deleted output %s still exists: %v", removed, err)
		}
	}
	for _, created := range []string{"c.html", "nested/d.html", "renamed.html"} {
		if _, err := os.Stat(filepath.Join(root, "public", created)); err != nil {
			t.Fatalf("expected output %s: %v", created, err)
		}
	}
	if got := readSiteBuildTestFile(t, filepath.Join(root, "public", "kept.asset")); got != "keep" {
		t.Fatalf("incremental copy lost unrelated public artifact: %q", got)
	}
}

func TestSiteBuilderRejectsSymlinkAndFallsBackFromCorruptManifest(t *testing.T) {
	t.Run("public symlink is not copied", func(t *testing.T) {
		root := newSiteBuildTestRoot(t, map[string]string{"a.md": "alpha"})
		builder := newTestSiteBuilder(nil)
		if _, err := builder.BuildIncremental(context.Background(), root); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(root, "outside.txt")
		writeSiteBuildTestFile(t, outside, "outside")
		linkPath := filepath.Join(root, "public", "escape-link")
		if err := os.Symlink(outside, linkPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		oldPublic := snapshotSiteBuildTestTree(t, filepath.Join(root, "public"))
		writeSiteBuildTestFile(t, filepath.Join(root, "content", "a.md"), "changed")

		if _, err := builder.BuildIncremental(context.Background(), root); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected symlink rejection, got %v", err)
		}
		assertSiteBuildTestTree(t, filepath.Join(root, "public"), oldPublic)
	})

	t.Run("malicious manifest path falls back to full", func(t *testing.T) {
		root := newSiteBuildTestRoot(t, map[string]string{"a.md": "alpha"})
		var rendered []string
		builder := newTestSiteBuilder(&rendered)
		if _, err := builder.BuildIncremental(context.Background(), root); err != nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(root, "public", siteBuildManifestName)
		writeSiteBuildTestFile(t, manifestPath, `{"schema":1,"sources":{"content/a.md":{"checksum":"0000000000000000000000000000000000000000000000000000000000000000","output":"../escape.html"}}}`)
		rendered = nil

		result, err := builder.BuildIncremental(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Full {
			t.Fatal("corrupt manifest did not fall back to a full build")
		}
		assertSiteBuildPaths(t, rendered, "content/a.md")
		if _, err := os.Stat(filepath.Join(root, "escape.html")); !os.IsNotExist(err) {
			t.Fatalf("manifest output escaped public directory: %v", err)
		}
	})
}

func TestSiteBuilderFailuresPreservePreviousPublicAndIndex(t *testing.T) {
	tests := []struct {
		name  string
		hooks func(string) siteBuildHooks
	}{
		{
			name: "render",
			hooks: func(string) siteBuildHooks {
				return siteBuildHooks{renderMarkdown: func(context.Context, string, string) (string, error) {
					return "", errors.New("render failure")
				}}
			},
		},
		{
			name: "index encode",
			hooks: func(string) siteBuildHooks {
				return siteBuildHooks{encodeIndex: func(siteBuildIndex) ([]byte, error) {
					return nil, errors.New("index failure")
				}}
			},
		},
		{
			name: "manifest encode",
			hooks: func(string) siteBuildHooks {
				return siteBuildHooks{encodeManifest: func(siteBuildManifest) ([]byte, error) {
					return nil, errors.New("manifest failure")
				}}
			},
		},
		{
			name: "backup rename",
			hooks: func(string) siteBuildHooks {
				return siteBuildHooks{rename: func(oldPath, newPath string) error {
					if filepath.Base(oldPath) == "public" {
						return errors.New("backup rename failure")
					}
					return os.Rename(oldPath, newPath)
				}}
			},
		},
		{
			name: "stage rename",
			hooks: func(string) siteBuildHooks {
				return siteBuildHooks{rename: func(oldPath, newPath string) error {
					if filepath.Base(newPath) == "public" && strings.HasPrefix(filepath.Base(oldPath), ".site-build-stage-") && !strings.HasSuffix(oldPath, "-public-backup") {
						return errors.New("stage rename failure")
					}
					return os.Rename(oldPath, newPath)
				}}
			},
		},
		{
			name: "index commit",
			hooks: func(string) siteBuildHooks {
				return siteBuildHooks{commitIndex: func(_, targetPath string) error {
					if err := os.WriteFile(targetPath, []byte("partial index"), 0o644); err != nil {
						return err
					}
					return errors.New("index commit failure")
				}}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := newSiteBuildTestRoot(t, map[string]string{"a.md": "old"})
			baselineBuilder := newTestSiteBuilder(nil)
			if _, err := baselineBuilder.BuildIncremental(context.Background(), root); err != nil {
				t.Fatal(err)
			}
			oldPublic := snapshotSiteBuildTestTree(t, filepath.Join(root, "public"))
			oldIndex := readSiteBuildTestFile(t, filepath.Join(root, ".mdsys", "index.json"))
			writeSiteBuildTestFile(t, filepath.Join(root, "content", "a.md"), "new")

			hooks := test.hooks(root)
			if test.name != "render" {
				hooks = withTestSiteBuildRenderer(hooks)
			}
			builder := newSiteBuilder(hooks)
			if _, err := builder.BuildIncremental(context.Background(), root); err == nil {
				t.Fatal("expected build failure")
			}
			assertSiteBuildTestTree(t, filepath.Join(root, "public"), oldPublic)
			if got := readSiteBuildTestFile(t, filepath.Join(root, ".mdsys", "index.json")); got != oldIndex {
				t.Fatalf("failed build changed site index: got %q want %q", got, oldIndex)
			}
			stages, err := filepath.Glob(filepath.Join(root, ".mdsys", ".site-build-stage-*"))
			if err != nil {
				t.Fatal(err)
			}
			if len(stages) != 0 {
				t.Fatalf("failed build leaked staging paths: %v", stages)
			}
		})
	}
}

func TestSiteBuilderPartialBackupCleanupFailureKeepsCommittedOutput(t *testing.T) {
	root := newSiteBuildTestRoot(t, map[string]string{"a.md": "old"})
	if _, err := newTestSiteBuilder(nil).BuildIncremental(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	writeSiteBuildTestFile(t, filepath.Join(root, "content", "a.md"), "new")
	writeSiteBuildTestFile(t, filepath.Join(root, "content", "b.md"), "added")

	cleanupFailed := false
	hooks := siteBuildHooks{removeAll: func(path string) error {
		if !cleanupFailed && strings.HasSuffix(path, "-public-backup") {
			cleanupFailed = true
			// Model RemoveAll's documented partial-deletion failure.  Restoring
			// this incomplete backup would lose the previous a.html artifact.
			_ = os.Remove(filepath.Join(path, "a.html"))
			return errors.New("partial backup cleanup failure")
		}
		return os.RemoveAll(path)
	}}
	builder := newSiteBuilder(withTestSiteBuildRenderer(hooks))
	if _, err := builder.BuildIncremental(context.Background(), root); err == nil || !strings.Contains(err.Error(), "after commit") {
		t.Fatalf("expected committed cleanup error, got %v", err)
	}
	if got := readSiteBuildTestFile(t, filepath.Join(root, "public", "a.html")); got != "rendered:new" {
		t.Fatalf("cleanup failure rolled back committed a.html: %q", got)
	}
	if got := readSiteBuildTestFile(t, filepath.Join(root, "public", "b.html")); got != "rendered:added" {
		t.Fatalf("cleanup failure lost committed b.html: %q", got)
	}
	index := readSiteBuildTestFile(t, filepath.Join(root, ".mdsys", "index.json"))
	if !strings.Contains(index, `"id": "b.md"`) {
		t.Fatalf("cleanup failure rolled back committed index: %s", index)
	}

	backups, err := filepath.Glob(filepath.Join(root, ".mdsys", ".site-build-stage-*-public-backup"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 {
		t.Fatalf("partial backup should remain for retry cleanup: %v", backups)
	}
	var rendered []string
	result, err := newTestSiteBuilder(&rendered).BuildIncremental(context.Background(), root)
	if err != nil {
		t.Fatalf("next build did not clean the stale backup: %v", err)
	}
	if result.Full || len(rendered) != 0 {
		t.Fatalf("stale backup cleanup caused an unnecessary render: result=%+v rendered=%v", result, rendered)
	}
	backups, err = filepath.Glob(filepath.Join(root, ".mdsys", ".site-build-stage-*-public-backup"))
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("stale backup was not cleaned: %v", backups)
	}
}

func TestSiteBuilderRecoversBackupWhenCrashLeftPublicMissing(t *testing.T) {
	root := newSiteBuildTestRoot(t, map[string]string{"a.md": "alpha"})
	if _, err := newTestSiteBuilder(nil).BuildIncremental(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	publicDir := filepath.Join(root, "public")
	backupDir := filepath.Join(root, ".mdsys", ".site-build-stage-crash-public-backup")
	if err := os.Rename(publicDir, backupDir); err != nil {
		t.Fatal(err)
	}
	var rendered []string

	result, err := newTestSiteBuilder(&rendered).BuildIncremental(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if result.Full || len(rendered) != 0 {
		t.Fatalf("recovered valid baseline was rebuilt: result=%+v rendered=%v", result, rendered)
	}
	if got := readSiteBuildTestFile(t, filepath.Join(publicDir, "a.html")); got != "rendered:alpha" {
		t.Fatalf("crash recovery did not restore old public bytes: %q", got)
	}
	if _, err := os.Stat(backupDir); !os.IsNotExist(err) {
		t.Fatalf("recovered backup still exists: %v", err)
	}
}

func TestSiteBuilderRejectsMetadataSymlinkAndHonorsCancelledCopy(t *testing.T) {
	t.Run("metadata symlink", func(t *testing.T) {
		root := newSiteBuildTestRoot(t, map[string]string{"a.md": "alpha"})
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(root, ".mdsys")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := newTestSiteBuilder(nil).BuildIncremental(context.Background(), root); err == nil || !strings.Contains(err.Error(), "metadata path") {
			t.Fatalf("expected metadata symlink rejection, got %v", err)
		}
		entries, err := os.ReadDir(outside)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("metadata symlink caused writes outside root: %v", entries)
		}
	})

	t.Run("cancelled copy", func(t *testing.T) {
		source := t.TempDir()
		destination := t.TempDir()
		writeSiteBuildTestFile(t, filepath.Join(source, "large.html"), strings.Repeat("x", 128*1024))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := copySiteBuildTree(ctx, source, destination); !errors.Is(err, context.Canceled) {
			t.Fatalf("copy ignored cancellation: %v", err)
		}
	})
}

func newSiteBuildTestRoot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range files {
		writeSiteBuildTestFile(t, filepath.Join(root, "content", filepath.FromSlash(path)), content)
	}
	return root
}

func newTestSiteBuilder(rendered *[]string) *siteBuilder {
	hooks := withTestSiteBuildRenderer(siteBuildHooks{})
	if rendered != nil {
		originalRender := hooks.renderMarkdown
		hooks.renderMarkdown = func(ctx context.Context, root, sourcePath string) (string, error) {
			relativePath, err := filepath.Rel(root, sourcePath)
			if err != nil {
				return "", err
			}
			*rendered = append(*rendered, filepath.ToSlash(relativePath))
			return originalRender(ctx, root, sourcePath)
		}
	}
	return newSiteBuilder(hooks)
}

func withTestSiteBuildRenderer(hooks siteBuildHooks) siteBuildHooks {
	hooks.renderMarkdown = func(_ context.Context, _, sourcePath string) (string, error) {
		content, err := os.ReadFile(sourcePath)
		if err != nil {
			return "", err
		}
		return "rendered:" + string(content), nil
	}
	return hooks
}

func writeSiteBuildTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readSiteBuildTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func snapshotSiteBuildTestTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relativePath, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			result[filepath.ToSlash(relativePath)] = "symlink:" + target
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(relativePath)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertSiteBuildTestTree(t *testing.T, root string, want map[string]string) {
	t.Helper()
	got := snapshotSiteBuildTestTree(t, root)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("site tree changed:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func assertSiteBuildPaths(t *testing.T, got []string, want ...string) {
	t.Helper()
	gotCopy := append([]string(nil), got...)
	wantCopy := append([]string(nil), want...)
	sort.Strings(gotCopy)
	sort.Strings(wantCopy)
	if !reflect.DeepEqual(gotCopy, wantCopy) {
		t.Fatalf("unexpected paths: got %v want %v", gotCopy, wantCopy)
	}
}
