package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type artifactZipFixtureEntry struct {
	name string
	mode os.FileMode
	data string
}

func TestExtractArtifactZipPreservesExecutableAndInternalSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{
		{name: "bundle with spaces/", mode: os.ModeDir | 0o755},
		{name: "bundle with spaces/bin/", mode: os.ModeDir | 0o755},
		{name: "bundle with spaces/bin/karte", mode: 0o755, data: "executable"},
		{name: "bundle with spaces/bin/karte-v1", mode: os.ModeSymlink | 0o777, data: "karte"},
		{name: "bundle with spaces/bin/karte-current", mode: os.ModeSymlink | 0o777, data: "karte-v1"},
		{name: "bundle with spaces/implicit directory/file.txt", mode: 0o644, data: "asset"},
		{name: "bundle with spaces/implicit-current", mode: os.ModeSymlink | 0o777, data: "implicit directory"},
	})
	destination := filepath.Join(t.TempDir(), "separate extraction")
	entries, expanded, err := extractArtifactZip(archivePath, destination)
	if err != nil {
		t.Fatal(err)
	}
	if entries != 7 || expanded != uint64(len("executable")+len("asset")) {
		t.Fatalf("result = entries:%d bytes:%d", entries, expanded)
	}
	executable := filepath.Join(destination, "bundle with spaces", "bin", "karte")
	if contents, err := os.ReadFile(executable); err != nil {
		t.Fatal(err)
	} else if string(contents) != "executable" {
		t.Fatalf("executable contents = %q", contents)
	}
	if info, err := os.Stat(executable); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("executable mode = %o", info.Mode().Perm())
	}
	link := filepath.Join(destination, "bundle with spaces", "bin", "karte-current")
	if target, err := os.Readlink(link); err != nil {
		t.Fatal(err)
	} else if target != "karte-v1" {
		t.Fatalf("symlink target = %q", target)
	}
	if contents, err := os.ReadFile(link); err != nil {
		t.Fatal(err)
	} else if string(contents) != "executable" {
		t.Fatalf("chained symlink contents = %q", contents)
	}
	implicitLink := filepath.Join(destination, "bundle with spaces", "implicit-current")
	if info, err := os.Stat(implicitLink); err != nil {
		t.Fatal(err)
	} else if !info.IsDir() {
		t.Fatalf("implicit directory symlink target mode = %s", info.Mode())
	}
}

func TestArtifactZipPreflightRejectsTraversalAndNonPortableNames(t *testing.T) {
	tests := []string{
		"../outside",
		"safe/../../outside",
		"/absolute/path",
		"C:/windows/path",
		"safe/C:/windows/path",
		`safe\..\outside`,
		"safe//duplicate-separator",
		"safe/./dot",
		"safe/NUL.txt",
		"safe/COM1",
		"safe/trailing-dot.",
		"safe/trailing-space ",
		"safe/control-\x1f",
	}
	for _, name := range tests {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{{name: name, mode: 0o644, data: "escape"}})
			destination := filepath.Join(t.TempDir(), "extract")
			if _, _, err := extractArtifactZip(archivePath, destination); err == nil {
				t.Fatalf("unsafe entry %q was accepted", name)
			}
		})
	}
}

func TestArtifactZipPreflightRejectsEscapingDanglingAndAncestorSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("zip symlink mode is not portable on Windows")
	}
	tests := []struct {
		name    string
		entries []artifactZipFixtureEntry
		want    string
	}{
		{
			name: "absolute target",
			entries: []artifactZipFixtureEntry{
				{name: "bundle/link", mode: os.ModeSymlink | 0o777, data: "/tmp/outside"},
			},
			want: "unsafe target",
		},
		{
			name: "relative escape",
			entries: []artifactZipFixtureEntry{
				{name: "bundle/deep/link", mode: os.ModeSymlink | 0o777, data: "../../../outside"},
			},
			want: "escapes extraction root",
		},
		{
			name: "dangling target",
			entries: []artifactZipFixtureEntry{
				{name: "bundle/link", mode: os.ModeSymlink | 0o777, data: "missing"},
			},
			want: "missing target",
		},
		{
			name: "entry through symlink",
			entries: []artifactZipFixtureEntry{
				{name: "bundle/safe", mode: os.ModeDir | 0o755},
				{name: "bundle/link", mode: os.ModeSymlink | 0o777, data: "safe"},
				{name: "bundle/link/escaped", mode: 0o644, data: "bad"},
			},
			want: "descends through non-directory",
		},
		{
			name: "symlink cycle",
			entries: []artifactZipFixtureEntry{
				{name: "bundle/first", mode: os.ModeSymlink | 0o777, data: "second"},
				{name: "bundle/second", mode: os.ModeSymlink | 0o777, data: "first"},
			},
			want: "forms a cycle",
		},
		{
			name: "symlink chain escape",
			entries: []artifactZipFixtureEntry{
				{name: "bundle/first", mode: os.ModeSymlink | 0o777, data: "second"},
				{name: "bundle/second", mode: os.ModeSymlink | 0o777, data: "../../outside"},
			},
			want: "escapes extraction root",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archivePath := writeArtifactZipFixture(t, test.entries)
			destination := filepath.Join(t.TempDir(), "extract")
			_, _, err := extractArtifactZip(archivePath, destination)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v，want %q", err, test.want)
			}
		})
	}
}

func TestArtifactZipPreflightRejectsPortableNameCollision(t *testing.T) {
	archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{
		{name: "Karte/file.txt", mode: 0o644, data: "first"},
		{name: "karte/file.txt", mode: 0o644, data: "second"},
	})
	if _, _, err := extractArtifactZip(archivePath, filepath.Join(t.TempDir(), "extract")); err == nil || !strings.Contains(err.Error(), "case-colliding") {
		t.Fatalf("collision error = %v", err)
	}
}

func TestArtifactZipExtractionRejectsForgedExpandedSize(t *testing.T) {
	archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{
		{name: "payload.bin", mode: 0o644, data: strings.Repeat("x", 1024)},
	})
	archive, err := preflightArtifactZip(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.reader.Close()
	archive.entries[0].file.UncompressedSize64 = 1
	archive.entries[0].file.UncompressedSize = 1

	destination := filepath.Join(t.TempDir(), "extract")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	rootInfo, err := os.Lstat(destination)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(destination)
	if err != nil {
		t.Fatal(err)
	}
	err = extractPreflightedArtifact(archive, destination, artifactExtractionRootIdentity{
		info:         rootInfo,
		resolvedPath: filepath.Clean(resolved),
	})
	if err == nil {
		t.Fatalf("forged expanded size error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "payload.bin")); !os.IsNotExist(err) {
		t.Fatalf("partially expanded file was not removed: %v", err)
	}
}

func TestArtifactZipExtractionRevalidatesSymlinkAncestorAfterPreflight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{
		{name: "bundle/file.txt", mode: 0o644, data: "must stay inside"},
	})
	root := t.TempDir()
	destination := filepath.Join(root, "extract")
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := extractArtifactZipWithHook(archivePath, destination, func(extractionRoot string) error {
		return os.Symlink(outside, filepath.Join(extractionRoot, "bundle"))
	})
	if err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("TOCTOU symlink error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("extractor wrote outside its root: %v", err)
	}
}

func TestArtifactZipExtractionRejectsRootReplacementAfterPreflight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{
		{name: "file.txt", mode: 0o644, data: "must stay inside"},
	})
	root := t.TempDir()
	destination := filepath.Join(root, "extract")
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := extractArtifactZipWithHook(archivePath, destination, func(extractionRoot string) error {
		if err := os.Remove(extractionRoot); err != nil {
			return err
		}
		return os.Symlink(outside, extractionRoot)
	})
	if err == nil || !strings.Contains(err.Error(), "not a real directory") {
		t.Fatalf("root replacement error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outside, "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("extractor wrote through replaced root: %v", err)
	}
}

func TestArtifactZipExtractionRejectsParentSwapAfterPreflight(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{
		{name: "file.txt", mode: 0o644, data: "must stay inside"},
	})
	root := t.TempDir()
	originalParent := filepath.Join(root, "original parent")
	if err := os.Mkdir(originalParent, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(originalParent, "extract")
	outsideParent := filepath.Join(root, "outside parent")
	if err := os.MkdirAll(filepath.Join(outsideParent, "extract"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, _, err := extractArtifactZipWithHook(archivePath, destination, func(string) error {
		if err := os.Rename(originalParent, originalParent+" displaced"); err != nil {
			return err
		}
		return os.Symlink(outsideParent, originalParent)
	})
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("parent swap error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(outsideParent, "extract", "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("extractor wrote through swapped parent: %v", err)
	}
}

func TestArtifactZipExtractionRejectsParentSymlinkToMovedOriginalRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{
		{name: "file.txt", mode: 0o644, data: "must stay at the original path"},
	})
	root := t.TempDir()
	originalParent := filepath.Join(root, "original parent")
	if err := os.Mkdir(originalParent, 0o755); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(originalParent, "extract")
	displacedParent := originalParent + " displaced"
	_, _, err := extractArtifactZipWithHook(archivePath, destination, func(string) error {
		if err := os.Rename(originalParent, displacedParent); err != nil {
			return err
		}
		return os.Symlink(displacedParent, originalParent)
	})
	if err == nil || !strings.Contains(err.Error(), "resolved path changed") {
		t.Fatalf("moved-root parent symlink error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(displacedParent, "extract", "file.txt")); !os.IsNotExist(err) {
		t.Fatalf("extractor wrote through parent symlink to moved original root: %v", err)
	}
}

func TestExtractArtifactZipRequiresNewAbsoluteDestination(t *testing.T) {
	archivePath := writeArtifactZipFixture(t, []artifactZipFixtureEntry{{name: "karte", mode: 0o755, data: "binary"}})
	if _, _, err := extractArtifactZip(archivePath, "relative"); err == nil || !strings.Contains(err.Error(), "destination path must be absolute") {
		t.Fatalf("relative destination error = %v", err)
	}
	existing := t.TempDir()
	if _, _, err := extractArtifactZip(archivePath, existing); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing destination error = %v", err)
	}
}

func writeArtifactZipFixture(t *testing.T, entries []artifactZipFixtureEntry) string {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "artifact fixture.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		item, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := item.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}
