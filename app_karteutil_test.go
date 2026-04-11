package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestInitializeKarteUtilInstallAndManifest(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "karte_data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}

	writeBundledCLIForTest(t, root, []byte("v1-cli"))

	a := NewApp()
	a.root = root
	a.dataDir = dataDir
	if err := a.initializeKarteUtil(); err != nil {
		t.Fatalf("initializeKarteUtil failed: %v", err)
	}
	if !a.karteUtilReady {
		t.Fatal("expected util ready")
	}
	if a.karteUtilPath == "" {
		t.Fatal("expected util path")
	}
	if _, err := os.Stat(a.karteUtilPath); err != nil {
		t.Fatalf("expected installed util: %v", err)
	}

	manifestPath := filepath.Join(dataDir, "karte_util", "manifest.json")
	manifest, err := readKarteUtilManifest(manifestPath)
	if err != nil {
		t.Fatalf("manifest should exist: %v", err)
	}
	if manifest.BinaryHash == "" || manifest.UtilVersion == "" {
		t.Fatalf("invalid manifest: %+v", manifest)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(a.karteUtilPath)
		if err != nil {
			t.Fatalf("stat installed util: %v", err)
		}
		if info.Mode().Perm()&0o111 == 0 {
			t.Fatalf("expected executable mode, got %o", info.Mode().Perm())
		}
	}
}

func TestInitializeKarteUtilSkipsWhenSameVersionAndHash(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "karte_data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	writeBundledCLIForTest(t, root, []byte("same-cli"))

	a := NewApp()
	a.root = root
	a.dataDir = dataDir
	if err := a.initializeKarteUtil(); err != nil {
		t.Fatalf("first initialize failed: %v", err)
	}
	info1, err := os.Stat(a.karteUtilPath)
	if err != nil {
		t.Fatalf("stat util: %v", err)
	}
	firstMod := info1.ModTime()

	time.Sleep(20 * time.Millisecond)
	if err := a.initializeKarteUtil(); err != nil {
		t.Fatalf("second initialize failed: %v", err)
	}
	info2, err := os.Stat(a.karteUtilPath)
	if err != nil {
		t.Fatalf("stat util second: %v", err)
	}
	if !info2.ModTime().Equal(firstMod) {
		t.Fatalf("expected no replacement, modtime changed: %v -> %v", firstMod, info2.ModTime())
	}
}

func TestInitializeKarteUtilReplacesWhenBundledHashChanged(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "karte_data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	writeBundledCLIForTest(t, root, []byte("old-cli"))

	a := NewApp()
	a.root = root
	a.dataDir = dataDir
	if err := a.initializeKarteUtil(); err != nil {
		t.Fatalf("first initialize failed: %v", err)
	}
	initialHash, err := fileSHA256Hex(a.karteUtilPath)
	if err != nil {
		t.Fatalf("hash initial util: %v", err)
	}

	writeBundledCLIForTest(t, root, []byte("new-cli"))
	if err := a.initializeKarteUtil(); err != nil {
		t.Fatalf("second initialize failed: %v", err)
	}
	updatedHash, err := fileSHA256Hex(a.karteUtilPath)
	if err != nil {
		t.Fatalf("hash updated util: %v", err)
	}
	if updatedHash == initialHash {
		t.Fatalf("expected updated hash, still %s", updatedHash)
	}
}

func TestInitializeKarteUtilRecoversFromBrokenManifest(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "karte_data")
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatalf("mkdir dataDir: %v", err)
	}
	writeBundledCLIForTest(t, root, []byte("cli-data"))

	utilDir := filepath.Join(dataDir, "karte_util")
	if err := os.MkdirAll(utilDir, 0o755); err != nil {
		t.Fatalf("mkdir utilDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(utilDir, "manifest.json"), []byte("{broken"), 0o644); err != nil {
		t.Fatalf("write broken manifest: %v", err)
	}

	a := NewApp()
	a.root = root
	a.dataDir = dataDir
	if err := a.initializeKarteUtil(); err != nil {
		t.Fatalf("initialize with broken manifest failed: %v", err)
	}
	if !a.karteUtilReady {
		t.Fatal("expected util ready")
	}
	if _, err := readKarteUtilManifest(filepath.Join(utilDir, "manifest.json")); err != nil {
		t.Fatalf("manifest should be repaired: %v", err)
	}
}

func writeBundledCLIForTest(t *testing.T, root string, body []byte) string {
	t.Helper()
	bundlePath := filepath.Join(root, "karte_util_bundles", karteUtilPlatformKeys()[0], karteUtilBinaryName())
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o755); err != nil {
		t.Fatalf("mkdir bundle path: %v", err)
	}
	mode := os.FileMode(0o644)
	if runtime.GOOS != "windows" {
		mode = 0o755
	}
	if err := os.WriteFile(bundlePath, body, mode); err != nil {
		t.Fatalf("write bundled cli: %v", err)
	}
	return bundlePath
}
