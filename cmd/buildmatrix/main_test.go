package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func init() {
	if os.Getenv("KARTE_BUILDMATRIX_WAILS_HELPER") != "1" {
		return
	}
	recordPath := os.Getenv("KARTE_WAILS_RECORD")
	record := strings.Join([]string{
		os.Getenv("DYLD_LIBRARY_PATH"),
		os.Getenv("CGO_CFLAGS"),
		os.Getenv("CGO_CXXFLAGS"),
		os.Getenv("CGO_LDFLAGS"),
		strings.Join(os.Args[1:], " "),
		"",
	}, "\n")
	if err := os.WriteFile(recordPath, []byte(record), 0o600); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "write Wails helper record: %v\n", err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestRunWailsBuildUsesVerifiedBinaryAndPreservesMacOSEnvironment(t *testing.T) {
	tempDir := t.TempDir()
	recordPath := filepath.Join(tempDir, "wails environment.txt")
	fakeWails, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("KARTE_WAILS_BIN", fakeWails)
	t.Setenv("KARTE_BUILDMATRIX_WAILS_HELPER", "1")
	t.Setenv("KARTE_WAILS_RECORD", recordPath)
	t.Setenv("DYLD_LIBRARY_PATH", "/native path/sherpa:/native path/portaudio")
	t.Setenv("CGO_CFLAGS", "-mmacosx-version-min=11.0")
	t.Setenv("CGO_CXXFLAGS", "-mmacosx-version-min=11.0")
	t.Setenv("CGO_LDFLAGS", `"-L/native path/sherpa" -framework UniformTypeIdentifiers -mmacosx-version-min=11.0`)

	buildTarget := target{
		Name:     "darwin-arm64",
		Platform: "darwin/arm64",
		Env:      map[string]string{"GOOS": "darwin", "GOARCH": "arm64"},
		Flags:    []string{"-tags", "fixture tag"},
	}
	if err := runWailsBuild(context.Background(), buildTarget); err != nil {
		t.Fatal(err)
	}
	record, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"/native path/sherpa:/native path/portaudio",
		"-mmacosx-version-min=11.0",
		"-mmacosx-version-min=11.0",
		`"-L/native path/sherpa" -framework UniformTypeIdentifiers -mmacosx-version-min=11.0`,
		"build -platform darwin/arm64 -tags fixture tag",
		"",
	}, "\n")
	if string(record) != want {
		t.Fatalf("verified Wails invocation record = %q，want %q", record, want)
	}
}

func TestMaterializeDarwinInfoPlistUsesTrackedTemplateAsSourceOfTruth(t *testing.T) {
	projectRoot := t.TempDir()
	templatePath := filepath.Join(projectRoot, "templates", "macos", "Info.plist")
	first := "<plist><dict><key>LSMinimumSystemVersion</key><string>11.0.0</string></dict></plist>\n"
	writeBuildmatrixTestFile(t, templatePath, first)

	if err := materializeDarwinInfoPlist(projectRoot); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(projectRoot, "build", "darwin", "Info.plist")
	materialized, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(materialized) != first || !strings.Contains(string(materialized), "<string>11.0.0</string>") {
		t.Fatalf("materialized Info.plist = %q，want exact tracked template", materialized)
	}

	second := strings.Replace(first, "11.0.0", "12.3.4", 1)
	writeBuildmatrixTestFile(t, templatePath, second)
	if err := materializeDarwinInfoPlist(projectRoot); err != nil {
		t.Fatal(err)
	}
	materialized, err = os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(materialized) != second {
		t.Fatalf("rematerialized Info.plist = %q，want %q", materialized, second)
	}
}

func TestMaterializeDarwinInfoPlistRejectsMissingAndSymlinkedTemplate(t *testing.T) {
	projectRoot := t.TempDir()
	if err := materializeDarwinInfoPlist(projectRoot); err == nil || !strings.Contains(err.Error(), "inspect macOS Info.plist template") {
		t.Fatalf("missing template error = %v", err)
	}

	templatePath := filepath.Join(projectRoot, "templates", "macos", "Info.plist")
	if err := os.MkdirAll(filepath.Dir(templatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "Info.plist")
	writeBuildmatrixTestFile(t, outside, "outside")
	if err := os.Symlink(outside, templatePath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := materializeDarwinInfoPlist(projectRoot); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlinked template error = %v", err)
	}
}

func TestPackageComplianceDocumentsUsesPlatformContract(t *testing.T) {
	projectRoot := t.TempDir()
	sources := map[string]string{
		"LICENSE": "license", "THIRD_PARTY_NOTICES.md": "notices", "bom.cdx.json": "sbom",
		filepath.Join("compliance", "assets.json"):     "assets",
		filepath.Join("compliance", "components.json"): "components",
	}
	for path, content := range sources {
		writeBuildmatrixTestFile(t, filepath.Join(projectRoot, path), content)
	}
	for _, test := range []struct {
		name        string
		platform    string
		documentDir string
	}{
		{name: "darwin-arm64", platform: "darwin/arm64", documentDir: "Karte.app/Contents/Resources"},
		{name: "windows", platform: "windows/amd64", documentDir: "."},
		{name: "linux", platform: "linux/amd64", documentDir: "."},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifactRoot := t.TempDir()
			if test.documentDir != "." {
				if err := os.MkdirAll(filepath.Join(artifactRoot, filepath.FromSlash(test.documentDir)), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			target := target{Name: test.name, Platform: test.platform, ArtifactDir: artifactRoot}
			if err := packageComplianceDocuments(projectRoot, target); err != nil {
				t.Fatal(err)
			}
			for relative, content := range sources {
				path := filepath.Join(artifactRoot, filepath.FromSlash(test.documentDir), relative)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read packaged %s: %v", relative, err)
				}
				if string(data) != content {
					t.Fatalf("packaged %s = %q，want %q", relative, data, content)
				}
			}
		})
	}
}

func TestComplianceArtifactRootUsesAuditContract(t *testing.T) {
	for _, test := range []struct {
		target   target
		wantRoot string
		wantOS   string
	}{
		{target: target{Name: "darwin-arm64", ArtifactDir: "dist/darwin-arm64"}, wantRoot: filepath.Join("dist/darwin-arm64", "Karte.app"), wantOS: "darwin"},
		{target: target{Name: "windows", Platform: "windows/amd64", ArtifactDir: "dist/windows"}, wantRoot: "dist/windows", wantOS: "windows"},
		{target: target{Name: "linux", Platform: "linux/amd64", ArtifactDir: "dist/linux"}, wantRoot: "dist/linux", wantOS: "linux"},
	} {
		root, platform := complianceArtifactRoot(test.target)
		if root != test.wantRoot || platform != test.wantOS {
			t.Errorf("complianceArtifactRoot(%+v) = %q，%q，want %q，%q", test.target, root, platform, test.wantRoot, test.wantOS)
		}
	}
}

func TestPackageComplianceDocumentsFailsForMissingOrSymlinkedSource(t *testing.T) {
	projectRoot := t.TempDir()
	artifactRoot := t.TempDir()
	if err := packageComplianceDocuments(projectRoot, target{Name: "linux", ArtifactDir: artifactRoot}); err == nil || !strings.Contains(err.Error(), "required compliance source") {
		t.Fatalf("expected missing generated document failure，got %v", err)
	}
	writeBuildmatrixTestFile(t, filepath.Join(projectRoot, "LICENSE"), "license")
	writeBuildmatrixTestFile(t, filepath.Join(projectRoot, "THIRD_PARTY_NOTICES.md"), "notices")
	writeBuildmatrixTestFile(t, filepath.Join(projectRoot, "bom.cdx.json"), "sbom")
	writeBuildmatrixTestFile(t, filepath.Join(projectRoot, "compliance", "components.json"), "components")
	outside := filepath.Join(t.TempDir(), "assets.json")
	writeBuildmatrixTestFile(t, outside, "assets")
	if err := os.Symlink(outside, filepath.Join(projectRoot, "compliance", "assets.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := packageComplianceDocuments(projectRoot, target{Name: "linux", ArtifactDir: artifactRoot}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("expected symlinked source rejection，got %v", err)
	}
}

func writeBuildmatrixTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
