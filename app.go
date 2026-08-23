package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"log"
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
	"karte/internal/screenshot"
	syncpkg "karte/internal/sync"
	"karte/internal/webpchunk"

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
var emitPDFExportEvent = runtime.EventsEmit

const maxImageSizeForPDF = 10 * 1024 * 1024 // 10MB
const maxImageWidthForPDF = 800             // PDF表示用に最大横幅800px（Previewで開く速度を改善）
const maxImageFileSizeForPDF = 300 * 1024   // 各画像の目標サイズ300KB

// App struct
type App struct {
	ctx             context.Context
	lifecycle       appLifecycle
	startupSmoke    *startupSmokeState
	root            string
	dataDir         string
	logFilePath     string
	fs              FileSystem
	syncManager     *syncpkg.SyncManager
	vcs             *gitvcs.VCS
	asrResource     appASRResourceState
	realtimeService appRealtimeASRService // For real-time ASR with partial text support
	// Recording fields
	recorder                appAudioRecorder
	recordingControlMu      sync.Mutex
	recordingMu             sync.Mutex
	recordingASRLease       *asrResourceLease
	recordingNewRealtime    func(*asr.Config, asr.LogFunc) (appRealtimeASRService, error)
	recordingNewRecorder    func() (appAudioRecorder, error)
	recordingNewVAD         func() *audio.SimpleVAD
	recordingNewWAVWriter   func(string, int) (appRecordingWAVWriter, error)
	recordingNow            func() time.Time
	recordingLstat          func(string) (os.FileInfo, error)
	isRecording             bool
	recordingPipeline       *appRecordingPipeline
	recordingSequence       recordingSequence
	recordingFinalizeRun    func(context.Context, int, []float32)
	recordingWg             sync.WaitGroup // WaitGroup for recording processing goroutine
	recordingStopCh         chan struct{}  // Channel to stop recording processing
	recordingStopOnce       sync.Once
	recordingTranscriptPath string // Path to transcript file (created at start)
	// NOTE: Multi-window support requires Wails v3 (currently in development)
	// Uncomment when upgrading to Wails v3:
	// presenter windows keyed by document id (e.g., "content/xxx.md")
	// presenters map[string]*Presenter

	// Window close control
	allowCloseMu   sync.Mutex
	allowCloseFlag bool

	jobs         appJobState
	siteBuild    appSiteBuildState
	transcripts  appTranscriptState
	mediaImports appMediaImportState
	csvStore     appCSVStoreState

	fileSearchMu          sync.Mutex
	fileSearchReadFile    func(string) ([]byte, error)
	fileSearchChangeToken func(string, os.FileInfo) string
	documentMapMu         sync.Mutex
	documentMapStore      documentMapStore
	documentRenameFile    func(string, string) error

	graphRefreshMu      sync.Mutex
	graphSnapshotMu     sync.RWMutex
	graphCacheState     graphCache
	graphCacheLoaded    bool
	graphReadFile       func(string) ([]byte, error)
	graphPersistFile    func(string, []byte, fs.FileMode) error
	graphParseDocument  func(string, []byte) graphParsedDocument
	graphMigrationParse func([]byte) *fm.FrontMatter
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
	Path    string    `json:"path"`
	Title   string    `json:"title"`
	ModTime time.Time `json:"modTime"`
	Size    int64     `json:"size"`
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
	app := &App{
		fs:           fs,
		startupSmoke: newStartupSmokeState(os.Getenv(startupSmokeReadyFileEnv)),
	}
	app.lifecycle.start(context.Background())
	return app
}

// startup is called when the app starts. The context is saved
// so we can call the runtime methods
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.lifecycle.start(ctx)
	a.resetJobManager()
	a.resetSiteBuildCoordinator()
	a.resetASRResourceManager()
	// NOTE: Multi-window support requires Wails v3 (currently in development)
	// Uncomment when upgrading to Wails v3:
	// if a.presenters == nil {
	// 	a.presenters = make(map[string]*Presenter)
	// }
	// Determine base directory from executable location
	exePath, err := os.Executable()
	if err != nil {
		startupErr := fmt.Errorf("failed to get executable path: %w", err)
		runtime.LogError(ctx, startupErr.Error())
		a.finishStartup(ctx, startupErr)
		return
	}
	exeDir := filepath.Dir(exePath)
	dataResolution, err := resolveRuntimeDataDirectory(exePath)
	if err != nil {
		startupErr := fmt.Errorf("failed to resolve data directory: %w", err)
		runtime.LogError(ctx, startupErr.Error())
		a.finishStartup(ctx, startupErr)
		return
	}
	a.root = dataResolution.RootDirectory
	a.dataDir = dataResolution.DataDirectory
	switch dataResolution.Kind {
	case dataDirectoryOverride:
		a.logInfo(fmt.Sprintf("Using KARTE_DATA_DIR override: %s", a.dataDir))
	case dataDirectoryDev:
		a.logInfo(fmt.Sprintf("Using development karte_data: %s", a.dataDir))
	case dataDirectoryUser:
		a.logInfo(fmt.Sprintf("Using macOS user data directory: %s", a.dataDir))
	}

	if dataResolution.LegacyDataDirectory != "" {
		report, err := migrateLegacyDataDirectory(dataResolution.LegacyDataDirectory, a.dataDir)
		if err != nil {
			startupErr := fmt.Errorf("failed to migrate legacy data directory: %w", err)
			runtime.LogError(ctx, startupErr.Error())
			a.finishStartup(ctx, startupErr)
			return
		}
		if report.Copied > 0 || report.Preserved > 0 {
			a.logInfo(fmt.Sprintf(
				"Legacy data migration completed: source=%s destination=%s copied=%d preserved=%d",
				dataResolution.LegacyDataDirectory,
				a.dataDir,
				report.Copied,
				report.Preserved,
			))
		}
	}

	if err := a.initializeDataDirectory(); err != nil {
		startupErr := fmt.Errorf("failed to initialize data directory: %w", err)
		runtime.LogError(ctx, startupErr.Error())
		a.finishStartup(ctx, startupErr)
		return
	}
	if migrated, err := a.MigrateLegacyGraphDocumentIDs(); err != nil {
		runtime.LogError(ctx, fmt.Sprintf("Failed to migrate legacy document IDs: %v", err))
	} else if migrated > 0 {
		a.logInfo(fmt.Sprintf("Assigned doc_id to %d legacy Markdown documents", migrated))
	}
	if err := a.RefreshGraphData(); err != nil {
		// The graph cache is derived data. Keep the application usable and retry
		// at the next explicit mutation boundary if startup rebuilding fails.
		runtime.LogError(ctx, fmt.Sprintf("Failed to initialize graph cache: %v", err))
	}
	if a.getJobManager() == nil {
		startupErr := errors.New("failed to initialize background job manager")
		runtime.LogError(ctx, startupErr.Error())
		a.finishStartup(ctx, startupErr)
		return
	}

	a.logInfo(fmt.Sprintf("Karte started. root=%s dataDir=%s exeDir=%s", a.root, a.dataDir, exeDir))
	a.finishStartup(ctx, nil)

	// Initialize sync manager (disabled for now - will be implemented with git integration)
	// a.syncManager = syncpkg.NewSyncManager(ctx, a.root)
	// if err := a.syncManager.Start(); err != nil {
	// 	runtime.LogError(ctx, fmt.Sprintf("Failed to start sync manager: %v", err))
	// }
}

// shutdown is invoked by Wails when the app is closing.
func (a *App) shutdown(ctx context.Context) {
	if a == nil {
		return
	}
	// Seal public job creation first，but keep the existing manager and lifecycle
	// context alive until an accepted recording has submitted and drained its
	// final ASR segment．
	jobManager := a.sealJobAdmission()
	asrManager := a.closeASRAdmission()
	if !a.lifecycle.beginShutdownDrain() {
		return
	}
	// StartRecording and StopRecording own the complete recording session under
	// this lock．Shutdown waits at the same boundary，so Realtime Flush／Close and
	// offline lease release cannot overlap or run twice．
	a.recordingControlMu.Lock()
	defer a.recordingControlMu.Unlock()
	// Free the bounded worker/category budget before the recording processor
	// submits its final offline segment．The recording group remains admitted
	// through recordingJobManager until its consistency boundary completes．
	cancelNonRecordingJobsForShutdown(jobManager)

	a.recordingMu.Lock()
	recordingActive := a.isRecording
	a.recordingMu.Unlock()
	if recordingActive {
		if audioPath, err := a.finishRecordingSession(false); err != nil {
			a.logError(fmt.Sprintf("Failed to finalize recording during shutdown: %v", err))
		} else {
			a.logInfo(fmt.Sprintf("Finalized recording during shutdown: %s", audioPath))
		}
	}

	if jobManager != nil {
		jobManager.Close()
	}
	a.lifecycle.cancelShutdownWorkers()

	if a.syncManager != nil {
		if err := a.syncManager.Stop(); err != nil {
			a.logError(fmt.Sprintf("Failed to stop sync manager during shutdown: %v", err))
		}
	}

	if jobManager != nil {
		jobWaitCtx, cancelJobWait := context.WithTimeout(context.Background(), 10*time.Second)
		if !jobManager.Shutdown(jobWaitCtx) {
			a.logError("Timeout waiting for managed background jobs during shutdown")
		}
		cancelJobWait()
	}

	workerWaitCtx, cancelWorkerWait := context.WithTimeout(context.Background(), 10*time.Second)
	if !a.lifecycle.wait(workerWaitCtx) {
		a.logError("Timeout waiting for background workers during shutdown")
	}
	cancelWorkerWait()

	// Workers no longer use the recognizers，so native handles can be released
	// without racing an in-flight transcription or recording callback．
	a.recordingMu.Lock()
	a.recorder = nil
	realtimeService, recordingASRLease := a.takeRecordingASRResourcesLocked()
	recordingPipeline := a.recordingPipeline
	a.recordingPipeline = nil
	a.recordingTranscriptPath = ""
	a.recordingStopCh = nil
	a.isRecording = false
	a.recordingMu.Unlock()
	if recordingPipeline != nil {
		recordingPipeline.stopProcessing()
		if err := recordingPipeline.transcript.Abort(); err != nil {
			a.logError(fmt.Sprintf("Failed to close incomplete transcript during shutdown: %v", err))
		}
		if err := recordingPipeline.wav.Abort(); err != nil {
			a.logError(fmt.Sprintf("Failed to abort incomplete recording during shutdown: %v", err))
		}
	}
	closeRecordingASRResources(realtimeService, recordingASRLease)
	if !a.shutdownASRResource(asrManager) {
		a.logError("Timeout waiting for ASR resource leases during shutdown")
	}

	if err := audio.Terminate(); err != nil {
		a.logError(fmt.Sprintf("Failed to terminate audio runtime during shutdown: %v", err))
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
		filepath.Join(a.dataDir, ".mdsys", "index.json"): "{}",
		filepath.Join(a.dataDir, ".mdsys", "graph.json"): "{}",
		filepath.Join(a.dataDir, "data", "asr", "config.json"): `{
  "enabled": false,
  "sampleRate": 16000,
  "model": {
    "tokens": "",
    "encoder": "",
    "decoder": "",
    "joiner": ""
  },
  "decoding": {
    "method": "greedy_search"
  },
  "runtime": {
    "threads": 2,
    "provider": "cpu",
    "idleTimeoutSeconds": 300
  }
}`,
	}

	for filePath, content := range defaultFiles {
		if _, err := fsys.Stat(filePath); os.IsNotExist(err) {
			if err := fsys.WriteFile(filePath, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to create default file %s: %v", filePath, err)
			}
		}
	}

	return a.initializeGitRepository()
}

// findTemplatePath finds the karte_data_template path in .app bundle
func (a *App) findTemplatePath() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	exeDir := filepath.Dir(exePath)

	// Check if running inside .app bundle
	if strings.HasSuffix(filepath.ToSlash(exeDir), "/Contents/MacOS") {
		contentsDir := filepath.Dir(exeDir)       // .../Contents
		appBundleDir := filepath.Dir(contentsDir) // .../Karte.app
		templatePath := filepath.Join(appBundleDir, "Contents", "Resources", "karte_data_template")
		if _, err := os.Stat(templatePath); err == nil {
			return templatePath
		}
	}
	return ""
}

// copyTemplateToDataDir overlays karte_data_template onto karte_data.
// Existing user files are preserved (not overwritten) for user-owned areas.
func (a *App) copyTemplateToDataDir(templatePath string) error {
	return filepath.Walk(templatePath, func(srcPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate relative path from template root
		relPath, err := filepath.Rel(templatePath, srcPath)
		if err != nil {
			return err
		}

		// Skip root directory
		if relPath == "." {
			return nil
		}

		dstPath := filepath.Join(a.dataDir, relPath)

		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		if !a.shouldOverwriteTemplateFile(relPath) {
			return nil
		}

		// Copy file (overwrite to ensure template updates)
		srcFile, err := os.Open(srcPath)
		if err != nil {
			return err
		}
		defer srcFile.Close()

		dstFile, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
		if err != nil {
			return err
		}
		defer dstFile.Close()

		_, err = io.Copy(dstFile, srcFile)
		return err
	})
}

// shouldOverwriteTemplateFile decides whether template file should overwrite user data.
func (a *App) shouldOverwriteTemplateFile(relPath string) bool {
	normalized := filepath.ToSlash(relPath)
	// Preserve user-authored markdown/content/audio if file already exists
	if strings.HasPrefix(normalized, "content/") || strings.HasPrefix(normalized, "data/audio/") {
		if _, err := os.Stat(filepath.Join(a.dataDir, relPath)); err == nil {
			return false
		}
	}
	// Preserve user-configured ASR config.json if it already exists
	if normalized == "data/asr/config.json" {
		if _, err := os.Stat(filepath.Join(a.dataDir, relPath)); err == nil {
			return false
		}
	}
	// Everything else (themes, ASR models, default configs) gets overwritten
	return true
}

// initializeGitRepository initializes git repository in karte_data
func (a *App) initializeGitRepository() error {
	vcs, err := gitvcs.NewVCS(a.ctx, a.dataDir, a.logInfo)
	if err != nil {
		return fmt.Errorf("failed to initialize git repository: %v", err)
	}
	a.vcs = vcs

	// Make initial commit if repository is new
	_, err = vcs.Repository().Head()
	if err != nil {
		// No HEAD means it's a new repository, make initial commit
		worktree, err := vcs.Repository().Worktree()
		if err == nil {
			// Add all files
			worktree.Add(".")
			// Make initial commit
			_, err = worktree.Commit("Initial commit", &git.CommitOptions{
				Author: &object.Signature{
					Name:  "Karte User",
					Email: "karte@localhost",
					When:  time.Now(),
				},
			})
			if err != nil {
				a.logError(fmt.Sprintf("Failed to make initial commit: %v", err))
				// Don't fail initialization if commit fails
			} else {
				a.logInfo("Created initial git commit")
			}
		}
	}

	return nil
}

// GetFileList returns metadata for markdown and PDF files in the content directory.
// Markdown bodies remain in the backend search index and are never returned over IPC.
func (a *App) GetFileList() []FileItem {
	_, files, err := a.refreshFileSearchIndex()
	if err != nil {
		a.logError(fmt.Sprintf("GetFileList failed: %v", err))
		return []FileItem{}
	}
	a.logInfo(fmt.Sprintf("GetFileList completed: Found %d files (markdown and PDF)", len(files)))
	return files
}

// GetImageList returns image files from the shared image directory and Web Clip assets.
func (a *App) GetImageList() []ImageItem {
	var images []ImageItem
	imageDir := filepath.Join(a.dataDir, "data", "image")

	a.logInfo(fmt.Sprintf("GetImageList: imageDir=%s", imageDir))

	// Check if image directory exists
	if _, err := os.Stat(imageDir); os.IsNotExist(err) {
		a.logInfo(fmt.Sprintf("Image directory does not exist: %s", imageDir))
	} else {
		err := filepath.Walk(imageDir, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				a.logError(fmt.Sprintf("Error walking path %s: %v", p, err))
				return nil
			}
			if info.IsDir() {
				return nil
			}
			if strings.ToLower(filepath.Ext(info.Name())) != ".webp" {
				return nil
			}

			rel, _ := filepath.Rel(a.dataDir, p)
			rel = filepath.ToSlash(rel)
			metadataPath := strings.TrimSuffix(rel, ".webp") + ".yaml"
			imageItem := ImageItem{
				Path:         rel,
				Name:         info.Name(),
				Size:         info.Size(),
				ModTime:      info.ModTime(),
				MetadataPath: metadataPath,
				OriginalPath: a.findOriginalImagePath(rel),
			}
			images = append(images, imageItem)
			a.logInfo(fmt.Sprintf("Found image: %s", imageItem.Path))
			return nil
		})

		if err != nil {
			a.logError(fmt.Sprintf("Error walking image directory: %v", err))
		}
	}

	clipImages := a.getWebClipAssetImageList()
	images = append(images, clipImages...)

	// Sort by modification time (newest first)
	sort.Slice(images, func(i, j int) bool {
		return images[i].ModTime.After(images[j].ModTime)
	})

	a.logInfo(fmt.Sprintf("Found %d image files", len(images)))
	return images
}

func (a *App) getWebClipAssetImageList() []ImageItem {
	assetRoot := filepath.Join(a.dataDir, "content", "clips", "assets")
	if _, err := os.Stat(assetRoot); os.IsNotExist(err) {
		return nil
	}

	type candidate struct {
		path string
		info os.FileInfo
	}
	candidates := map[string]candidate{}
	err := filepath.Walk(assetRoot, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			a.logError(fmt.Sprintf("Error walking web clip asset path %s: %v", p, err))
			return nil
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if !isGalleryImageExt(ext) {
			return nil
		}
		baseKey := strings.TrimSuffix(filepath.ToSlash(p), ext)
		existing, exists := candidates[baseKey]
		if !exists || ext == ".webp" || strings.ToLower(filepath.Ext(existing.path)) != ".webp" {
			candidates[baseKey] = candidate{path: p, info: info}
		}
		return nil
	})
	if err != nil {
		a.logError(fmt.Sprintf("Error walking web clip asset directory: %v", err))
		return nil
	}

	images := make([]ImageItem, 0, len(candidates))
	for _, candidate := range candidates {
		rel, err := filepath.Rel(a.dataDir, candidate.path)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		images = append(images, ImageItem{
			Path:         rel,
			Name:         candidate.info.Name(),
			Size:         candidate.info.Size(),
			ModTime:      candidate.info.ModTime(),
			MetadataPath: metadataPathFromImage(rel),
			OriginalPath: a.findOriginalImagePath(rel),
		})
	}
	return images
}

func isGalleryImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	default:
		return false
	}
}

// GetImageMetadata returns the YAML metadata associated with the provided image.
// If metadata file doesn't exist, it reads tags from WebP chunk and generates metadata.
func (a *App) GetImageMetadata(imagePath string) (string, error) {
	relImagePath, _, err := a.resolveImagePath(imagePath)
	if err != nil {
		return "", err
	}
	metaRelPath := metadataPathFromImage(relImagePath)
	metaAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(metaRelPath))

	var metadataContent string
	var metadataMap map[string]interface{}

	// Try to read metadata file
	if _, err := os.Stat(metaAbsPath); err == nil {
		data, err := os.ReadFile(metaAbsPath)
		if err != nil {
			return "", fmt.Errorf("read metadata: %w", err)
		}
		metadataContent = string(data)

		// Parse metadata to map
		var validation interface{}
		if yamlErr := yaml.Unmarshal(data, &validation); yamlErr == nil {
			if mapVal, ok := validation.(map[string]interface{}); ok {
				metadataMap = mapVal
			} else {
				metadataMap = make(map[string]interface{})
			}
		} else if jsonErr := json.Unmarshal(data, &validation); jsonErr == nil {
			if mapVal, ok := validation.(map[string]interface{}); ok {
				metadataMap = mapVal
			} else {
				metadataMap = make(map[string]interface{})
			}
		} else {
			metadataMap = make(map[string]interface{})
		}
	} else {
		// Metadata file doesn't exist, create empty map
		metadataMap = make(map[string]interface{})
		metadataContent = "{}\n"
	}

	// Read tags from WebP chunk if it's a WebP file
	imageAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(relImagePath))
	if strings.ToLower(filepath.Ext(relImagePath)) == ".webp" {
		webpTags, err := webpchunk.ReadTagsFromWebP(imageAbsPath)
		if err != nil {
			// Log error but continue (metadata file may still have tags)
			a.logError(fmt.Sprintf("Failed to read tags from WebP chunk: %v", err))
		} else if len(webpTags) > 0 {
			// Merge WebP tags with existing metadata tags
			existingTags := a.extractTagsFromMetadata(metadataMap)
			// Combine and deduplicate
			tagMap := make(map[string]bool)
			for _, tag := range existingTags {
				tagMap[tag] = true
			}
			for _, tag := range webpTags {
				tagMap[tag] = true
			}
			combinedTags := make([]string, 0, len(tagMap))
			for tag := range tagMap {
				combinedTags = append(combinedTags, tag)
			}
			// Update metadata map with combined tags
			if len(combinedTags) > 0 {
				metadataMap["tags"] = strings.Join(combinedTags, ", ")
			}

			// If metadata file didn't exist, generate new metadata with tags
			if _, err := os.Stat(metaAbsPath); os.IsNotExist(err) {
				// Generate YAML from map
				yamlBytes, err := yaml.Marshal(metadataMap)
				if err == nil {
					metadataContent = string(yamlBytes)
				}
			} else {
				// Update existing metadata content with tags
				// Try to preserve original format (YAML or JSON)
				if strings.TrimSpace(metadataContent) == "{}" || strings.TrimSpace(metadataContent) == "" {
					// Empty metadata, generate YAML
					yamlBytes, err := yaml.Marshal(metadataMap)
					if err == nil {
						metadataContent = string(yamlBytes)
					}
				} else {
					// Try to update tags in existing content
					// For simplicity, regenerate from map
					yamlBytes, err := yaml.Marshal(metadataMap)
					if err == nil {
						metadataContent = string(yamlBytes)
					}
				}
			}
		}

	}

	return metadataContent, nil
}

// GetImageSystemMetadata returns privileged image metadata stored in the KMTD WebP chunk.
func (a *App) GetImageSystemMetadata(imagePath string) (string, error) {
	relImagePath, _, err := a.resolveImagePath(imagePath)
	if err != nil {
		return "", err
	}
	imageAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(relImagePath))

	var metadata map[string]interface{}
	if strings.ToLower(filepath.Ext(relImagePath)) == ".webp" {
		if webClipMetadata, err := readWebClipMetadataChunk(imageAbsPath); err != nil {
			a.logError(fmt.Sprintf("Failed to read Web Clip metadata from WebP chunk: %v", err))
		} else if webClipMetadata != nil {
			metadata = webClipMetadata
		}
	}
	if metadata == nil {
		metadata = a.webClipMetadataFromManifest(relImagePath)
	}
	if metadata == nil {
		return "{}\n", nil
	}
	yamlBytes, err := yaml.Marshal(metadata)
	if err != nil {
		return "", fmt.Errorf("marshal system metadata: %w", err)
	}
	return string(yamlBytes), nil
}

// SaveImageMetadata writes YAML metadata for the image, validating the format beforehand.
func (a *App) SaveImageMetadata(imagePath, yamlContent string) (bool, error) {
	if imagePath == "" {
		return false, fmt.Errorf("image path is required")
	}

	relImagePath, _, err := a.resolveImagePath(imagePath)
	if err != nil {
		return false, err
	}

	if strings.TrimSpace(yamlContent) == "" {
		yamlContent = "{}\n"
	}

	// Try to parse as YAML first, then JSON
	var validation interface{}
	var metadataMap map[string]interface{}
	yamlErr := yaml.Unmarshal([]byte(yamlContent), &validation)
	if yamlErr != nil {
		// Try JSON format
		jsonErr := json.Unmarshal([]byte(yamlContent), &validation)
		if jsonErr != nil {
			return false, fmt.Errorf("invalid YAML/JSON: YAML error: %v, JSON error: %v", yamlErr, jsonErr)
		}
		// JSON parsed successfully, convert to map
		if mapVal, ok := validation.(map[string]interface{}); ok {
			metadataMap = mapVal
		} else {
			metadataMap = make(map[string]interface{})
		}
	} else {
		// YAML parsed successfully
		if mapVal, ok := validation.(map[string]interface{}); ok {
			metadataMap = mapVal
		} else {
			metadataMap = make(map[string]interface{})
		}
	}

	metaRelPath := metadataPathFromImage(relImagePath)
	metaAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(metaRelPath))

	if err := os.MkdirAll(filepath.Dir(metaAbsPath), 0o755); err != nil {
		return false, fmt.Errorf("prepare metadata directory: %w", err)
	}

	if err := os.WriteFile(metaAbsPath, []byte(yamlContent), 0o644); err != nil {
		return false, fmt.Errorf("write metadata file: %w", err)
	}

	// Extract tags from metadata and save to WebP chunk
	tags := a.extractTagsFromMetadata(metadataMap)
	// Check if the image is a WebP file
	imageAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(relImagePath))
	if strings.ToLower(filepath.Ext(relImagePath)) == ".webp" {
		if err := webpchunk.WriteTagsToWebP(imageAbsPath, tags); err != nil {
			// Log error but don't fail the operation (metadata file is already saved)
			a.logError(fmt.Sprintf("Failed to write tags to WebP chunk: %v", err))
		} else {
			a.logInfo(fmt.Sprintf("Saved tags to WebP chunk: %v", tags))
		}
	}

	a.logInfo(fmt.Sprintf("Saved image metadata: %s", metaRelPath))
	a.refreshGraphAfterMutation("image metadata save")
	return true, nil
}

// SaveImageSystemMetadata writes privileged metadata to the KMTD WebP chunk.
// Permission checks will be added here when the authorization model exists.
func (a *App) SaveImageSystemMetadata(imagePath, metadataContent string) (bool, error) {
	if imagePath == "" {
		return false, fmt.Errorf("image path is required")
	}

	relImagePath, _, err := a.resolveImagePath(imagePath)
	if err != nil {
		return false, err
	}
	if strings.ToLower(filepath.Ext(relImagePath)) != ".webp" {
		return false, fmt.Errorf("system metadata can only be saved to WebP images")
	}
	if strings.TrimSpace(metadataContent) == "" {
		metadataContent = "{}\n"
	}

	metadataMap, err := parseImageMetadataMap([]byte(metadataContent))
	if err != nil {
		return false, err
	}
	if _, ok := metadataMap["schema"]; !ok && len(metadataMap) > 0 {
		metadataMap["schema"] = "karte.image.metadata.v1"
	}
	jsonBytes, err := json.MarshalIndent(metadataMap, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal system metadata: %w", err)
	}

	imageAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(relImagePath))
	if err := webpchunk.WriteMetadataToWebP(imageAbsPath, jsonBytes); err != nil {
		return false, fmt.Errorf("write system metadata chunk: %w", err)
	}
	a.logInfo(fmt.Sprintf("Saved image system metadata to KMTD chunk: %s", relImagePath))
	a.refreshGraphAfterMutation("image system metadata save")
	return true, nil
}

func parseImageMetadataMap(data []byte) (map[string]interface{}, error) {
	var validation interface{}
	if yamlErr := yaml.Unmarshal(data, &validation); yamlErr == nil {
		if mapVal, ok := normalizeMetadataMap(validation); ok {
			return mapVal, nil
		}
		return map[string]interface{}{}, nil
	}
	if jsonErr := json.Unmarshal(data, &validation); jsonErr == nil {
		if mapVal, ok := normalizeMetadataMap(validation); ok {
			return mapVal, nil
		}
		return map[string]interface{}{}, nil
	}
	return nil, fmt.Errorf("invalid YAML/JSON")
}

func normalizeMetadataMap(value interface{}) (map[string]interface{}, bool) {
	switch typed := value.(type) {
	case map[string]interface{}:
		return typed, true
	case map[interface{}]interface{}:
		result := make(map[string]interface{}, len(typed))
		for key, val := range typed {
			keyStr, ok := key.(string)
			if !ok {
				continue
			}
			result[keyStr] = val
		}
		return result, true
	default:
		return nil, false
	}
}

// CreateNewFile creates a new markdown file in the content directory
func (a *App) CreateNewFile(filename string) (bool, error) {
	if filename == "" {
		return false, fmt.Errorf("filename cannot be empty")
	}

	// Ensure filename has .md extension
	if !strings.HasSuffix(filename, ".md") {
		filename += ".md"
	}

	// Validate filename (no path separators)
	if strings.Contains(filename, "/") || strings.Contains(filename, "\\") {
		return false, fmt.Errorf("filename cannot contain path separators")
	}

	filePath := filepath.Join(a.dataDir, "content", filename)

	// Check if file already exists
	if _, err := os.Stat(filePath); err == nil {
		return false, fmt.Errorf("file already exists: %s", filename)
	}

	// Create default content
	defaultContent := fmt.Sprintf("# %s\n\nStart writing your content here...\n", strings.TrimSuffix(filename, ".md"))
	defaultContent, documentID, err := a.ensureDocID(defaultContent)
	if err != nil {
		return false, fmt.Errorf("failed to assign doc_id: %v", err)
	}

	// Ensure directory exists and write the file
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return false, fmt.Errorf("failed to prepare directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(defaultContent), 0644); err != nil {
		return false, fmt.Errorf("failed to create file: %v", err)
	}
	if _, err := a.updateDocumentMapping(documentID, filepath.ToSlash(filepath.Join("content", filename))); err != nil {
		if rollbackErr := os.Remove(filePath); rollbackErr != nil && !errors.Is(rollbackErr, os.ErrNotExist) {
			return false, fmt.Errorf("failed to update document map: %w; created file rollback failed: %v", err, rollbackErr)
		}
		return false, fmt.Errorf("failed to update document map; created file was rolled back: %w", err)
	}

	a.logInfo(fmt.Sprintf("Created new file: %s", filePath))
	a.scheduleSiteBuild(filepath.ToSlash(filepath.Join("content", filename)))
	a.refreshGraphAfterMutation("file creation")
	return true, nil
}

// ClipURL imports a web article URL as a Markdown document under content/clips.
func (a *App) ClipURL(req clip.ClipRequest) (clip.ClipResult, error) {
	if a == nil {
		return clip.ClipResult{}, fmt.Errorf("app is not initialized")
	}
	if a.dataDir == "" {
		return clip.ClipResult{}, fmt.Errorf("dataDir is not initialized")
	}

	service := clip.Service{DataDir: a.dataDir}
	result, err := service.ClipURL(a.ctx, req)
	if err != nil {
		a.logError(fmt.Sprintf("ClipURL failed for %s: %v", req.URL, err))
		return clip.ClipResult{}, err
	}
	if _, err := a.ensureDocumentIDAtMutation(result.MarkdownPath); err != nil {
		return result, fmt.Errorf("assign doc_id to clipped Markdown: %w", err)
	}

	a.logInfo(fmt.Sprintf("ClipURL: imported %s to %s", result.SourceURL, result.MarkdownPath))
	a.scheduleSiteBuild(result.MarkdownPath)
	a.refreshGraphAfterMutation("web clip import")
	a.emitEvent("file-changed", result.MarkdownPath)
	a.enqueueWebClipAssetConversion(result.MarkdownPath, result.AssetDir)
	return result, nil
}

func (a *App) emitEvent(name string, data interface{}) {
	if a != nil && a.ctx != nil {
		runtime.EventsEmit(a.ctx, name, data)
	}
}

func (a *App) enqueueWebClipAssetConversion(markdownPath, assetDir string) {
	if a == nil || markdownPath == "" || assetDir == "" {
		return
	}
	if !isWebClipMarkdownPath(markdownPath) || !isWebClipAssetDir(assetDir) {
		return
	}
	manager := a.getJobManager()
	if manager == nil {
		a.logInfo(fmt.Sprintf("Web Clip image conversion was not queued during shutdown: %s", assetDir))
		return
	}
	job := webClipConversionJob{
		MarkdownPath: filepath.ToSlash(markdownPath),
		AssetDir:     filepath.ToSlash(assetDir),
	}
	submission := manager.Submit(managedJob{
		Category: appJobCategoryWebClip,
		Group:    appJobGroupWebClip,
		Key:      job.AssetDir,
		Priority: jobPriorityLow,
		Coalesce: jobReplacePending,
		Run: func(ctx context.Context) error {
			startupDelay := time.NewTimer(3 * time.Second)
			defer startupDelay.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-startupDelay.C:
			}
			err := a.processWebClipConversionJobContext(ctx, job, 250*time.Millisecond)
			if err != nil && ctx.Err() == nil {
				a.logError(fmt.Sprintf("Web Clip image conversion failed for %s: %v", job.AssetDir, err))
			}
			return err
		},
	})
	switch submission.Status {
	case jobAccepted, jobDeduplicated, jobReplacedPending:
		return
	case jobRejectedFull:
		a.logError(fmt.Sprintf(
			"Web Clip image conversion queue is full for %s; original assets were preserved and conversion can be retried by importing the clip again",
			job.AssetDir,
		))
	case jobRejectedClosed, jobRejectedCanceled:
		a.logInfo(fmt.Sprintf("Web Clip image conversion was canceled during shutdown: %s", job.AssetDir))
	default:
		a.logError(fmt.Sprintf("Web Clip image conversion was rejected for %s: %v", job.AssetDir, submission.Err))
	}
}

type imagePathReplacement struct {
	OriginalRel string
	WebPRel     string
}

func (a *App) processWebClipConversionJob(job webClipConversionJob, interImageDelay time.Duration) error {
	return a.processWebClipConversionJobContext(context.Background(), job, interImageDelay)
}

func (a *App) processWebClipConversionJobContext(ctx context.Context, job webClipConversionJob, interImageDelay time.Duration) error {
	if a == nil || a.dataDir == "" {
		return fmt.Errorf("app dataDir is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !isWebClipMarkdownPath(job.MarkdownPath) || !isWebClipAssetDir(job.AssetDir) {
		return fmt.Errorf("invalid web clip conversion job: markdown=%s assetDir=%s", job.MarkdownPath, job.AssetDir)
	}

	markdownAbs := filepath.Join(a.dataDir, filepath.FromSlash(job.MarkdownPath))
	assetAbs := filepath.Join(a.dataDir, filepath.FromSlash(job.AssetDir))
	if _, err := os.Stat(markdownAbs); err != nil {
		return fmt.Errorf("stat clip markdown: %w", err)
	}
	if _, err := os.Stat(assetAbs); err != nil {
		return fmt.Errorf("stat clip asset dir: %w", err)
	}

	replacements := []imagePathReplacement{}
	imageManifest := a.loadWebClipImageManifest(assetAbs)
	err := filepath.Walk(assetAbs, func(p string, info os.FileInfo, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			a.logError(fmt.Sprintf("Error walking web clip conversion path %s: %v", p, err))
			return nil
		}
		if info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if ext == ".webp" {
			webpRel, relErr := filepath.Rel(a.dataDir, p)
			if relErr == nil {
				webpRel = filepath.ToSlash(webpRel)
				item := imageManifest[webClipImageManifestKey(webpRel)]
				if err := a.writeWebClipMetadataToWebP(p, webpRel, webpRel, markdownAbs, item, ext); err != nil {
					a.logError(fmt.Sprintf("Failed to write Web Clip metadata to WebP: %s: %v", p, err))
				}
			}
			return nil
		}
		if !isConvertibleWebClipImageExt(ext) {
			return nil
		}
		webpAbs := strings.TrimSuffix(p, ext) + ".webp"
		if fileExists(webpAbs) {
			originalRel, relErr := filepath.Rel(a.dataDir, p)
			if relErr == nil {
				webpRel, webpRelErr := filepath.Rel(a.dataDir, webpAbs)
				if webpRelErr == nil {
					originalRel = filepath.ToSlash(originalRel)
					webpRel = filepath.ToSlash(webpRel)
					item := imageManifest[webClipImageManifestKey(originalRel)]
					if err := a.writeWebClipMetadataToWebP(webpAbs, originalRel, webpRel, markdownAbs, item, ext); err != nil {
						a.logError(fmt.Sprintf("Failed to write Web Clip metadata to existing WebP: %s: %v", webpAbs, err))
					}
				}
			}
			return nil
		}
		if err := a.convertImageFileToWebPContext(ctx, p, webpAbs, ext); err != nil {
			a.logError(fmt.Sprintf("Failed to convert Web Clip image to WebP: %s -> %s: %v", p, webpAbs, err))
			return nil
		}

		originalRel, err := filepath.Rel(a.dataDir, p)
		if err != nil {
			return nil
		}
		webpRel, err := filepath.Rel(a.dataDir, webpAbs)
		if err != nil {
			return nil
		}
		replacements = append(replacements, imagePathReplacement{
			OriginalRel: filepath.ToSlash(originalRel),
			WebPRel:     filepath.ToSlash(webpRel),
		})
		item := imageManifest[webClipImageManifestKey(filepath.ToSlash(originalRel))]
		if err := a.writeWebClipMetadataToWebP(webpAbs, filepath.ToSlash(originalRel), filepath.ToSlash(webpRel), markdownAbs, item, ext); err != nil {
			a.logError(fmt.Sprintf("Failed to write Web Clip metadata to WebP: %s: %v", webpAbs, err))
		}
		a.emitEvent("image-imported", map[string]interface{}{
			"path":          filepath.ToSlash(webpRel),
			"webp_path":     filepath.ToSlash(webpRel),
			"original_path": filepath.ToSlash(originalRel),
			"source":        "web_clip",
		})
		if interImageDelay > 0 {
			delay := time.NewTimer(interImageDelay)
			defer delay.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-delay.C:
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk web clip assets: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(replacements) == 0 {
		a.refreshGraphAfterMutation("web clip image conversion")
		return nil
	}

	if updated, err := a.updateWebClipMarkdownImageRefs(markdownAbs, replacements); err != nil {
		return err
	} else if updated {
		a.emitEvent("file-changed", job.MarkdownPath)
		a.scheduleSiteBuild(job.MarkdownPath)
	}
	a.refreshGraphAfterMutation("web clip image conversion")
	return nil
}

func (a *App) loadWebClipImageManifest(assetAbs string) map[string]clip.ImageManifestItem {
	result := map[string]clip.ImageManifestItem{}
	data, err := os.ReadFile(filepath.Join(assetAbs, clip.ImageManifestFile))
	if err != nil {
		if !os.IsNotExist(err) {
			a.logError(fmt.Sprintf("Failed to read Web Clip image manifest: %v", err))
		}
		return result
	}
	var manifest clip.ImageManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		a.logError(fmt.Sprintf("Failed to parse Web Clip image manifest: %v", err))
		return result
	}
	for _, item := range manifest.Images {
		if item.LocalPath == "" {
			continue
		}
		key := webClipImageManifestKey(item.LocalPath)
		result[key] = item
	}
	return result
}

func webClipImageManifestKey(relPath string) string {
	relPath = filepath.ToSlash(filepath.Clean(relPath))
	return strings.TrimSuffix(relPath, strings.ToLower(filepath.Ext(relPath)))
}

func (a *App) writeWebClipMetadataToWebP(webpAbs, originalRel, webpRel, markdownAbs string, item clip.ImageManifestItem, sourceExt string) error {
	markdownReference, ok := a.webClipMarkdownReference(markdownAbs, webpRel)
	if !ok {
		markdownReference = filepath.ToSlash(webpRel)
	}
	documentPath := filepath.ToSlash(markdownAbs)
	if rel, err := filepath.Rel(a.dataDir, markdownAbs); err == nil {
		documentPath = filepath.ToSlash(rel)
	}
	originalFormat := item.OriginalFormat
	if originalFormat == "" {
		originalFormat = imageContentTypeFromExt(sourceExt)
	}
	if originalFormat == "" {
		originalFormat = "application/octet-stream"
	}
	metadata := webClipImageMetadata{
		Schema: "karte.image.metadata.v1",
		Source: webClipMetadataSource{
			Kind:             "web_clip",
			PageURL:          item.PageURL,
			ImageURL:         item.ImageURL,
			ResolvedImageURL: item.ResolvedImageURL,
			SiteName:         item.SiteName,
			PageTitle:        item.PageTitle,
			HTMLAlt:          item.HTMLAlt,
			HTMLCaption:      item.HTMLCaption,
		},
		Capture: webClipMetadataCapture{
			CapturedAt:   item.CapturedAt,
			Method:       "url_clip",
			HTTPStatus:   item.HTTPStatus,
			ContentType:  item.ContentType,
			ETag:         item.ETag,
			LastModified: item.LastModified,
		},
		Relations: webClipMetadataRelations{
			DocumentPath:      documentPath,
			MarkdownReference: markdownReference,
		},
		Processing: webClipMetadataProcessing{
			OriginalFormat: originalFormat,
			ConvertedTo:    "image/webp",
		},
	}
	if metadata.Capture.ContentType == "" {
		metadata.Capture.ContentType = originalFormat
	}
	if metadata.Source.ResolvedImageURL == "" {
		metadata.Source.ResolvedImageURL = item.ImageURL
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal Web Clip image metadata: %w", err)
	}
	return webpchunk.WriteMetadataToWebP(webpAbs, data)
}

func (a *App) webClipMarkdownReference(markdownAbs, imageRel string) (string, bool) {
	imageAbs := filepath.Join(a.dataDir, filepath.FromSlash(imageRel))
	ref, err := filepath.Rel(filepath.Dir(markdownAbs), imageAbs)
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(ref), true
}

func imageContentTypeFromExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

func readWebClipMetadataChunk(webpAbs string) (map[string]interface{}, error) {
	data, err := webpchunk.ReadMetadataFromWebP(webpAbs)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var metadata map[string]interface{}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("parse Web Clip metadata chunk: %w", err)
	}
	return metadata, nil
}

func (a *App) webClipMetadataFromManifest(relImagePath string) map[string]interface{} {
	if !strings.HasPrefix(filepath.ToSlash(relImagePath), "content/clips/assets/") {
		return nil
	}
	assetDir := filepath.Dir(filepath.Join(a.dataDir, filepath.FromSlash(relImagePath)))
	manifest := a.loadWebClipImageManifest(assetDir)
	item, ok := manifest[webClipImageManifestKey(relImagePath)]
	if !ok {
		return nil
	}
	originalFormat := item.OriginalFormat
	if originalFormat == "" {
		originalFormat = imageContentTypeFromExt(filepath.Ext(relImagePath))
	}
	metadata := webClipImageMetadata{
		Schema: "karte.image.metadata.v1",
		Source: webClipMetadataSource{
			Kind:             "web_clip",
			PageURL:          item.PageURL,
			ImageURL:         item.ImageURL,
			ResolvedImageURL: item.ResolvedImageURL,
			SiteName:         item.SiteName,
			PageTitle:        item.PageTitle,
			HTMLAlt:          item.HTMLAlt,
			HTMLCaption:      item.HTMLCaption,
		},
		Capture: webClipMetadataCapture{
			CapturedAt:   item.CapturedAt,
			Method:       "url_clip",
			HTTPStatus:   item.HTTPStatus,
			ContentType:  item.ContentType,
			ETag:         item.ETag,
			LastModified: item.LastModified,
		},
		Relations: webClipMetadataRelations{
			DocumentPath:      webClipDocumentPathFromAsset(relImagePath),
			MarkdownReference: item.MarkdownReference,
		},
		Processing: webClipMetadataProcessing{
			OriginalFormat: originalFormat,
			ConvertedTo:    "",
		},
	}
	if strings.ToLower(filepath.Ext(relImagePath)) == ".webp" {
		metadata.Processing.ConvertedTo = "image/webp"
		metadata.Relations.MarkdownReference = strings.TrimSuffix(item.MarkdownReference, filepath.Ext(item.MarkdownReference)) + ".webp"
	}
	var asMap map[string]interface{}
	data, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	if err := json.Unmarshal(data, &asMap); err != nil {
		return nil
	}
	return asMap
}

func webClipDocumentPathFromAsset(relImagePath string) string {
	relImagePath = filepath.ToSlash(filepath.Clean(relImagePath))
	const prefix = "content/clips/assets/"
	if !strings.HasPrefix(relImagePath, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(relImagePath, prefix)
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return "content/clips/" + parts[0] + ".md"
}

func (a *App) updateWebClipMarkdownImageRefs(markdownAbs string, replacements []imagePathReplacement) (bool, error) {
	contentBytes, err := os.ReadFile(markdownAbs)
	if err != nil {
		return false, fmt.Errorf("read clip markdown: %w", err)
	}
	content := string(contentBytes)
	updated := content
	markdownDir := filepath.Dir(markdownAbs)
	for _, replacement := range replacements {
		originalAbs := filepath.Join(a.dataDir, filepath.FromSlash(replacement.OriginalRel))
		webpAbs := filepath.Join(a.dataDir, filepath.FromSlash(replacement.WebPRel))
		originalMarkdownRel, err := filepath.Rel(markdownDir, originalAbs)
		if err != nil {
			continue
		}
		webpMarkdownRel, err := filepath.Rel(markdownDir, webpAbs)
		if err != nil {
			continue
		}
		originalMarkdownRel = filepath.ToSlash(originalMarkdownRel)
		webpMarkdownRel = filepath.ToSlash(webpMarkdownRel)
		updated = strings.ReplaceAll(updated, originalMarkdownRel, webpMarkdownRel)
		updated = strings.ReplaceAll(updated, "./"+originalMarkdownRel, "./"+webpMarkdownRel)
	}
	if updated == content {
		return false, nil
	}
	if err := os.WriteFile(markdownAbs, []byte(updated), 0o644); err != nil {
		return false, fmt.Errorf("write clip markdown: %w", err)
	}
	return true, nil
}

func isWebClipMarkdownPath(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	return strings.HasPrefix(path, "content/clips/") && strings.HasSuffix(strings.ToLower(path), ".md")
}

func isWebClipAssetDir(path string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	return strings.HasPrefix(path, "content/clips/assets/")
}

func isConvertibleWebClipImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg", ".png", ".gif":
		return true
	default:
		return false
	}
}

func (a *App) saveBoardDocument(doc *boardpkg.Document) (*boardpkg.Document, error) {
	if doc == nil {
		return nil, fmt.Errorf("board document is nil")
	}

	if doc.Path == "" {
		return nil, fmt.Errorf("board path is required")
	}

	absPath, ok := a.resolveContentPath(doc.Path)
	if !ok {
		return nil, fmt.Errorf("invalid board path: %s", doc.Path)
	}
	releaseTranscriptMutation, err := a.reserveTranscriptPathMutation(doc.Path)
	if err != nil {
		return nil, err
	}
	defer releaseTranscriptMutation()

	now := time.Now().Format("2006-01-02")
	if doc.Type == "" {
		doc.Type = boardpkg.BoardType
	}
	if doc.Title == "" {
		doc.Title = strings.TrimSuffix(filepath.Base(doc.Path), ".board.md")
	}
	if doc.Version == 0 {
		doc.Version = 1
	}
	if doc.Created == "" {
		doc.Created = now
	}
	doc.Updated = now
	if doc.DocID == "" {
		generated, err := docid.GenerateDocID(filepath.Join(a.dataDir, ".mdsys", "doc_seq.json"))
		if err != nil {
			return nil, fmt.Errorf("failed to generate board doc_id: %w", err)
		}
		doc.DocID = "board:" + generated[:12]
	}
	if doc.Layout.Cards == nil {
		doc.Layout.Cards = map[string]boardpkg.CardLayout{}
	}
	if doc.Layout.Viewport.Zoom == 0 {
		doc.Layout.Viewport.Zoom = 1
	}
	if doc.Tags == nil {
		doc.Tags = []string{}
	}

	content, err := boardpkg.Serialize(doc)
	if err != nil {
		return nil, err
	}

	previous := captureDocumentFileSnapshot(absPath)
	var oldHash string
	if previous.existed {
		oldHash = gitvcs.CalculateHash(string(previous.content))
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return nil, fmt.Errorf("failed to create board directory: %w", err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("failed to save board file: %w", err)
	}
	if _, err := a.updateDocumentMapping(doc.DocID, doc.Path); err != nil {
		return nil, documentMappingFailure("save board", absPath, previous, err)
	}

	newHash := gitvcs.CalculateHash(content)
	if a.vcs != nil && oldHash != newHash {
		relPath, err := filepath.Rel(a.dataDir, absPath)
		if err == nil {
			commitMessage := fmt.Sprintf("Update board: %s", doc.Path)
			if err := a.vcs.CommitFile(relPath, commitMessage); err != nil {
				a.logError(fmt.Sprintf("Failed to commit board file to git: %v", err))
			}
		}
	}

	a.scheduleSiteBuild(doc.Path)
	a.refreshGraphAfterMutation("board save")
	a.emitEvent("file-changed", doc.Path)

	saved, err := boardpkg.Parse(doc.Path, content)
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// LoadFile loads the content of a markdown file
// For PDF files, returns an empty string since PDFs are not editable
func (a *App) LoadFile(path string) (string, error) {
	a.logInfo(fmt.Sprintf("LoadFile called with path: %s", path))

	absPath, ok := a.resolveContentPath(path)
	if !ok {
		a.logError(fmt.Sprintf("Invalid path: %s", path))
		return "", fmt.Errorf("invalid path: %s", path)
	}

	a.logInfo(fmt.Sprintf("Resolved path: %s", absPath))

	// Check if this is a PDF file
	if strings.HasSuffix(strings.ToLower(path), ".pdf") {
		a.logInfo("PDF file detected, returning empty string")
		return "", nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to read file %s: %v", absPath, err))
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	contentStr := string(content)
	a.logInfo(fmt.Sprintf("Successfully loaded file, content length: %d", len(contentStr)))
	return contentStr, nil
}

var saveFileAtomicReplace = atomicReplaceFile

func atomicWriteSaveFile(path string, data []byte, defaultPerm fs.FileMode) error {
	return atomicWriteFileWithReplace(path, data, defaultPerm, saveFileAtomicReplace)
}

func atomicWriteDerivedFile(path string, data []byte, defaultPerm fs.FileMode) error {
	return atomicWriteFileWithReplace(path, data, defaultPerm, atomicReplaceFile)
}

func atomicWriteFileWithReplace(path string, data []byte, defaultPerm fs.FileMode, replace func(string, string) error) (err error) {
	perm := defaultPerm
	if info, statErr := os.Stat(path); statErr == nil {
		perm = info.Mode().Perm()
	}

	tempFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary save file: %w", err)
	}
	tempPath := tempFile.Name()
	replaced := false
	defer func() {
		_ = tempFile.Close()
		if !replaced {
			_ = os.Remove(tempPath)
		}
	}()

	if err := tempFile.Chmod(perm); err != nil {
		return fmt.Errorf("failed to set temporary save file permissions: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("failed to write temporary save file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("failed to sync temporary save file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temporary save file: %w", err)
	}
	if err := replace(tempPath, path); err != nil {
		return fmt.Errorf("failed to replace save file atomically: %w", err)
	}
	replaced = true
	return nil
}

type documentFileSnapshot struct {
	captured bool
	existed  bool
	content  []byte
	perm     fs.FileMode
}

func captureDocumentFileSnapshot(path string) documentFileSnapshot {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return documentFileSnapshot{captured: true}
	}
	if err != nil || !info.Mode().IsRegular() {
		return documentFileSnapshot{}
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return documentFileSnapshot{}
	}
	return documentFileSnapshot{
		captured: true,
		existed:  true,
		content:  content,
		perm:     info.Mode().Perm(),
	}
}

func rollbackDocumentFile(path string, snapshot documentFileSnapshot) error {
	if !snapshot.captured {
		return errors.New("prior document state was not captured")
	}
	if snapshot.existed {
		return atomicWriteDerivedFile(path, snapshot.content, snapshot.perm)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func documentMappingFailure(operation string, path string, snapshot documentFileSnapshot, mappingErr error) error {
	if rollbackErr := rollbackDocumentFile(path, snapshot); rollbackErr != nil {
		return fmt.Errorf("%s document map update failed: %w; document rollback failed: %v", operation, mappingErr, rollbackErr)
	}
	return fmt.Errorf("%s document map update failed and document change was rolled back: %w", operation, mappingErr)
}

// SaveFile saves content to a markdown file
func (a *App) SaveFile(path, content string) error {
	a.logInfo(fmt.Sprintf("SaveFile called for path: %s, content length: %d", path, len(content)))

	absPath, ok := a.resolveContentPath(path)
	if !ok {
		a.logError(fmt.Sprintf("SaveFile: invalid path: %s", path))
		return fmt.Errorf("invalid path: %s", path)
	}
	releaseTranscriptMutation, err := a.reserveTranscriptPathMutation(path)
	if err != nil {
		return err
	}
	defer releaseTranscriptMutation()

	// Capture the prior state for hashing and rollback if the document-map
	// transaction cannot be committed after the file replacement.
	previous := captureDocumentFileSnapshot(absPath)
	var oldHash string
	if previous.existed {
		oldHash = gitvcs.CalculateHash(string(previous.content))
		a.logInfo(fmt.Sprintf("SaveFile: existing file hash: %s (length: %d)", oldHash[:8], len(previous.content)))
	} else {
		a.logInfo(fmt.Sprintf("SaveFile: file does not exist yet or cannot be read"))
	}

	// Ensure doc_id exists (lazy assignment) - do this first
	contentWithDocID, docID, err := a.ensureDocID(content)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to ensure doc_id for %s: %v", path, err))
		return fmt.Errorf("failed to ensure doc_id for %s: %w", path, err)
	} else {
		if docID != "" {
			a.logInfo(fmt.Sprintf("File %s has doc_id: %s", path, docID))
		}
	}

	a.logInfo(fmt.Sprintf("SaveFile: after ensureDocID, content length: %d (original: %d)", len(contentWithDocID), len(content)))

	// Parse and format frontmatter after doc_id assignment
	frontMatter, markdownBody := fm.ParseFrontMatter(contentWithDocID)
	if frontMatter != nil {
		// Format frontmatter with normalized tags
		formattedFM := fm.FormatFrontMatter(frontMatter)
		content = formattedFM + markdownBody
		a.logInfo(fmt.Sprintf("SaveFile: formatted frontmatter for %s (title: %q, tags: %q, doc_id: %q, body length: %d)", path, frontMatter.Title, frontMatter.Tags, frontMatter.DocID, len(markdownBody)))
	} else {
		// No frontmatter, use content as-is
		content = contentWithDocID
		a.logInfo(fmt.Sprintf("SaveFile: no frontmatter for %s, using content as-is (length: %d)", path, len(content)))
	}

	// Detect conflicts without changing the working-tree file. HEAD is the
	// merge base, content is the editor's local version, and the current file is
	// the potentially external version.
	if a.vcs != nil {
		relPath, err := filepath.Rel(a.dataDir, absPath)
		if err != nil {
			return fmt.Errorf("failed to get relative save path: %w", err)
		}
		conflict, err := gitvcs.DetectConflictWithContent(a.vcs, a.dataDir, relPath, content)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to detect conflict: %v", err))
			return fmt.Errorf("failed to detect conflict: %w", err)
		}
		if conflict != nil {
			// Create backup before handling conflict
			if err := a.createBackup(path, content); err != nil {
				a.logError(fmt.Sprintf("Failed to create backup: %v", err))
			}

			// Try auto-merge for auto-resolvable or warning conflicts.
			if conflict.Severity == gitvcs.ConflictAutoResolvable || conflict.Severity == gitvcs.ConflictWarning {
				merged, severity, mergeErr := gitvcs.AutoMergeMarkdown(conflict.BaseContent, conflict.LocalContent, conflict.RemoteContent)
				if mergeErr == nil && severity != gitvcs.ConflictCritical {
					content = merged
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "auto-merge-success", map[string]interface{}{
							"path":        path,
							"merged_hash": gitvcs.CalculateHash(merged),
						})
					}
					a.logInfo(fmt.Sprintf("Auto-merged conflict for file: %s", path))
				} else {
					if a.ctx != nil {
						runtime.EventsEmit(a.ctx, "conflict-detected", conflict)
					}
					return fmt.Errorf("conflict detected: automatic merge requires manual resolution")
				}
			} else {
				// Critical conflict - require manual resolution.
				if a.ctx != nil {
					runtime.EventsEmit(a.ctx, "conflict-detected", conflict)
				}
				return fmt.Errorf("conflict detected: file has been modified elsewhere and requires manual resolution")
			}
		}
	}

	// Save via a same-directory temporary file and atomic replacement.
	a.logInfo(fmt.Sprintf("SaveFile: writing file %s (content length: %d)", absPath, len(content)))
	if err := atomicWriteSaveFile(absPath, []byte(content), 0o644); err != nil {
		a.logError(fmt.Sprintf("SaveFile: failed to write file %s: %v", absPath, err))
		return fmt.Errorf("failed to write file: %v", err)
	}
	savedDocumentID := docID
	if savedFrontMatter, _ := fm.ParseFrontMatter(content); savedFrontMatter != nil && savedFrontMatter.DocID != "" {
		savedDocumentID = savedFrontMatter.DocID
	}
	if savedDocumentID != "" {
		if _, err := a.updateDocumentMapping(savedDocumentID, path); err != nil {
			return documentMappingFailure("save file", absPath, previous, err)
		}
	}
	a.logInfo(fmt.Sprintf("SaveFile: successfully wrote file %s", absPath))

	// Calculate new hash
	newHash := gitvcs.CalculateHash(content)
	oldHashShort := ""
	newHashShort := ""
	if len(oldHash) >= 8 {
		oldHashShort = oldHash[:8]
	}
	if len(newHash) >= 8 {
		newHashShort = newHash[:8]
	}
	a.logInfo(fmt.Sprintf("SaveFile: oldHash=%s, newHash=%s", oldHashShort, newHashShort))

	// Commit to Git if content changed
	if a.vcs != nil && oldHash != newHash {
		// Get relative path from dataDir
		relPath, err := filepath.Rel(a.dataDir, absPath)
		if err == nil {
			commitMessage := fmt.Sprintf("Update: %s", path)
			if err := a.vcs.CommitFile(relPath, commitMessage); err != nil {
				a.logError(fmt.Sprintf("Failed to commit file to git: %v", err))
				// Don't fail save if git commit fails
			}
		}
	}

	// Queue an incremental build without blocking the editor save path.
	a.scheduleSiteBuild(path)
	a.refreshGraphAfterMutation("file save")

	// Emit file changed event
	if a.ctx != nil {
		runtime.EventsEmit(a.ctx, "file-changed", path)
	}

	return nil
}

// createBackup creates a backup of the file before conflict resolution
func (a *App) createBackup(path, content string) error {
	backupDir := filepath.Join(a.dataDir, ".backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %v", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	safePath := strings.ReplaceAll(path, "/", "_")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("%s_%s.md", safePath, timestamp))

	if err := os.WriteFile(backupPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write backup: %v", err)
	}

	a.logInfo(fmt.Sprintf("Created backup: %s", backupPath))
	return nil
}

// ensureDocID ensures that the content has a doc_id in frontmatter, generating one if needed
// Returns the content with doc_id and the doc_id value
func (a *App) ensureDocID(content string) (string, string, error) {
	frontMatter, body := fm.ParseFrontMatter(content)

	var docID string
	if frontMatter != nil {
		docID = frontMatter.DocID
	}

	// If doc_id doesn't exist, generate one
	if docID == "" {
		// Ensure .mdsys directory exists
		mdsysDir := filepath.Join(a.dataDir, ".mdsys")
		if err := os.MkdirAll(mdsysDir, 0755); err != nil {
			return "", "", fmt.Errorf("failed to create .mdsys directory: %v", err)
		}

		seqFile := filepath.Join(mdsysDir, "doc_seq.json")
		newDocID, err := docid.GenerateDocID(seqFile)
		if err != nil {
			return "", "", fmt.Errorf("failed to generate doc_id: %v", err)
		}
		docID = newDocID

		// Create frontmatter if it doesn't exist
		if frontMatter == nil {
			frontMatter = &fm.FrontMatter{}
		}
		frontMatter.DocID = docID

		// Reconstruct content with new frontmatter
		formattedFM := fm.FormatFrontMatter(frontMatter)
		content = formattedFM + body
	}

	return content, docID, nil
}

// ResolveConflict resolves a file conflict using the specified strategy
func (a *App) ResolveConflict(path, strategy string) error {
	absPath, ok := a.resolveContentPath(path)
	if !ok {
		return fmt.Errorf("invalid path: %s", path)
	}
	releaseTranscriptMutation, err := a.reserveTranscriptPathMutation(path)
	if err != nil {
		return err
	}
	defer releaseTranscriptMutation()

	if a.vcs == nil {
		return fmt.Errorf("git repository not initialized")
	}

	relPath, err := filepath.Rel(a.dataDir, absPath)
	if err != nil {
		return fmt.Errorf("failed to get relative path: %v", err)
	}

	// Get conflict info
	conflict, err := gitvcs.DetectConflict(a.vcs, a.dataDir, relPath)
	if err != nil {
		return fmt.Errorf("failed to detect conflict: %v", err)
	}
	if conflict == nil {
		return nil // No conflict
	}

	var resolvedContent string
	switch strategy {
	case "local":
		// Use local version
		resolvedContent = conflict.LocalContent
	case "remote":
		// Use remote version
		resolvedContent = conflict.RemoteContent
	case "merge":
		// Try to merge
		merged, _, err := gitvcs.AutoMergeMarkdown(conflict.BaseContent, conflict.LocalContent, conflict.RemoteContent)
		if err != nil {
			return fmt.Errorf("auto-merge failed: %v", err)
		}
		resolvedContent = merged
	default:
		return fmt.Errorf("unknown strategy: %s", strategy)
	}
	resolvedContent, resolvedDocumentID, err := a.ensureDocID(resolvedContent)
	if err != nil {
		return fmt.Errorf("ensure resolved document ID: %w", err)
	}

	// Save resolved content and keep the document-map mutation in the same
	// failure domain as the file replacement.
	previous := captureDocumentFileSnapshot(absPath)
	if err := atomicWriteDerivedFile(absPath, []byte(resolvedContent), 0o644); err != nil {
		return fmt.Errorf("failed to write resolved file: %v", err)
	}
	if _, err := a.updateDocumentMapping(resolvedDocumentID, path); err != nil {
		return documentMappingFailure("resolve conflict", absPath, previous, err)
	}

	// Commit the resolution
	commitMessage := fmt.Sprintf("Resolve conflict: %s (strategy: %s)", path, strategy)
	if err := a.vcs.CommitFile(relPath, commitMessage); err != nil {
		a.logError(fmt.Sprintf("Failed to commit conflict resolution: %v", err))
	}

	a.scheduleSiteBuild(path)
	a.refreshGraphAfterMutation("conflict resolution")
	a.logInfo(fmt.Sprintf("Resolved conflict for %s using strategy: %s", path, strategy))
	return nil
}

// PreviewMarkdown renders markdown content to HTML.
func (a *App) PreviewMarkdown(content string) (string, error) {
	return a.PreviewMarkdownForPath("", content)
}

// enableMarpInFrontMatter adds the explicit flag required by KarteRenderer to
// front matter that Karte recognized as Marp through legacy presentation
// fields. Keeping the original front matter intact ensures renderer options
// such as header, footer, pagination, aspect ratio, and theme are forwarded.
func enableMarpInFrontMatter(content string) string {
	openingEnd := strings.IndexByte(content, '\n')
	if openingEnd == -1 {
		return content
	}

	lineEnding := "\n"
	if openingEnd > 0 && content[openingEnd-1] == '\r' {
		lineEnding = "\r\n"
	}
	openingEnd++
	return content[:openingEnd] + "marp: true" + lineEnding + content[openingEnd:]
}

// PreviewMarkdownForPath renders markdown content to HTML using currentPath as
// the base for document-relative assets such as Web Clip images.
func (a *App) PreviewMarkdownForPath(currentPath, content string) (string, error) {
	// Parse frontmatter to check if this is a Marp presentation
	frontMatter, markdownBody := fm.ParseFrontMatter(content)

	// Check if Marp mode is enabled
	// Marp mode is enabled if:
	// 1. marp: true is explicitly set
	// 2. or Marp-specific fields (header, footer, paginate) are present
	isMarpMode := false

	if frontMatter != nil {
		if frontMatter.Marp {
			isMarpMode = true
		}

		// Extract header, footer, paginate, aspectRatio, and marpTheme from Raw
		if frontMatter.Raw != nil {
			if _, ok := frontMatter.Raw["header"].(string); ok {
				isMarpMode = true // Header presence indicates Marp mode
			}
			if _, ok := frontMatter.Raw["footer"].(string); ok {
				isMarpMode = true // Footer presence indicates Marp mode
			}
			if _, ok := frontMatter.Raw["paginate"].(bool); ok {
				isMarpMode = true // Paginate presence indicates Marp mode
			}
		}
	}

	if isMarpMode {
		// Render as a Marp presentation through the extracted renderer module.
		rendererSource := content
		if !frontMatter.Marp {
			// Preserve Karte's legacy Marp detection for documents that use only
			// header/footer/paginate fields. KarteRenderer selects Marp from the
			// explicit flag, so add it without discarding the other metadata.
			rendererSource = enableMarpInFrontMatter(content)
		}
		html, _, err := karterenderer.RenderString(a.dataDir, rendererSource)
		if err != nil {
			a.logError(fmt.Sprintf("PreviewMarkdown [Marp]: renderer failed: %v", err))
			return "", fmt.Errorf("failed to render Marp markdown: %w", err)
		}

		// Log HTML sample to debug image rendering
		if strings.Contains(html, "<img") {
			imgMatch := regexp.MustCompile(`<img[^>]+>`)
			imgs := imgMatch.FindAllString(html, 5)
			for i, img := range imgs {
				a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: Found img tag %d: %s", i+1, img))
			}
		} else {
			a.logInfo("PreviewMarkdown [Marp]: No img tags found in HTML")
		}

		html = a.rewritePreviewImageSources(html, currentPath)

		// Check for pinned version references and add warnings (same logic as regular markdown)
		sourceDocID := ""
		if frontMatter != nil {
			sourceDocID = frontMatter.DocID
		}
		a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: sourceDocID=%s", sourceDocID))

		// If we have a doc_id, check for pinned version references
		if sourceDocID != "" {
			// Get graph data to find edges
			graphData, err := a.GetGraphData()
			if err != nil {
				a.logError(fmt.Sprintf("PreviewMarkdown [Marp]: failed to get graph data: %v", err))
			} else {
				a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: got graph data with %d edges", len(graphData.Edges)))
				// Resolve doc_id paths from the graph's immutable in-memory snapshot.
				// Preview must never read doc_map.json or trigger graph rebuilding.
				docMap := a.graphDocMapSnapshot()
				actualFilePath := docMap[sourceDocID]
				if actualFilePath != "" {
					a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: found file path for doc_id %s: %s", sourceDocID, actualFilePath))
				} else {
					a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: no file path found for doc_id %s in graph snapshot", sourceDocID))
				}

				// Extract links from markdown body to build a mapping
				links := a.extractLinks(markdownBody)
				a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: extracted %d links from markdown", len(links)))
				// Create a map of target node IDs to original link targets for quick lookup
				targetIDToLinkTarget := make(map[string]string) // targetID -> original link target (e.g., "file.md")
				// Use actual file path if available, otherwise use content root
				currentFilePath := actualFilePath
				if currentFilePath == "" {
					currentFilePath = "content/"
				}
				for _, link := range links {
					if link.Kind == "wikilink" || link.Kind == "markdown_link" {
						// Resolve link target using the actual file path
						targetID := a.resolveLinkTarget(link, currentFilePath)
						if targetID != "" {
							targetIDToLinkTarget[targetID] = link.Target
							a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: mapped link %s -> targetID %s (from file %s)", link.Target, targetID, currentFilePath))
						} else {
							a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: failed to resolve link %s from file %s", link.Target, currentFilePath))
						}
					}
				}

				// Find edges that reference pinned versions and have been updated
				warningCount := 0
				for _, edge := range graphData.Edges {
					a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: checking edge: SourceDocID=%s, ToVersionMode=%s, TargetUpdated=%v", edge.SourceDocID, edge.ToVersionMode, edge.TargetUpdated))
					if edge.SourceDocID == sourceDocID && edge.ToVersionMode == "pinned" && edge.TargetUpdated {
						a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: found pinned updated edge: %s -> %s (TargetDocID: %s)", edge.Source, edge.Target, edge.TargetDocID))
						// This link references a pinned version that has been updated
						// Find the corresponding HTML link and add warning

						// Get the current target path from the in-memory doc map using TargetDocID.
						targetPath := strings.TrimPrefix(edge.Target, "doc:/")
						if edge.TargetDocID != "" {
							if currentPath, exists := docMap[edge.TargetDocID]; exists {
								targetPath = strings.TrimPrefix(currentPath, "content/")
								a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: resolved target path from graph snapshot: %s -> %s (doc_id: %s)", edge.Target, targetPath, edge.TargetDocID))
							}
						}

						// Get the original link target from the markdown
						// Try both old and new target IDs
						originalLinkTarget := ""
						hasLink := false

						// First try with the edge.Target (might be old path after rename)
						if linkTarget, ok := targetIDToLinkTarget[edge.Target]; ok {
							originalLinkTarget = linkTarget
							hasLink = true
							a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: found original link target using edge.Target: %s -> %s", edge.Target, originalLinkTarget))
						} else if edge.TargetDocID != "" {
							// Try to find by constructing target ID from current path
							currentTargetID := "doc:/" + targetPath
							if linkTarget, ok := targetIDToLinkTarget[currentTargetID]; ok {
								originalLinkTarget = linkTarget
								hasLink = true
								a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: found original link target using current path: %s -> %s", currentTargetID, originalLinkTarget))
							} else {
								a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: no original link target found for %s or %s", edge.Target, currentTargetID))
							}
						} else {
							a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: no original link target found for %s", edge.Target))
						}

						// Build patterns to match the href attribute in HTML
						// The href might be the original link target, or a resolved path
						linkPatterns := []string{}
						if hasLink {
							// Add the original link target (e.g., "file.md" or "../file.md")
							linkPatterns = append(linkPatterns, regexp.QuoteMeta(originalLinkTarget))
							// Also try with .html extension
							if strings.HasSuffix(originalLinkTarget, ".md") {
								linkPatterns = append(linkPatterns, regexp.QuoteMeta(strings.TrimSuffix(originalLinkTarget, ".md")+".html"))
							}
						}
						// Add the resolved target path
						linkPatterns = append(linkPatterns, regexp.QuoteMeta(targetPath))
						if strings.HasSuffix(targetPath, ".md") {
							linkPatterns = append(linkPatterns, regexp.QuoteMeta(strings.TrimSuffix(targetPath, ".md")+".html"))
						}
						// Also try with leading slash
						linkPatterns = append(linkPatterns, regexp.QuoteMeta("/"+targetPath))
						if strings.HasSuffix(targetPath, ".md") {
							linkPatterns = append(linkPatterns, regexp.QuoteMeta("/"+strings.TrimSuffix(targetPath, ".md")+".html"))
						}

						// Also add URL-encoded versions of patterns for multibyte characters
						urlEncodedPatterns := []string{}
						for _, pattern := range linkPatterns {
							urlEncodedPatterns = append(urlEncodedPatterns, pattern)
							// Add URL-encoded version (for multibyte characters)
							// Remove regex escaping first, then URL encode, then re-escape for regex
							unescaped := strings.ReplaceAll(pattern, "\\", "")
							urlEncoded := url.QueryEscape(unescaped)
							if urlEncoded != unescaped {
								urlEncodedPatterns = append(urlEncodedPatterns, regexp.QuoteMeta(urlEncoded))
							}
						}
						linkPatterns = urlEncodedPatterns

						a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: trying %d patterns to match link (including URL-encoded)", len(linkPatterns)))

						// Match <a> tags that link to this target and add warning
						// Use a map to track which links already have warnings to avoid duplicates
						warnedLinks := make(map[string]bool)
						for _, pattern := range linkPatterns {
							// Match <a> tag with href containing the pattern, but not already containing the warning
							linkRegex := regexp.MustCompile(`(<a[^>]+href=["']([^"']*` + pattern + `[^"']*)["'][^>]*>.*?</a>)`)
							html = linkRegex.ReplaceAllStringFunc(html, func(match string) string {
								// Check if warning already added to this link
								if strings.Contains(match, "version-warning") {
									return match
								}
								// Extract href to check if we've already warned for this link
								hrefMatch := regexp.MustCompile(`href=["']([^"']+)["']`)
								if hrefSubmatch := hrefMatch.FindStringSubmatch(match); len(hrefSubmatch) > 1 {
									href := hrefSubmatch[1]
									if warnedLinks[href] {
										// Already warned for this href, skip
										return match
									}
									warnedLinks[href] = true
								}
								// Add warning after the closing </a> tag
								// Create warning HTML with update button for this specific edge
								warningHTML := fmt.Sprintf(
									`<span class="version-warning" style="color: #ff6b6b; font-size: 0.9em; margin-left: 0.5em;">⚠️ 古いバージョンを参照しています <button class="update-to-latest-btn" data-source-doc-id="%s" data-target-doc-id="%s" style="margin-left: 0.5em; padding: 2px 8px; font-size: 0.85em; background: #4CAF50; color: white; border: none; border-radius: 3px; cursor: pointer;" onclick="updateLinkToLatest(this)">最新版に更新</button></span>`,
									edge.SourceDocID, edge.TargetDocID)
								warningCount++
								a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: added warning to link matching pattern: %s", pattern))
								return match + warningHTML
							})
						}
					}
				}
				if warningCount > 0 {
					a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: added %d warnings total", warningCount))
				} else {
					a.logInfo("PreviewMarkdown [Marp]: no warnings added (no matching edges or links)")
				}
			}
		}

		return html, nil
	}

	// Render unsaved editor content directly through KarteRenderer. The data
	// directory remains the trusted root for layouts and imports.
	html, _, err := karterenderer.RenderString(a.dataDir, content)
	if err != nil {
		a.logError(fmt.Sprintf("PreviewMarkdown: renderer failed: %v (root: %s)", err, a.dataDir))
		return "", fmt.Errorf("failed to render markdown: %w", err)
	}

	html = a.rewritePreviewImageSources(html, currentPath)

	// Check for pinned version references and add warnings
	// Extract doc_id from frontmatter
	sourceDocID := ""
	if frontMatter != nil {
		sourceDocID = frontMatter.DocID
	}
	a.logInfo(fmt.Sprintf("PreviewMarkdown: sourceDocID=%s", sourceDocID))

	// If we have a doc_id, check for pinned version references
	if sourceDocID != "" {
		// Get graph data to find edges
		graphData, err := a.GetGraphData()
		if err != nil {
			a.logError(fmt.Sprintf("PreviewMarkdown: failed to get graph data: %v", err))
		} else {
			a.logInfo(fmt.Sprintf("PreviewMarkdown: got graph data with %d edges", len(graphData.Edges)))
			// Resolve doc_id paths from the graph's immutable in-memory snapshot.
			docMap := a.graphDocMapSnapshot()
			actualFilePath := docMap[sourceDocID]
			if actualFilePath != "" {
				a.logInfo(fmt.Sprintf("PreviewMarkdown: found file path for doc_id %s: %s", sourceDocID, actualFilePath))
			} else {
				a.logInfo(fmt.Sprintf("PreviewMarkdown: no file path found for doc_id %s in graph snapshot", sourceDocID))
			}

			// Extract links from markdown body to build a mapping
			links := a.extractLinks(markdownBody)
			a.logInfo(fmt.Sprintf("PreviewMarkdown: extracted %d links from markdown", len(links)))
			// Create a map of target node IDs to original link targets for quick lookup
			targetIDToLinkTarget := make(map[string]string) // targetID -> original link target (e.g., "file.md")
			// Use actual file path if available, otherwise use content root
			currentFilePath := actualFilePath
			if currentFilePath == "" {
				currentFilePath = "content/"
			}
			for _, link := range links {
				if link.Kind == "wikilink" || link.Kind == "markdown_link" {
					// Resolve link target using the actual file path
					targetID := a.resolveLinkTarget(link, currentFilePath)
					if targetID != "" {
						targetIDToLinkTarget[targetID] = link.Target
						a.logInfo(fmt.Sprintf("PreviewMarkdown: mapped link %s -> targetID %s (from file %s)", link.Target, targetID, currentFilePath))
					} else {
						a.logInfo(fmt.Sprintf("PreviewMarkdown: failed to resolve link %s from file %s", link.Target, currentFilePath))
					}
				}
			}

			// Find edges that reference pinned versions and have been updated
			warningCount := 0
			for _, edge := range graphData.Edges {
				a.logInfo(fmt.Sprintf("PreviewMarkdown: checking edge: SourceDocID=%s, ToVersionMode=%s, TargetUpdated=%v", edge.SourceDocID, edge.ToVersionMode, edge.TargetUpdated))
				if edge.SourceDocID == sourceDocID && edge.ToVersionMode == "pinned" && edge.TargetUpdated {
					a.logInfo(fmt.Sprintf("PreviewMarkdown: found pinned updated edge: %s -> %s (TargetDocID: %s)", edge.Source, edge.Target, edge.TargetDocID))
					// This link references a pinned version that has been updated
					// Find the corresponding HTML link and add warning

					// Get the current target path from the in-memory doc map using TargetDocID.
					targetPath := strings.TrimPrefix(edge.Target, "doc:/")
					if edge.TargetDocID != "" {
						if currentPath, exists := docMap[edge.TargetDocID]; exists {
							targetPath = strings.TrimPrefix(currentPath, "content/")
							a.logInfo(fmt.Sprintf("PreviewMarkdown: resolved target path from graph snapshot: %s -> %s (doc_id: %s)", edge.Target, targetPath, edge.TargetDocID))
						}
					}

					// Get the original link target from the markdown
					// Try both old and new target IDs
					originalLinkTarget := ""
					hasLink := false

					// First try with the edge.Target (might be old path after rename)
					if linkTarget, ok := targetIDToLinkTarget[edge.Target]; ok {
						originalLinkTarget = linkTarget
						hasLink = true
						a.logInfo(fmt.Sprintf("PreviewMarkdown: found original link target using edge.Target: %s -> %s", edge.Target, originalLinkTarget))
					} else if edge.TargetDocID != "" {
						// Try to find by constructing target ID from current path
						currentTargetID := "doc:/" + targetPath
						if linkTarget, ok := targetIDToLinkTarget[currentTargetID]; ok {
							originalLinkTarget = linkTarget
							hasLink = true
							a.logInfo(fmt.Sprintf("PreviewMarkdown: found original link target using current path: %s -> %s", currentTargetID, originalLinkTarget))
						} else {
							a.logInfo(fmt.Sprintf("PreviewMarkdown: no original link target found for %s or %s", edge.Target, currentTargetID))
						}
					} else {
						a.logInfo(fmt.Sprintf("PreviewMarkdown: no original link target found for %s", edge.Target))
					}

					// Build patterns to match the href attribute in HTML
					// The href might be the original link target, or a resolved path
					linkPatterns := []string{}
					if hasLink {
						// Add the original link target (e.g., "file.md" or "../file.md")
						linkPatterns = append(linkPatterns, regexp.QuoteMeta(originalLinkTarget))
						// Also try with .html extension
						if strings.HasSuffix(originalLinkTarget, ".md") {
							linkPatterns = append(linkPatterns, regexp.QuoteMeta(strings.TrimSuffix(originalLinkTarget, ".md")+".html"))
						}
					}
					// Add the resolved target path
					linkPatterns = append(linkPatterns, regexp.QuoteMeta(targetPath))
					if strings.HasSuffix(targetPath, ".md") {
						linkPatterns = append(linkPatterns, regexp.QuoteMeta(strings.TrimSuffix(targetPath, ".md")+".html"))
					}
					// Also try with leading slash
					linkPatterns = append(linkPatterns, regexp.QuoteMeta("/"+targetPath))
					if strings.HasSuffix(targetPath, ".md") {
						linkPatterns = append(linkPatterns, regexp.QuoteMeta("/"+strings.TrimSuffix(targetPath, ".md")+".html"))
					}

					// Also add URL-encoded versions of patterns for multibyte characters
					urlEncodedPatterns := []string{}
					for _, pattern := range linkPatterns {
						urlEncodedPatterns = append(urlEncodedPatterns, pattern)
						// Add URL-encoded version (for multibyte characters)
						// Remove regex escaping first, then URL encode, then re-escape for regex
						unescaped := strings.ReplaceAll(pattern, "\\", "")
						urlEncoded := url.QueryEscape(unescaped)
						if urlEncoded != unescaped {
							urlEncodedPatterns = append(urlEncodedPatterns, regexp.QuoteMeta(urlEncoded))
						}
					}
					linkPatterns = urlEncodedPatterns

					a.logInfo(fmt.Sprintf("PreviewMarkdown: trying %d patterns to match link (including URL-encoded)", len(linkPatterns)))
					// Debug: log first few patterns
					if len(linkPatterns) > 0 {
						maxLog := 3
						if len(linkPatterns) < maxLog {
							maxLog = len(linkPatterns)
						}
						for i := 0; i < maxLog; i++ {
							a.logInfo(fmt.Sprintf("PreviewMarkdown: pattern %d: %s", i+1, linkPatterns[i]))
						}
					}

					// Debug: extract all href attributes from HTML to see what we're matching against
					hrefRegex := regexp.MustCompile(`href=["']([^"']+)["']`)
					allHrefs := hrefRegex.FindAllStringSubmatch(html, -1)
					a.logInfo(fmt.Sprintf("PreviewMarkdown: found %d href attributes in HTML", len(allHrefs)))
					if len(allHrefs) > 0 {
						maxLog := 3
						if len(allHrefs) < maxLog {
							maxLog = len(allHrefs)
						}
						for i := 0; i < maxLog; i++ {
							if len(allHrefs[i]) > 1 {
								a.logInfo(fmt.Sprintf("PreviewMarkdown: href %d: %s", i+1, allHrefs[i][1]))
							}
						}
					}

					// Match <a> tags that link to this target and add warning
					// Use a map to track which links already have warnings to avoid duplicates
					warnedLinks := make(map[string]bool)
					for _, pattern := range linkPatterns {
						// Match <a> tag with href containing the pattern, but not already containing the warning
						linkRegex := regexp.MustCompile(`(<a[^>]+href=["']([^"']*` + pattern + `[^"']*)["'][^>]*>.*?</a>)`)
						html = linkRegex.ReplaceAllStringFunc(html, func(match string) string {
							// Check if warning already added to this link
							if strings.Contains(match, "version-warning") {
								return match
							}
							// Extract href to check if we've already warned for this link
							hrefMatch := regexp.MustCompile(`href=["']([^"']+)["']`)
							if hrefSubmatch := hrefMatch.FindStringSubmatch(match); len(hrefSubmatch) > 1 {
								href := hrefSubmatch[1]
								if warnedLinks[href] {
									// Already warned for this href, skip
									return match
								}
								warnedLinks[href] = true
							}
							// Add warning after the closing </a> tag
							// Create warning HTML with update button for this specific edge
							warningHTML := fmt.Sprintf(
								`<span class="version-warning" style="color: #ff6b6b; font-size: 0.9em; margin-left: 0.5em;">⚠️ 古いバージョンを参照しています <button class="update-to-latest-btn" data-source-doc-id="%s" data-target-doc-id="%s" style="margin-left: 0.5em; padding: 2px 8px; font-size: 0.85em; background: #4CAF50; color: white; border: none; border-radius: 3px; cursor: pointer;" onclick="updateLinkToLatest(this)">最新版に更新</button></span>`,
								edge.SourceDocID, edge.TargetDocID)
							warningCount++
							a.logInfo(fmt.Sprintf("PreviewMarkdown: added warning to link matching pattern: %s", pattern))
							return match + warningHTML
						})
					}
				}
			}
			if warningCount > 0 {
				a.logInfo(fmt.Sprintf("PreviewMarkdown: added %d warnings total", warningCount))
			} else {
				a.logInfo("PreviewMarkdown: no warnings added (no matching edges or links)")
			}
		}
	} else {
		a.logInfo("PreviewMarkdown: no sourceDocID, skipping version warning check")
	}

	// Debug: log a sample of the generated HTML to check for KaTeX processing
	if strings.Contains(html, "katex-inline") || strings.Contains(html, "katex-block") {
		// Extract a sample of KaTeX content
		inlineMatch := regexp.MustCompile(`<span class="katex-inline">([^<]+)</span>`)
		blockMatch := regexp.MustCompile(`<div class="katex-block">([^<]+)</div>`)
		if m := inlineMatch.FindStringSubmatch(html); len(m) > 1 {
			a.logInfo(fmt.Sprintf("PreviewMarkdown: Found inline math in HTML: %q", m[1]))
		}
		if m := blockMatch.FindStringSubmatch(html); len(m) > 1 {
			// Limit length to avoid huge logs
			sample := m[1]
			if len(sample) > 100 {
				sample = sample[:100] + "..."
			}
			a.logInfo(fmt.Sprintf("PreviewMarkdown: Found block math in HTML: %q", sample))
		}
	}

	html = a.applyPrintoutConfigToPreviewHTML(html, frontMatter)

	return html, nil
}

func (a *App) rewritePreviewImageSources(htmlContent, currentPath string) string {
	imgPathRegex := regexp.MustCompile(`(<img[^>]+src=["'])([^"']+)(["'][^>]*>)`)
	return imgPathRegex.ReplaceAllStringFunc(htmlContent, func(match string) string {
		parts := imgPathRegex.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		urlPath, ok := a.resolvePreviewImageURL(parts[2], currentPath)
		if !ok {
			return match
		}
		return parts[1] + urlPath + parts[3]
	})
}

func (a *App) resolvePreviewImageURL(imagePath, currentPath string) (string, bool) {
	imagePath = strings.TrimSpace(strings.ReplaceAll(imagePath, "\\", "/"))
	if imagePath == "" ||
		strings.HasPrefix(imagePath, "#") ||
		strings.HasPrefix(imagePath, "/image/") ||
		strings.HasPrefix(imagePath, "data:") ||
		strings.HasPrefix(imagePath, "blob:") ||
		strings.HasPrefix(imagePath, "http://") ||
		strings.HasPrefix(imagePath, "https://") ||
		strings.HasPrefix(imagePath, "//") {
		return "", false
	}

	parsed, err := url.Parse(imagePath)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" {
		return "", false
	}
	pathPart := parsed.Path
	if unescaped, err := url.PathUnescape(pathPart); err == nil {
		pathPart = unescaped
	}
	pathPart = strings.TrimPrefix(strings.ReplaceAll(pathPart, "\\", "/"), "/")

	relPath := ""
	switch {
	case strings.HasPrefix(pathPart, "data/image/"), strings.HasPrefix(pathPart, "content/"):
		relPath = pathPart
	case currentPath != "" && strings.HasPrefix(filepath.ToSlash(currentPath), "content/"):
		baseDir := filepath.Dir(filepath.ToSlash(currentPath))
		relPath = filepath.ToSlash(filepath.Clean(filepath.Join(baseDir, pathPart)))
	default:
		return "", false
	}

	if relPath == "." || strings.HasPrefix(relPath, "../") || strings.Contains(relPath, "/../") {
		return "", false
	}
	absPath := filepath.Join(a.dataDir, filepath.FromSlash(relPath))
	if info, err := os.Stat(absPath); err != nil || info.IsDir() {
		return "", false
	}

	parsed.Path = "/image/" + relPath
	parsed.RawPath = ""
	return parsed.String(), true
}

// BuildSite builds the static site
func (a *App) BuildSite() error {
	return a.build(a.siteBuildRoot())
}

// InitProject initializes a new Karte project
func (a *App) InitProject() error {
	return a.initProject(a.root)
}

// resolveContentPath safely resolves a content path
func (a *App) resolveContentPath(rel string) (string, bool) {
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "/")
	if !strings.HasPrefix(rel, "content/") {
		return "", false
	}
	abs := filepath.Join(a.dataDir, filepath.FromSlash(rel))
	canonical, err := filepath.Abs(abs)
	if err != nil {
		return "", false
	}
	contentRoot, _ := filepath.Abs(filepath.Join(a.dataDir, "content"))
	relToContent, err := filepath.Rel(contentRoot, canonical)
	if err != nil {
		return "", false
	}
	if filepath.IsAbs(relToContent) {
		return "", false
	}
	slashRel := filepath.ToSlash(relToContent)
	if slashRel == ".." || strings.HasPrefix(slashRel, "../") {
		return "", false
	}
	return canonical, true
}

func boardPathForResource(path string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if strings.HasSuffix(strings.ToLower(normalized), ".board.md") {
		return normalized
	}
	ext := filepath.Ext(normalized)
	if ext == "" {
		return normalized + ".board.md"
	}
	base := strings.TrimSuffix(normalized, ext)
	return base + ".board.md"
}

func resourceCardTitle(path string) string {
	base := filepath.Base(path)
	ext := filepath.Ext(base)
	if ext == "" {
		return base
	}
	return strings.TrimSuffix(base, ext)
}

// build implements the Build function from runner package
func (a *App) build(root string) error {
	_, err := a.getSiteBuilder().BuildFull(context.Background(), root)
	return err
}

// initProject implements the InitProject function from runner package
func (a *App) initProject(root string) error {
	paths := []string{".mdsys", "content", "data", filepath.Join("themes", "default")}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			return err
		}
	}
	// For now, we'll create basic skeleton files
	// In a full implementation, you'd copy from embedded skeleton files
	return nil
}

func isSupportedImageExt(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	_, ok := supportedImageExt[ext]
	return ok
}

func sanitizeImageBaseName(name string) string {
	base := audio.SanitizeFileName(name)
	if base == "" || base == "audio" {
		return "image"
	}
	return base
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func (a *App) findOriginalImagePath(webpRelPath string) string {
	if webpRelPath == "" {
		return ""
	}
	base := strings.TrimSuffix(webpRelPath, ".webp")
	for _, ext := range backupImageExtCandidates {
		candidate := base + ext
		abs := filepath.Join(a.dataDir, filepath.FromSlash(candidate))
		if fileExists(abs) {
			return filepath.ToSlash(candidate)
		}
	}
	sourceCandidate := base + "_source.webp"
	abs := filepath.Join(a.dataDir, filepath.FromSlash(sourceCandidate))
	if fileExists(abs) {
		return filepath.ToSlash(sourceCandidate)
	}
	return ""
}

func (a *App) resolveImagePath(imagePath string) (string, string, error) {
	if imagePath == "" {
		return "", "", fmt.Errorf("image path is empty")
	}

	if filepath.IsAbs(imagePath) {
		rel, err := filepath.Rel(a.dataDir, imagePath)
		if err != nil {
			return "", "", fmt.Errorf("resolve image path: %w", err)
		}
		if strings.HasPrefix(rel, "..") {
			return "", "", fmt.Errorf("image path resolves outside data directory")
		}
		return filepath.ToSlash(rel), imagePath, nil
	}

	cleanRel := filepath.ToSlash(filepath.Clean(imagePath))
	if strings.HasPrefix(cleanRel, "..") {
		return "", "", fmt.Errorf("image path resolves outside data directory")
	}
	abs := filepath.Join(a.dataDir, filepath.FromSlash(cleanRel))
	return cleanRel, abs, nil
}

func metadataPathFromImage(imageRelPath string) string {
	ext := filepath.Ext(imageRelPath)
	return strings.TrimSuffix(imageRelPath, ext) + ".yaml"
}

// extractTagsFromMetadata extracts tags from metadata map
// Supports both string (comma-separated) and array formats
func (a *App) extractTagsFromMetadata(metadataMap map[string]interface{}) []string {
	if metadataMap == nil {
		return []string{}
	}

	tagsValue, exists := metadataMap["tags"]
	if !exists {
		return []string{}
	}

	var tagsStr string
	switch v := tagsValue.(type) {
	case string:
		tagsStr = v
	case []interface{}:
		// Convert array to comma-separated string
		var tagStrs []string
		for _, item := range v {
			if str, ok := item.(string); ok {
				tagStrs = append(tagStrs, str)
			}
		}
		tagsStr = strings.Join(tagStrs, ",")
	default:
		return []string{}
	}

	// Normalize tags using frontmatter package
	return fm.NormalizeTags(tagsStr)
}

// SaveEventLogs validates and appends a frontend event log snapshot．
func (a *App) SaveEventLogs(logsJson string) (bool, error) {
	if a == nil {
		return false, fmt.Errorf("app is nil")
	}
	if err := defaultEventLogStore.append(a.dataDir, logsJson); err != nil {
		return false, err
	}
	return true, nil
}

func (a *App) startTranscriptionJob(absAudioPath, relAudioPath string) {
	manager := a.getJobManager()
	if manager == nil {
		a.logInfo(fmt.Sprintf("ASR transcription was not queued during shutdown: %s", relAudioPath))
		return
	}
	submission := manager.Submit(managedJob{
		Category: appJobCategoryASRHeavy,
		Group:    appJobGroupAudioImport,
		Key:      filepath.ToSlash(relAudioPath),
		Priority: jobPriorityHigh,
		Coalesce: jobKeepExisting,
		Run: func(jobContext context.Context) error {
			ctx, cancel := context.WithTimeout(jobContext, 15*time.Minute)
			defer cancel()
			lease, err := a.acquireASRResource(ctx)
			if errors.Is(err, errASRResourceDisabled) {
				a.logInfo("ASR service not configured; skipping transcription")
				return nil
			}
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				a.logError(fmt.Sprintf("ASR failed to initialize for %s: %v", relAudioPath, err))
				a.emitEvent("audio-transcribed", map[string]interface{}{
					"audioPath": relAudioPath,
					"error":     err.Error(),
				})
				return err
			}
			defer lease.Release()

			transcriptPath, err := a.startStreamingTranscription(ctx, lease.Service(), absAudioPath, relAudioPath)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				a.logError(fmt.Sprintf("ASR failed for %s: %v", relAudioPath, err))
				a.emitEvent("audio-transcribed", map[string]interface{}{
					"audioPath": relAudioPath,
					"error":     err.Error(),
				})
				return err
			}

			a.emitEvent("audio-transcribed", map[string]interface{}{
				"audioPath":      relAudioPath,
				"transcriptPath": transcriptPath,
			})
			return nil
		},
	})
	switch submission.Status {
	case jobAccepted, jobDeduplicated:
		return
	case jobRejectedFull:
		err := "ASR transcription queue is full; retry the audio import after current transcription finishes"
		a.logError(fmt.Sprintf("%s: %s", err, relAudioPath))
		a.emitEvent("audio-transcribed", map[string]interface{}{
			"audioPath": relAudioPath,
			"error":     err,
		})
	case jobRejectedClosed, jobRejectedCanceled:
		a.logInfo(fmt.Sprintf("ASR transcription was canceled during shutdown: %s", relAudioPath))
	default:
		a.logError(fmt.Sprintf("ASR transcription was rejected for %s: %v", relAudioPath, submission.Err))
	}
}

func (a *App) startStreamingTranscription(ctx context.Context, svc appASRService, absAudioPath, relAudioPath string) (transcriptPath string, resultErr error) {
	transcriptionCtx, cancelTranscription := context.WithCancel(ctx)
	defer cancelTranscription()
	var transcriptFailureMu sync.Mutex
	var transcriptFailure error
	recordTranscriptFailure := func(err error) {
		if err == nil {
			return
		}
		transcriptFailureMu.Lock()
		firstFailure := transcriptFailure == nil
		if firstFailure {
			transcriptFailure = err
		}
		transcriptFailureMu.Unlock()
		if firstFailure {
			cancelTranscription()
		}
	}
	currentTranscriptFailure := func() error {
		transcriptFailureMu.Lock()
		defer transcriptFailureMu.Unlock()
		return transcriptFailure
	}
	transcriptPath, buffer, err := a.writeTranscriptDocument(relAudioPath, "", nil, recordTranscriptFailure)
	if err != nil {
		return "", err
	}
	defer func() {
		resultErr = errors.Join(resultErr, currentTranscriptFailure(), a.completeTranscriptBuffer(buffer))
	}()

	progressHandler := func(line string, segmentIndex, totalSegments int, timestamp float64) {
		if transcriptionCtx.Err() != nil {
			return
		}
		// Format timestamp as [HH:MM:SS] or [MM:SS]
		timestampStr := formatTimestamp(timestamp)
		timestampedLine := fmt.Sprintf("%s %s", timestampStr, line)

		if err := buffer.AppendFinalAndEmit(timestampedLine, func() {
			a.emitEvent("audio-transcribe-progress", map[string]interface{}{
				"audioPath":      relAudioPath,
				"transcriptPath": transcriptPath,
				"text":           line,
				"segmentIndex":   segmentIndex,
				"totalSegments":  totalSegments,
				"timestamp":      timestamp,
			})
		}); err != nil {
			recordTranscriptFailure(err)
		}
	}

	if svc == nil {
		return transcriptPath, fmt.Errorf("ASR service is not initialized")
	}
	text, err := svc.TranscribeFile(transcriptionCtx, absAudioPath, progressHandler)
	if err != nil {
		return transcriptPath, err
	}
	if failure := currentTranscriptFailure(); failure != nil {
		return transcriptPath, failure
	}
	if ctx.Err() != nil {
		return transcriptPath, ctx.Err()
	}

	if strings.TrimSpace(text) == "" {
		if err := buffer.AppendFinal("_（ASRから有効な結果が得られませんでした）_"); err != nil {
			return transcriptPath, err
		}
	}
	return transcriptPath, nil
}

func (a *App) writeTranscriptDocument(
	audioRelPath string,
	transcript string,
	partialEmit func(transcriptPartialPayload),
	onError func(error),
) (string, *transcriptBuffer, error) {
	baseName := audio.SanitizeFileName(strings.TrimSuffix(filepath.Base(audioRelPath), filepath.Ext(audioRelPath)))
	if baseName == "" {
		baseName = fmt.Sprintf("audio-%s", time.Now().Format("20060102-150405"))
	}

	dirRel := filepath.ToSlash(filepath.Join("content", "transcripts"))
	body := a.composeTranscriptMarkdown(audioRelPath, transcript)
	for suffix := 1; suffix <= 10_000; suffix++ {
		filename := baseName + ".md"
		if suffix > 1 {
			filename = fmt.Sprintf("%s-%d.md", baseName, suffix)
		}
		contentRel := filepath.ToSlash(filepath.Join(dirRel, filename))
		absPath, ok := a.resolveContentPath(contentRel)
		if !ok {
			return "", nil, fmt.Errorf("invalid transcript path: %s", contentRel)
		}
		if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
			return "", nil, fmt.Errorf("prepare transcript dir: %w", err)
		}
		buffer, err := a.createTranscriptDocumentAndBuffer(contentRel, body, partialEmit, onError)
		if errors.Is(err, errTranscriptPathExists) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return contentRel, buffer, nil
	}
	return "", nil, fmt.Errorf("transcript identity space exhausted for %s", baseName)
}

func (a *App) composeTranscriptMarkdown(audioRelPath, transcript string) string {
	now := time.Now().Format(time.RFC3339)
	return fmt.Sprintf(`---
title: %q
tags:
  - transcript
  - audio
created_at: %s
audio_path: %q
---

## Transcript

%s
`, fmt.Sprintf("Transcript %s", filepath.Base(audioRelPath)), now, audioRelPath, strings.TrimSpace(transcript))
}

// ASRStatus represents the current status of the ASR service
type ASRStatus struct {
	Initialized  bool `json:"initialized"`
	Initializing bool `json:"initializing"`
}

// GetASRStatus returns the current initialization status of the ASR service
func (a *App) GetASRStatus() ASRStatus {
	status := a.currentASRResourceManager().Status()
	return ASRStatus{
		Initialized:  status.Loaded,
		Initializing: status.Loading,
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// GetConnectedPeers returns the list of connected peers (disabled for now - will be implemented with git integration)
func (a *App) GetConnectedPeers() []syncpkg.Peer {
	// TODO: Implement with git integration
	return []syncpkg.Peer{}
}

// ConnectToPeer connects to a peer (disabled for now - will be implemented with git integration)
func (a *App) ConnectToPeer(address string, port int) error {
	// TODO: Implement with git integration
	return fmt.Errorf("file sharing not implemented yet - will be available with git integration")
}

// DisconnectFromPeer disconnects from a peer (disabled for now - will be implemented with git integration)
func (a *App) DisconnectFromPeer(peerID string) error {
	// TODO: Implement with git integration
	return fmt.Errorf("file sharing not implemented yet - will be available with git integration")
}

// ExportPreviewHTML saves given HTML into karte_data/export and returns a file URL
func (a *App) ExportPreviewHTML(html string) (string, error) {
	if html == "" {
		return "", fmt.Errorf("empty html")
	}
	exportDir := filepath.Join(a.dataDir, "export")
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create export dir: %v", err)
	}
	filename := fmt.Sprintf("preview-%s.html", time.Now().Format("20060102-150405"))
	fp := filepath.Join(exportDir, filename)
	if err := os.WriteFile(fp, []byte(html), 0644); err != nil {
		return "", fmt.Errorf("failed to write export file: %v", err)
	}
	a.logInfo(fmt.Sprintf("Exported preview HTML: %s", fp))
	// Build file URL
	url := "file://" + filepath.ToSlash(fp)
	return url, nil
}

// ExportPDF renders given HTML as PDF to karte_data/export and returns the path
func (a *App) ExportPDF(html string) (pdfURL string, err error) {
	if html == "" {
		return "", fmt.Errorf("empty html")
	}

	defer func() {
		if r := recover(); r != nil {
			panicMsg := fmt.Sprintf("PDF export panic: %v", r)
			a.logError(fmt.Sprintf("ExportPDF panic recovered: %v", r))
			runtime.EventsEmit(a.ctx, "pdf-export-error", map[string]interface{}{
				"error": panicMsg,
			})
			err = fmt.Errorf("%s", panicMsg)
		}
	}()

	pdfPath, err := a.exportPDFInternal(html)
	if err != nil {
		a.logError(fmt.Sprintf("ExportPDF failed: %v", err))
		runtime.EventsEmit(a.ctx, "pdf-export-error", map[string]interface{}{
			"error": err.Error(),
		})
		return "", err
	}

	info, err := os.Stat(pdfPath)
	if err != nil {
		a.logError(fmt.Sprintf("ExportPDF: Failed to stat PDF file: %v", err))
		runtime.EventsEmit(a.ctx, "pdf-export-error", map[string]interface{}{
			"error": fmt.Sprintf("Failed to stat PDF file: %v", err),
		})
		return "", err
	}

	a.logInfo(fmt.Sprintf("PDF exported: %s (size: %d bytes)", pdfPath, info.Size()))
	url := strings.ReplaceAll(pdfPath, "\\", "/")
	runtime.EventsEmit(a.ctx, "pdf-export-completed", map[string]interface{}{
		"pdfPath": url,
		"size":    info.Size(),
	})

	if err := a.openPDFInViewer(pdfPath); err != nil {
		a.logError(fmt.Sprintf("PDF open failed: %v", err))
		runtime.EventsEmit(a.ctx, "pdf-open-error", map[string]interface{}{
			"error": err.Error(),
		})
	}

	return url, nil
}

func (a *App) openPDFInViewer(pdfPath string) error {
	pdfPath = strings.TrimSpace(pdfPath)
	if pdfPath == "" {
		return fmt.Errorf("empty pdf path")
	}
	if goruntime.GOOS != "darwin" {
		return nil
	}
	pathToOpen, err := filepath.Abs(filepath.Clean(pdfPath))
	if err != nil {
		return fmt.Errorf("abs pdf: %w", err)
	}
	info, err := os.Stat(pathToOpen)
	if err != nil {
		return fmt.Errorf("stat pdf: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("pdf path is directory: %s", pathToOpen)
	}
	if strings.ToLower(filepath.Ext(pathToOpen)) != ".pdf" {
		return fmt.Errorf("not a pdf file: %s", pathToOpen)
	}
	a.logInfo(fmt.Sprintf("Opening PDF with default app: %s", pathToOpen))
	cmd := exec.Command("open", pathToOpen)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open pdf: %w", err)
	}
	return nil
}

// exportPDFInternal performs the actual PDF export work
func (a *App) exportPDFInternal(html string) (string, error) {
	html = a.injectPDFRenderHelpers(html)
	printoutSpec := printout.ParseFromHTML(html)
	pageDOMCount := countPrintPageSections(html)
	readyMeta := extractMetaContent(html, "karte-printout-ready")
	errorMeta := extractMetaContent(html, "karte-printout-error")
	pagesMeta := extractMetaContent(html, "karte-printout-pages")
	a.logInfo(fmt.Sprintf("ExportPDF: resolved printout=%s (finite=%v, pageDOMCount=%d, readyMeta=%q, pageMeta=%q, errorMeta=%q, htmlLen=%d)", printoutSpec.Name, !printoutSpec.Infinite, pageDOMCount, readyMeta, pagesMeta, errorMeta, len(html)))
	// Convert image URLs to data URIs for PDF export
	// WKWebView cannot access HTTP URLs, so we need to embed images as data URIs
	// Track temporary files for cleanup
	var tempFiles []string
	defer func() {
		// Clean up all temporary files
		for _, tmpFile := range tempFiles {
			if err := os.Remove(tmpFile); err != nil {
				a.logError(fmt.Sprintf("Failed to remove temp file: %s, error: %v", tmpFile, err))
			} else {
				a.logInfo(fmt.Sprintf("Cleaned up temp file: %s", tmpFile))
			}
		}
	}()

	// 画像変換の進捗を追跡するため、まず画像の数をカウント
	originalHTMLSize := len(html)
	imgRegex := regexp.MustCompile(`(<img[^>]+src=["'])([^"']+)(["'][^>]*>)`)
	matches := imgRegex.FindAllString(html, -1)
	totalImages := 0
	for _, match := range matches {
		parts := imgRegex.FindStringSubmatch(match)
		if len(parts) >= 4 {
			imgURL := parts[2]
			if strings.HasPrefix(imgURL, "/image/") || strings.HasPrefix(imgURL, "data/image/") {
				totalImages++
			}
		}
	}

	a.logInfo(fmt.Sprintf("PDF export: Starting image conversion (original HTML size: %d bytes, images: %d)", originalHTMLSize, totalImages))

	// 画像変換開始イベント
	if totalImages > 0 {
		emitPDFExportEvent(a.ctx, "pdf-export-progress", map[string]interface{}{
			"currentImage": 0,
			"totalImages":  totalImages,
			"htmlSize":     len(html),
			"stage":        "converting-images",
		})
	}

	conversionStartTime := time.Now()
	html, tempFiles = a.convertImageURLsToDataURIs(html, totalImages)
	conversionDuration := time.Since(conversionStartTime)

	// HTMLサイズの監視と警告
	finalHTMLSize := len(html)
	sizeIncrease := finalHTMLSize - originalHTMLSize
	a.logInfo(fmt.Sprintf("PDF export: Image conversion completed (duration: %v, original HTML: %d bytes, final HTML: %d bytes, increase: %d bytes)", conversionDuration, originalHTMLSize, finalHTMLSize, sizeIncrease))

	// デバッグ: 変換後のHTMLの内容を確認
	imgTagCount := strings.Count(html, "<img")
	dataImageCount := strings.Count(html, "data:image")
	a.logInfo(fmt.Sprintf("PDF export: DEBUG - After conversion: img tags=%d, data:image occurrences=%d", imgTagCount, dataImageCount))

	// HTMLの最初の2000文字をログに出力（画像部分が含まれる可能性が高い）
	htmlPreview := html
	if len(htmlPreview) > 2000 {
		htmlPreview = htmlPreview[:2000] + "...(truncated)"
	}
	a.logInfo(fmt.Sprintf("PDF export: DEBUG - HTML preview (first 2000 chars):\n%s", htmlPreview))

	// data:imageが含まれている部分を抽出してログに出力
	dataImageRegex := regexp.MustCompile(`data:image[^"']+`)
	dataImageMatches := dataImageRegex.FindAllString(html, 10) // 最初の10個
	if len(dataImageMatches) > 0 {
		for i, match := range dataImageMatches {
			preview := match
			if len(preview) > 200 {
				preview = preview[:200] + "...(truncated)"
			}
			a.logInfo(fmt.Sprintf("PDF export: DEBUG - data:image[%d] preview: %s", i, preview))
		}
	} else {
		a.logInfo("PDF export: DEBUG - No data:image found in converted HTML!")
	}

	if finalHTMLSize > 1024*1024 {
		a.logError(fmt.Sprintf("PDF export: WARNING - HTML size exceeds 1MB (%d bytes)", finalHTMLSize))
	}
	if finalHTMLSize > 2*1024*1024 {
		a.logError(fmt.Sprintf("PDF export: ERROR - HTML size exceeds 2MB (%d bytes), PDF generation may fail", finalHTMLSize))
	}

	exportDir := filepath.Join(a.dataDir, "export")
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create export dir: %v", err)
	}
	base := fmt.Sprintf("export-%s.pdf", time.Now().Format("20060102-150405"))
	pdfPath := filepath.Join(exportDir, base)
	a.logInfo(fmt.Sprintf("ExportPDF start: out=%s html.len=%d", pdfPath, len(html)))

	// WKWebView読み込み開始イベント
	emitPDFExportEvent(a.ctx, "pdf-export-progress", map[string]interface{}{
		"currentImage": totalImages,
		"totalImages":  totalImages,
		"htmlSize":     len(html),
		"stage":        "loading-webview",
	})

	a.logInfo("ExportPDF: Calling KarteRenderer.ExportHTMLPDF...")
	if err := exportHTMLToPDFWithRenderer(a.ctx, html, pdfPath); err != nil {
		a.logError(fmt.Sprintf("ExportPDF failed: %v", err))
		return "", fmt.Errorf("PDF export failed: %w", err)
	}
	a.logInfo("ExportPDF: KarteRenderer.ExportHTMLPDF returned successfully")

	// PDF生成完了イベント
	emitPDFExportEvent(a.ctx, "pdf-export-progress", map[string]interface{}{
		"currentImage": totalImages,
		"totalImages":  totalImages,
		"htmlSize":     len(html),
		"stage":        "generating-pdf",
	})

	// Verify that the PDF file was actually created
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		a.logError(fmt.Sprintf("ExportPDF: PDF file was not created at %s", pdfPath))
		return "", fmt.Errorf("PDF file was not created: %s", pdfPath)
	}

	// Get file info to verify it's not empty
	info, err := os.Stat(pdfPath)
	if err != nil {
		a.logError(fmt.Sprintf("ExportPDF: Failed to stat PDF file: %v", err))
		return "", fmt.Errorf("failed to stat PDF file: %w", err)
	}
	if info.Size() == 0 {
		a.logError(fmt.Sprintf("ExportPDF: PDF file is empty (0 bytes) at %s", pdfPath))
		return "", fmt.Errorf("PDF file is empty: %s", pdfPath)
	}

	return pdfPath, nil
}

func exportHTMLToPDFWithRenderer(ctx context.Context, html, pdfPath string) error {
	tmp, err := os.CreateTemp("", "karte-renderer-*.html")
	if err != nil {
		return fmt.Errorf("create renderer HTML input: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.WriteString(html); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write renderer HTML input: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close renderer HTML input: %w", err)
	}

	return exportRendererHTMLPDF(ctx, tmpPath, pdfPath, karterenderer.PDFOptions{
		Engine:          "auto",
		AllowLocalFiles: true,
	})
}

func (a *App) injectPDFRenderHelpers(html string) string {
	html = a.ensurePDFRenderAssets(html)
	if strings.Contains(html, "karte-pdf-enhancers") {
		return html
	}
	script := `<script id="karte-pdf-enhancers">
(function() {
  function decodeHtmlEntities(text) {
    var textarea = document.createElement('textarea');
    textarea.innerHTML = text;
    return textarea.value;
  }
  function renderKaTeX() {
    if (typeof katex === 'undefined') return;
    document.querySelectorAll('.katex-inline').forEach(function(el) {
      if (el.querySelector('.katex')) return;
      var raw = el.innerHTML.trim();
      if (!raw) return;
      var math = decodeHtmlEntities(raw);
      try { katex.render(math, el, { throwOnError: false, displayMode: false }); } catch (e) {}
    });
    document.querySelectorAll('.katex-block').forEach(function(el) {
      if (el.querySelector('.katex')) return;
      var raw = el.innerHTML.trim();
      if (!raw) return;
      var math = decodeHtmlEntities(raw);
      try { katex.render(math, el, { throwOnError: false, displayMode: true }); } catch (e) {}
    });
  }
  function convertMermaidCodeBlocks() {
    var codeBlocks = document.querySelectorAll('pre > code.language-mermaid, pre > code.lang-mermaid');
    codeBlocks.forEach(function(code) {
      var pre = code.parentElement;
      if (!pre) return;
      var container = document.createElement('div');
      container.className = 'mermaid';
      container.textContent = code.textContent || '';
      pre.replaceWith(container);
    });
  }
  function renderMermaid() {
    if (typeof mermaid === 'undefined') return;
    convertMermaidCodeBlocks();
    var nodes = document.querySelectorAll('.mermaid:not([data-processed])');
    if (nodes.length === 0) return;
    try {
      mermaid.initialize({
        startOnLoad: false,
        securityLevel: 'loose',
        htmlLabels: true,
        flowchart: { htmlLabels: true },
        sequence: { htmlLabels: true }
      });
    } catch (e) {}
    try { mermaid.run({ nodes: nodes }); } catch (e) {}
  }
  function runAll() {
    renderKaTeX();
    renderMermaid();
  }
  function schedule() {
    var attempts = 0;
    var timer = setInterval(function() {
      attempts++;
      runAll();
      if (attempts > 200) {
        clearInterval(timer);
      }
      if (typeof katex !== 'undefined' && typeof mermaid !== 'undefined') {
        clearInterval(timer);
      }
    }, 50);
  }
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', schedule);
  } else {
    schedule();
  }
})();
</script>`
	if strings.Contains(html, "</body>") {
		return strings.Replace(html, "</body>", script+"</body>", 1)
	}
	if strings.Contains(html, "</html>") {
		return strings.Replace(html, "</html>", script+"</html>", 1)
	}
	return html + script
}

func (a *App) ensurePDFRenderAssets(html string) string {
	const (
		mermaidCDN = "https://cdn.jsdelivr.net/npm/mermaid/dist/mermaid.min.js"
		katexCSS   = "https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.css"
		katexJS    = "https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.js"
	)

	var inserts []string
	if !strings.Contains(html, mermaidCDN) {
		inserts = append(inserts, `<script src="`+mermaidCDN+`"></script>`)
	}
	if !strings.Contains(html, katexCSS) {
		inserts = append(inserts, `<link rel="stylesheet" href="`+katexCSS+`">`)
	}
	if !strings.Contains(html, katexJS) {
		inserts = append(inserts, `<script src="`+katexJS+`"></script>`)
	}
	if len(inserts) == 0 {
		return html
	}

	injection := strings.Join(inserts, "\n")
	if strings.Contains(html, "</head>") {
		return strings.Replace(html, "</head>", injection+"\n</head>", 1)
	}
	if strings.Contains(html, "<head>") {
		return strings.Replace(html, "<head>", "<head>\n"+injection+"\n", 1)
	}
	if strings.Contains(strings.ToLower(html), "<html") {
		re := regexp.MustCompile(`(?i)<html([^>]*)>`)
		return re.ReplaceAllString(html, `<html$1><head>`+injection+`</head>`)
	}
	if strings.Contains(strings.ToLower(html), "<!doctype html>") {
		re := regexp.MustCompile(`(?i)<!doctype html>`)
		return re.ReplaceAllString(html, "<!doctype html>\n<head>\n"+injection+"\n</head>")
	}
	return injection + "\n" + html
}

func (a *App) applyPrintoutConfigToPreviewHTML(html string, frontMatter *fm.FrontMatter) string {
	spec := printout.Resolve("")
	raw := ""
	if frontMatter != nil {
		raw = strings.TrimSpace(frontMatter.Printout)
		spec = printout.Resolve(raw)
	}
	if raw != "" && spec.Name == printout.Infinite && !strings.EqualFold(raw, printout.Infinite) {
		a.logInfo(fmt.Sprintf("PreviewMarkdown: unknown printout %q, fallback to infinite", raw))
	}
	html = upsertPrintoutMeta(html, spec.Name)
	html = setHTMLDataPrintoutAttr(html, spec.Name)
	if spec.Infinite {
		return html
	}
	return injectPrintoutLayout(html, spec)
}

func upsertPrintoutMeta(html, printoutName string) string {
	meta := fmt.Sprintf(`<meta name="karte-printout" content="%s">`, printoutName)
	metaRe := regexp.MustCompile(`(?i)<meta[^>]+name=["']karte-printout["'][^>]*>`)
	if metaRe.MatchString(html) {
		return metaRe.ReplaceAllString(html, meta)
	}
	return injectIntoHead(html, meta)
}

func setHTMLDataPrintoutAttr(html, printoutName string) string {
	htmlTagRe := regexp.MustCompile(`(?i)<html([^>]*)>`)
	if htmlTagRe.MatchString(html) {
		return htmlTagRe.ReplaceAllStringFunc(html, func(tag string) string {
			attrRe := regexp.MustCompile(`(?i)\sdata-printout=["'][^"']*["']`)
			if attrRe.MatchString(tag) {
				return attrRe.ReplaceAllString(tag, fmt.Sprintf(` data-printout="%s"`, printoutName))
			}
			return strings.TrimSuffix(tag, ">") + fmt.Sprintf(` data-printout="%s">`, printoutName)
		})
	}
	return `<html data-printout="` + printoutName + `">` + html + `</html>`
}

func injectIntoHead(html, fragment string) string {
	if strings.Contains(html, "</head>") {
		return strings.Replace(html, "</head>", fragment+"\n</head>", 1)
	}
	if strings.Contains(html, "<head>") {
		return strings.Replace(html, "<head>", "<head>\n"+fragment+"\n", 1)
	}
	if strings.Contains(strings.ToLower(html), "<html") {
		re := regexp.MustCompile(`(?i)<html([^>]*)>`)
		return re.ReplaceAllString(html, `<html$1><head>`+fragment+`</head>`)
	}
	if strings.Contains(strings.ToLower(html), "<!doctype html>") {
		re := regexp.MustCompile(`(?i)<!doctype html>`)
		return re.ReplaceAllString(html, "<!doctype html>\n<head>\n"+fragment+"\n</head>")
	}
	return "<head>\n" + fragment + "\n</head>\n" + html
}

func countPrintPageSections(html string) int {
	re := regexp.MustCompile(`(?is)<section[^>]*class=["'][^"']*\bkarte-print-page\b[^"']*["'][^>]*>`)
	return len(re.FindAllStringIndex(html, -1))
}

func extractMetaContent(html, name string) string {
	pattern := fmt.Sprintf(`(?is)<meta[^>]*name=["']%s["'][^>]*content=["']([^"']*)["'][^>]*>`, regexp.QuoteMeta(name))
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(html)
	if len(m) >= 2 {
		return m[1]
	}
	return ""
}

func injectPrintoutLayout(html string, spec printout.Spec) string {
	styleTagRe := regexp.MustCompile(`(?is)<style[^>]*id=["']karte-printout-style["'][^>]*>.*?</style>`)
	scriptTagRe := regexp.MustCompile(`(?is)<script[^>]*id=["']karte-printout-pagination["'][^>]*>.*?</script>`)
	html = styleTagRe.ReplaceAllString(html, "")
	html = scriptTagRe.ReplaceAllString(html, "")

	contentWidthMM := spec.WidthMM - 24.0
	contentHeightMM := spec.HeightMM - 24.0
	if contentWidthMM < 10 {
		contentWidthMM = spec.WidthMM
	}
	if contentHeightMM < 10 {
		contentHeightMM = spec.HeightMM
	}

	style := fmt.Sprintf(`<style id="karte-printout-style">
:root {
  --karte-print-page-width: %.3gmm;
  --karte-print-page-height: %.3gmm;
  --karte-print-content-width: %.3gmm;
  --karte-print-content-height: %.3gmm;
}
@page {
  size: %.3gmm %.3gmm;
  margin: 0;
}
html[data-printout]:not([data-printout="infinite"]) main.container {
  max-width: none !important;
  margin: 0 auto !important;
  padding: 16px !important;
}
html[data-printout]:not([data-printout="infinite"]) article {
  background: transparent !important;
  border: 0 !important;
  border-radius: 0 !important;
  padding: 0 !important;
  inline-size: var(--karte-print-page-width);
  max-inline-size: var(--karte-print-page-width);
}
html[data-printout]:not([data-printout="infinite"]) .karte-print-pages {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
}
html[data-printout]:not([data-printout="infinite"]) .karte-print-page {
  width: var(--karte-print-page-width);
  height: var(--karte-print-page-height);
  box-sizing: border-box;
  background: #fff;
  color: inherit;
  overflow: hidden;
  border: 1px solid #ddd;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12);
  break-after: page;
  page-break-after: always;
}
html[data-printout]:not([data-printout="infinite"]) .karte-print-page:last-child {
  break-after: auto;
  page-break-after: auto;
}
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content {
  box-sizing: border-box;
  inline-size: 100%%;
  block-size: 100%%;
  padding: 12mm;
  overflow: hidden;
}
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content * {
  box-sizing: border-box;
  max-inline-size: 100%% !important;
}
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content p,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content li,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content h1,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content h2,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content h3,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content h4,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content h5,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content h6,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content td,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content th {
  overflow-wrap: anywhere;
  word-break: break-word;
}
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content img,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content table,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content pre,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content code {
  max-inline-size: 100%% !important;
}
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content table,
html[data-printout]:not([data-printout="infinite"]) .karte-print-page-content pre {
  overflow-x: auto;
}
@media print {
  html[data-printout]:not([data-printout="infinite"]) body {
    background: #fff !important;
  }
  html[data-printout]:not([data-printout="infinite"]) main.container {
    margin: 0 !important;
    padding: 0 !important;
  }
  html[data-printout]:not([data-printout="infinite"]) .karte-print-pages {
    gap: 0;
  }
  html[data-printout]:not([data-printout="infinite"]) .karte-print-page {
    margin: 0;
    border: 0;
    box-shadow: none;
  }
}
</style>`, spec.WidthMM, spec.HeightMM, contentWidthMM, contentHeightMM, spec.WidthMM, spec.HeightMM)

	script := `<script id="karte-printout-pagination">
` + kartePrintoutPaginationRuntime + `
window.__karteRunPrintoutPagination && window.__karteRunPrintoutPagination(window.document);
</script>`

	html = injectIntoHead(html, style)
	if strings.Contains(html, "</body>") {
		return strings.Replace(html, "</body>", script+"\n</body>", 1)
	}
	return html + "\n" + script
}

// convertImageURLsToDataURIs converts image URLs in HTML to data URIs
// This is necessary for PDF export because WKWebView cannot access HTTP URLs
// Returns the converted HTML and a list of temporary files that need to be cleaned up
func (a *App) convertImageURLsToDataURIs(html string, totalImages int) (string, []string) {
	var tempFiles []string
	var currentImage int
	var totalDataURISize int64 // 総Data URIサイズを追跡
	// Match img tags with src attributes
	imgRegex := regexp.MustCompile(`(<img[^>]+src=["'])([^"']+)(["'][^>]*>)`)

	convertedHTML := imgRegex.ReplaceAllStringFunc(html, func(match string) string {
		parts := imgRegex.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}

		prefix := parts[1]
		imgURL := parts[2]
		suffix := parts[3]

		var imgPath string

		imgURL = strings.ReplaceAll(imgURL, "\\", "/")

		// Process URLs that start with /image/
		if strings.HasPrefix(imgURL, "/image/") {
			// Extract the image path (remove /image/ prefix)
			// URL format: /image/data/image/xxx.webp -> actual path: data/image/xxx.webp
			imgPath = strings.TrimPrefix(imgURL, "/image/")
			a.logInfo(fmt.Sprintf("PDF export: Converting image URL: %s -> path: %s", imgURL, imgPath))
		} else if strings.HasPrefix(imgURL, "data/image/") {
			// Process paths that start with data/image/ directly (e.g., from Marp mode)
			imgPath = imgURL
			a.logInfo(fmt.Sprintf("PDF export: Converting image path: %s", imgPath))
		} else {
			// Skip other URLs (e.g., http://, https://, data: URIs already)
			return match
		}

		// プログレスイベントを発行
		currentImage++
		if totalImages > 0 {
			emitPDFExportEvent(a.ctx, "pdf-export-progress", map[string]interface{}{
				"currentImage": currentImage,
				"totalImages":  totalImages,
				"htmlSize":     len(html),
				"stage":        "converting-images",
			})
		}

		// Convert to absolute file path
		var absPath string
		if filepath.IsAbs(imgPath) {
			absPath = imgPath
		} else {
			absPath = filepath.Join(a.dataDir, filepath.FromSlash(imgPath))
		}

		a.logInfo(fmt.Sprintf("PDF export: Resolved absolute path: %s", absPath))

		// Check file size before reading
		fileInfo, err := os.Stat(absPath)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to stat image file for PDF export: %s, error: %v", absPath, err))
			return match // Return original if file cannot be accessed
		}

		// Check if file size exceeds limit
		if fileInfo.Size() > maxImageSizeForPDF {
			a.logError(fmt.Sprintf("Image file too large for PDF export: %s (size: %d bytes, limit: %d bytes)", absPath, fileInfo.Size(), maxImageSizeForPDF))
			return match // Return original if file is too large
		}

		// Read image file
		imgData, err := os.ReadFile(absPath)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to read image file for PDF export: %s, error: %v", absPath, err))
			return match // Return original if file cannot be read
		}
		originalSize := len(imgData)

		// Determine MIME type and process image
		ext := strings.ToLower(filepath.Ext(absPath))
		var img image.Image

		// Decode image
		if ext == ".webp" {
			// //TODO webp.Decode()でエラーが発生する。この部分はなくても(無いほうが)動くっぽい
			// // WebP画像の検証と変換
			// img, err = webp.Decode(bytes.NewReader(imgData))
			// if err != nil {
			// 	a.logError(fmt.Sprintf("Failed to decode WebP image for PDF export: %s, error: %v", absPath, err))
			// 	return match // Return original if WebP cannot be decoded
			// }
			// originalBounds := img.Bounds()
			// a.logInfo(fmt.Sprintf("PDF export: WebP image size: %s (width: %d, height: %d, file size: %d bytes)", absPath, originalBounds.Dx(), originalBounds.Dy(), originalSize))
			a.logInfo("###SKIP webp.Decode()###")
			// imageとして生データのDecodeは可能だった。やはりwebpファイルのデータに不備がある?
			// else{}のコードをそのまま移植。普通にこれで動く
			img, _, err = image.Decode(bytes.NewReader(imgData))
			if err != nil {
				a.logError(fmt.Sprintf("Failed to decode image for PDF export: %s, error: %v", absPath, err))
				return match // Return original if image cannot be decoded
			}
			originalBounds := img.Bounds()
			a.logInfo(fmt.Sprintf("PDF export: Image size: %s (width: %d, height: %d, file size: %d bytes)", absPath, originalBounds.Dx(), originalBounds.Dy(), originalSize))
		} else if ext == ".svg" {
			// SVGはリサイズできないので、そのまま使用
			base64Data := base64.StdEncoding.EncodeToString(imgData)
			dataURI := fmt.Sprintf("data:image/svg+xml;base64,%s", base64Data)
			dataURISize := len(dataURI)
			totalDataURISize += int64(dataURISize)
			a.logInfo(fmt.Sprintf("PDF export: Converted SVG image %d/%d: %s -> data:image/svg+xml (original: %d bytes, data URI: %d bytes)", currentImage, totalImages, imgURL, originalSize, dataURISize))
			return prefix + dataURI + suffix
		} else {
			// For non-WebP images, decode
			img, _, err = image.Decode(bytes.NewReader(imgData))
			if err != nil {
				a.logError(fmt.Sprintf("Failed to decode image for PDF export: %s, error: %v", absPath, err))
				return match // Return original if image cannot be decoded
			}
			originalBounds := img.Bounds()
			a.logInfo(fmt.Sprintf("PDF export: Image size: %s (width: %d, height: %d, file size: %d bytes)", absPath, originalBounds.Dx(), originalBounds.Dy(), originalSize))
		}

		// Create optimized temporary PNG file
		imageStartTime := time.Now()
		tmpFile, err := a.createOptimizedImageTempFile(img, absPath)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to create optimized temp file for PDF export: %s, error: %v", absPath, err))
			return match // Return original if temp file creation fails
		}

		// Add to cleanup list
		tempFiles = append(tempFiles, tmpFile)

		// Read the temporary file and convert to data URI
		tmpData, err := os.ReadFile(tmpFile)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to read temp file for PDF export: %s, error: %v", tmpFile, err))
			return match
		}

		// Encode to base64
		base64Data := base64.StdEncoding.EncodeToString(tmpData)
		dataURISize := len(base64Data) + len("data:image/png;base64,") // Data URIの実際のサイズ
		totalDataURISize += int64(dataURISize)

		// Create data URI (always PNG for optimized images)
		dataURI := fmt.Sprintf("data:image/png;base64,%s", base64Data)
		imageDuration := time.Since(imageStartTime)

		a.logInfo(fmt.Sprintf("PDF export: Converted image %d/%d: %s -> data:image/png (original: %d bytes, temp file: %d bytes, data URI: %d bytes, duration: %v)", currentImage, totalImages, imgURL, originalSize, len(tmpData), dataURISize, imageDuration))

		return prefix + dataURI + suffix
	})

	a.logInfo(fmt.Sprintf("PDF export: Image conversion summary - total images: %d, total Data URI size: %d bytes", totalImages, totalDataURISize))

	return convertedHTML, tempFiles
}

// ---- Presenter multi-window APIs ----
// NOTE: Multi-window support requires Wails v3 (currently in development)
// Uncomment when upgrading to Wails v3:

// OpenPresenter opens or focuses a presenter window for the specified document id.
// docID should be a content path like "content/xxx.md".
// func (a *App) OpenPresenter(docID, title string) error {
// 	if docID == "" {
// 		return fmt.Errorf("docID is required")
// 	}
// 	if a.presenters == nil {
// 		a.presenters = make(map[string]*Presenter)
// 	}
// 	// If already exists, just focus
// 	if p, ok := a.presenters[docID]; ok && p != nil && p.win != nil {
// 		runtime.WindowShow(p.win)
// 		runtime.WindowFocus(p.win)
// 		return nil
// 	}
// 	// Create new window
// 	opts := &runtime.WindowOptions{
// 		Title:  fmt.Sprintf("Presenter - %s", title),
// 		Width:  1280,
// 		Height: 720,
// 		Center: true,
// 		URL:    "index.html#presenter",
// 	}
// 	win, wctx, err := runtime.NewWindow(a.ctx, opts)
// 	if err != nil {
// 		return fmt.Errorf("failed to create presenter window: %v", err)
// 	}
// 	p := &Presenter{win: win, ctx: wctx, docID: docID}
// 	a.presenters[docID] = p
// 	// cleanup on close
// 	runtime.WindowOnClose(wctx, func() {
// 		delete(a.presenters, docID)
// 	})
// 	// notify presenter window of doc id
// 	runtime.EventsEmit(wctx, "presenter:init", map[string]any{"docId": docID})
// 	return nil
// }

// UpdatePresenter pushes latest HTML and slide index to presenter window.
// func (a *App) UpdatePresenter(docID, html string, index, total int) {
// 	if a.presenters == nil {
// 		return
// 	}
// 	if p, ok := a.presenters[docID]; ok && p != nil {
// 		runtime.EventsEmit(p.ctx, "presenter:render", map[string]any{"html": html})
// 		runtime.EventsEmit(p.ctx, "presenter:slide", map[string]any{"index": index, "total": total})
// 	}
// }

// PresenterSlideChanged is invoked from presenter window to inform editor about slide change.
// func (a *App) PresenterSlideChanged(docID string, index int) {
// 	// Re-broadcast to main window(s)
// 	runtime.EventsEmit(a.ctx, "editor:slide", map[string]any{"docId": docID, "index": index})
// }

// ---- Custom CSS (preview-only) management ----

func (a *App) customCSSPath() string {
	return filepath.Join(a.dataDir, "themes", "custom.css")
}

// GetCustomCSS returns the custom CSS contents for preview (empty if none)
func (a *App) GetCustomCSS() (string, error) {
	p := a.customCSSPath()
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read custom.css: %v", err)
	}
	return string(b), nil
}

// SetCustomCSS saves custom CSS to karte_data/themes/custom.css
func (a *App) SetCustomCSS(css string) error {
	dir := filepath.Join(a.dataDir, "themes")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to ensure themes dir: %v", err)
	}
	p := a.customCSSPath()
	if err := os.WriteFile(p, []byte(css), 0644); err != nil {
		return fmt.Errorf("failed to write custom.css: %v", err)
	}
	a.logInfo(fmt.Sprintf("Saved custom CSS: %s (%d bytes)", p, len(css)))
	return nil
}

// ClearCustomCSS deletes custom.css if present
func (a *App) ClearCustomCSS() error {
	p := a.customCSSPath()
	if err := os.Remove(p); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to remove custom.css: %v", err)
	}
	a.logInfo("Cleared custom CSS")
	return nil
}

// formatTimestamp formats a timestamp in seconds as [HH:MM:SS.mmm] or [MM:SS.mmm]
// Shows milliseconds (3 decimal places) for more precise timestamps
func formatTimestamp(seconds float64) string {
	totalSeconds := int(seconds)
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	secs := totalSeconds % 60
	milliseconds := int((seconds - float64(totalSeconds)) * 1000)

	if hours > 0 {
		return fmt.Sprintf("[%02d:%02d:%02d.%03d]", hours, minutes, secs, milliseconds)
	}
	return fmt.Sprintf("[%02d:%02d.%03d]", minutes, secs, milliseconds)
}

// calculateRMS calculates the Root Mean Square of audio samples
// Used for microphone input level indicator
func calculateRMS(samples []float32) float32 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s * s)
	}
	meanSquare := sum / float64(len(samples))
	// Return RMS (square root of mean square)
	// For performance, we'll use a simple approximation or just return meanSquare
	// since we're just using it for visualization
	return float32(meanSquare)
}

// LinkInfo represents a link found in markdown content
type LinkInfo struct {
	Target string
	Kind   string
}

// extractTitleFromContent extracts title from frontmatter
func (a *App) extractTitleFromContent(content, defaultTitle string) string {
	return fm.ExtractTitle(content, defaultTitle)
}

// RenameFile renames a markdown file and updates all references to it
func (a *App) RenameFile(oldPath, newPath string) error {
	// Validate paths
	oldAbsPath, ok := a.resolveContentPath(oldPath)
	if !ok {
		return fmt.Errorf("invalid old path: %s", oldPath)
	}

	newAbsPath, ok := a.resolveContentPath(newPath)
	if !ok {
		return fmt.Errorf("invalid new path: %s", newPath)
	}
	releaseTranscriptMutation, err := a.reserveTranscriptPathMutation(oldPath, newPath)
	if err != nil {
		return err
	}
	defer releaseTranscriptMutation()

	// Check if old file exists
	if _, err := os.Stat(oldAbsPath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", oldPath)
	}

	// Check if new file already exists
	if _, err := os.Stat(newAbsPath); err == nil {
		return fmt.Errorf("target file already exists: %s", newPath)
	}

	// Read old file to get doc_id
	oldContent, err := os.ReadFile(oldAbsPath)
	if err != nil {
		return fmt.Errorf("failed to read old file: %v", err)
	}

	frontMatter, _ := fm.ParseFrontMatter(string(oldContent))
	if frontMatter == nil || frontMatter.DocID == "" {
		return fmt.Errorf("file does not have doc_id: %s", oldPath)
	}

	docID := frontMatter.DocID
	a.logInfo(fmt.Sprintf("Renaming file %s -> %s (doc_id: %s)", oldPath, newPath, docID))

	// Ensure new directory exists
	if err := os.MkdirAll(filepath.Dir(newAbsPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	// Move file
	if err := a.renameDocumentPath(oldAbsPath, newAbsPath); err != nil {
		return fmt.Errorf("failed to rename file: %v", err)
	}

	// Update the persistent doc_id mapping before touching any referencing
	// document. A failed store transaction rolls the rename back so callers
	// never receive success with a missing mapping.
	oldContentPath := filepath.ToSlash(oldPath)
	if relativeOldPath, relErr := filepath.Rel(a.dataDir, oldAbsPath); relErr == nil {
		oldContentPath = filepath.ToSlash(relativeOldPath)
	}
	newContentPath := filepath.ToSlash(newPath)
	docMap, mapErr := a.updateDocumentMapping(docID, newContentPath)
	if mapErr != nil {
		if rollbackErr := a.renameDocumentPath(newAbsPath, oldAbsPath); rollbackErr != nil {
			return fmt.Errorf(
				"update document map after rename: %w",
				errors.Join(mapErr, fmt.Errorf("rollback file rename: %w", rollbackErr)),
			)
		}
		return fmt.Errorf("update document map after rename; file rename was rolled back: %w", mapErr)
	}
	newContentPath = docMap[docID]
	a.logInfo(fmt.Sprintf("Updated doc_map: %s -> %s", docID, newContentPath))

	// Collect files that reference this document based on links.json (doc_id ベース)
	referencingSet := make(map[string]struct{})

	linkInfoPath := filepath.Join(a.dataDir, ".mdsys", "links.json")
	if linkData, err := os.ReadFile(linkInfoPath); err == nil {
		var edges []GraphEdge
		if err := json.Unmarshal(linkData, &edges); err == nil {
			for _, edge := range edges {
				// このドキュメントをターゲットとして参照しているエッジのみ対象
				if edge.TargetDocID != "" && edge.TargetDocID == docID {
					var refContentPath string
					// SourceDocID から doc_map を引くのが理想
					if edge.SourceDocID != "" {
						if p, ok := docMap[edge.SourceDocID]; ok {
							refContentPath = filepath.ToSlash(p)
						}
					}
					// doc_map に無い場合は Source (doc:/...) からパスを組み立てる
					if refContentPath == "" && strings.HasPrefix(edge.Source, "doc:/") {
						sourcePath := strings.TrimPrefix(edge.Source, "doc:/")
						refContentPath = filepath.ToSlash(filepath.Join("content", sourcePath))
					}

					// 自分自身（リネーム対象ファイル）は除外
					if refContentPath != "" && refContentPath != newContentPath {
						referencingSet[refContentPath] = struct{}{}
					}
				}
			}
			a.logInfo(fmt.Sprintf("RenameFile: doc_id-based references found: %d", len(referencingSet)))
		} else {
			a.logError(fmt.Sprintf("RenameFile: failed to parse links.json: %v", err))
		}
	} else {
		a.logError(fmt.Sprintf("RenameFile: failed to read links.json: %v", err))
	}

	// 既存のパスベース探索で見つかったものもマージするためのスライス
	contentDir := filepath.Join(a.dataDir, "content")
	var referencingFiles []string

	err = filepath.Walk(contentDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}

		// Skip the renamed file itself
		if p == newAbsPath {
			return nil
		}

		content, err := os.ReadFile(p)
		if err != nil {
			return nil // Skip files we can't read
		}

		// Check if content references old path
		contentStr := string(content)
		oldPathBase := strings.TrimSuffix(oldPath, ".md")
		oldPathBaseLower := strings.ToLower(oldPathBase)

		// Check for wiki links and markdown links
		wikiLinkRegex := regexp.MustCompile(`\[\[([^|\]]+)(?:\|([^\]]+))?\]\]`)
		markdownLinkRegex := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)

		hasReference := false

		// Check wiki links
		matches := wikiLinkRegex.FindAllStringSubmatch(contentStr, -1)
		for _, match := range matches {
			if len(match) >= 2 {
				title := strings.TrimSuffix(strings.ToLower(match[1]), ".md")
				if title == oldPathBaseLower || title == strings.ToLower(oldPathBase) {
					hasReference = true
					break
				}
			}
		}

		// Check markdown links
		if !hasReference {
			matches = markdownLinkRegex.FindAllStringSubmatch(contentStr, -1)
			for _, match := range matches {
				if len(match) >= 3 {
					url := match[2]
					if strings.HasSuffix(strings.ToLower(url), ".md") {
						urlBase := strings.TrimSuffix(strings.ToLower(url), ".md")
						if urlBase == oldPathBaseLower ||
							strings.HasSuffix(urlBase, "/"+oldPathBaseLower) ||
							url == oldPath || url == oldContentPath {
							hasReference = true
							break
						}
					}
				}
			}
		}

		if hasReference {
			relPath, err := filepath.Rel(a.dataDir, p)
			if err == nil {
				referencingSet[filepath.ToSlash(relPath)] = struct{}{}
			}
		}

		return nil
	})

	if err != nil {
		a.logError(fmt.Sprintf("Error walking content directory: %v", err))
	}

	// マップから最終的なスライスを構築
	for p := range referencingSet {
		referencingFiles = append(referencingFiles, p)
	}

	a.logInfo(fmt.Sprintf("Found %d files referencing %s (doc_id: %s)", len(referencingFiles), oldPath, docID))

	// Update all referencing files
	for _, refPath := range referencingFiles {
		refAbsPath := filepath.Join(a.dataDir, refPath)
		refContent, err := os.ReadFile(refAbsPath)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to read referencing file %s: %v", refPath, err))
			continue
		}

		// Replace links
		updatedContent, err := markdown.ReplaceLinksInContent(string(refContent), oldPath, newPath)
		if err != nil {
			a.logError(fmt.Sprintf("Failed to replace links in %s: %v", refPath, err))
			continue
		}

		// Save updated content
		if err := os.WriteFile(refAbsPath, []byte(updatedContent), 0644); err != nil {
			a.logError(fmt.Sprintf("Failed to save updated file %s: %v", refPath, err))
			continue
		}

		a.logInfo(fmt.Sprintf("Updated references in %s", refPath))
	}

	// Rebuild the renamed document and every rewritten referrer asynchronously.
	dirtySitePaths := []string{oldPath, newPath}
	dirtySitePaths = append(dirtySitePaths, referencingFiles...)
	a.scheduleSiteBuild(dirtySitePaths...)

	// Commit to Git if vcs is enabled
	if a.vcs != nil {
		// Commit the renamed file
		relNewPath, err := filepath.Rel(a.dataDir, newAbsPath)
		if err == nil {
			commitMessage := fmt.Sprintf("Rename: %s -> %s", oldPath, newPath)
			if err := a.vcs.CommitFile(relNewPath, commitMessage); err != nil {
				a.logError(fmt.Sprintf("Failed to commit renamed file: %v", err))
			}
		}

		// Commit referencing files
		for _, refPath := range referencingFiles {
			relRefPath, err := filepath.Rel(a.dataDir, filepath.Join(a.dataDir, refPath))
			if err == nil {
				commitMessage := fmt.Sprintf("Update references after rename: %s -> %s", oldPath, newPath)
				if err := a.vcs.CommitFile(relRefPath, commitMessage); err != nil {
					a.logError(fmt.Sprintf("Failed to commit updated reference: %v", err))
				}
			}
		}
	}
	a.refreshGraphAfterMutation("file rename")

	// Emit file changed event
	a.emitEvent("file-renamed", map[string]interface{}{
		"oldPath": oldPath,
		"newPath": newPath,
		"docId":   docID,
	})

	a.logInfo(fmt.Sprintf("Successfully renamed file %s -> %s", oldPath, newPath))
	return nil
}

// RenamePdfFile renames a PDF file under content/ without doc_id or link updates.
// This is intentionally simpler than RenameFile, as PDFs are not part of the markdown link graph.
func (a *App) RenamePdfFile(oldPath, newPath string) error {
	// Basic validation: paths must be under content/ and have .pdf extension
	if !strings.HasPrefix(oldPath, "content/") {
		return fmt.Errorf("old path must be under content/: %s", oldPath)
	}
	if !strings.HasPrefix(newPath, "content/") {
		return fmt.Errorf("new path must be under content/: %s", newPath)
	}

	oldExt := strings.ToLower(filepath.Ext(oldPath))
	newExt := strings.ToLower(filepath.Ext(newPath))
	if oldExt != ".pdf" || newExt != ".pdf" {
		return fmt.Errorf("both old and new paths must have .pdf extension")
	}

	// Resolve to absolute paths safely
	oldAbsPath, ok := a.resolveContentPath(oldPath)
	if !ok {
		return fmt.Errorf("invalid old path: %s", oldPath)
	}

	newAbsPath, ok := a.resolveContentPath(newPath)
	if !ok {
		return fmt.Errorf("invalid new path: %s", newPath)
	}

	// Check if old file exists
	if _, err := os.Stat(oldAbsPath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", oldPath)
	}

	// Check if new file already exists
	if _, err := os.Stat(newAbsPath); err == nil {
		return fmt.Errorf("target file already exists: %s", newPath)
	}

	a.logInfo(fmt.Sprintf("Renaming PDF %s -> %s", oldPath, newPath))

	// Ensure new directory exists
	if err := os.MkdirAll(filepath.Dir(newAbsPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	// Move file
	if err := os.Rename(oldAbsPath, newAbsPath); err != nil {
		return fmt.Errorf("failed to rename pdf file: %v", err)
	}

	return nil
}

// UpdateLinkToLatest updates a link to reference the latest version instead of a pinned version
func (a *App) UpdateLinkToLatest(sourceDocID, targetDocID string) error {
	a.logInfo(fmt.Sprintf("UpdateLinkToLatest: sourceDocID=%s, targetDocID=%s", sourceDocID, targetDocID))

	// Refresh at this explicit mutation boundary, then use the complete graph
	// snapshot. Newly discovered links are no longer written by GetGraphData, so
	// links.json alone is not an authoritative edge list.
	if err := a.RefreshGraphData(); err != nil {
		return fmt.Errorf("refresh graph before link update: %w", err)
	}
	graphData, _ := a.GetGraphData()
	edges := append([]GraphEdge(nil), graphData.Edges...)

	currentTargetHash := ""
	for _, node := range graphData.Nodes {
		if node.DocID == targetDocID {
			currentTargetHash = node.Hash
			break
		}
	}

	linkInfoPath := filepath.Join(a.dataDir, ".mdsys", "links.json")

	// Find and update the matching edge
	found := false
	for i := range edges {
		if edges[i].SourceDocID == sourceDocID && edges[i].TargetDocID == targetDocID {
			edges[i].ToVersionMode = "latest"
			edges[i].TargetUpdated = false
			if currentTargetHash != "" {
				edges[i].ToVersionID = currentTargetHash
				edges[i].TargetHash = currentTargetHash
			}
			found = true
			a.logInfo(fmt.Sprintf("Updated edge %s -> %s to latest version", edges[i].Source, edges[i].Target))
			break
		}
	}

	if !found {
		return fmt.Errorf("link not found: sourceDocID=%s, targetDocID=%s", sourceDocID, targetDocID)
	}

	linkInfoJSON, err := json.MarshalIndent(edges, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal links: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(linkInfoPath), 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}
	if err := atomicWriteDerivedFile(linkInfoPath, linkInfoJSON, 0o644); err != nil {
		return fmt.Errorf("failed to write links.json: %v", err)
	}
	a.logInfo(fmt.Sprintf("Saved updated link records to %s", linkInfoPath))
	a.refreshGraphAfterMutation("link update")

	// Emit event to refresh preview
	a.emitEvent("link-updated", map[string]interface{}{
		"sourceDocID": sourceDocID,
		"targetDocID": targetDocID,
	})

	return nil
}

// extractLinks extracts various types of links from markdown content
func (a *App) extractLinks(content string) []LinkInfo {
	var links []LinkInfo

	// Wikiリンク [[title]] または [[title|display]]
	wikiLinkRegex := regexp.MustCompile(`\[\[([^|\]]+)(?:\|([^\]]+))?\]\]`)
	matches := wikiLinkRegex.FindAllStringSubmatch(content, -1)
	a.logInfo(fmt.Sprintf("Found %d wiki links", len(matches)))
	for _, match := range matches {
		title := match[1]
		// .md拡張子を追加
		if !strings.HasSuffix(strings.ToLower(title), ".md") {
			title += ".md"
		}
		links = append(links, LinkInfo{Target: title, Kind: "wikilink"})
		a.logInfo(fmt.Sprintf("  Wiki link: %s", title))
	}

	// Markdownリンク [text](url)
	markdownLinkRegex := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	matches = markdownLinkRegex.FindAllStringSubmatch(content, -1)
	a.logInfo(fmt.Sprintf("Found %d markdown links", len(matches)))
	for _, match := range matches {
		url := match[2]
		if strings.HasSuffix(strings.ToLower(url), ".md") {
			links = append(links, LinkInfo{Target: url, Kind: "markdown_link"})
			a.logInfo(fmt.Sprintf("  Markdown link: %s", url))
		}
	}

	// 画像リンク ![alt](src)
	imgLinkRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	matches = imgLinkRegex.FindAllStringSubmatch(content, -1)
	a.logInfo(fmt.Sprintf("Found %d image links", len(matches)))
	// imgLinkTrimRegex := regexp.MustCompile(`\s*".*?"`)
	for _, match := range matches {
		// src := imgLinkTrimRegex.ReplaceAllString(match[2], "")
		src := match[2]
		links = append(links, LinkInfo{Target: src, Kind: "img"})
		a.logInfo(fmt.Sprintf("  Image link: %s", src))
	}

	// 引用 > text 内のWikiリンク
	quoteRegex := regexp.MustCompile(`(?m)^>\s*.*?\[\[([^|\]]+)(?:\|([^\]]+))?\]\].*$`)
	matches = quoteRegex.FindAllStringSubmatch(content, -1)
	a.logInfo(fmt.Sprintf("Found %d quote blocks with wiki links", len(matches)))
	for _, match := range matches {
		title := match[1]
		if !strings.HasSuffix(strings.ToLower(title), ".md") {
			title += ".md"
		}
		links = append(links, LinkInfo{Target: title, Kind: "quote"})
		a.logInfo(fmt.Sprintf("  Quote link: %s", title))
	}

	a.logInfo(fmt.Sprintf("Total links extracted: %d", len(links)))
	return links
}

// resolveLinkTarget resolves a link target to a node ID
func (a *App) resolveLinkTarget(link LinkInfo, currentFile string) string {
	a.logInfo(fmt.Sprintf("Resolving link: %s (kind: %s) from file: %s", link.Target, link.Kind, currentFile))

	switch link.Kind {
	case "wikilink", "markdown_link":
		// 相対パスを解決
		target := link.Target
		if !strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "http") {
			// 相対パスの場合、現在のファイルからの相対パスとして解決
			currentDir := filepath.Dir(currentFile)
			target = filepath.Join(currentDir, target)
			target = filepath.ToSlash(target)
		}
		result := "doc:/" + strings.TrimPrefix(target, "content/")
		a.logInfo(fmt.Sprintf("  Resolved to: %s", result))
		return result
	case "img":
		result := "img:/" + link.Target
		a.logInfo(fmt.Sprintf("  Resolved to: %s", result))
		return result
	default:
		a.logInfo(fmt.Sprintf("  No resolution for kind: %s", link.Kind))
		return ""
	}
}

func waitForWaitGroup(ctx context.Context, wg *sync.WaitGroup) bool {
	if wg == nil {
		return true
	}
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
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

	var recorder appAudioRecorder
	var realtimeService appRealtimeASRService
	var recordingASRLease *asrResourceLease
	var recordingPipeline *appRecordingPipeline
	func() {
		a.recordingMu.Lock()
		defer a.recordingMu.Unlock()
		recorder = a.recorder
		a.recorder = nil
		realtimeService, recordingASRLease = a.takeRecordingASRResourcesLocked()
		recordingPipeline = a.recordingPipeline
		a.recordingPipeline = nil
		a.recordingTranscriptPath = ""
		a.recordingStopCh = nil
		a.isRecording = false
	}()

	if recorder != nil {
		a.logInfo("[Recording] Closing audio recorder...")
		if err := recorder.Close(); err != nil {
			a.logError(fmt.Sprintf("[Recording] Failed to close audio recorder: %v", err))
		}
		a.logInfo("[Recording] Audio recorder closed")
	}
	if recordingPipeline != nil {
		recordingPipeline.stopProcessing()
		if err := recordingPipeline.transcript.Abort(); err != nil {
			a.logError(fmt.Sprintf("[Recording] Failed to close transcript buffer: %v", err))
		}
		if err := recordingPipeline.wav.Abort(); err != nil {
			a.logError(fmt.Sprintf("[Recording] Failed to remove incomplete WAV: %v", err))
		}
	}

	if realtimeService != nil {
		a.logInfo("[Recording] Closing RealtimeService...")
	}
	closeRecordingASRResources(realtimeService, recordingASRLease)
	if realtimeService != nil {
		a.logInfo("[Recording] RealtimeService closed")
	}

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
