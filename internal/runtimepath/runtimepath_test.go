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

func TestConfiguredDataDirResolvesAbsoluteAndRelativePaths(t *testing.T) {
	placed := t.TempDir()
	absolute := filepath.Join(t.TempDir(), "personal context")
	configPath := filepath.Join(placed, DataDirConfigName)
	if err := os.WriteFile(configPath, []byte(absolute+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, configured, err := ConfiguredDataDir(placed)
	if err != nil {
		t.Fatal(err)
	}
	if !configured || got != absolute {
		t.Fatalf("ConfiguredDataDir() = %q, %v; want %q, true", got, configured, absolute)
	}

	if err := os.WriteFile(configPath, []byte(filepath.Join("..", "shared")), 0o644); err != nil {
		t.Fatal(err)
	}
	got, configured, err = ConfiguredDataDir(placed)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(filepath.Join(placed, "..", "shared"))
	if err != nil {
		t.Fatal(err)
	}
	if !configured || got != want {
		t.Fatalf("ConfiguredDataDir() = %q, %v; want %q, true", got, configured, want)
	}
}

func TestConfiguredDataDirRejectsEmptyOrMultilineConfig(t *testing.T) {
	placed := t.TempDir()
	configPath := filepath.Join(placed, DataDirConfigName)
	for _, value := range []string{"", "one\ntwo\n"} {
		if err := os.WriteFile(configPath, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, configured, err := ConfiguredDataDir(placed); !configured || err == nil {
			t.Fatalf("ConfiguredDataDir(%q) configured=%v err=%v; want configured error", value, configured, err)
		}
	}
}

func TestConfiguredDataDirReportsMissingConfig(t *testing.T) {
	if got, configured, err := ConfiguredDataDir(t.TempDir()); err != nil || configured || got != "" {
		t.Fatalf("ConfiguredDataDir() = %q, %v, %v; want empty, false, nil", got, configured, err)
	}
}

func TestRuntimePIDLifecycleDoesNotRemoveNewerOwner(t *testing.T) {
	dataDir := t.TempDir()
	path, err := WriteRuntimePID(dataDir, 101)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ReadRuntimePID(dataDir); err != nil || got != 101 {
		t.Fatalf("ReadRuntimePID() = %d, %v; want 101, nil", got, err)
	}
	if _, err := WriteRuntimePID(dataDir, 202); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRuntimePID(dataDir, 101); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadRuntimePID(dataDir); err != nil || got != 202 {
		t.Fatalf("older owner removed marker: pid=%d err=%v", got, err)
	}
	if err := RemoveRuntimePID(dataDir, 202); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("runtime marker still exists: %v", err)
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
