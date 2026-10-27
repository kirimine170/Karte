package runtimepath

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileURLPreservesSpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "日本語 📄", "preview #1 100%.html")
	encoded, err := FileURL(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "file:///") || parsed.Host != "" || parsed.Fragment != "" || parsed.RawQuery != "" {
		t.Fatalf("invalid local file URL: %s", encoded)
	}
	decoded := parsed.Path
	if runtime.GOOS == "windows" {
		decoded = strings.TrimPrefix(decoded, "/")
	}
	if filepath.FromSlash(decoded) != path {
		t.Fatalf("file URL changed path: %q -> %s -> %q", path, encoded, decoded)
	}
}

func TestWindowsFileURL(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path resolution")
	}
	for _, tc := range []struct{ path, want string }{
		{`C:\Karte User\preview #1.html`, "file:///C:/Karte%20User/preview%20%231.html"},
		{`\\server\share\Karte User\preview.html`, "file://server/share/Karte%20User/preview.html"},
		{`\\?\C:\Karte User\preview.html`, "file:///C:/Karte%20User/preview.html"},
		{`\\?\UNC\server\share\preview.html`, "file://server/share/preview.html"},
	} {
		got, err := FileURL(tc.path)
		if err != nil || got != tc.want {
			t.Errorf("FileURL(%q) = %q, %v; want %q", tc.path, got, err, tc.want)
		}
	}
}
