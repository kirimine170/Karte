package runtimepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultDataDirUsesLocalAppDataOnWindows(t *testing.T) {
	localAppData := filepath.Join(`C:\`, "Users", "karte", "AppData", "Local")
	root, dataDir, err := DefaultDataDir("windows", filepath.Join(`C:\`, "Apps", "Karte"), localAppData)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(localAppData, "Karte"); root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
	if want := filepath.Join(localAppData, "Karte", "karte_data"); dataDir != want {
		t.Fatalf("dataDir = %q, want %q", dataDir, want)
	}
}

func TestDefaultDataDirRequiresLocalAppDataOnWindows(t *testing.T) {
	if _, _, err := DefaultDataDir("windows", `C:\Apps\Karte`, ""); err == nil {
		t.Fatal("expected missing LOCALAPPDATA error")
	}
}

func TestMigrateLegacyDataDirCopiesWithoutDeletingSource(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(root, "portable", "karte_data")
	destination := filepath.Join(root, "local", "Karte", "karte_data")
	if err := os.MkdirAll(filepath.Join(legacy, "content", "日本語"), 0o755); err != nil {
		t.Fatal(err)
	}
	sourceFile := filepath.Join(legacy, "content", "日本語", "メモ.md")
	if err := os.WriteFile(sourceFile, []byte("# retained\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateLegacyDataDir(legacy, destination)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated {
		t.Fatal("expected migration")
	}
	if _, err := os.Stat(sourceFile); err != nil {
		t.Fatalf("legacy source was removed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "content", "日本語", "メモ.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# retained\n" {
		t.Fatalf("unexpected migrated content: %q", content)
	}

	migrated, err = MigrateLegacyDataDir(legacy, destination)
	if err != nil {
		t.Fatal(err)
	}
	if migrated {
		t.Fatal("existing destination must not be overwritten")
	}
}
