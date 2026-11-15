package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"karte/internal/portablepackage"
)

func TestPortableSnapshotRoundTripAcrossWorkspaces(t *testing.T) {
	origin := portableTestApp(t)
	destination := portableTestApp(t)
	const sourcePath = "content/原稿.md"
	source := []byte("---\ndoc_id: DOC-000031\ntitle: 合成資料\nsensitivity: confidential\nlocal_note: /private/source\n---\n# テスト\n")
	writePortableFixture(t, origin.dataDir, sourcePath, source)
	snapshot, err := origin.portablePackageSnapshot(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "資料.kartepack")
	if err := portablepackage.Write(path, snapshot); err != nil {
		t.Fatal(err)
	}
	revised := append(bytes.Clone(source), []byte("改版\n")...)
	writePortableFixture(t, origin.dataDir, sourcePath, revised)
	received, err := portablepackage.Read(path)
	if err != nil || received.Manifest != snapshot.Manifest || !bytes.Equal(received.Markdown, snapshot.Markdown) {
		t.Fatalf("snapshot changed after editing the source: %v", err)
	}
	imported, err := destination.installPortableSnapshot(received)
	if err != nil {
		t.Fatal(err)
	}
	again, err := destination.installPortableSnapshot(received)
	if err != nil || again != imported {
		t.Fatalf("repeat import changed the destination: %v", err)
	}
	reexported, err := destination.portablePackageSnapshot(imported)
	if err != nil || reexported.Manifest != received.Manifest || !bytes.Equal(reexported.Markdown, received.Markdown) {
		t.Fatalf("re-export changed the received snapshot: %v", err)
	}
	newVersion, err := origin.portablePackageSnapshot(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := destination.installPortableSnapshot(newVersion); err == nil || !strings.Contains(err.Error(), "文書 ID") {
		t.Fatalf("expected a version conflict, got %v", err)
	}
	persisted, err := os.ReadFile(filepath.Join(destination.dataDir, filepath.FromSlash(imported)))
	if err != nil || !bytes.Equal(persisted, received.Markdown) {
		t.Fatalf("conflict changed the stored version: %v", err)
	}
	original, err := os.ReadFile(filepath.Join(origin.dataDir, filepath.FromSlash(sourcePath)))
	if err != nil || !bytes.Equal(original, revised) {
		t.Fatalf("export changed the authoring copy: %v", err)
	}
}

func TestPortableSnapshotEnforcesPolicyInBothWorkspaces(t *testing.T) {
	origin := portableTestApp(t)
	destination := portableTestApp(t)
	const path = "content/private.md"
	writePortableFixture(t, origin.dataDir, path, []byte("---\ndoc_id: DOC-000041\ntitle: private\nsensitivity: confidential\n---\n# private\n"))
	snapshot, err := origin.portablePackageSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := []byte(`{"protocol_version":"1.0","actors":{"human":{"sensitivity_ceiling":"internal","projects":["*"],"capabilities":["export","review"]}}}`)
	for _, app := range []*App{origin, destination} {
		writePortableFixture(t, app.dataDir, ".mdsys/context/v1/policy.json", policy)
	}
	denied, err := origin.portablePackageSnapshot(path)
	missing, missingErr := origin.portablePackageSnapshot("content/missing.md")
	if err == nil || missingErr == nil || err.Error() != missingErr.Error() || len(denied.Markdown)+len(missing.Markdown) != 0 {
		t.Fatal("denied and missing exports must both be opaque")
	}
	if _, err := destination.installPortableSnapshot(snapshot); err == nil {
		t.Fatal("local policy permitted a prohibited import")
	}
	files, err := os.ReadDir(filepath.Join(destination.dataDir, "content"))
	if err != nil || len(files) != 0 {
		t.Fatalf("policy denial changed workspace content: %v %v", files, err)
	}
}

func portableTestApp(t *testing.T) *App {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "content"), 0700); err != nil {
		t.Fatal(err)
	}
	return &App{root: root, dataDir: root}
}

func writePortableFixture(t *testing.T, root, path string, data []byte) {
	t.Helper()
	path = filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
