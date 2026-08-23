package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gitvcs "karte/internal/git"
)

const saveFileTestFrontMatter = "---\ntitle: \"Atomic save\"\ndoc_id: \"DOC-000001\"\nprintout: \"infinite\"\n---\n"

func TestSaveFileCriticalConflictLeavesOriginalUnchanged(t *testing.T) {
	base := saveFileDivergentContent("base")
	remote := saveFileDivergentContent("remote")
	local := saveFileDivergentContent("local")
	app, relativePath, absolutePath := newSaveFileTestApp(t, base, 0o640)

	if err := os.WriteFile(absolutePath, []byte(remote), 0o640); err != nil {
		t.Fatal(err)
	}
	beforeBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeHash := gitvcs.CalculateHash(string(beforeBytes))

	conflict, err := gitvcs.DetectConflictWithContent(app.vcs, app.dataDir, relativePath, local)
	if err != nil {
		t.Fatal(err)
	}
	if conflict == nil || conflict.Severity != gitvcs.ConflictCritical {
		t.Fatalf("expected a reachable critical conflict, got %#v", conflict)
	}

	originalReplace := saveFileAtomicReplace
	replaceCalls := 0
	saveFileAtomicReplace = func(oldPath, newPath string) error {
		replaceCalls++
		return originalReplace(oldPath, newPath)
	}
	t.Cleanup(func() { saveFileAtomicReplace = originalReplace })

	err = app.SaveFile(relativePath, local)
	if err == nil || !strings.Contains(err.Error(), "requires manual resolution") {
		t.Fatalf("expected critical conflict error, got %v", err)
	}
	if replaceCalls != 0 {
		t.Fatalf("critical conflict performed %d atomic replacements, want 0", replaceCalls)
	}

	afterBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	afterHash := gitvcs.CalculateHash(string(afterBytes))
	if string(afterBytes) != string(beforeBytes) {
		t.Fatalf("critical conflict changed original bytes\nwant: %q\n got: %q", beforeBytes, afterBytes)
	}
	if afterHash != beforeHash {
		t.Fatalf("critical conflict changed original hash: want %s, got %s", beforeHash, afterHash)
	}
}

func TestSaveFileNormalSaveUsesAtomicReplacement(t *testing.T) {
	base := saveFileTestFrontMatter + "base paragraph\n"
	local := saveFileTestFrontMatter + "local paragraph\n"
	app, relativePath, absolutePath := newSaveFileTestApp(t, base, 0o640)

	conflict, err := gitvcs.DetectConflictWithContent(app.vcs, app.dataDir, relativePath, local)
	if err != nil {
		t.Fatal(err)
	}
	if conflict != nil {
		t.Fatalf("ordinary editor save unexpectedly reported a conflict: %#v", conflict)
	}

	assertSaveFileAtomicReplace(t, absolutePath, base, local)
	if err := app.SaveFile(relativePath, local); err != nil {
		t.Fatal(err)
	}
	assertSaveFileContentsAndNoTemp(t, absolutePath, local)
}

func TestSaveFileAutoMergeUsesAtomicReplacement(t *testing.T) {
	base := saveFileTestFrontMatter + "first base\n\nsecond base\n"
	local := saveFileTestFrontMatter + "first local\n\nsecond base\n"
	remote := saveFileTestFrontMatter + "first base\n\nsecond remote\n"
	merged := saveFileTestFrontMatter + "first local\n\nsecond remote"
	app, relativePath, absolutePath := newSaveFileTestApp(t, base, 0o640)

	if err := os.WriteFile(absolutePath, []byte(remote), 0o640); err != nil {
		t.Fatal(err)
	}
	conflict, err := gitvcs.DetectConflictWithContent(app.vcs, app.dataDir, relativePath, local)
	if err != nil {
		t.Fatal(err)
	}
	if conflict == nil || conflict.Severity != gitvcs.ConflictAutoResolvable {
		t.Fatalf("expected an auto-resolvable conflict, got %#v", conflict)
	}

	assertSaveFileAtomicReplace(t, absolutePath, remote, merged)
	if err := app.SaveFile(relativePath, local); err != nil {
		t.Fatal(err)
	}
	assertSaveFileContentsAndNoTemp(t, absolutePath, merged)
}

func TestSaveFileAtomicReplaceFailureLeavesOriginalUnchanged(t *testing.T) {
	base := saveFileTestFrontMatter + "base paragraph\n"
	local := saveFileTestFrontMatter + "local paragraph\n"
	app, relativePath, absolutePath := newSaveFileTestApp(t, base, 0o640)

	originalReplace := saveFileAtomicReplace
	saveFileAtomicReplace = func(tempPath, destinationPath string) error {
		if filepath.Dir(tempPath) != filepath.Dir(destinationPath) {
			t.Fatalf("temporary file and destination are on different directories: %q, %q", tempPath, destinationPath)
		}
		return errors.New("injected atomic replace failure")
	}
	t.Cleanup(func() { saveFileAtomicReplace = originalReplace })

	err := app.SaveFile(relativePath, local)
	if err == nil || !strings.Contains(err.Error(), "injected atomic replace failure") {
		t.Fatalf("expected atomic replace failure, got %v", err)
	}
	assertSaveFileContentsAndNoTemp(t, absolutePath, base)
}

func newSaveFileTestApp(t *testing.T, baseContent string, perm os.FileMode) (*App, string, string) {
	t.Helper()
	root := t.TempDir()
	relativePath := filepath.ToSlash(filepath.Join("content", "atomic-save.md"))
	absolutePath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte(baseContent), perm); err != nil {
		t.Fatal(err)
	}

	vcs, err := gitvcs.NewVCS(nil, root, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := vcs.CommitFile(relativePath, "Add atomic save fixture"); err != nil {
		t.Fatal(err)
	}

	return &App{root: root, dataDir: root, vcs: vcs}, relativePath, absolutePath
}

func assertSaveFileAtomicReplace(t *testing.T, targetPath, oldContent, newContent string) {
	t.Helper()
	originalReplace := saveFileAtomicReplace
	replaceCalls := 0
	saveFileAtomicReplace = func(tempPath, destinationPath string) error {
		replaceCalls++
		if destinationPath != targetPath {
			t.Fatalf("atomic replace destination = %q, want %q", destinationPath, targetPath)
		}
		if filepath.Dir(tempPath) != filepath.Dir(targetPath) {
			t.Fatalf("temporary file directory = %q, want %q", filepath.Dir(tempPath), filepath.Dir(targetPath))
		}
		wantPrefix := "." + filepath.Base(targetPath) + ".tmp-"
		if !strings.HasPrefix(filepath.Base(tempPath), wantPrefix) {
			t.Fatalf("temporary file name = %q, want prefix %q", filepath.Base(tempPath), wantPrefix)
		}
		current, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(current) != oldContent {
			t.Fatalf("target changed before atomic replace\nwant: %q\n got: %q", oldContent, current)
		}
		pending, err := os.ReadFile(tempPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(pending) != newContent {
			t.Fatalf("temporary save content\nwant: %q\n got: %q", newContent, pending)
		}
		return originalReplace(tempPath, destinationPath)
	}
	t.Cleanup(func() {
		saveFileAtomicReplace = originalReplace
		if replaceCalls != 1 {
			t.Errorf("atomic replacement calls = %d, want 1", replaceCalls)
		}
	})
}

func assertSaveFileContentsAndNoTemp(t *testing.T, targetPath, want string) {
	t.Helper()
	got, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("saved content\nwant: %q\n got: %q", want, got)
	}
	tempFiles, err := filepath.Glob(filepath.Join(filepath.Dir(targetPath), "."+filepath.Base(targetPath)+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(tempFiles) != 0 {
		t.Fatalf("temporary save files were not cleaned up: %v", tempFiles)
	}
}

func saveFileDivergentContent(prefix string) string {
	lines := make([]string, 12)
	for index := range lines {
		lines[index] = fmt.Sprintf("%s line %02d", prefix, index)
	}
	return saveFileTestFrontMatter + strings.Join(lines, "\n") + "\n"
}
