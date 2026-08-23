package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

type mediaRouteSpec struct {
	prefix          string
	mediaType       string
	allowedPrefixes []string
	contentTypes    map[string]string
}

type mediaFileOpenHooks struct {
	afterInitialLstat func(*os.Root, string, os.FileInfo) error
}

var mediaRouteSpecs = map[string]mediaRouteSpec{
	"audio": {
		prefix:          "/audio/",
		mediaType:       "audio",
		allowedPrefixes: []string{"data/audio/"},
		contentTypes: map[string]string{
			".wav": "audio/wav",
			".mp3": "audio/mpeg",
			".m4a": "audio/mp4",
			".ogg": "audio/ogg",
		},
	},
	"image": {
		prefix:          "/image/",
		mediaType:       "image",
		allowedPrefixes: []string{"data/image/", "content/clips/assets/"},
		contentTypes: map[string]string{
			".jpg":  "image/jpeg",
			".jpeg": "image/jpeg",
			".png":  "image/png",
			".gif":  "image/gif",
			".webp": "image/webp",
		},
	},
	"pdf": {
		prefix:          "/pdf/",
		mediaType:       "pdf",
		allowedPrefixes: []string{"content/"},
		contentTypes: map[string]string{
			".pdf": "application/pdf",
		},
	},
}

// GetAudioFileURL returns a confined URL for an imported audio file.
func (a *App) GetAudioFileURL(audioPath string) (string, error) {
	return a.mediaFileURL(audioPath, mediaRouteSpecs["audio"])
}

// GetImageFileURL returns a confined URL for an imported raster image.
func (a *App) GetImageFileURL(imagePath string) (string, error) {
	return a.mediaFileURL(imagePath, mediaRouteSpecs["image"])
}

// GetPdfFileURL returns a confined URL for an imported PDF.
func (a *App) GetPdfFileURL(pdfPath string) (string, error) {
	return a.mediaFileURL(pdfPath, mediaRouteSpecs["pdf"])
}

func (a *App) mediaFileURL(rawPath string, spec mediaRouteSpec) (string, error) {
	relativePath, contentType, err := validateMediaRelativePath(rawPath, spec)
	if err != nil {
		return "", err
	}
	file, info, err := openConfinedMediaFile(a.dataDir, relativePath)
	if err != nil {
		return "", fmt.Errorf("open %s file: %w", spec.mediaType, err)
	}
	defer file.Close()
	if err := validateMediaFileMagic(file, filepath.Ext(relativePath), spec.mediaType); err != nil {
		return "", err
	}
	urlPath := (&url.URL{Path: spec.prefix + relativePath}).EscapedPath()
	a.logInfo(fmt.Sprintf("Media file URL (%s): %s (size: %d bytes，type: %s)", spec.mediaType, urlPath, info.Size(), contentType))
	return urlPath, nil
}

// createAssetHandler wraps embedded frontend assets with strictly confined
// media routes. Invalid media paths are terminal 404 responses and never fall
// through to the embedded file server.
func (a *App) createAssetHandler() http.Handler {
	defaultHandler := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, spec := range []mediaRouteSpec{mediaRouteSpecs["audio"], mediaRouteSpecs["image"], mediaRouteSpecs["pdf"]} {
			if requestTargetsMediaRoute(r, spec.prefix) {
				a.serveMediaFile(w, r, spec)
				return
			}
		}
		defaultHandler.ServeHTTP(w, r)
	})
}

func requestTargetsMediaRoute(request *http.Request, prefix string) bool {
	if request == nil || request.URL == nil {
		return false
	}
	for _, initial := range []string{request.URL.Path, request.URL.EscapedPath()} {
		current := initial
		for depth := 0; depth < 3; depth++ {
			if strings.HasPrefix(current, prefix) {
				return true
			}
			decoded, err := url.PathUnescape(current)
			if err != nil || decoded == current {
				break
			}
			current = decoded
		}
	}
	return false
}

func (a *App) serveMediaFile(w http.ResponseWriter, r *http.Request, spec mediaRouteSpec) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	escapedPath := r.URL.EscapedPath()
	if !strings.HasPrefix(escapedPath, spec.prefix) {
		mediaNotFound(w)
		return
	}
	escapedRelative := strings.TrimPrefix(escapedPath, spec.prefix)
	if err := validateEscapedMediaPath(escapedRelative); err != nil {
		mediaNotFound(w)
		return
	}

	rawRelative := strings.TrimPrefix(r.URL.Path, spec.prefix)
	relativePath, contentType, err := validateMediaRelativePath(rawRelative, spec)
	if err != nil {
		mediaNotFound(w)
		return
	}
	file, info, err := openConfinedMediaFile(a.dataDir, relativePath)
	if err != nil {
		mediaNotFound(w)
		return
	}
	defer file.Close()
	if err := validateMediaFileMagic(file, filepath.Ext(relativePath), spec.mediaType); err != nil {
		mediaNotFound(w)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, filepath.Base(relativePath), info.ModTime(), file)
}

func mediaNotFound(w http.ResponseWriter) {
	http.Error(w, "media file not found", http.StatusNotFound)
}

func validateMediaRelativePath(rawPath string, spec mediaRouteSpec) (string, string, error) {
	if strings.TrimSpace(rawPath) == "" {
		return "", "", fmt.Errorf("%s path is empty", spec.mediaType)
	}
	if err := validateEscapedMediaPath(rawPath); err != nil {
		return "", "", fmt.Errorf("invalid %s path: %w", spec.mediaType, err)
	}
	if strings.ContainsRune(rawPath, '\x00') || strings.Contains(rawPath, "\\") || strings.Contains(rawPath, ":") {
		return "", "", fmt.Errorf("invalid %s path", spec.mediaType)
	}
	relativePath := strings.TrimPrefix(strings.ReplaceAll(rawPath, "\\", "/"), "/")
	if relativePath != rawPath || pathpkg.IsAbs(relativePath) || filepath.IsAbs(filepath.FromSlash(relativePath)) {
		return "", "", fmt.Errorf("absolute %s paths are not allowed", spec.mediaType)
	}
	cleaned := pathpkg.Clean(relativePath)
	if cleaned != relativePath || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", "", fmt.Errorf("%s path traversal is not allowed", spec.mediaType)
	}
	allowed := false
	for _, prefix := range spec.allowedPrefixes {
		if strings.HasPrefix(relativePath, prefix) && len(relativePath) > len(prefix) {
			allowed = true
			break
		}
	}
	if !allowed {
		return "", "", fmt.Errorf("%s path is outside its allowed directory", spec.mediaType)
	}
	extension := strings.ToLower(pathpkg.Ext(relativePath))
	contentType, ok := spec.contentTypes[extension]
	if !ok {
		return "", "", fmt.Errorf("unsupported %s extension %q", spec.mediaType, extension)
	}
	return relativePath, contentType, nil
}

func validateEscapedMediaPath(value string) error {
	current := value
	for depth := 0; depth < 3; depth++ {
		lower := strings.ToLower(current)
		if strings.Contains(lower, "%2f") || strings.Contains(lower, "%5c") || strings.Contains(lower, "%00") {
			return errors.New("encoded path separator is not allowed")
		}
		decoded, err := url.PathUnescape(current)
		if err != nil {
			return fmt.Errorf("decode escaped path: %w", err)
		}
		if decoded == current {
			return validateDecodedMediaPath(decoded)
		}
		if err := validateDecodedMediaPath(decoded); err != nil {
			return err
		}
		current = decoded
	}
	return errors.New("excessively encoded path")
}

func validateDecodedMediaPath(value string) error {
	value = strings.ReplaceAll(value, "\\", "/")
	if strings.ContainsRune(value, '\x00') || pathpkg.IsAbs(value) {
		return errors.New("absolute or NUL path is not allowed")
	}
	cleaned := pathpkg.Clean(value)
	if cleaned != value || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return errors.New("path traversal is not allowed")
	}
	return nil
}

func openConfinedMediaFile(dataDirectory, relativePath string) (*os.File, os.FileInfo, error) {
	return openConfinedMediaFileWithHooks(dataDirectory, relativePath, mediaFileOpenHooks{})
}

func openConfinedMediaFileWithHooks(dataDirectory, relativePath string, hooks mediaFileOpenHooks) (*os.File, os.FileInfo, error) {
	root, err := os.OpenRoot(dataDirectory)
	if err != nil {
		return nil, nil, fmt.Errorf("open data directory root: %w", err)
	}
	defer root.Close()

	rootPath := filepath.FromSlash(relativePath)
	linkInfo, err := root.Lstat(rootPath)
	if err != nil {
		return nil, nil, err
	}
	if !linkInfo.Mode().IsRegular() || linkInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errors.New("media path is not a regular non-symlink file")
	}
	if hooks.afterInitialLstat != nil {
		if err := hooks.afterInitialLstat(root, rootPath, linkInfo); err != nil {
			return nil, nil, err
		}
	}
	file, err := root.Open(rootPath)
	if err != nil {
		return nil, nil, err
	}
	openedInfo, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !openedInfo.Mode().IsRegular() || !os.SameFile(linkInfo, openedInfo) {
		file.Close()
		return nil, nil, errors.New("opened media does not match the inspected regular file")
	}
	currentInfo, err := root.Lstat(rootPath)
	if err != nil || !currentInfo.Mode().IsRegular() || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, currentInfo) {
		file.Close()
		return nil, nil, errors.New("media path changed while it was opened")
	}
	return file, openedInfo, nil
}

func validateMediaFileMagic(file *os.File, extension, mediaType string) error {
	if file == nil {
		return errors.New("media file is nil")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek media header: %w", err)
	}
	var header [1024]byte
	read, err := io.ReadFull(file, header[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read media header: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind media file: %w", err)
	}
	data := header[:read]
	extension = strings.ToLower(extension)
	valid := false
	switch extension {
	case ".jpg", ".jpeg":
		valid = len(data) >= 3 && bytes.Equal(data[:3], []byte{0xff, 0xd8, 0xff})
	case ".png":
		valid = len(data) >= 8 && bytes.Equal(data[:8], []byte("\x89PNG\r\n\x1a\n"))
	case ".gif":
		valid = len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a")))
	case ".webp":
		valid = len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
	case ".pdf":
		valid = bytes.Contains(data, []byte("%PDF-"))
	case ".wav":
		valid = len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE"))
	case ".mp3":
		valid = len(data) >= 3 && (bytes.Equal(data[:3], []byte("ID3")) || (data[0] == 0xff && data[1]&0xe0 == 0xe0))
	case ".m4a":
		valid = len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp"))
	case ".ogg":
		valid = len(data) >= 4 && bytes.Equal(data[:4], []byte("OggS"))
	}
	if !valid {
		return fmt.Errorf("%s content does not match extension %s", mediaType, extension)
	}
	return nil
}
