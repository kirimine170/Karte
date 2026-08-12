package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"karte/internal/asr"
	"karte/internal/audio"
	boardpkg "karte/internal/board"
	"karte/internal/clip"
	"karte/internal/docid"
	fm "karte/internal/frontmatter"
	gitvcs "karte/internal/git"
	"karte/internal/markdown"
	"karte/internal/printout"
	"karte/internal/runtimepath"
	"karte/internal/screenshot"
	syncpkg "karte/internal/sync"
	"karte/internal/webpchunk"
	"karte/internal/webputil"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	karterenderer "github.com/kirimine170/KarteRenderer"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"golang.org/x/image/draw"
	"gopkg.in/yaml.v3"
)

var (
	supportedImageExt = map[string]struct{}{
		".jpg":  {},
		".jpeg": {},
		".png":  {},
		".gif":  {},
		".webp": {},
	}
	backupImageExtCandidates = []string{".jpg", ".jpeg", ".png", ".gif"}
)

//go:embed frontend/src/printout/generated/pagination-runtime.js
var kartePrintoutPaginationRuntime string

var exportRendererHTMLPDF = karterenderer.ExportHTMLPDF

const maxImageSizeForPDF = 10 * 1024 * 1024 // 10MB
const maxImageWidthForPDF = 800             // PDF表示用に最大横幅800px（Previewで開く速度を改善）
const maxImageFileSizeForPDF = 300 * 1024   // 各画像の目標サイズ300KB

// App struct
type App struct {
	ctx             context.Context
	root            string
	dataDir         string
	logFilePath     string
	fs              FileSystem
	syncManager     *syncpkg.SyncManager
	vcs             *gitvcs.VCS
	asrService      *asr.Service
	realtimeService *asr.RealtimeService // For real-time ASR with partial text support
	asrInitDone     chan struct{}
	// Recording fields
	recorder                *audio.Recorder
	recordingMu             sync.Mutex
	isRecording             bool
	recordingSamples        []float32 // Buffer for recording samples
	recordingVAD            *audio.SimpleVAD
	recordingSegment        *recordingSegment
	recordingWg             sync.WaitGroup // WaitGroup for recording processing goroutine
	recordingStopCh         chan struct{}  // Channel to stop recording processing
	recordingTranscriptPath string         // Path to transcript file (created at start)
	// NOTE: Multi-window support requires Wails v3 (currently in development)
	// Uncomment when upgrading to Wails v3:
	// presenter windows keyed by document id (e.g., "content/xxx.md")
	// presenters map[string]*Presenter

	// Window close control
	allowCloseMu   sync.Mutex
	allowCloseFlag bool

	webClipConversionMu      sync.Mutex
	webClipConversionQueue   []webClipConversionJob
	webClipConversionRunning bool
	webClipConversionClosing bool
}

type webClipConversionJob struct {
	MarkdownPath string
	AssetDir     string
}

type webClipImageMetadata struct {
	Schema     string                    `json:"schema"`
	Source     webClipMetadataSource     `json:"source"`
	Capture    webClipMetadataCapture    `json:"capture"`
	Relations  webClipMetadataRelations  `json:"relations"`
	Processing webClipMetadataProcessing `json:"processing"`
}

type webClipMetadataSource struct {
	Kind             string `json:"kind"`
	PageURL          string `json:"page_url"`
	ImageURL         string `json:"image_url"`
	ResolvedImageURL string `json:"resolved_image_url"`
	SiteName         string `json:"site_name"`
	PageTitle        string `json:"page_title"`
	HTMLAlt          string `json:"html_alt"`
	HTMLCaption      string `json:"html_caption"`
}

type webClipMetadataCapture struct {
	CapturedAt   string `json:"captured_at"`
	Method       string `json:"method"`
	HTTPStatus   int    `json:"http_status"`
	ContentType  string `json:"content_type"`
	ETag         string `json:"etag"`
	LastModified string `json:"last_modified"`
}

type webClipMetadataRelations struct {
	DocumentPath      string `json:"document_path"`
	MarkdownReference string `json:"markdown_reference"`
}

type webClipMetadataProcessing struct {
	OriginalFormat string `json:"original_format"`
	ConvertedTo    string `json:"converted_to"`
}

// NOTE: Multi-window support requires Wails v3 (currently in development)
// Uncomment when upgrading to Wails v3:
// Presenter window context
// type Presenter struct {
// 	win   runtime.Window
// 	ctx   context.Context
// 	docID string
// }

// logInfo writes info logs to both Wails runtime and app log file
func (a *App) logInfo(msg string) {
	if a.ctx != nil {
		runtime.LogInfo(a.ctx, msg)
	}
	a.appendLog("INFO", msg)
}

// logError writes error logs to both Wails runtime and app log file
func (a *App) logError(msg string) {
	if a.ctx != nil {
		runtime.LogError(a.ctx, msg)
	}
	a.appendLog("ERROR", msg)
}

// LogJS logs a message from JavaScript to the app log file
// This allows JavaScript code to write logs that appear in app.log
func (a *App) LogJS(level, msg string) {
	// Prepend [JS] prefix to distinguish from Go logs
	formattedMsg := fmt.Sprintf("[JS] %s", msg)
	switch strings.ToUpper(level) {
	case "ERROR", "ERR":
		runtime.LogError(a.ctx, formattedMsg)
		a.appendLog("ERROR", formattedMsg)
	case "WARN", "WARNING":
		runtime.LogWarning(a.ctx, formattedMsg)
		a.appendLog("WARN", formattedMsg)
	case "DEBUG":
		runtime.LogDebug(a.ctx, formattedMsg)
		a.appendLog("DEBUG", formattedMsg)
	default:
		// Default to INFO
		runtime.LogInfo(a.ctx, formattedMsg)
		a.appendLog("INFO", formattedMsg)
	}
}

func (a *App) appendLog(level, msg string) {
	if a.logFilePath == "" {
		return
	}
	// Prepend timestamp
	line := fmt.Sprintf("%s [%s] %s\n", time.Now().Format(time.RFC3339), level, msg)
	fs := a.fs
	if fs == nil {
		fs = OSFileSystem{}
	}
	f, err := fs.OpenFile(a.logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// fallback to std logger if file can't be opened
		log.Printf("log open error: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.Write([]byte(line))
}

// FileItem represents a markdown file in the content directory
type FileItem struct {
	Path       string    `json:"path"`
	Title      string    `json:"title"`
	ModTime    time.Time `json:"modTime"`
	SearchText string    `json:"searchText,omitempty"`
}

// ImageItem represents an image file in the gallery
type ImageItem struct {
	Path         string    `json:"path"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	ModTime      time.Time `json:"modTime"`
	MetadataPath string    `json:"metadataPath,omitempty"`
	OriginalPath string    `json:"originalPath,omitempty"`
}

// CSVItem represents a CSV file in the gallery
type CSVItem struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

func (a *App) LoadBoard(path string) (*boardpkg.Document, error) {
	absPath, ok := a.resolveContentPath(path)
	if !ok {
		return nil, fmt.Errorf("invalid path: %s", path)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read board file: %w", err)
	}

	doc, err := boardpkg.Parse(path, string(content))
	if err != nil {
		return nil, err
	}

	if doc.DocID == "" {
		contentWithDocID, docID, ensureErr := a.ensureDocID(string(content))
		if ensureErr == nil && docID != "" {
			doc.DocID = docID
			doc.RawContent = contentWithDocID
			_ = os.WriteFile(absPath, []byte(contentWithDocID), 0o644)
		}
	}

	return doc, nil
}

func (a *App) SaveBoard(path string, doc boardpkg.Document) (*boardpkg.Document, error) {
	doc.Path = path
	return a.saveBoardDocument(&doc)
}

func (a *App) CreateBoardForResource(path string) (*boardpkg.Document, error) {
	resourcePath := filepath.ToSlash(strings.TrimSpace(path))
	if resourcePath == "" {
		return nil, fmt.Errorf("resource path is required")
	}

	boardPath := boardPathForResource(resourcePath)
	if strings.HasSuffix(strings.ToLower(resourcePath), ".board.md") {
		boardPath = resourcePath
	}

	if absPath, ok := a.resolveContentPath(boardPath); ok {
		if _, err := os.Stat(absPath); err == nil {
			return a.LoadBoard(boardPath)
		}
	}

	now := time.Now().Format("2006-01-02")
	doc := &boardpkg.Document{
		Path:    boardPath,
		Title:   strings.TrimSuffix(filepath.Base(boardPath), ".board.md"),
		Type:    boardpkg.BoardType,
		Version: 1,
		Created: now,
		Updated: now,
		Tags:    []string{"corkboard"},
		Cards:   []boardpkg.Card{},
		Edges:   []boardpkg.Edge{},
		Layout: boardpkg.Layout{
			Cards: map[string]boardpkg.CardLayout{},
			Viewport: boardpkg.Viewport{
				X:    0,
				Y:    0,
				Zoom: 1,
			},
		},
	}

	if !strings.HasSuffix(strings.ToLower(resourcePath), ".board.md") {
		doc.Cards = append(doc.Cards, boardpkg.Card{
			ID:        "card:resource-001",
			Type:      "resource",
			Title:     resourceCardTitle(resourcePath),
			Source:    resourcePath,
			CreatedBy: "user",
			Body:      fmt.Sprintf("Linked resource: %s", resourcePath),
		})
		doc.Layout.Cards["card:resource-001"] = boardpkg.CardLayout{
			X:      120,
			Y:      80,
			Width:  320,
			Height: 180,
		}
	}

	return a.saveBoardDocument(doc)
}

func (a *App) GetBoardResourceCandidates(boardPath string) []FileItem {
	files := a.GetFileList()
	result := make([]FileItem, 0, len(files))
	for _, file := range files {
		if file.Path == boardPath {
			continue
		}
		result = append(result, file)
	}
	return result
}

// CaptureScreenInteractive captures a screenshot using the platform-specific
// implementation and stores it under karte_data/data/image as a WebP file.
// It returns the image path relative to dataDir (e.g. "data/image/xxx.webp").
func (a *App) CaptureScreenInteractive() (string, error) {
	if a == nil {
		return "", fmt.Errorf("app is not initialized")
	}
	if a.dataDir == "" {
		return "", fmt.Errorf("dataDir is not initialized")
	}

	a.logInfo("CaptureScreenInteractive: start")
	path, err := screenshot.CaptureScreenInteractive(a.dataDir)
	if err != nil {
		a.logError(fmt.Sprintf("CaptureScreenInteractive failed: %v", err))
		return "", err
	}

	a.logInfo(fmt.Sprintf("CaptureScreenInteractive: saved screenshot to %s", path))
	return path, nil
}

// GraphNode represents a node in the graph
type GraphNode struct {
	ID     string   `json:"id"`              // Path-based ID (e.g., "doc:/path/to/file.md")
	DocID  string   `json:"docId,omitempty"` // Document ID (logical identifier, persistent across renames)
	Label  string   `json:"label"`
	Kind   string   `json:"kind"`
	Exists bool     `json:"exists"`
	DegIn  int      `json:"degIn"`
	DegOut int      `json:"degOut"`
	Tags   []string `json:"tags"`
	Hash   string   `json:"hash,omitempty"` // SHA256 hash of the file content
}

// GraphEdge represents an edge in the graph
type GraphEdge struct {
	ID            string `json:"id"`
	Source        string `json:"source"`                // Path-based source ID (backward compatibility)
	Target        string `json:"target"`                // Path-based target ID (backward compatibility)
	SourceDocID   string `json:"sourceDocId,omitempty"` // Document ID of source (logical identifier)
	TargetDocID   string `json:"targetDocId,omitempty"` // Document ID of target (logical identifier)
	Kind          string `json:"kind"`
	Weight        int    `json:"weight"`
	TargetHash    string `json:"targetHash,omitempty"`    // Hash of target file when link was created
	SourceHash    string `json:"sourceHash,omitempty"`    // Hash of source file when link was created
	LinkVersion   int    `json:"linkVersion,omitempty"`   // Version number when link was created
	TargetUpdated bool   `json:"targetUpdated,omitempty"` // True if target file has been updated since link creation
	ToVersionMode string `json:"toVersionMode,omitempty"` // "latest" or "pinned" (for future version management)
	ToVersionID   string `json:"toVersionId,omitempty"`   // Version ID when pinned (content_hash)
}

// GraphData represents the complete graph structure
type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	Meta  GraphMeta   `json:"meta"`
}

// GraphMeta contains metadata about the graph
type GraphMeta struct {
	Directed bool `json:"directed"`
}

func graphNodeKindForPath(path string) string {
	if strings.HasSuffix(strings.ToLower(filepath.ToSlash(path)), ".board.md") {
		return "board"
	}
	return "note"
}

func graphNodeDefaultTitleForPath(path string) string {
	base := filepath.Base(path)
	lower := strings.ToLower(base)
	if strings.HasSuffix(lower, ".board.md") {
		return strings.TrimSuffix(base, ".board.md")
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// FileSystem abstracts file operations for easier testing.
type FileSystem interface {
	MkdirAll(path string, perm fs.FileMode) error
	Stat(name string) (fs.FileInfo, error)
	WriteFile(name string, data []byte, perm fs.FileMode) error
	OpenFile(name string, flag int, perm fs.FileMode) (io.WriteCloser, error)
}

// OSFileSystem provides FileSystem backed by the os package.
type OSFileSystem struct{}

func (OSFileSystem) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (OSFileSystem) Stat(name string) (fs.FileInfo, error) {
	return os.Stat(name)
}

func (OSFileSystem) WriteFile(name string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (OSFileSystem) OpenFile(name string, flag int, perm fs.FileMode) (io.WriteCloser, error) {
	return os.OpenFile(name, flag, perm)
}

// NewApp creates a new App application struct with default dependencies.
func NewApp() *App {
	return NewAppWithFileSystem(OSFileSystem{})
}

// NewAppWithFileSystem creates a new App with the provided FileSystem.
func NewAppWithFileSystem(fs FileSystem) *App {
	return &App{fs: fs}
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// NOTE: Multi-window support requires Wails v3 (currently in development)
	// Uncomment when upgrading to Wails v3:
	// if a.presenters == nil {
	// 	a.presenters = make(map[string]*Presenter)
	// }
	// Determine base directory from executable location
	exePath, err := os.Executable()
	if err != nil {
		runtime.LogError(ctx, fmt.Sprintf("Failed to get executable path: %v", err))
		return
	}
	exeDir := filepath.Dir(exePath)

	// If running inside a macOS .app bundle, place data next to the app bundle
	// exeDir: .../Karte.app/Contents/MacOS
	// appBundleDir := .../Karte.app, appPlacedDir := parent directory of app bundle
	appPlacedDir := exeDir
	// Detect .app bundle structure
	if strings.HasSuffix(filepath.ToSlash(exeDir), "/Contents/MacOS") {
		contentsDir := filepath.Dir(exeDir)       // .../Contents
		appBundleDir := filepath.Dir(contentsDir) // .../Karte.app
		appPlacedDir = filepath.Dir(appBundleDir) // directory containing Karte.app
	}

	a.root = appPlacedDir

	// Allow explicitly overriding the runtime data directory for local development.
	if override := strings.TrimSpace(os.Getenv("KARTE_DATA_DIR")); override != "" {
		if absOverride, err := filepath.Abs(override); err == nil {
			a.root = filepath.Dir(absOverride)
			a.dataDir = absOverride
			a.logInfo(fmt.Sprintf("Using KARTE_DATA_DIR override: %s", a.dataDir))
		} else {
			a.logError(fmt.Sprintf("Invalid KARTE_DATA_DIR %q: %v", override, err))
			return
		}
	} else {
		// When running `wails dev`, the generated app lives under build/bin.
		// In that case prefer the repo-local karte_data so development uses real project data.
		if cwd, err := os.Getwd(); err == nil {
			devDataDir := filepath.Join(cwd, "karte_data")
			if strings.HasSuffix(filepath.ToSlash(appPlacedDir), "/build/bin") {
				if info, statErr := os.Stat(devDataDir); statErr == nil && info.IsDir() {
					a.root = cwd
					a.dataDir = devDataDir
					a.logInfo(fmt.Sprintf("Using development karte_data: %s", a.dataDir))
				}
			}
		}
	}

	// Initialize the runtime data directory unless it was explicitly overridden
	// or resolved to the development workspace above.
	if a.dataDir == "" {
		defaultRoot, defaultDataDir, pathErr := runtimepath.DefaultDataDir(
			goruntime.GOOS,
			appPlacedDir,
			os.Getenv("LOCALAPPDATA"),
		)
		if pathErr != nil {
			runtime.LogError(ctx, fmt.Sprintf("Failed to resolve data directory: %v", pathErr))
			return
		}
		a.root = defaultRoot
		a.dataDir = defaultDataDir

		if goruntime.GOOS == "windows" {
			legacyDataDir := filepath.Join(appPlacedDir, "karte_data")
			migrated, migrationErr := runtimepath.MigrateLegacyDataDir(legacyDataDir, a.dataDir)
			if migrationErr != nil {
				runtime.LogError(ctx, fmt.Sprintf("Failed to migrate legacy data directory: %v", migrationErr))
				return
			}
			if migrated {
				a.logInfo(fmt.Sprintf("Copied legacy data directory from %s to %s; source was preserved", legacyDataDir, a.dataDir))
			}
		}
	}
	if err := a.initializeDataDirectory(); err != nil {
		runtime.LogError(ctx, fmt.Sprintf("Failed to initialize data directory: %v", err))
		return
	}

	a.asrInitDone = make(chan struct{})
	go func() {
		if err := a.initASRService(); err != nil {
			runtime.LogError(ctx, fmt.Sprintf("Failed to initialize ASR service: %v", err))
		} else if a.asrService != nil {
			a.logInfo("ASR service initialized")
		}
		close(a.asrInitDone)
	}()

	a.logInfo(fmt.Sprintf("Karte started. root=%s dataDir=%s exeDir=%s", a.root, a.dataDir, exeDir))

	// Initialize sync manager (disabled for now - will be implemented with git integration)
	// a.syncManager = syncpkg.NewSyncManager(ctx, a.root)
	// if err := a.syncManager.Start(); err != nil {
	// 	runtime.LogError(ctx, fmt.Sprintf("Failed to start sync manager: %v", err))
	// }
}

// shutdown is invoked by Wails when the app is closing.
func (a *App) shutdown(ctx context.Context) {
	a.webClipConversionMu.Lock()
	a.webClipConversionClosing = true
	a.webClipConversionMu.Unlock()

	if a.asrService != nil {
		a.asrService.Close()
		a.asrService = nil
	}
	// Cleanup recording if active
	if a.isRecording {
		a.cleanupRecording()
	}
}

// initializeDataDirectory creates and initializes the karte_data directory structure
func (a *App) initializeDataDirectory() error {
	fsys := a.fs
	if fsys == nil {
		fsys = OSFileSystem{}
	}
	// Ensure base directory exists
	if err := fsys.MkdirAll(a.dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %v", err)
	}

	// Try to overlay template contents (if available)
	if templatePath := a.findTemplatePath(); templatePath != "" {
		if err := a.copyTemplateToDataDir(templatePath); err != nil {
			a.logError(fmt.Sprintf("Failed to overlay template %s: %v", templatePath, err))
		} else {
			a.logInfo(fmt.Sprintf("Overlaid karte_data_template from %s", templatePath))
		}
	}

	// Create subdirectories
	subdirs := []string{
		"content",
		filepath.Join("content", "transcripts"),
		"data",
		filepath.Join("data", "audio"),
		filepath.Join("data", "image"),
		filepath.Join("data", "csv"),
		filepath.Join("data", "asr"),
		"themes",
		"public",
		".mdsys",
	}
	for _, subdir := range subdirs {
		dirPath := filepath.Join(a.dataDir, subdir)
		if err := fsys.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Errorf("failed to create subdirectory %s: %v", subdir, err)
		}
	}

	// Create default theme directory
	themeDir := filepath.Join(a.dataDir, "themes", "default")
	if err := fsys.MkdirAll(themeDir, 0755); err != nil {
		return fmt.Errorf("failed to create theme directory: %v", err)
	}

	// Create log directory
	logDir := filepath.Join(a.dataDir, "log")
	if err := fsys.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %v", err)
	}
	a.logFilePath = filepath.Join(logDir, "app.log")

	// Create default files if they don't exist
	defaultFiles := map[string]string{
		filepath.Join(a.dataDir, "content", "README.md"): "# Welcome to Karte\n\nThis is your first document. Start writing!",
		filepath.Join(a…49990 tokens truncated…ervice for partial text (if available)
			if a.realtimeService != nil {
				// Process audio chunks with RealtimeService
				for i := 0; i < len(newSamples); i += chunkSize {
					end := i + chunkSize
					if end > len(newSamples) {
						end = len(newSamples)
					}
					chunk := newSamples[i:end]

					// Process with RealtimeService to get partial/final text
					partialText, finalText, isFinal := a.realtimeService.ProcessAudio(chunk)

					// Emit partial text event if text changed and write to file
					if partialText != "" {
						// Get current timestamp
						currentSampleIndex := processedSamples + i
						timestamp := float64(currentSampleIndex) / float64(audio.RecordingSampleRate)
						var transcriptPath string
						func() {
							a.recordingMu.Lock()
							defer a.recordingMu.Unlock()
							transcriptPath = a.recordingTranscriptPath
						}()
						runtime.EventsEmit(a.ctx, "recording-transcript-partial", map[string]interface{}{
							"text":           partialText,
							"timestamp":      timestamp,
							"transcriptPath": transcriptPath,
						})

						// Write partial text to transcript file
						if transcriptPath != "" {
							a.appendTranscriptPartial(transcriptPath, partialText)
						}
					}

					// Emit final text event if endpoint reached
					if isFinal && finalText != "" {
						currentSampleIndex := processedSamples + i
						timestamp := float64(currentSampleIndex) / float64(audio.RecordingSampleRate)
						*segmentIndex++
						var transcriptPath string
						func() {
							a.recordingMu.Lock()
							defer a.recordingMu.Unlock()
							transcriptPath = a.recordingTranscriptPath
						}()
						runtime.EventsEmit(a.ctx, "recording-transcript-final", map[string]interface{}{
							"text":           finalText,
							"segmentIndex":   *segmentIndex,
							"timestamp":      timestamp,
							"transcriptPath": transcriptPath,
						})
						a.logInfo(fmt.Sprintf("[Recording] Final transcript segment %d: %s (timestamp: %.2f)", *segmentIndex, finalText, timestamp))

						// Append to transcript file
						if transcriptPath != "" {
							minutes := int(timestamp) / 60
							seconds := int(timestamp) % 60
							timestampedLine := fmt.Sprintf("**%02d:%02d** %s", minutes, seconds, finalText)
							a.appendTranscriptLine(transcriptPath, timestampedLine)
							a.logInfo(fmt.Sprintf("[Recording] Appended transcript segment %d to file", *segmentIndex))
						}
					}
				}
			}

			// Process in chunks (for VAD and segment detection - keep existing logic)
			for i := 0; i < len(newSamples); i += chunkSize {
				// Check stop signal periodically during processing
				select {
				case <-a.recordingStopCh:
					a.logInfo("[Recording] Processing goroutine received stop signal during chunk processing")
					ticker.Stop()

					// Get segment data without holding lock for long
					var seg *recordingSegment
					var startIdx int
					var samplesCopy []float32
					func() {
						a.recordingMu.Lock()
						defer a.recordingMu.Unlock()
						if a.recordingSegment != nil && len(a.recordingSegment.samples) > 0 {
							seg = a.recordingSegment
							startIdx = seg.startSampleIndex
							samplesCopy = make([]float32, len(seg.samples))
							copy(samplesCopy, seg.samples)
							a.recordingSegment = nil
						}
					}()

					// Only process if segment is long enough (minimum 0.1 seconds = 1600 samples)
					if seg != nil && len(samplesCopy) > 0 {
						minSamples := 1600 // 0.1 seconds at 16kHz
						if len(samplesCopy) >= minSamples {
							go func() {
								a.finalizeRecordingSegment(segmentIndex, startIdx, samplesCopy)
							}()
						} else {
							a.logInfo(fmt.Sprintf("[Recording] Skipping segment (too short: %d samples, minimum: %d)", len(samplesCopy), minSamples))
						}
					}
					a.logInfo("[Recording] Processing goroutine exiting from chunk processing")
					return
				default:
					// Continue processing
				}

				end := i + chunkSize
				if end > len(newSamples) {
					end = len(newSamples)
				}
				chunk := newSamples[i:end]

				// Use VAD to detect speech
				isSpeech, flush := a.recordingVAD.Process(chunk)

				if isSpeech {
					// Update segment with lock protection
					func() {
						a.recordingMu.Lock()
						defer a.recordingMu.Unlock()
						if a.recordingSegment == nil {
							// Start new segment
							a.recordingSegment = &recordingSegment{
								samples:          make([]float32, 0),
								startSampleIndex: processedSamples + i,
							}
						}
						a.recordingSegment.samples = append(a.recordingSegment.samples, chunk...)
					}()

					// Force flush if segment too long
					var seg *recordingSegment
					func() {
						a.recordingMu.Lock()
						defer a.recordingMu.Unlock()
						if a.recordingSegment != nil && len(a.recordingSegment.samples) >= maxSegmentSamples {
							seg = a.recordingSegment
							a.recordingSegment = nil
						}
					}()
					// Note: maxSegmentSamples is 15 seconds, so this segment is definitely long enough
					if seg != nil {
						startIdx := seg.startSampleIndex
						samplesCopy := make([]float32, len(seg.samples))
						copy(samplesCopy, seg.samples)
						go func() {
							a.finalizeRecordingSegment(segmentIndex, startIdx, samplesCopy)
						}()
						a.recordingVAD.Reset()
					}
				}

				if flush {
					var seg *recordingSegment
					func() {
						a.recordingMu.Lock()
						defer a.recordingMu.Unlock()
						if a.recordingSegment != nil && len(a.recordingSegment.samples) > 0 {
							seg = a.recordingSegment
							a.recordingSegment = nil
						}
					}()
					// Only process if segment is long enough (minimum 0.1 seconds = 1600 samples)
					if seg != nil {
						startIdx := seg.startSampleIndex
						samplesCopy := make([]float32, len(seg.samples))
						copy(samplesCopy, seg.samples)
						minSamples := 1600 // 0.1 seconds at 16kHz
						if len(samplesCopy) >= minSamples {
							go func() {
								a.finalizeRecordingSegment(segmentIndex, startIdx, samplesCopy)
							}()
						} else {
							a.logInfo(fmt.Sprintf("[Recording] Skipping VAD flush segment (too short: %d samples, minimum: %d)", len(samplesCopy), minSamples))
						}
						a.recordingVAD.Reset()
					}
				}

				processedSamples += len(chunk)
			}
		}
	}
}

// finalizeRecordingSegment processes a completed speech segment with ASR
// Note: This function should be called with segment data as arguments,
// as it may be called asynchronously after recordingSegment has been cleared
func (a *App) finalizeRecordingSegment(segmentIndex *int, startSampleIndex int, samples []float32) {
	// Add panic recovery to prevent crashes
	defer func() {
		if r := recover(); r != nil {
			a.logError(fmt.Sprintf("[Recording] Panic in finalizeRecordingSegment: %v", r))
		}
	}()

	if a.realtimeService != nil {
		return
	}

	if len(samples) == 0 {
		return
	}

	// Skip processing if samples are too short (less than 0.1 seconds = 1600 samples at 16kHz)
	// ASR models typically require a minimum duration to work properly
	minSamples := 1600 // 0.1 seconds at 16kHz
	if len(samples) < minSamples {
		a.logInfo(fmt.Sprintf("[Recording] Skipping ASR for segment with %d samples (too short, minimum: %d)", len(samples), minSamples))
		return
	}

	// Process with ASR (this uses CGO, but runs in separate goroutine)
	if a.asrService != nil {
		text, err := a.asrService.ProcessSamples(samples)
		if err != nil {
			a.logError(fmt.Sprintf("[Recording] ASR processing failed: %v", err))
			return
		}

		if strings.TrimSpace(text) != "" {
			*segmentIndex++
			timestamp := float64(startSampleIndex) / float64(audio.RecordingSampleRate)
			var transcriptPath string
			func() {
				a.recordingMu.Lock()
				defer a.recordingMu.Unlock()
				transcriptPath = a.recordingTranscriptPath
			}()
			a.logInfo(fmt.Sprintf("[Recording] Final transcript segment %d: %s (timestamp: %.2f)", *segmentIndex, text, timestamp))

			// Emit event for UI
			runtime.EventsEmit(a.ctx, "recording-transcript-final", map[string]interface{}{
				"text":           text,
				"segmentIndex":   *segmentIndex,
				"timestamp":      timestamp,
				"transcriptPath": transcriptPath,
			})

			// Append to transcript file immediately (with flush)
			if transcriptPath != "" {
				// Format timestamp as MM:SS
				minutes := int(timestamp) / 60
				seconds := int(timestamp) % 60
				timestampedLine := fmt.Sprintf("**%02d:%02d** %s", minutes, seconds, text)
				a.appendTranscriptLine(transcriptPath, timestampedLine)
				a.logInfo(fmt.Sprintf("[Recording] Appended transcript segment %d to file", *segmentIndex))
			} else {
				a.logError("[Recording] Transcript path not set, cannot append transcript line")
			}
		}
	}
}

// StopRecording stops recording and saves the audio file and transcript
func (a *App) StopRecording() (string, error) {
	a.logInfo("[Recording] StopRecording called")

	// Check if recording first (without holding lock)
	a.recordingMu.Lock()
	isRecording := a.isRecording
	a.recordingMu.Unlock()

	if !isRecording {
		a.logError("[Recording] StopRecording called but not recording")
		return "", fmt.Errorf("not recording")
	}

	// Stop recorder first (before stopping processing goroutine)
	a.logInfo("[Recording] Stopping audio recorder...")
	var recorder *audio.Recorder
	func() {
		a.recordingMu.Lock()
		defer a.recordingMu.Unlock()
		recorder = a.recorder
	}()

	if recorder != nil {
		if err := recorder.Stop(); err != nil {
			a.logError(fmt.Sprintf("[Recording] Failed to stop recorder: %v", err))
		} else {
			a.logInfo("[Recording] Audio recorder stopped successfully")
		}
	}

	// Flush RealtimeService before stopping processing
	if a.realtimeService != nil {
		a.logInfo("[Recording] Flushing RealtimeService...")
		finalText := a.realtimeService.Flush()
		if finalText != "" {
			// Get current timestamp
			var currentSamples int
			func() {
				a.recordingMu.Lock()
				defer a.recordingMu.Unlock()
				currentSamples = len(a.recordingSamples)
			}()
			timestamp := float64(currentSamples) / float64(audio.RecordingSampleRate)
			var segmentIndex int
			func() {
				a.recordingMu.Lock()
				defer a.recordingMu.Unlock()
				// Get last segment index (we'll increment it)
				segmentIndex = 0 // We'll need to track this differently
			}()
			var transcriptPath string
			func() {
				a.recordingMu.Lock()
				defer a.recordingMu.Unlock()
				transcriptPath = a.recordingTranscriptPath
			}()
			runtime.EventsEmit(a.ctx, "recording-transcript-final", map[string]interface{}{
				"text":           finalText,
				"segmentIndex":   segmentIndex,
				"timestamp":      timestamp,
				"transcriptPath": transcriptPath,
			})
			a.logInfo(fmt.Sprintf("[Recording] Flushed final transcript: %s", finalText))

			// Append to transcript file
			if transcriptPath != "" {
				minutes := int(timestamp) / 60
				seconds := int(timestamp) % 60
				timestampedLine := fmt.Sprintf("**%02d:%02d** %s", minutes, seconds, finalText)
				a.appendTranscriptLine(transcriptPath, timestampedLine)
			}
		}
	}

	// Stop processing goroutine
	a.logInfo("[Recording] Stopping processing goroutine...")
	if a.recordingStopCh != nil {
		a.logInfo("[Recording] Closing recordingStopCh...")
		close(a.recordingStopCh)
		a.logInfo("[Recording] recordingStopCh closed, waiting for goroutine...")

		// Wait with timeout to avoid hanging
		done := make(chan struct{})
		go func() {
			a.logInfo("[Recording] Wait goroutine: calling recordingWg.Wait()...")
			a.recordingWg.Wait()
			a.logInfo("[Recording] Wait goroutine: recordingWg.Wait() completed")
			close(done)
		}()

		select {
		case <-done:
			a.logInfo("[Recording] Processing goroutine stopped successfully")
		case <-time.After(5 * time.Second):
			a.logError("[Recording] Timeout waiting for processing goroutine to stop (5 seconds)")
			a.logError("[Recording] This may indicate the goroutine is blocked or waiting on a lock")
		}
		a.recordingStopCh = nil
		a.logInfo("[Recording] recordingStopCh set to nil")
	} else {
		a.logInfo("[Recording] Processing goroutine already stopped (recordingStopCh is nil)")
	}

	// Get recorded samples from our buffer (not from recorder, which may be empty)
	a.logInfo("[Recording] Getting recorded samples...")
	var samples []float32
	func() {
		a.recordingMu.Lock()
		defer a.recordingMu.Unlock()
		samples = make([]float32, len(a.recordingSamples))
		copy(samples, a.recordingSamples)
		a.logInfo(fmt.Sprintf("[Recording] Copied %d samples from buffer", len(samples)))
	}()
	a.logInfo(fmt.Sprintf("[Recording] Recorded %d samples (%.2f seconds)", len(samples), float64(len(samples))/float64(audio.RecordingSampleRate)))
	if len(samples) == 0 {
		a.logError("[Recording] No audio samples recorded")
		a.cleanupRecording()
		return "", fmt.Errorf("no audio recorded")
	}

	// Generate filename with timestamp
	timestamp := time.Now().Format("20060102-150405")
	filename := fmt.Sprintf("%s_recording.m4a", timestamp)
	a.logInfo(fmt.Sprintf("[Recording] Generated filename: %s", filename))

	// Save audio file
	audioDir := filepath.Join(a.dataDir, "data", "audio")
	a.logInfo(fmt.Sprintf("[Recording] Creating audio directory: %s", audioDir))
	if err := os.MkdirAll(audioDir, 0755); err != nil {
		a.logError(fmt.Sprintf("[Recording] Failed to create audio directory: %v", err))
		a.cleanupRecording()
		return "", fmt.Errorf("failed to create audio directory: %w", err)
	}

	audioPath := filepath.Join(audioDir, filename)
	a.logInfo(fmt.Sprintf("[Recording] Encoding audio to M4A: %s", audioPath))
	if err := audio.EncodePCMToM4A(context.Background(), samples, audio.RecordingSampleRate, audioPath); err != nil {
		a.logError(fmt.Sprintf("[Recording] Failed to encode audio: %v", err))
		a.cleanupRecording()
		return "", fmt.Errorf("failed to encode audio: %w", err)
	}
	a.logInfo(fmt.Sprintf("[Recording] Audio file saved: %s", audioPath))

	// Get relative path
	relAudioPath, err := filepath.Rel(a.dataDir, audioPath)
	if err != nil {
		relAudioPath = audioPath
	}
	relAudioPath = filepath.ToSlash(relAudioPath)

	// Get transcript path (file was created at start)
	var transcriptPath string
	func() {
		a.recordingMu.Lock()
		defer a.recordingMu.Unlock()
		transcriptPath = a.recordingTranscriptPath
	}()

	if transcriptPath == "" {
		a.logError("[Recording] Transcript path not set, creating new transcript file...")
		// Fallback: create transcript file if path was not set
		transcriptText := "_（リアルタイム文字起こしが使用されました。録音中の文字起こし結果は個別のイベントで送信されました。）_\n"
		var err error
		transcriptPath, err = a.writeTranscriptDocument(relAudioPath, transcriptText)
		if err != nil {
			a.logError(fmt.Sprintf("[Recording] Failed to write transcript: %v", err))
		} else {
			a.logInfo(fmt.Sprintf("[Recording] Transcript document created: %s", transcriptPath))
		}
	} else {
		a.logInfo(fmt.Sprintf("[Recording] Using existing transcript file: %s", transcriptPath))
		// Update audio_path in frontmatter with actual audio file path
		if err := a.updateTranscriptAudioPath(transcriptPath, relAudioPath); err != nil {
			a.logError(fmt.Sprintf("[Recording] Failed to update audio_path in transcript: %v", err))
			// Continue even if update fails
		} else {
			a.logInfo(fmt.Sprintf("[Recording] Updated audio_path in transcript: %s", relAudioPath))
		}
	}

	// Cleanup
	a.logInfo("[Recording] Cleaning up recording resources...")
	a.cleanupRecording()

	// Emit event
	runtime.EventsEmit(a.ctx, "recording-stopped", map[string]interface{}{
		"audioPath":      relAudioPath,
		"transcriptPath": transcriptPath,
	})

	a.logInfo(fmt.Sprintf("[Recording] Recording stopped successfully: audio=%s, transcript=%s", relAudioPath, transcriptPath))

	return relAudioPath, nil
}

// IsRecording returns whether recording is currently in progress
func (a *App) IsRecording() bool {
	a.recordingMu.Lock()
	defer a.recordingMu.Unlock()
	return a.isRecording
}

// checkUnsavedBeforeClose emits an event to JS to check for unsaved changes
// JS will show a modal and call AllowClose() if user confirms closing
func (a *App) checkUnsavedBeforeClose() {
	runtime.EventsEmit(a.ctx, "check-unsaved-before-close", nil)
}

// AllowClose is called by JS after user confirms closing (save/discard)
// This allows the window to close after user interaction
func (a *App) AllowClose() {
	// Set flag to allow closing
	a.allowCloseMu.Lock()
	a.allowCloseFlag = true
	a.allowCloseMu.Unlock()

	// Quit the application
	// This will trigger OnBeforeClose again, but it will return false because allowCloseFlag is true
	runtime.Quit(a.ctx)
}

// cleanupRecording cleans up recording resources
func (a *App) cleanupRecording() {
	a.logInfo("[Recording] cleanupRecording called")

	var recorder *audio.Recorder
	func() {
		a.recordingMu.Lock()
		defer a.recordingMu.Unlock()
		recorder = a.recorder
		a.recorder = nil
		a.recordingSamples = nil
		a.recordingVAD = nil
		a.recordingSegment = nil
		a.recordingTranscriptPath = ""
		a.isRecording = false
	}()

	if recorder != nil {
		a.logInfo("[Recording] Closing audio recorder...")
		recorder.Close()
		a.logInfo("[Recording] Audio recorder closed")
	}

	// Close RealtimeService if it exists
	func() {
		a.recordingMu.Lock()
		defer a.recordingMu.Unlock()
		if a.realtimeService != nil {
			a.logInfo("[Recording] Closing RealtimeService...")
			a.realtimeService.Close()
			a.realtimeService = nil
			a.logInfo("[Recording] RealtimeService closed")
		}
	}()

	a.logInfo("[Recording] Cleanup completed")
}

// resizeImageIfNeeded resizes an image if its longer edge exceeds maxWidth, maintaining aspect ratio
func resizeImageIfNeeded(img image.Image, maxWidth int) image.Image {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	// 長辺がmaxWidth以下の場合はリサイズ不要
	longerEdge := width
	if height > width {
		longerEdge = height
	}

	if longerEdge <= maxWidth {
		return img
	}

	// 長辺をmaxWidthに合わせてリサイズ
	var newWidth, newHeight int
	if width > height {
		// 横長画像: 横幅を基準
		newWidth = maxWidth
		newHeight = (height * maxWidth) / width
	} else {
		// 縦長画像: 高さを基準
		newHeight = maxWidth
		newWidth = (width * maxWidth) / height
	}

	// Create a new RGBA image for the resized image
	resized := image.NewRGBA(image.Rect(0, 0, newWidth, newHeight))

	// Use high-quality resampling (CatmullRom is good for downscaling)
	// draw.Src is used instead of draw.Over for golang.org/x/image/draw
	draw.CatmullRom.Scale(resized, resized.Bounds(), img, bounds, draw.Src, nil)

	return resized
}

// createOptimizedImageTempFile creates an optimized PNG temporary file from an image
// Returns the temporary file path and error
// The image is resized if needed and encoded as PNG (lossless compression)
func (a *App) createOptimizedImageTempFile(img image.Image, originalPath string) (string, error) {
	// Resize if needed (longer edge <= 1920px)
	originalBounds := img.Bounds()
	img = resizeImageIfNeeded(img, maxImageWidthForPDF)
	if img.Bounds().Dx() != originalBounds.Dx() || img.Bounds().Dy() != originalBounds.Dy() {
		a.logInfo(fmt.Sprintf("PDF export: Resized image: %s (original: %dx%d, resized: %dx%d)", originalPath, originalBounds.Dx(), originalBounds.Dy(), img.Bounds().Dx(), img.Bounds().Dy()))
	}

	// Create temporary file
	tmpDir := os.TempDir()
	if tmpDir == "" {
		tmpDir = "."
	}
	tmpFile := filepath.Join(tmpDir, fmt.Sprintf("karte-pdf-img-%d-%d.png", time.Now().UnixNano(), os.Getpid()))

	// Encode as PNG (lossless compression)
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		return "", fmt.Errorf("failed to encode image to PNG: %w", err)
	}

	finalData := pngBuf.Bytes()

	// Write to temporary file
	if err := os.WriteFile(tmpFile, finalData, 0644); err != nil {
		return "", fmt.Errorf("failed to write temporary file: %w", err)
	}

	a.logInfo(fmt.Sprintf("PDF export: Created optimized PNG temp file: %s (size: %d bytes)", tmpFile, len(finalData)))

	return tmpFile, nil
}
