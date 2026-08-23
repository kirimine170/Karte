package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDataDirectory(t *testing.T) {
	tests := []struct {
		name    string
		options dataDirectoryResolveOptions
		want    dataDirectoryResolution
	}{
		{
			name: "Finder app bundle",
			options: dataDirectoryResolveOptions{
				GOOS:                "darwin",
				ExecutablePath:      "/Applications/Karte.app/Contents/MacOS/Karte",
				WorkingDirectory:    "/Users/test",
				UserConfigDirectory: "/Users/test/Library/Application Support",
			},
			want: dataDirectoryResolution{
				RootDirectory:       "/Users/test/Library/Application Support/Karte",
				DataDirectory:       "/Users/test/Library/Application Support/Karte",
				LegacyDataDirectory: "/Applications/karte_data",
				Kind:                dataDirectoryUser,
			},
		},
		{
			name: "App Translocation",
			options: dataDirectoryResolveOptions{
				GOOS:                "darwin",
				ExecutablePath:      "/private/var/folders/xy/AppTranslocation/ABC/d/Karte.app/Contents/MacOS/Karte",
				WorkingDirectory:    "/Users/test/Downloads",
				UserConfigDirectory: "/Users/test/Library/Application Support",
			},
			want: dataDirectoryResolution{
				RootDirectory:       "/Users/test/Library/Application Support/Karte",
				DataDirectory:       "/Users/test/Library/Application Support/Karte",
				LegacyDataDirectory: "/private/var/folders/xy/AppTranslocation/ABC/d/karte_data",
				Kind:                dataDirectoryUser,
			},
		},
		{
			name: "Unicode paths",
			options: dataDirectoryResolveOptions{
				GOOS:                "darwin",
				ExecutablePath:      "/Users/山田/アプリ/Karte.app/Contents/MacOS/Karte",
				WorkingDirectory:    "/Users/山田",
				UserConfigDirectory: "/Users/山田/Library/Application Support",
			},
			want: dataDirectoryResolution{
				RootDirectory:       "/Users/山田/Library/Application Support/Karte",
				DataDirectory:       "/Users/山田/Library/Application Support/Karte",
				LegacyDataDirectory: "/Users/山田/アプリ/karte_data",
				Kind:                dataDirectoryUser,
			},
		},
		{
			name: "KARTE_DATA_DIR override wins",
			options: dataDirectoryResolveOptions{
				GOOS:                           "darwin",
				ExecutablePath:                 "/private/var/folders/xy/AppTranslocation/ABC/d/Karte.app/Contents/MacOS/Karte",
				WorkingDirectory:               "/Users/山田/開発",
				UserConfigDirectory:            "/Users/山田/Library/Application Support",
				Override:                       "保存/カルテ",
				DevelopmentDataDirectoryExists: true,
			},
			want: dataDirectoryResolution{
				RootDirectory: "/Users/山田/開発/保存",
				DataDirectory: "/Users/山田/開発/保存/カルテ",
				Kind:          dataDirectoryOverride,
			},
		},
		{
			name: "wails dev uses repository data",
			options: dataDirectoryResolveOptions{
				GOOS:                           "darwin",
				ExecutablePath:                 "/Users/test/Karte/build/bin/Karte",
				WorkingDirectory:               "/Users/test/Karte",
				UserConfigDirectory:            "/Users/test/Library/Application Support",
				DevelopmentDataDirectoryExists: true,
			},
			want: dataDirectoryResolution{
				RootDirectory: "/Users/test/Karte",
				DataDirectory: "/Users/test/Karte/karte_data",
				Kind:          dataDirectoryDev,
			},
		},
		{
			name: "non Darwin keeps legacy location",
			options: dataDirectoryResolveOptions{
				GOOS:                "linux",
				ExecutablePath:      "/opt/Karte/Karte.app/Contents/MacOS/Karte",
				WorkingDirectory:    "/tmp",
				UserConfigDirectory: "/home/test/.config",
			},
			want: dataDirectoryResolution{
				RootDirectory: "/opt/Karte",
				DataDirectory: "/opt/Karte/karte_data",
				Kind:          dataDirectoryLegacy,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveDataDirectory(test.options)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("resolution\nwant: %#v\n got: %#v", test.want, got)
			}
			if test.name == "App Translocation" && (strings.Contains(got.DataDirectory, "AppTranslocation") || strings.Contains(got.RootDirectory, "AppTranslocation")) {
				t.Fatalf("translocated bundle leaked into writable roots: %#v", got)
			}
		})
	}
}

func TestResolveDataDirectoryRejectsRelativeOverrideWithoutWorkingDirectory(t *testing.T) {
	_, err := resolveDataDirectory(dataDirectoryResolveOptions{
		GOOS:           "darwin",
		ExecutablePath: "/Applications/Karte.app/Contents/MacOS/Karte",
		Override:       "relative/data",
	})
	if err == nil || !strings.Contains(err.Error(), "requires a working directory") {
		t.Fatalf("expected relative override error, got %v", err)
	}
}

func TestMigrateLegacyDataDirectoryPreservesExistingDestinationAndSource(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "旧データ", "karte_data")
	destinationDirectory := filepath.Join(root, "Library", "Application Support", "Karte")
	sourceConflict := filepath.Join(sourceDirectory, "content", "資料.md")
	sourceNew := filepath.Join(sourceDirectory, "data", "csv", "測定.csv")
	destinationConflict := filepath.Join(destinationDirectory, "content", "資料.md")
	sourceGitConfig := filepath.Join(sourceDirectory, ".git", "config")
	sourceGitObject := filepath.Join(sourceDirectory, ".git", "objects", "legacy-object")
	destinationGitConfig := filepath.Join(destinationDirectory, ".git", "config")
	sourceSymlinkConflict := filepath.Join(sourceDirectory, "content", "リンク.md")
	destinationSymlinkConflict := filepath.Join(destinationDirectory, "content", "リンク.md")
	symlinkTarget := filepath.Join(root, "destination-symlink-target.md")
	writeDataDirectoryTestFile(t, sourceConflict, "legacy user content", 0o444)
	writeDataDirectoryTestFile(t, sourceNew, "legacy,csv", 0o444)
	writeDataDirectoryTestFile(t, sourceGitConfig, "legacy repository", 0o444)
	writeDataDirectoryTestFile(t, sourceGitObject, "legacy object", 0o444)
	writeDataDirectoryTestFile(t, sourceSymlinkConflict, "legacy link replacement", 0o444)
	writeDataDirectoryTestFile(t, destinationConflict, "new destination content", 0o640)
	writeDataDirectoryTestFile(t, destinationGitConfig, "destination repository", 0o640)
	writeDataDirectoryTestFile(t, symlinkTarget, "destination symlink content", 0o640)
	if err := os.Symlink(symlinkTarget, destinationSymlinkConflict); err != nil {
		t.Fatal(err)
	}

	report, err := migrateLegacyDataDirectory(sourceDirectory, destinationDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 1 || report.Preserved != 3 {
		t.Fatalf("migration report = %#v, want copied=1 preserved=3", report)
	}
	assertDataDirectoryTestFile(t, destinationConflict, "new destination content")
	assertDataDirectoryTestFile(t, filepath.Join(destinationDirectory, "data", "csv", "測定.csv"), "legacy,csv")
	assertDataDirectoryTestFile(t, destinationGitConfig, "destination repository")
	linkTargetAfterMigration, err := os.Readlink(destinationSymlinkConflict)
	if err != nil {
		t.Fatal(err)
	}
	if linkTargetAfterMigration != symlinkTarget {
		t.Fatalf("destination symlink target = %q, want %q", linkTargetAfterMigration, symlinkTarget)
	}
	assertDataDirectoryTestFile(t, symlinkTarget, "destination symlink content")
	if _, err := os.Stat(filepath.Join(destinationDirectory, ".git", "objects", "legacy-object")); !os.IsNotExist(err) {
		t.Fatalf("legacy repository entries must not merge into an existing destination repository: %v", err)
	}
	assertDataDirectoryTestFile(t, sourceConflict, "legacy user content")
	assertDataDirectoryTestFile(t, sourceNew, "legacy,csv")
	assertDataDirectoryTestFile(t, sourceGitConfig, "legacy repository")
	assertDataDirectoryTestFile(t, sourceGitObject, "legacy object")
	assertDataDirectoryTestFile(t, sourceSymlinkConflict, "legacy link replacement")

	secondReport, err := migrateLegacyDataDirectory(sourceDirectory, destinationDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if secondReport.Copied != 0 || secondReport.Preserved != 4 {
		t.Fatalf("second migration report = %#v, want copied=0 preserved=4", secondReport)
	}
	assertNoMigrationTemporaryFiles(t, destinationDirectory)
}

func TestMigrateLegacyDataDirectoryRetriesAfterPartialFailure(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "legacy", "karte_data")
	destinationDirectory := filepath.Join(root, "Application Support", "Karte")
	firstSource := filepath.Join(sourceDirectory, "a-first.md")
	secondSource := filepath.Join(sourceDirectory, "b-second.md")
	writeDataDirectoryTestFile(t, firstSource, "first", 0o640)
	writeDataDirectoryTestFile(t, secondSource, "second", 0o640)

	injectedFailure := errors.New("injected migration interruption")
	report, err := migrateLegacyDataDirectoryWithCopy(
		sourceDirectory,
		destinationDirectory,
		func(sourcePath, destinationPath string, info fs.FileInfo) error {
			if filepath.Base(sourcePath) == "b-second.md" {
				return injectedFailure
			}
			return copyMigrationFileNoReplace(sourcePath, destinationPath, info)
		},
	)
	if !errors.Is(err, injectedFailure) {
		t.Fatalf("expected injected migration failure, got %v", err)
	}
	if report.Copied != 1 {
		t.Fatalf("partial migration report = %#v, want one copied file", report)
	}
	assertDataDirectoryTestFile(t, filepath.Join(destinationDirectory, "a-first.md"), "first")
	if _, err := os.Stat(filepath.Join(destinationDirectory, "b-second.md")); !os.IsNotExist(err) {
		t.Fatalf("failed file should not exist at destination: %v", err)
	}

	retryReport, err := migrateLegacyDataDirectory(sourceDirectory, destinationDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if retryReport.Copied != 1 || retryReport.Preserved != 1 {
		t.Fatalf("retry report = %#v, want copied=1 preserved=1", retryReport)
	}
	assertDataDirectoryTestFile(t, filepath.Join(destinationDirectory, "a-first.md"), "first")
	assertDataDirectoryTestFile(t, filepath.Join(destinationDirectory, "b-second.md"), "second")
	assertDataDirectoryTestFile(t, firstSource, "first")
	assertDataDirectoryTestFile(t, secondSource, "second")
	assertNoMigrationTemporaryFiles(t, destinationDirectory)
}

func TestMigrateLegacyDataDirectoryDoesNotReplaceLateDestination(t *testing.T) {
	root := t.TempDir()
	sourceDirectory := filepath.Join(root, "legacy", "karte_data")
	destinationDirectory := filepath.Join(root, "Application Support", "Karte")
	sourcePath := filepath.Join(sourceDirectory, "content.md")
	writeDataDirectoryTestFile(t, sourcePath, "legacy content", 0o640)

	report, err := migrateLegacyDataDirectoryWithCopy(
		sourceDirectory,
		destinationDirectory,
		func(sourcePath, destinationPath string, info fs.FileInfo) error {
			if err := os.WriteFile(destinationPath, []byte("concurrent destination content"), 0o640); err != nil {
				return err
			}
			return copyMigrationFileNoReplace(sourcePath, destinationPath, info)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if report.Copied != 0 || report.Preserved != 1 {
		t.Fatalf("late destination report = %#v, want copied=0 preserved=1", report)
	}
	assertDataDirectoryTestFile(t, filepath.Join(destinationDirectory, "content.md"), "concurrent destination content")
	assertDataDirectoryTestFile(t, sourcePath, "legacy content")
	assertNoMigrationTemporaryFiles(t, destinationDirectory)
}

func writeDataDirectoryTestFile(t *testing.T, path, content string, mode fs.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertDataDirectoryTestFile(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != want {
		t.Fatalf("%s content = %q, want %q", path, content, want)
	}
}

func assertNoMigrationTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(entry.Name(), ".karte-migrate-") {
			t.Errorf("migration temporary file remains: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
