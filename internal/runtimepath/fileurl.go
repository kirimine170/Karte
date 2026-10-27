package runtimepath

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// FileURL encodes a local path for opening in a browser, including Windows drive
// letters, UNC shares, and characters such as spaces, percent signs and hashes.
func FileURL(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(abs, `\\?\`) || strings.HasPrefix(abs, `\\.\`) {
			abs = abs[4:]
			if strings.HasPrefix(strings.ToUpper(abs), `UNC\`) {
				abs = `\\` + abs[4:]
			}
		}
	}
	urlPath := filepath.ToSlash(abs)
	host := ""
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(urlPath, "//") {
			host, urlPath, _ = strings.Cut(urlPath[2:], "/")
		}
		urlPath = "/" + strings.TrimPrefix(urlPath, "/")
	}
	return (&url.URL{Scheme: "file", Host: host, Path: urlPath}).String(), nil
}
