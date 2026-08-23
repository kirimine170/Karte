package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"karte/internal/audio"
	"karte/internal/webputil"

	"golang.org/x/image/draw"
)

const (
	mediaImportKindImage = "image"
	mediaImportKindAudio = "audio"
	mediaImportKindPDF   = "pdf"
	mediaImportKindCSV   = "csv"

	defaultMaxImageImportBytes  = int64(64 * 1024 * 1024)
	defaultMaxAudioImportBytes  = int64(512 * 1024 * 1024)
	defaultMaxPDFImportBytes    = int64(512 * 1024 * 1024)
	defaultMaxCSVImportBytes    = defaultMaxCSVBytes
	defaultLegacyImportMaxBytes = int64(64 * 1024 * 1024)
	mediaImportCopyBufferBytes  = 128 * 1024
	mediaImportChunkBytes       = 256 * 1024
	mediaImportMaxSessions      = 4
	mediaImportSessionTTL       = 10 * time.Minute
	mediaImportMaxDimension     = 16_384
	mediaImportMaxPixels        = int64(40_000_000)
	mediaImportTargetLongEdge   = 4_096
	mediaImportTargetPixels     = int64(16_000_000)
	mediaImportCollisionLimit   = 10_000
)

var (
	errMediaImportTooLarge = errors.New("media import exceeds its byte limit")
	errMediaImportClosed   = errors.New("media import session is closed")
)

type MediaImportSession struct {
	ID        string `json:"id"`
	ChunkSize int    `json:"chunkSize"`
	MaxBytes  int64  `json:"maxBytes"`
}

type mediaImportConfig struct {
	ImageMaxBytes  int64
	AudioMaxBytes  int64
	PDFMaxBytes    int64
	CSVMaxBytes    int64
	LegacyMaxBytes int64
	MaxSessions    int
	SessionTTL     time.Duration
}

func defaultMediaImportConfig() mediaImportConfig {
	return mediaImportConfig{
		ImageMaxBytes:  defaultMaxImageImportBytes,
		AudioMaxBytes:  defaultMaxAudioImportBytes,
		PDFMaxBytes:    defaultMaxPDFImportBytes,
		CSVMaxBytes:    defaultMaxCSVImportBytes,
		LegacyMaxBytes: defaultLegacyImportMaxBytes,
		MaxSessions:    mediaImportMaxSessions,
		SessionTTL:     mediaImportSessionTTL,
	}
}

func (config mediaImportConfig) normalized() mediaImportConfig {
	defaults := defaultMediaImportConfig()
	if config.ImageMaxBytes <= 0 {
		config.ImageMaxBytes = defaults.ImageMaxBytes
	}
	if config.AudioMaxBytes <= 0 {
		config.AudioMaxBytes = defaults.AudioMaxBytes
	}
	if config.PDFMaxBytes <= 0 {
		config.PDFMaxBytes = defaults.PDFMaxBytes
	}
	if config.CSVMaxBytes <= 0 {
		config.CSVMaxBytes = defaults.CSVMaxBytes
	}
	if config.LegacyMaxBytes <= 0 {
		config.LegacyMaxBytes = defaults.LegacyMaxBytes
	}
	if config.MaxSessions <= 0 {
		config.MaxSessions = defaults.MaxSessions
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = defaults.SessionTTL
	}
	return config
}

type mediaImportSpec struct {
	kind       string
	directory  string
	maxBytes   int64
	extensions map[string]struct{}
}

func mediaImportSpecForKind(kind string, config mediaImportConfig) (mediaImportSpec, error) {
	config = config.normalized()
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case mediaImportKindImage:
		return mediaImportSpec{
			kind:      mediaImportKindImage,
			directory: "data/image",
			maxBytes:  config.ImageMaxBytes,
			extensions: map[string]struct{}{
				".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {}, ".webp": {},
			},
		}, nil
	case mediaImportKindAudio:
		return mediaImportSpec{
			kind:      mediaImportKindAudio,
			directory: "data/audio",
			maxBytes:  config.AudioMaxBytes,
			extensions: map[string]struct{}{
				".wav": {}, ".mp3": {}, ".m4a": {},
			},
		}, nil
	case mediaImportKindPDF:
		return mediaImportSpec{
			kind:       mediaImportKindPDF,
			directory:  "content",
			maxBytes:   config.PDFMaxBytes,
			extensions: map[string]struct{}{".pdf": {}},
		}, nil
	case mediaImportKindCSV:
		return mediaImportSpec{
			kind:       mediaImportKindCSV,
			directory:  "data/csv",
			maxBytes:   config.CSVMaxBytes,
			extensions: map[string]struct{}{".csv": {}},
		}, nil
	default:
		return mediaImportSpec{}, fmt.Errorf("unsupported media import kind %q", kind)
	}
}

type mediaImportHooks struct {
	now                  func() time.Time
	randomID             func() (string, error)
	openRoot             func(string) (*os.Root, error)
	afterSourceLstat     func(*os.Root, string, os.FileInfo) error
	afterDestinationOpen func(*os.Root, string, *os.Root) error
	afterSessionLookup   func()
	write                func(*os.File, []byte) (int, error)
	chmod                func(*os.File, os.FileMode) error
	sync                 func(*os.File) error
	close                func(*os.File) error
	link                 func(*os.Root, string, string) error
	remove               func(*os.Root, string) error
	syncRoot             func(*os.Root) error
	decodeConfig         func(io.Reader) (image.Config, string, error)
	decodeImage          func(io.Reader) (image.Image, string, error)
	encodeWebP           func(io.Writer, image.Image, bool) error
	emitEvent            func(*App, string, interface{})
	refreshImageGraph    func(*App)
	startTranscription   func(*App, string, string)
}

func defaultMediaImportHooks() mediaImportHooks {
	return mediaImportHooks{
		now:      time.Now,
		randomID: randomMediaImportID,
		openRoot: os.OpenRoot,
		write: func(file *os.File, data []byte) (int, error) {
			return file.Write(data)
		},
		chmod: func(file *os.File, mode os.FileMode) error { return file.Chmod(mode) },
		sync:  func(file *os.File) error { return file.Sync() },
		close: func(file *os.File) error { return file.Close() },
		link: func(root *os.Root, oldName, newName string) error {
			return root.Link(oldName, newName)
		},
		remove: func(root *os.Root, name string) error { return root.Remove(name) },
		syncRoot: func(root *os.Root) error {
			directory, err := root.Open(".")
			if err != nil {
				return err
			}
			defer directory.Close()
			return syncMediaImportDirectory(directory)
		},
		decodeConfig: image.DecodeConfig,
		decodeImage:  image.Decode,
		encodeWebP:   webputil.EncodeWebP,
		emitEvent: func(app *App, name string, data interface{}) {
			app.emitEvent(name, data)
		},
		refreshImageGraph: func(app *App) {
			app.refreshGraphAfterMutation("image import")
		},
		startTranscription: func(app *App, absolutePath, relativePath string) {
			app.startTranscriptionJob(absolutePath, relativePath)
		},
	}
}

func (hooks mediaImportHooks) normalized() mediaImportHooks {
	defaults := defaultMediaImportHooks()
	if hooks.now == nil {
		hooks.now = defaults.now
	}
	if hooks.randomID == nil {
		hooks.randomID = defaults.randomID
	}
	if hooks.openRoot == nil {
		hooks.openRoot = defaults.openRoot
	}
	if hooks.write == nil {
		hooks.write = defaults.write
	}
	if hooks.chmod == nil {
		hooks.chmod = defaults.chmod
	}
	if hooks.sync == nil {
		hooks.sync = defaults.sync
	}
	if hooks.close == nil {
		hooks.close = defaults.close
	}
	if hooks.link == nil {
		hooks.link = defaults.link
	}
	if hooks.remove == nil {
		hooks.remove = defaults.remove
	}
	if hooks.syncRoot == nil {
		hooks.syncRoot = defaults.syncRoot
	}
	if hooks.decodeConfig == nil {
		hooks.decodeConfig = defaults.decodeConfig
	}
	if hooks.decodeImage == nil {
		hooks.decodeImage = defaults.decodeImage
	}
	if hooks.encodeWebP == nil {
		hooks.encodeWebP = defaults.encodeWebP
	}
	if hooks.emitEvent == nil {
		hooks.emitEvent = defaults.emitEvent
	}
	if hooks.refreshImageGraph == nil {
		hooks.refreshImageGraph = defaults.refreshImageGraph
	}
	if hooks.startTranscription == nil {
		hooks.startTranscription = defaults.startTranscription
	}
	return hooks
}

type appMediaImportState struct {
	mu       sync.Mutex
	sessions map[string]*mediaImportSessionState
	creating int
	watcher  bool
	config   *mediaImportConfig
	hooks    *mediaImportHooks
}

type mediaImportSessionState struct {
	mu           sync.Mutex
	stage        *mediaImportStage
	declaredSize int64
	lastTouched  time.Time
	closed       bool
}

type mediaImportStageFile struct {
	name   string
	file   *os.File
	closed bool
}

type mediaImportStage struct {
	root         *os.Root
	spec         mediaImportSpec
	originalName string
	createdAt    time.Time
	hooks        mediaImportHooks
	original     *mediaImportStageFile
	derived      *mediaImportStageFile
	bytesWritten int64
	published    bool
	closed       bool
}

type mediaImportResult struct {
	relativePath string
	absolutePath string
	savedName    string
	originalRel  string
}

func randomMediaImportID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (a *App) mediaImportSettings() (mediaImportConfig, mediaImportHooks) {
	a.mediaImports.mu.Lock()
	defer a.mediaImports.mu.Unlock()
	config := defaultMediaImportConfig()
	if a.mediaImports.config != nil {
		config = *a.mediaImports.config
	}
	hooks := defaultMediaImportHooks()
	if a.mediaImports.hooks != nil {
		hooks = *a.mediaImports.hooks
	}
	return config.normalized(), hooks.normalized()
}

func (l *appLifecycle) acquireMediaImportOperation() (context.Context, func(), bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closing {
		return nil, nil, false
	}
	if l.ctx == nil {
		l.ctx, l.cancel = context.WithCancel(context.Background())
	}
	l.workers.Add(1)
	return l.ctx, l.workers.Done, true
}

// ImportAudioFile securely streams an audio file into the managed audio root.
func (a *App) ImportAudioFile(sourcePath string) (string, error) {
	return a.importMediaPath(mediaImportKindAudio, sourcePath)
}

// ImportImageFile securely streams an image and publishes its derived WebP.
func (a *App) ImportImageFile(sourcePath string) (string, error) {
	return a.importMediaPath(mediaImportKindImage, sourcePath)
}

// ImportPdfFile securely streams a PDF into the managed content root.
func (a *App) ImportPdfFile(sourcePath string) (string, error) {
	return a.importMediaPath(mediaImportKindPDF, sourcePath)
}

// Legacy one-shot Base64 methods remain for API compatibility. New frontend
// code uses bounded chunk sessions instead.
func (a *App) ImportAudioBase64(filename, encoded string) (string, error) {
	return a.importLegacyMediaBase64(mediaImportKindAudio, filename, encoded)
}

func (a *App) ImportImageBase64(filename, encoded string) (string, error) {
	return a.importLegacyMediaBase64(mediaImportKindImage, filename, encoded)
}

func (a *App) ImportPdfBase64(filename, encoded string) (string, error) {
	return a.importLegacyMediaBase64(mediaImportKindPDF, filename, encoded)
}

func (a *App) importMediaPath(kind, sourcePath string) (string, error) {
	ctx, release, admitted := a.lifecycle.acquireMediaImportOperation()
	if !admitted {
		return "", errMediaImportClosed
	}
	defer release()
	if strings.TrimSpace(sourcePath) == "" {
		return "", errors.New("source path is required")
	}
	config, hooks := a.mediaImportSettings()
	spec, err := mediaImportSpecForKind(kind, config)
	if err != nil {
		return "", err
	}
	source, sourceName, sourceInfo, err := openRegularNonSymlinkSource(sourcePath, hooks)
	if err != nil {
		return "", fmt.Errorf("open %s source: %w", kind, err)
	}
	defer source.Close()
	if sourceInfo.Size() > spec.maxBytes {
		return "", fmt.Errorf("%w: %s limit is %d bytes", errMediaImportTooLarge, kind, spec.maxBytes)
	}
	stage, err := a.newMediaImportStage(spec, sourceName, hooks)
	if err != nil {
		return "", err
	}
	defer stage.abort()
	if err := stage.copyFrom(ctx, source, spec.maxBytes); err != nil {
		return "", err
	}
	result, err := a.finishMediaImportStage(ctx, stage)
	if err != nil {
		return "", err
	}
	return result.relativePath, nil
}

func (a *App) importLegacyMediaBase64(kind, filename, encoded string) (string, error) {
	ctx, release, admitted := a.lifecycle.acquireMediaImportOperation()
	if !admitted {
		return "", errMediaImportClosed
	}
	defer release()
	config, hooks := a.mediaImportSettings()
	spec, err := mediaImportSpecForKind(kind, config)
	if err != nil {
		return "", err
	}
	if err := validateStrictBase64Context(ctx, encoded, config.LegacyMaxBytes); err != nil {
		return "", fmt.Errorf("decode %s data: %w", kind, err)
	}
	stage, err := a.newMediaImportStage(spec, filename, hooks)
	if err != nil {
		return "", err
	}
	defer stage.abort()
	decoder := base64.NewDecoder(base64.StdEncoding.Strict(), strings.NewReader(encoded))
	if err := stage.copyFrom(ctx, decoder, minInt64(spec.maxBytes, config.LegacyMaxBytes)); err != nil {
		return "", err
	}
	result, err := a.finishMediaImportStage(ctx, stage)
	if err != nil {
		return "", err
	}
	return result.relativePath, nil
}

func (a *App) importImageFromReader(originalName string, reader io.Reader) (string, error) {
	return a.importMediaReader(mediaImportKindImage, originalName, reader)
}

func (a *App) importAudioFromReader(originalName string, reader io.Reader) (string, error) {
	return a.importMediaReader(mediaImportKindAudio, originalName, reader)
}

func (a *App) importPdfFromReader(originalName string, reader io.Reader) (string, error) {
	return a.importMediaReader(mediaImportKindPDF, originalName, reader)
}

func (a *App) importMediaReader(kind, originalName string, reader io.Reader) (string, error) {
	ctx, release, admitted := a.lifecycle.acquireMediaImportOperation()
	if !admitted {
		return "", errMediaImportClosed
	}
	defer release()
	if reader == nil {
		return "", errors.New("media reader is nil")
	}
	config, hooks := a.mediaImportSettings()
	spec, err := mediaImportSpecForKind(kind, config)
	if err != nil {
		return "", err
	}
	stage, err := a.newMediaImportStage(spec, originalName, hooks)
	if err != nil {
		return "", err
	}
	defer stage.abort()
	if err := stage.copyFrom(ctx, reader, spec.maxBytes); err != nil {
		return "", err
	}
	result, err := a.finishMediaImportStage(ctx, stage)
	if err != nil {
		return "", err
	}
	return result.relativePath, nil
}

func openRegularNonSymlinkSource(path string, hooks mediaImportHooks) (*os.File, string, os.FileInfo, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, "", nil, err
	}
	baseName := filepath.Base(absolutePath)
	if baseName == "." || baseName == string(filepath.Separator) {
		return nil, "", nil, errors.New("source path has no filename")
	}
	root, err := hooks.openRoot(filepath.Dir(absolutePath))
	if err != nil {
		return nil, "", nil, err
	}
	defer root.Close()
	before, err := root.Lstat(baseName)
	if err != nil {
		return nil, "", nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, "", nil, errors.New("source is not a regular non-symlink file")
	}
	if hooks.afterSourceLstat != nil {
		if err := hooks.afterSourceLstat(root, baseName, before); err != nil {
			return nil, "", nil, err
		}
	}
	file, err := root.Open(baseName)
	if err != nil {
		return nil, "", nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, "", nil, err
	}
	after, err := root.Lstat(baseName)
	if err != nil || !after.Mode().IsRegular() || after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
		file.Close()
		return nil, "", nil, errors.New("source changed while it was opened")
	}
	return file, baseName, opened, nil
}

func (a *App) newMediaImportStage(spec mediaImportSpec, originalName string, hooks mediaImportHooks) (*mediaImportStage, error) {
	originalName, err := validateMediaImportFilename(originalName, spec)
	if err != nil {
		return nil, err
	}
	directoryRoot, err := openStableMediaDirectory(a.dataDir, spec.directory, true, hooks)
	if err != nil {
		return nil, err
	}

	stagedFile, err := createMediaStageFile(directoryRoot, hooks)
	if err != nil {
		directoryRoot.Close()
		return nil, err
	}
	return &mediaImportStage{
		root:         directoryRoot,
		spec:         spec,
		originalName: originalName,
		createdAt:    hooks.now(),
		hooks:        hooks,
		original:     stagedFile,
	}, nil
}

func openStableMediaDirectory(dataDirectory, relativeDirectory string, create bool, hooks mediaImportHooks) (*os.Root, error) {
	dataRoot, err := hooks.openRoot(dataDirectory)
	if err != nil {
		return nil, fmt.Errorf("open media data root: %w", err)
	}
	defer dataRoot.Close()
	rootPath := filepath.FromSlash(relativeDirectory)
	if create {
		if err := dataRoot.MkdirAll(rootPath, 0o755); err != nil {
			return nil, fmt.Errorf("prepare media directory: %w", err)
		}
	}
	if err := validateMediaDirectoryComponents(dataRoot, relativeDirectory); err != nil {
		return nil, err
	}
	directoryRoot, err := dataRoot.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open media directory: %w", err)
	}
	directoryInfo, err := directoryRoot.Stat(".")
	if err != nil || !directoryInfo.IsDir() {
		directoryRoot.Close()
		return nil, errors.New("media destination is not a directory")
	}
	if hooks.afterDestinationOpen != nil {
		if err := hooks.afterDestinationOpen(dataRoot, rootPath, directoryRoot); err != nil {
			directoryRoot.Close()
			return nil, err
		}
	}
	if err := validateMediaDirectoryComponents(dataRoot, relativeDirectory); err != nil {
		directoryRoot.Close()
		return nil, err
	}
	pathInfo, err := dataRoot.Lstat(rootPath)
	if err != nil || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(directoryInfo, pathInfo) {
		directoryRoot.Close()
		return nil, errors.New("media destination changed while it was opened")
	}
	return directoryRoot, nil
}

func validateMediaDirectoryComponents(root *os.Root, relativeDirectory string) error {
	current := ""
	for _, component := range strings.Split(filepath.ToSlash(relativeDirectory), "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("invalid media destination directory")
		}
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect media destination: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("media destination contains a symlink or non-directory component")
		}
	}
	return nil
}

func createMediaStageFile(root *os.Root, hooks mediaImportHooks) (*mediaImportStageFile, error) {
	for attempt := 0; attempt < 16; attempt++ {
		id, err := hooks.randomID()
		if err != nil {
			return nil, fmt.Errorf("generate media temp name: %w", err)
		}
		name := ".karte-media-import-" + id + ".tmp"
		file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("create media temp file: %w", err)
		}
		openedInfo, statErr := file.Stat()
		pathInfo, lstatErr := root.Lstat(name)
		if statErr != nil || lstatErr != nil || !openedInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, pathInfo) {
			file.Close()
			_ = root.Remove(name)
			return nil, errors.New("media temp path is not a stable regular file")
		}
		return &mediaImportStageFile{name: name, file: file}, nil
	}
	return nil, errors.New("could not allocate a unique media temp file")
}

func validateMediaImportFilename(filename string, spec mediaImportSpec) (string, error) {
	filename = strings.TrimSpace(filename)
	if filename == "" || strings.ContainsRune(filename, '\x00') || strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return "", errors.New("media filename is invalid")
	}
	if filename != filepath.Base(filename) || len(filename) > 255 {
		return "", errors.New("media filename is invalid")
	}
	extension := strings.ToLower(filepath.Ext(filename))
	if _, ok := spec.extensions[extension]; !ok {
		return "", fmt.Errorf("unsupported %s format: %s", spec.kind, extension)
	}
	return filename, nil
}

func (stage *mediaImportStage) copyFrom(ctx context.Context, reader io.Reader, limit int64) error {
	if stage == nil || stage.original == nil || stage.original.closed {
		return errMediaImportClosed
	}
	if ctx == nil {
		ctx = context.Background()
	}
	buffer := make([]byte, mediaImportCopyBufferBytes)
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		remaining := limit - stage.bytesWritten
		readBuffer := buffer
		if remaining < int64(len(readBuffer)) {
			readBuffer = readBuffer[:remaining+1]
		}
		read, readErr := reader.Read(readBuffer)
		if read > 0 {
			emptyReads = 0
			if stage.bytesWritten+int64(read) > limit {
				return fmt.Errorf("%w: %s limit is %d bytes", errMediaImportTooLarge, stage.spec.kind, limit)
			}
			if err := stage.writeBytes(readBuffer[:read]); err != nil {
				return err
			}
		} else if readErr == nil {
			emptyReads++
			if emptyReads >= 100 {
				return io.ErrNoProgress
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return fmt.Errorf("read %s data: %w", stage.spec.kind, readErr)
		}
	}
}

func (stage *mediaImportStage) writeBytes(data []byte) error {
	if stage == nil || stage.original == nil || stage.original.closed {
		return errMediaImportClosed
	}
	for len(data) > 0 {
		written, err := stage.hooks.write(stage.original.file, data)
		if written < 0 || written > len(data) {
			return errors.New("invalid media temp write count")
		}
		if written > 0 {
			stage.bytesWritten += int64(written)
			data = data[written:]
		}
		if err != nil {
			return fmt.Errorf("write %s temp file: %w", stage.spec.kind, err)
		}
		if written == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (a *App) finishMediaImportStage(ctx context.Context, stage *mediaImportStage) (mediaImportResult, error) {
	if stage == nil || stage.bytesWritten == 0 {
		return mediaImportResult{}, errors.New("media data is empty")
	}
	if err := mediaImportContextError(ctx); err != nil {
		return mediaImportResult{}, err
	}
	var result mediaImportResult
	var err error
	switch stage.spec.kind {
	case mediaImportKindImage:
		result, err = stage.finishImage(ctx)
	case mediaImportKindAudio, mediaImportKindPDF, mediaImportKindCSV:
		result, err = stage.finishSingle(ctx)
	default:
		err = fmt.Errorf("unsupported media import kind %q", stage.spec.kind)
	}
	if err != nil {
		return mediaImportResult{}, err
	}
	if cleanupErr := stage.cleanup(); cleanupErr != nil {
		// The destination has already been durably published. A best-effort
		// temp cleanup failure must not suppress the post-publish event or make
		// the caller retry and create a duplicate import.
		a.logError(fmt.Sprintf("Media import temp cleanup failed after publish: %v", cleanupErr))
	}

	payload := map[string]interface{}{
		"path":          result.relativePath,
		"original_name": stage.originalName,
		"saved_name":    result.savedName,
	}
	switch stage.spec.kind {
	case mediaImportKindImage:
		payload["webp_path"] = result.relativePath
		payload["original_path"] = result.originalRel
		stage.hooks.emitEvent(a, "image-imported", payload)
		a.logInfo(fmt.Sprintf("Image imported: %s -> webp=%s (original=%s)", stage.originalName, result.relativePath, result.originalRel))
		stage.hooks.refreshImageGraph(a)
	case mediaImportKindAudio:
		stage.hooks.emitEvent(a, "audio-imported", payload)
		a.logInfo(fmt.Sprintf("Audio imported: %s -> %s", stage.originalName, result.relativePath))
		stage.hooks.startTranscription(a, result.absolutePath, result.relativePath)
	case mediaImportKindPDF:
		stage.hooks.emitEvent(a, "pdf-imported", payload)
		a.logInfo(fmt.Sprintf("PDF imported: %s -> %s", stage.originalName, result.relativePath))
	case mediaImportKindCSV:
		stage.hooks.emitEvent(a, "csv-imported", payload)
		a.logInfo(fmt.Sprintf("CSV imported: %s -> %s", stage.originalName, result.relativePath))
	}
	return result, nil
}

func (stage *mediaImportStage) finishSingle(ctx context.Context) (mediaImportResult, error) {
	extension := strings.ToLower(filepath.Ext(stage.originalName))
	if stage.spec.kind == mediaImportKindCSV {
		if err := validateStrictCSVImport(stage.original.file); err != nil {
			return mediaImportResult{}, fmt.Errorf("validate imported csv: %w", err)
		}
	} else {
		if err := validateMediaFileMagic(stage.original.file, extension, stage.spec.kind); err != nil {
			return mediaImportResult{}, err
		}
	}
	if err := mediaImportContextError(ctx); err != nil {
		return mediaImportResult{}, err
	}
	if err := stage.syncAndClose(stage.original); err != nil {
		return mediaImportResult{}, err
	}
	// Cancellation is observed immediately before the first no-replace link.
	// Once publication starts，the durable commit or rollback boundary is
	// completed without interruption.
	if err := mediaImportContextError(ctx); err != nil {
		return mediaImportResult{}, err
	}
	for index := 1; index <= mediaImportCollisionLimit; index++ {
		if err := mediaImportContextError(ctx); err != nil {
			return mediaImportResult{}, err
		}
		filename := stage.candidateName(index, false)
		if err := stage.hooks.link(stage.root, stage.original.name, filename); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return mediaImportResult{}, fmt.Errorf("publish %s without replacement: %w", stage.spec.kind, err)
		}
		if err := stage.hooks.syncRoot(stage.root); err != nil {
			rollbackErr := stage.hooks.remove(stage.root, filename)
			rollbackSyncErr := stage.hooks.syncRoot(stage.root)
			return mediaImportResult{}, errors.Join(fmt.Errorf("sync published %s: %w", stage.spec.kind, err), rollbackErr, rollbackSyncErr)
		}
		stage.published = true
		relativePath := filepath.ToSlash(filepath.Join(stage.spec.directory, filename))
		return mediaImportResult{
			relativePath: relativePath,
			absolutePath: filepath.Join(stage.root.Name(), filename),
			savedName:    filename,
		}, nil
	}
	return mediaImportResult{}, errors.New("could not allocate a unique media destination")
}

func (stage *mediaImportStage) finishImage(ctx context.Context) (mediaImportResult, error) {
	config, format, imageValue, err := stage.decodeBoundedImage(ctx)
	if err != nil {
		return mediaImportResult{}, err
	}
	_ = config
	if err := mediaImportContextError(ctx); err != nil {
		return mediaImportResult{}, err
	}
	imageValue = resizeImportedImage(imageValue)
	if err := mediaImportContextError(ctx); err != nil {
		return mediaImportResult{}, err
	}
	derived, err := createMediaStageFile(stage.root, stage.hooks)
	if err != nil {
		return mediaImportResult{}, err
	}
	stage.derived = derived
	writer := &limitedMediaStageWriter{
		stage: stage,
		file:  derived,
		limit: stage.spec.maxBytes,
		ctx:   ctx,
	}
	lossless := format == "png" || format == "gif"
	if err := stage.hooks.encodeWebP(writer, imageValue, lossless); err != nil {
		return mediaImportResult{}, fmt.Errorf("encode imported image as WebP: %w", err)
	}
	if err := mediaImportContextError(ctx); err != nil {
		return mediaImportResult{}, err
	}
	if err := stage.syncAndClose(stage.original); err != nil {
		return mediaImportResult{}, err
	}
	if err := stage.syncAndClose(stage.derived); err != nil {
		return mediaImportResult{}, err
	}
	// This is the final cancellation boundary before publishing the pair.
	if err := mediaImportContextError(ctx); err != nil {
		return mediaImportResult{}, err
	}

	for index := 1; index <= mediaImportCollisionLimit; index++ {
		if err := mediaImportContextError(ctx); err != nil {
			return mediaImportResult{}, err
		}
		originalName := stage.candidateName(index, true)
		webpName := stage.candidateName(index, false)
		if err := stage.hooks.link(stage.root, stage.original.name, originalName); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return mediaImportResult{}, fmt.Errorf("publish original image without replacement: %w", err)
		}
		if err := stage.hooks.link(stage.root, stage.derived.name, webpName); err != nil {
			removeErr := stage.hooks.remove(stage.root, originalName)
			rollbackSyncErr := stage.hooks.syncRoot(stage.root)
			if errors.Is(err, os.ErrExist) && removeErr == nil && rollbackSyncErr == nil {
				continue
			}
			return mediaImportResult{}, errors.Join(
				fmt.Errorf("publish WebP without replacement: %w", err),
				wrapMediaImportError("remove partially published original image", removeErr),
				wrapMediaImportError("sync image rollback", rollbackSyncErr),
			)
		}
		if err := stage.hooks.syncRoot(stage.root); err != nil {
			removeOriginalErr := stage.hooks.remove(stage.root, originalName)
			removeWebPErr := stage.hooks.remove(stage.root, webpName)
			rollbackSyncErr := stage.hooks.syncRoot(stage.root)
			return mediaImportResult{}, errors.Join(fmt.Errorf("sync published image pair: %w", err), removeOriginalErr, removeWebPErr, rollbackSyncErr)
		}
		stage.published = true
		return mediaImportResult{
			relativePath: filepath.ToSlash(filepath.Join(stage.spec.directory, webpName)),
			absolutePath: filepath.Join(stage.root.Name(), webpName),
			savedName:    webpName,
			originalRel:  filepath.ToSlash(filepath.Join(stage.spec.directory, originalName)),
		}, nil
	}
	return mediaImportResult{}, errors.New("could not allocate a unique image destination")
}

func (stage *mediaImportStage) decodeBoundedImage(ctx context.Context) (image.Config, string, image.Image, error) {
	return decodeBoundedMediaImage(ctx, stage.original.file, stage.originalName, stage.hooks)
}

func decodeBoundedMediaImage(ctx context.Context, file *os.File, originalName string, hooks mediaImportHooks) (image.Config, string, image.Image, error) {
	if err := mediaImportContextError(ctx); err != nil {
		return image.Config{}, "", nil, err
	}
	if file == nil {
		return image.Config{}, "", nil, errors.New("image file is nil")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return image.Config{}, "", nil, err
	}
	config, format, err := hooks.decodeConfig(&mediaImportContextReader{ctx: ctx, reader: file})
	if err != nil {
		return image.Config{}, "", nil, fmt.Errorf("decode image configuration: %w", err)
	}
	if err := validateImportedImageDimensions(config.Width, config.Height); err != nil {
		return image.Config{}, "", nil, err
	}
	if err := mediaImportContextError(ctx); err != nil {
		return image.Config{}, "", nil, err
	}
	if !imageFormatMatchesExtension(format, strings.ToLower(filepath.Ext(originalName))) {
		return image.Config{}, "", nil, fmt.Errorf("image format %q does not match extension %s", format, filepath.Ext(originalName))
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return image.Config{}, "", nil, err
	}
	decoded, decodedFormat, err := hooks.decodeImage(&mediaImportContextReader{ctx: ctx, reader: file})
	if err != nil {
		return image.Config{}, "", nil, fmt.Errorf("decode imported image: %w", err)
	}
	if decodedFormat != format {
		return image.Config{}, "", nil, errors.New("image format changed between configuration and decode")
	}
	if err := mediaImportContextError(ctx); err != nil {
		return image.Config{}, "", nil, err
	}
	bounds := decoded.Bounds()
	if err := validateImportedImageDimensions(bounds.Dx(), bounds.Dy()); err != nil {
		return image.Config{}, "", nil, err
	}
	return config, format, decoded, nil
}

func validateImportedImageDimensions(width, height int) error {
	if width <= 0 || height <= 0 || width > mediaImportMaxDimension || height > mediaImportMaxDimension {
		return fmt.Errorf("image dimensions %dx%d exceed the %d dimension limit", width, height, mediaImportMaxDimension)
	}
	if int64(width) > mediaImportMaxPixels/int64(height) {
		return fmt.Errorf("image dimensions %dx%d exceed the %d pixel limit", width, height, mediaImportMaxPixels)
	}
	return nil
}

func imageFormatMatchesExtension(format, extension string) bool {
	switch strings.ToLower(extension) {
	case ".jpg", ".jpeg":
		return format == "jpeg"
	case ".png":
		return format == "png"
	case ".gif":
		return format == "gif"
	case ".webp":
		return format == "webp"
	default:
		return false
	}
}

func resizeImportedImage(source image.Image) image.Image {
	bounds := source.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	newWidth, newHeight := importedImageTargetDimensions(width, height)
	if newWidth == width && newHeight == height {
		return source
	}
	resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))
	draw.CatmullRom.Scale(resized, resized.Bounds(), source, bounds, draw.Src, nil)
	return resized
}

func importedImageTargetDimensions(width, height int) (int, int) {
	pixels := int64(width) * int64(height)
	scale := 1.0
	if longest := maxInt(width, height); longest > mediaImportTargetLongEdge {
		scale = float64(mediaImportTargetLongEdge) / float64(longest)
	}
	if pixels > mediaImportTargetPixels {
		pixelScale := math.Sqrt(float64(mediaImportTargetPixels) / float64(pixels))
		if pixelScale < scale {
			scale = pixelScale
		}
	}
	if scale >= 1 {
		return width, height
	}
	newWidth := maxInt(1, int(math.Floor(float64(width)*scale)))
	newHeight := maxInt(1, int(math.Floor(float64(height)*scale)))
	return newWidth, newHeight
}

type limitedMediaStageWriter struct {
	stage   *mediaImportStage
	file    *mediaImportStageFile
	written int64
	limit   int64
	ctx     context.Context
}

func (writer *limitedMediaStageWriter) Write(data []byte) (int, error) {
	if err := mediaImportContextError(writer.ctx); err != nil {
		return 0, err
	}
	if writer.written+int64(len(data)) > writer.limit {
		return 0, errMediaImportTooLarge
	}
	written, err := writer.stage.hooks.write(writer.file.file, data)
	if written > 0 {
		writer.written += int64(written)
	}
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	return written, err
}

type mediaImportContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *mediaImportContextReader) Read(data []byte) (int, error) {
	if err := mediaImportContextError(reader.ctx); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}

func mediaImportContextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

func (stage *mediaImportStage) syncAndClose(file *mediaImportStageFile) error {
	if file == nil || file.closed {
		return nil
	}
	if err := stage.hooks.chmod(file.file, 0o644); err != nil {
		return fmt.Errorf("set %s file permissions: %w", stage.spec.kind, err)
	}
	if err := stage.hooks.sync(file.file); err != nil {
		return fmt.Errorf("sync %s temp file: %w", stage.spec.kind, err)
	}
	closeErr := stage.hooks.close(file.file)
	file.closed = true
	if closeErr != nil {
		_ = file.file.Close()
		return fmt.Errorf("close %s temp file: %w", stage.spec.kind, closeErr)
	}
	return nil
}

func (stage *mediaImportStage) candidateName(index int, originalImage bool) string {
	extension := strings.ToLower(filepath.Ext(stage.originalName))
	originalBase := strings.TrimSuffix(stage.originalName, filepath.Ext(stage.originalName))
	base := audio.SanitizeFileName(originalBase)
	if strings.TrimSpace(originalBase) == "" {
		switch stage.spec.kind {
		case mediaImportKindImage:
			base = "image"
		case mediaImportKindPDF:
			base = "document"
		case mediaImportKindCSV:
			base = "data"
		default:
			base = "audio"
		}
	}
	if stage.spec.kind == mediaImportKindImage {
		base = sanitizeImageBaseName(originalBase)
		base = stage.createdAt.Format("20060102-150405") + "_" + base
		if index > 1 {
			base = fmt.Sprintf("%s_%02d", base, index)
		}
		if originalImage {
			if extension == ".webp" {
				return base + "_source.webp"
			}
			return base + extension
		}
		return base + ".webp"
	}
	if stage.spec.kind == mediaImportKindAudio {
		base = stage.createdAt.Format("20060102-150405") + "_" + base
		if index > 1 {
			base = fmt.Sprintf("%s_%02d", base, index)
		}
		return base + extension
	}
	if index > 1 {
		base = fmt.Sprintf("%s_%02d", base, index-1)
	}
	return base + extension
}

func (stage *mediaImportStage) cleanup() error {
	if stage == nil || stage.closed {
		return nil
	}
	var cleanupErrors []error
	for _, file := range []*mediaImportStageFile{stage.original, stage.derived} {
		if file == nil {
			continue
		}
		if !file.closed {
			if err := stage.hooks.close(file.file); err != nil && !errors.Is(err, os.ErrClosed) {
				cleanupErrors = append(cleanupErrors, err)
				_ = file.file.Close()
			}
			file.closed = true
		}
		if err := stage.hooks.remove(stage.root, file.name); err != nil && !errors.Is(err, os.ErrNotExist) {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	if err := stage.root.Close(); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	stage.closed = true
	return errors.Join(cleanupErrors...)
}

func (stage *mediaImportStage) abort() {
	_ = stage.cleanup()
}

func (a *App) BeginMediaImport(kind, filename string, declaredSize int64) (MediaImportSession, error) {
	ctx, release, admitted := a.lifecycle.acquireMediaImportOperation()
	if !admitted {
		return MediaImportSession{}, errMediaImportClosed
	}
	defer release()
	config, hooks := a.mediaImportSettings()
	spec, err := mediaImportSpecForKind(kind, config)
	if err != nil {
		return MediaImportSession{}, err
	}
	if declaredSize <= 0 || declaredSize > spec.maxBytes {
		return MediaImportSession{}, fmt.Errorf("declared %s size %d exceeds limit %d", kind, declaredSize, spec.maxBytes)
	}
	if !a.ensureMediaImportWatcher(config.SessionTTL) {
		return MediaImportSession{}, errMediaImportClosed
	}
	a.expireMediaImportSessions(hooks.now())
	a.mediaImports.mu.Lock()
	if len(a.mediaImports.sessions)+a.mediaImports.creating >= config.MaxSessions {
		a.mediaImports.mu.Unlock()
		return MediaImportSession{}, fmt.Errorf("at most %d media imports may be active", config.MaxSessions)
	}
	a.mediaImports.creating++
	a.mediaImports.mu.Unlock()
	reserved := true
	defer func() {
		if reserved {
			a.mediaImports.mu.Lock()
			a.mediaImports.creating--
			a.mediaImports.mu.Unlock()
		}
	}()

	stage, err := a.newMediaImportStage(spec, filename, hooks)
	if err != nil {
		return MediaImportSession{}, err
	}
	if err := ctx.Err(); err != nil {
		stage.abort()
		return MediaImportSession{}, err
	}
	sessionID, err := hooks.randomID()
	if err != nil {
		stage.abort()
		return MediaImportSession{}, err
	}
	session := &mediaImportSessionState{
		stage:        stage,
		declaredSize: declaredSize,
		lastTouched:  hooks.now(),
	}

	a.mediaImports.mu.Lock()
	a.mediaImports.creating--
	reserved = false
	if a.mediaImports.sessions == nil {
		a.mediaImports.sessions = make(map[string]*mediaImportSessionState)
	}
	if len(a.mediaImports.sessions) >= config.MaxSessions {
		a.mediaImports.mu.Unlock()
		stage.abort()
		return MediaImportSession{}, fmt.Errorf("at most %d media imports may be active", config.MaxSessions)
	}
	if _, exists := a.mediaImports.sessions[sessionID]; exists {
		a.mediaImports.mu.Unlock()
		stage.abort()
		return MediaImportSession{}, errors.New("media import session ID collision")
	}
	a.mediaImports.sessions[sessionID] = session
	a.mediaImports.mu.Unlock()
	return MediaImportSession{ID: sessionID, ChunkSize: mediaImportChunkBytes, MaxBytes: spec.maxBytes}, nil
}

func (a *App) ensureMediaImportWatcher(ttl time.Duration) bool {
	a.mediaImports.mu.Lock()
	defer a.mediaImports.mu.Unlock()
	if a.mediaImports.watcher {
		return true
	}
	a.mediaImports.watcher = true
	if !a.lifecycle.goWorker(func(ctx context.Context) {
		a.runMediaImportWatcher(ctx, ttl)
	}) {
		a.mediaImports.watcher = false
		return false
	}
	return true
}

func (a *App) AppendMediaImportChunk(sessionID string, expectedOffset int64, encodedChunk string) (int64, error) {
	session := a.lookupMediaImportSession(sessionID)
	if session == nil {
		return 0, errMediaImportClosed
	}
	if session.stage != nil && session.stage.hooks.afterSessionLookup != nil {
		session.stage.hooks.afterSessionLookup()
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || session.stage == nil {
		return 0, errMediaImportClosed
	}
	if err := a.lifecycle.context().Err(); err != nil {
		return session.stage.bytesWritten, err
	}
	if expectedOffset != session.stage.bytesWritten {
		return session.stage.bytesWritten, fmt.Errorf("media chunk offset %d does not match expected %d", expectedOffset, session.stage.bytesWritten)
	}
	decoded, err := decodeStrictMediaChunk(encodedChunk)
	if err != nil {
		return session.stage.bytesWritten, err
	}
	if session.stage.bytesWritten+int64(len(decoded)) > session.stage.spec.maxBytes ||
		session.stage.bytesWritten+int64(len(decoded)) > session.declaredSize {
		return session.stage.bytesWritten, errMediaImportTooLarge
	}
	if err := session.stage.writeBytes(decoded); err != nil {
		return session.stage.bytesWritten, err
	}
	session.lastTouched = session.stage.hooks.now()
	return session.stage.bytesWritten, nil
}

func (a *App) FinishMediaImport(sessionID string) (string, error) {
	ctx, release, admitted := a.lifecycle.acquireMediaImportOperation()
	if !admitted {
		return "", errMediaImportClosed
	}
	defer release()
	session := a.takeMediaImportSession(sessionID)
	if session == nil {
		return "", errMediaImportClosed
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || session.stage == nil {
		return "", errMediaImportClosed
	}
	session.closed = true
	stage := session.stage
	defer stage.abort()
	if stage.bytesWritten != session.declaredSize {
		return "", fmt.Errorf("received %d bytes，declared %d", stage.bytesWritten, session.declaredSize)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	result, err := a.finishMediaImportStage(ctx, stage)
	if err != nil {
		return "", err
	}
	return result.relativePath, nil
}

func (a *App) AbortMediaImport(sessionID string) error {
	session := a.takeMediaImportSession(sessionID)
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return nil
	}
	session.closed = true
	return session.stage.cleanup()
}

func (a *App) lookupMediaImportSession(sessionID string) *mediaImportSessionState {
	a.mediaImports.mu.Lock()
	defer a.mediaImports.mu.Unlock()
	return a.mediaImports.sessions[sessionID]
}

func (a *App) takeMediaImportSession(sessionID string) *mediaImportSessionState {
	a.mediaImports.mu.Lock()
	defer a.mediaImports.mu.Unlock()
	session := a.mediaImports.sessions[sessionID]
	delete(a.mediaImports.sessions, sessionID)
	return session
}

func (a *App) expireMediaImportSessions(now time.Time) {
	config, _ := a.mediaImportSettings()
	var expired []*mediaImportStage
	a.mediaImports.mu.Lock()
	for id, session := range a.mediaImports.sessions {
		session.mu.Lock()
		stale := !session.closed && now.Sub(session.lastTouched) >= config.SessionTTL
		if stale {
			session.closed = true
			expired = append(expired, session.stage)
			delete(a.mediaImports.sessions, id)
		}
		session.mu.Unlock()
	}
	a.mediaImports.mu.Unlock()
	for _, stage := range expired {
		if stage != nil {
			_ = stage.cleanup()
		}
	}
}

func (a *App) runMediaImportWatcher(ctx context.Context, ttl time.Duration) {
	interval := ttl / 4
	if interval > time.Minute {
		interval = time.Minute
	}
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer func() {
		a.mediaImports.mu.Lock()
		a.mediaImports.watcher = false
		a.mediaImports.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			a.abortAllMediaImports()
			return
		case now := <-ticker.C:
			a.expireMediaImportSessions(now)
		}
	}
}

func (a *App) abortAllMediaImports() {
	a.mediaImports.mu.Lock()
	stages := make([]*mediaImportStage, 0, len(a.mediaImports.sessions))
	for id, session := range a.mediaImports.sessions {
		session.mu.Lock()
		if !session.closed {
			session.closed = true
			stages = append(stages, session.stage)
		}
		session.mu.Unlock()
		delete(a.mediaImports.sessions, id)
	}
	a.mediaImports.mu.Unlock()
	for _, stage := range stages {
		if stage != nil {
			_ = stage.cleanup()
		}
	}
}

func decodeStrictMediaChunk(encoded string) ([]byte, error) {
	if encoded == "" {
		return nil, errors.New("media chunk is empty")
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(mediaImportChunkBytes) {
		return nil, errors.New("media chunk exceeds 256 KiB")
	}
	if err := validateStrictBase64(encoded, mediaImportChunkBytes); err != nil {
		return nil, err
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(encoded)))
	written, err := base64.StdEncoding.Strict().Decode(decoded, []byte(encoded))
	if err != nil {
		return nil, fmt.Errorf("decode media chunk: %w", err)
	}
	decoded = decoded[:written]
	if len(decoded) > mediaImportChunkBytes {
		return nil, errors.New("media chunk exceeds 256 KiB")
	}
	return decoded, nil
}

func validateStrictBase64(encoded string, maxDecoded int64) error {
	return validateStrictBase64Context(context.Background(), encoded, maxDecoded)
}

func validateStrictBase64Context(ctx context.Context, encoded string, maxDecoded int64) error {
	if encoded == "" || len(encoded)%4 != 0 {
		return errors.New("invalid Base64 length")
	}
	maxEncoded := ((maxDecoded + 2) / 3) * 4
	if int64(len(encoded)) > maxEncoded {
		return errMediaImportTooLarge
	}
	paddingStarted := false
	paddingCount := 0
	for index := 0; index < len(encoded); index++ {
		if index&((64*1024)-1) == 0 {
			if err := mediaImportContextError(ctx); err != nil {
				return err
			}
		}
		char := encoded[index]
		switch {
		case char >= 'A' && char <= 'Z', char >= 'a' && char <= 'z', char >= '0' && char <= '9', char == '+', char == '/':
			if paddingStarted {
				return errors.New("invalid Base64 padding")
			}
		case char == '=':
			paddingStarted = true
			paddingCount++
			if paddingCount > 2 {
				return errors.New("invalid Base64 padding")
			}
		default:
			return errors.New("invalid Base64 character")
		}
	}
	return nil
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func wrapMediaImportError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
}
