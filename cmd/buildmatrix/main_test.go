package main

import (
	"context"
	"debug/buildinfo"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestBuildKarteMCPBinaryUsesTargetPlatform(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	artifact := t.TempDir()
	target := target{Platform: "windows/amd64", ArtifactDir: artifact}
	if err := buildKarteMCPBinary(context.Background(), root, target); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(artifact, "karte-mcp.exe")
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]string{}
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	if settings["GOOS"] != "windows" || settings["GOARCH"] != "amd64" {
		t.Fatalf("wrong target in MCP artifact: GOOS=%q GOARCH=%q", settings["GOOS"], settings["GOARCH"])
	}
}

func TestSignAndVerifyAppBundleSealsFinalArtifact(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake codesign executable uses a POSIX script")
	}
	root := t.TempDir()
	artifactDir := filepath.Join(root, "dist")
	if err := os.MkdirAll(filepath.Join(artifactDir, "Karte.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "codesign.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CODESIGN_LOG\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "codesign"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("CODESIGN_LOG", logPath)
	t.Setenv("MACOS_CODESIGN_IDENTITY", "Developer ID Test")

	if err := signAndVerifyAppBundle(context.Background(), artifactDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "--force --deep --sign Developer ID Test --timestamp=none") ||
		!strings.Contains(lines[1], "--verify --deep --strict --verbose=2") {
		t.Fatalf("unexpected codesign calls: %q", lines)
	}
}

func TestSignAndVerifyAppBundleRequiresBundle(t *testing.T) {
	if err := signAndVerifyAppBundle(context.Background(), t.TempDir()); err == nil {
		t.Fatal("missing app bundle was accepted")
	}
}
