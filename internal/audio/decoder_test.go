package audio

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestFindFFmpegPrefersExplicitOverride(t *testing.T) {
	wanted := filepath.Join(`C:\`, "tools", "ffmpeg.exe")
	got, err := findFFmpegForOS(
		"windows",
		filepath.Join(`C:\`, "Karte", "Karte.exe"),
		func(name string) string {
			if name == "KARTE_FFMPEG_BINARY" {
				return wanted
			}
			return ""
		},
		func(string) (string, error) { return "", errors.New("not found") },
		func(path string) bool { return path == wanted },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != wanted {
		t.Fatalf("findFFmpegForOS() = %q, want override %q", got, wanted)
	}
}

func TestFindFFmpegUsesPackagedWindowsBinaryBeforePath(t *testing.T) {
	executable := filepath.Join(`C:\`, "Karte", "Karte.exe")
	wanted := filepath.Join(filepath.Dir(executable), "ffmpeg.exe")
	got, err := findFFmpegForOS(
		"windows",
		executable,
		func(string) string { return "" },
		func(string) (string, error) { return filepath.Join(`C:\`, "PATH", "ffmpeg.exe"), nil },
		func(path string) bool { return path == wanted },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != wanted {
		t.Fatalf("findFFmpegForOS() = %q, want packaged binary %q", got, wanted)
	}
}

func TestFindFFmpegFallsBackToPath(t *testing.T) {
	wanted := filepath.Join(`C:\`, "PATH", "ffmpeg.exe")
	got, err := findFFmpegForOS(
		"windows",
		filepath.Join(`C:\`, "Karte", "Karte.exe"),
		func(string) string { return "" },
		func(string) (string, error) { return wanted, nil },
		func(string) bool { return false },
	)
	if err != nil {
		t.Fatal(err)
	}
	if got != wanted {
		t.Fatalf("findFFmpegForOS() = %q, want PATH binary %q", got, wanted)
	}
}
