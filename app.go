package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"karte/internal/asr"
	"karte/internal/audio"
	"karte/internal/docid"
	fm "karte/internal/frontmatter"
	gitvcs "karte/internal/git"
	"karte/internal/markdown"
	"karte/internal/marp"
	pdfexport "karte/internal/pdf"
	"karte/internal/screenshot"
	"karte/internal/site"
	syncpkg "karte/internal/sync"
	"karte/internal/webpchunk"

	"github.com/chai2010/webp"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/wailsapp/wails/v2/pkg/runtime"
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

// App struct
type App struct {
	ctx             context.Context
	root            string
	dataDir         string
	logFilePath     string
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
	runtime.LogInfo(a.ctx, msg)
	a.appendLog("INFO", msg)
}

// logError writes error logs to both Wails runtime and app log file
func (a *App) logError(msg string) {
	runtime.LogError(a.ctx, msg)
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
	f, err := os.OpenFile(a.logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		// fallback to std logger if file can't be opened
		log.Printf("log open error: %v", err)
		return
	}
	defer f.Close()
	_, _ = f.WriteString(line)
}

// FileItem represents a markdown file in the content directory
type FileItem struct {
	Path  string `json:"path"`
	Title string `json:"title"`
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

// NewApp creates a new App application struct
func NewApp() *App {
	return &App{}
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

	// Initialize karte_data directory next to the application
	a.dataDir = filepath.Join(a.root, "karte_data")
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
	// Ensure base directory exists
	if err := os.MkdirAll(a.dataDir, 0755); err != nil {
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
		filepath.Join("data", "asr"),
		"themes",
		"public",
		".mdsys",
	}
	for _, subdir := range subdirs {
		dirPath := filepath.Join(a.dataDir, subdir)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			return fmt.Errorf("failed to create subdirectory %s: %v", subdir, err)
		}
	}

	// Create default theme directory
	themeDir := filepath.Join(a.dataDir, "themes", "default")
	if err := os.MkdirAll(themeDir, 0755); err != nil {
		return fmt.Errorf("failed to create theme directory: %v", err)
	}

	// Create log directory
	logDir := filepath.Join(a.dataDir, "log")
	if err := os.MkdirAll(logDir, 0755); err != nil {
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
    "threads": 4,
    "provider": "cpu"
  }
}`,
	}

	for filePath, content := range defaultFiles {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
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

// GetFileList returns a list of markdown files in the content directory
func (a *App) GetFileList() []FileItem {
	var files []FileItem
	contentDir := filepath.Join(a.dataDir, "content")

	a.logInfo(fmt.Sprintf("GetFileList: contentDir=%s", contentDir))

	// Check if content directory exists
	if _, err := os.Stat(contentDir); os.IsNotExist(err) {
		a.logError(fmt.Sprintf("Content directory does not exist: %s", contentDir))
		return []FileItem{}
	}

	err := filepath.Walk(contentDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			a.logError(fmt.Sprintf("Error walking path %s: %v", p, err))
			return nil
		}
		if info.IsDir() {
			return nil
		}
		lowerName := strings.ToLower(info.Name())
		isMarkdown := strings.HasSuffix(lowerName, ".md")
		isPdf := strings.HasSuffix(lowerName, ".pdf")
		if isMarkdown || isPdf {
			// Generate path relative to dataDir so that it starts with "content/..."
			rel, err := filepath.Rel(a.dataDir, p)
			if err != nil {
				a.logError(fmt.Sprintf("Failed to get relative path for %s: %v", p, err))
				return nil
			}
			title := info.Name()

			// Try to extract title from frontmatter for markdown files
			if isMarkdown {
				if b, err := os.ReadFile(p); err == nil {
					title = fm.ExtractTitle(string(b), title)
				} else {
					a.logError(fmt.Sprintf("Failed to read file %s: %v", p, err))
					// Continue with filename as title if read fails
				}
			} else if isPdf {
				// For PDF files, use filename without extension as title
				title = strings.TrimSuffix(title, filepath.Ext(title))
			}
			fileItem := FileItem{
				Path:  filepath.ToSlash(rel),
				Title: title,
			}
			files = append(files, fileItem)
			a.logInfo(fmt.Sprintf("Found file: %s -> %s", fileItem.Path, fileItem.Title))
		}
		return nil
	})

	if err != nil {
		a.logError(fmt.Sprintf("Error walking content directory: %v", err))
		return []FileItem{}
	}

	a.logInfo(fmt.Sprintf("GetFileList completed: Found %d files (markdown and PDF)", len(files)))
	if len(files) > 0 {
		a.logInfo(fmt.Sprintf("First file: %s", files[0].Path))
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
}

// GetImageList returns a list of image files in the data/image directory
func (a *App) GetImageList() []ImageItem {
	var images []ImageItem
	imageDir := filepath.Join(a.dataDir, "data", "image")

	a.logInfo(fmt.Sprintf("GetImageList: imageDir=%s", imageDir))

	// Check if image directory exists
	if _, err := os.Stat(imageDir); os.IsNotExist(err) {
		a.logInfo(fmt.Sprintf("Image directory does not exist: %s", imageDir))
		return []ImageItem{}
	}

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
		return []ImageItem{}
	}

	// Sort by modification time (newest first)
	sort.Slice(images, func(i, j int) bool {
		return images[i].ModTime.After(images[j].ModTime)
	})

	a.logInfo(fmt.Sprintf("Found %d image files", len(images)))
	return images
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
	return true, nil
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

	// Ensure directory exists and write the file
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return false, fmt.Errorf("failed to prepare directory: %v", err)
	}
	if err := os.WriteFile(filePath, []byte(defaultContent), 0644); err != nil {
		return false, fmt.Errorf("failed to create file: %v", err)
	}

	a.logInfo(fmt.Sprintf("Created new file: %s", filePath))
	return true, nil
}

// LoadFile loads the content of a markdown file
// For PDF files, returns an empty string since PDFs are not editable
func (a *App) LoadFile(path string) (string, error) {
	runtime.LogInfo(a.ctx, fmt.Sprintf("LoadFile called with path: %s", path))

	absPath, ok := a.resolveContentPath(path)
	if !ok {
		runtime.LogError(a.ctx, fmt.Sprintf("Invalid path: %s", path))
		return "", fmt.Errorf("invalid path: %s", path)
	}

	runtime.LogInfo(a.ctx, fmt.Sprintf("Resolved path: %s", absPath))

	// Check if this is a PDF file
	if strings.HasSuffix(strings.ToLower(path), ".pdf") {
		runtime.LogInfo(a.ctx, fmt.Sprintf("PDF file detected, returning empty string"))
		return "", nil
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		runtime.LogError(a.ctx, fmt.Sprintf("Failed to read file %s: %v", absPath, err))
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	contentStr := string(content)

	// Ensure doc_id exists (lazy assignment)
	contentWithDocID, docID, err := a.ensureDocID(contentStr)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to ensure doc_id for %s: %v", path, err))
		// Continue with original content if doc_id generation fails
	} else if docID != "" && contentWithDocID != contentStr {
		// Save the updated content with doc_id if it was added
		if err := os.WriteFile(absPath, []byte(contentWithDocID), 0644); err != nil {
			a.logError(fmt.Sprintf("Failed to save file with doc_id: %v", err))
		} else {
			contentStr = contentWithDocID
			a.logInfo(fmt.Sprintf("Assigned doc_id to file: %s -> %s", path, docID))
		}
	}

	runtime.LogInfo(a.ctx, fmt.Sprintf("Successfully loaded file, content length: %d", len(contentStr)))
	return contentStr, nil
}

// SaveFile saves content to a markdown file
func (a *App) SaveFile(path, content string) error {
	a.logInfo(fmt.Sprintf("SaveFile called for path: %s, content length: %d", path, len(content)))

	absPath, ok := a.resolveContentPath(path)
	if !ok {
		a.logError(fmt.Sprintf("SaveFile: invalid path: %s", path))
		return fmt.Errorf("invalid path: %s", path)
	}

	// Calculate hash before saving (but don't read file content here to avoid conflicts)
	var oldHash string
	if existingContent, err := os.ReadFile(absPath); err == nil {
		oldHash = gitvcs.CalculateHash(string(existingContent))
		a.logInfo(fmt.Sprintf("SaveFile: existing file hash: %s (length: %d)", oldHash[:8], len(existingContent)))
	} else {
		a.logInfo(fmt.Sprintf("SaveFile: file does not exist yet or cannot be read"))
	}

	// Ensure doc_id exists (lazy assignment) - do this first
	contentWithDocID, docID, err := a.ensureDocID(content)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to ensure doc_id for %s: %v", path, err))
		// Continue with original content if doc_id generation fails
		contentWithDocID = content
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

	// Detect conflict before saving
	// IMPORTANT: Use the content from frontend (with user edits) as LocalContent
	// We need to temporarily write it to disk so DetectConflict can read it
	if a.vcs != nil {
		relPath, err := filepath.Rel(a.dataDir, absPath)
		if err == nil {
			// Temporarily write the frontend content to disk for conflict detection
			// This ensures DetectConflict uses the user's edited content, not the old file content
			tempContent := content
			if err := os.WriteFile(absPath, []byte(tempContent), 0644); err != nil {
				a.logError(fmt.Sprintf("Failed to write temp content for conflict detection: %v", err))
			} else {
				// Now detect conflict - it will use the content we just wrote
				conflict, err := gitvcs.DetectConflict(a.vcs, a.dataDir, relPath)
				if err != nil {
					a.logError(fmt.Sprintf("Failed to detect conflict: %v", err))
				} else if conflict != nil {
					// Create backup before handling conflict
					if err := a.createBackup(path, content); err != nil {
						a.logError(fmt.Sprintf("Failed to create backup: %v", err))
					}

					// Try auto-merge for auto-resolvable or warning conflicts
					// Use the frontend content as LocalContent (user's current edits)
					if conflict.Severity == gitvcs.ConflictAutoResolvable || conflict.Severity == gitvcs.ConflictWarning {
						// Use content (from frontend) as LocalContent instead of conflict.LocalContent
						merged, severity, err := gitvcs.AutoMergeMarkdown(conflict.BaseContent, content, conflict.RemoteContent)
						if err == nil && severity != gitvcs.ConflictCritical {
							// Auto-merge successful - use merged content
							content = merged
							runtime.EventsEmit(a.ctx, "auto-merge-success", map[string]interface{}{
								"path":        path,
								"merged_hash": gitvcs.CalculateHash(merged),
							})
							a.logInfo(fmt.Sprintf("Auto-merged conflict for file: %s (using frontend content as LocalContent)", path))
						} else {
							// Auto-merge failed or still has conflicts - notify user
							runtime.EventsEmit(a.ctx, "conflict-detected", conflict)
							if conflict.Severity == gitvcs.ConflictCritical {
								return fmt.Errorf("conflict detected: file has been modified elsewhere and requires manual resolution")
							}
						}
					} else {
						// Critical conflict - require manual resolution
						runtime.EventsEmit(a.ctx, "conflict-detected", conflict)
						return fmt.Errorf("conflict detected: file has been modified elsewhere and requires manual resolution")
					}
				}
			}
		}
	}

	// Save file
	a.logInfo(fmt.Sprintf("SaveFile: writing file %s (content length: %d)", absPath, len(content)))
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		a.logError(fmt.Sprintf("SaveFile: failed to write file %s: %v", absPath, err))
		return fmt.Errorf("failed to write file: %v", err)
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

	// Build the site after saving
	if err := a.BuildSite(); err != nil {
		runtime.LogError(a.ctx, fmt.Sprintf("Failed to build site after save: %v", err))
	}

	// Emit file changed event
	runtime.EventsEmit(a.ctx, "file-changed", path)

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

	// Save resolved content
	if err := os.WriteFile(absPath, []byte(resolvedContent), 0644); err != nil {
		return fmt.Errorf("failed to write resolved file: %v", err)
	}

	// Commit the resolution
	commitMessage := fmt.Sprintf("Resolve conflict: %s (strategy: %s)", path, strategy)
	if err := a.vcs.CommitFile(relPath, commitMessage); err != nil {
		a.logError(fmt.Sprintf("Failed to commit conflict resolution: %v", err))
	}

	a.logInfo(fmt.Sprintf("Resolved conflict for %s using strategy: %s", path, strategy))
	return nil
}

// PreviewMarkdown renders markdown content to HTML
func (a *App) PreviewMarkdown(content string) (string, error) {
	// Parse frontmatter to check if this is a Marp presentation
	frontMatter, markdownBody := fm.ParseFrontMatter(content)

	// Check if Marp mode is enabled
	// Marp mode is enabled if:
	// 1. marp: true is explicitly set
	// 2. or Marp-specific fields (header, footer, paginate) are present
	isMarpMode := false
	header := ""
	footer := ""
	paginate := false
	aspectRatio := ""      // Default will be 16:9
	marpTheme := "default" // Default Marp theme

	if frontMatter != nil {
		if frontMatter.Marp {
			isMarpMode = true
		}

		// Extract header, footer, paginate, aspectRatio, and marpTheme from Raw
		if frontMatter.Raw != nil {
			if h, ok := frontMatter.Raw["header"].(string); ok {
				header = h
				isMarpMode = true // Header presence indicates Marp mode
			}
			if f, ok := frontMatter.Raw["footer"].(string); ok {
				footer = f
				isMarpMode = true // Footer presence indicates Marp mode
			}
			if p, ok := frontMatter.Raw["paginate"].(bool); ok {
				paginate = p
				isMarpMode = true // Paginate presence indicates Marp mode
			}
			if ar, ok := frontMatter.Raw["aspectRatio"].(string); ok {
				aspectRatio = ar
			}
			if mt, ok := frontMatter.Raw["marpTheme"].(string); ok {
				marpTheme = mt
			}
		}
	}

	if isMarpMode {
		// Render as Marp presentation
		slides := marp.ParseSlides(markdownBody)
		title := frontMatter.Title
		if title == "" {
			title = "Presentation"
		}

		html := marp.RenderMarpHTML(slides, title, header, footer, paginate, aspectRatio, marpTheme)

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

		// Convert image paths in HTML to URLs that can be served by the HTTP handler
		// Match img src attributes that point to data/image/ files
		imgPathRegex := regexp.MustCompile(`(<img[^>]+src=["'])([^"']+)(["'][^>]*>)`)
		html = imgPathRegex.ReplaceAllStringFunc(html, func(match string) string {
			parts := imgPathRegex.FindStringSubmatch(match)
			if len(parts) < 4 {
				return match
			}
			prefix := parts[1]
			imgPath := parts[2]
			suffix := parts[3]

			a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: Processing image path: %s", imgPath))

			// Check if this is a data/image/ path
			if strings.HasPrefix(imgPath, "data/image/") {
				// Convert to URL path that will be served by HTTP handler
				urlPath := "/image/" + imgPath
				a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: Converted image path: %s -> %s", imgPath, urlPath))
				return prefix + urlPath + suffix
			}
			a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: Image path does not start with data/image/: %s", imgPath))
			return match
		})

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
				// Get the actual file path from doc_id using doc_map.json
				actualFilePath := ""
				docMapPath := filepath.Join(a.dataDir, ".mdsys", "doc_map.json")
				if docMapData, err := os.ReadFile(docMapPath); err == nil {
					var docMap map[string]string
					if err := json.Unmarshal(docMapData, &docMap); err == nil {
						if path, exists := docMap[sourceDocID]; exists {
							actualFilePath = path
							a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: found file path for doc_id %s: %s", sourceDocID, actualFilePath))
						} else {
							a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: no file path found for doc_id %s in doc_map", sourceDocID))
						}
					}
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

						// Get the current target path from doc_map.json using TargetDocID (handles renames)
						targetPath := strings.TrimPrefix(edge.Target, "doc:/")
						if edge.TargetDocID != "" {
							// Try to get the current path from doc_map.json
							if docMapData, err := os.ReadFile(docMapPath); err == nil {
								var targetDocMap map[string]string
								if err := json.Unmarshal(docMapData, &targetDocMap); err == nil {
									if currentPath, exists := targetDocMap[edge.TargetDocID]; exists {
										// Use the current path from doc_map (handles renames)
										targetPath = strings.TrimPrefix(currentPath, "content/")
										a.logInfo(fmt.Sprintf("PreviewMarkdown [Marp]: resolved target path from doc_map: %s -> %s (doc_id: %s)", edge.Target, targetPath, edge.TargetDocID))
									}
								}
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

	// Regular markdown rendering
	// Ensure markdownBody doesn't start with frontmatter markers
	markdownBody = strings.TrimSpace(markdownBody)
	if strings.HasPrefix(markdownBody, "---") {
		// If body still starts with ---, try to find the end of frontmatter
		if idx := strings.Index(markdownBody[3:], "\n---"); idx >= 0 {
			markdownBody = strings.TrimSpace(markdownBody[idx+7:])
		}
	}

	// Create a temporary file for rendering
	tmpFile := filepath.Join(a.dataDir, ".mdsys", "temp_preview.md")
	err := os.MkdirAll(filepath.Dir(tmpFile), 0o755)
	if err != nil {
		a.logError(fmt.Sprintf("PreviewMarkdown: failed to create temp directory: %v", err))
		return "", fmt.Errorf("failed to create temp directory: %v", err)
	}

	err = os.WriteFile(tmpFile, []byte(markdownBody), 0o644)
	if err != nil {
		a.logError(fmt.Sprintf("PreviewMarkdown: failed to write temp file: %v", err))
		return "", fmt.Errorf("failed to write temp file: %v", err)
	}
	defer func() {
		if removeErr := os.Remove(tmpFile); removeErr != nil {
			a.logError(fmt.Sprintf("PreviewMarkdown: failed to remove temp file: %v", removeErr))
		}
	}()

	// Use dataDir as root so that site.RenderMarkdown can find themes directory
	html, _, err := site.RenderMarkdown(a.dataDir, tmpFile)
	if err != nil {
		a.logError(fmt.Sprintf("PreviewMarkdown: failed to render markdown: %v (root: %s, tmpFile: %s)", err, a.dataDir, tmpFile))
		return "", fmt.Errorf("failed to render markdown: %v", err)
	}

	// Convert image paths in HTML to URLs that can be served by the HTTP handler
	// Match img src attributes that point to data/image/ files
	imgPathRegex := regexp.MustCompile(`(<img[^>]+src=["'])([^"']+)(["'][^>]*>)`)
	html = imgPathRegex.ReplaceAllStringFunc(html, func(match string) string {
		parts := imgPathRegex.FindStringSubmatch(match)
		if len(parts) < 4 {
			return match
		}
		prefix := parts[1]
		imgPath := parts[2]
		suffix := parts[3]

		// Check if this is a data/image/ path
		if strings.HasPrefix(imgPath, "data/image/") {
			// Convert to URL path that will be served by HTTP handler
			urlPath := "/image/" + imgPath
			return prefix + urlPath + suffix
		}
		return match
	})

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
			// Get the actual file path from doc_id using doc_map.json
			actualFilePath := ""
			docMapPath := filepath.Join(a.dataDir, ".mdsys", "doc_map.json")
			if docMapData, err := os.ReadFile(docMapPath); err == nil {
				var docMap map[string]string
				if err := json.Unmarshal(docMapData, &docMap); err == nil {
					if path, exists := docMap[sourceDocID]; exists {
						actualFilePath = path
						a.logInfo(fmt.Sprintf("PreviewMarkdown: found file path for doc_id %s: %s", sourceDocID, actualFilePath))
					} else {
						a.logInfo(fmt.Sprintf("PreviewMarkdown: no file path found for doc_id %s in doc_map", sourceDocID))
					}
				}
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

					// Get the current target path from doc_map.json using TargetDocID (handles renames)
					targetPath := strings.TrimPrefix(edge.Target, "doc:/")
					if edge.TargetDocID != "" {
						// Try to get the current path from doc_map.json
						if docMapData, err := os.ReadFile(docMapPath); err == nil {
							var targetDocMap map[string]string
							if err := json.Unmarshal(docMapData, &targetDocMap); err == nil {
								if currentPath, exists := targetDocMap[edge.TargetDocID]; exists {
									// Use the current path from doc_map (handles renames)
									targetPath = strings.TrimPrefix(currentPath, "content/")
									a.logInfo(fmt.Sprintf("PreviewMarkdown: resolved target path from doc_map: %s -> %s (doc_id: %s)", edge.Target, targetPath, edge.TargetDocID))
								}
							}
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

	return html, nil
}

// BuildSite builds the static site
func (a *App) BuildSite() error {
	return a.build(a.root)
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

// build implements the Build function from runner package
func (a *App) build(root string) error {
	// ensure root is absolute
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	root = absRoot

	pub := filepath.Join(root, "public")
	tmp := filepath.Join(root, ".mdsys", "_public_tmp")
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}

	type IndexEntry struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	type Index struct {
		Items []IndexEntry `json:"items"`
	}
	idx := Index{}

	contentDir := filepath.Join(root, "content")
	fs.WalkDir(os.DirFS(contentDir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(p) != ".md" {
			return nil
		}
		src := filepath.Join(contentDir, p)
		dst := filepath.Join(tmp, p[:len(p)-3]+".html")
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		html, _, err := site.RenderMarkdown(root, src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, []byte(html), 0o644); err != nil {
			return err
		}
		id := filepath.ToSlash(p)
		idx.Items = append(idx.Items, IndexEntry{ID: id, Path: id})
		return nil
	})
	b, _ := json.MarshalIndent(idx, "", "  ")
	_ = os.WriteFile(filepath.Join(root, ".mdsys", "index.json"), b, 0o644)
	// ここで原子的入れ替え（旧publicを消してからrename）
	_ = os.RemoveAll(pub)
	if err := os.Rename(tmp, pub); err != nil {
		// 失敗時は tmp を消しておく（次回ビルドに影響しないように）
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
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

// ImportAudioFile copies an audio file into karte_data/data/audio and triggers transcription.
func (a *App) ImportAudioFile(src string) (string, error) {
	if src == "" {
		return "", fmt.Errorf("source path is required")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("stat audio: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("audio path must be a file")
	}
	f, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("open audio: %w", err)
	}
	defer f.Close()
	return a.importAudioFromReader(info.Name(), f)
}

// ImportImageFile copies an image file into karte_data/data/image.
func (a *App) ImportImageFile(src string) (string, error) {
	if src == "" {
		return "", fmt.Errorf("source path is required")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("stat image: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("image path must be a file")
	}
	f, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("open image: %w", err)
	}
	defer f.Close()
	return a.importImageFromReader(info.Name(), f)
}

// ImportImageBase64 saves image content provided as base64 (used when native paths are not available).
func (a *App) ImportImageBase64(filename, base64Data string) (string, error) {
	if filename == "" {
		return "", fmt.Errorf("filename is required")
	}
	if base64Data == "" {
		return "", fmt.Errorf("image data is empty")
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("decode image data: %w", err)
	}
	return a.importImageFromReader(filename, bytes.NewReader(data))
}

func (a *App) importImageFromReader(originalName string, src io.Reader) (string, error) {
	if originalName == "" {
		return "", fmt.Errorf("original name is required")
	}
	if !isSupportedImageExt(originalName) {
		return "", fmt.Errorf("unsupported image format: %s", filepath.Ext(originalName))
	}

	destDir := filepath.Join(a.dataDir, "data", "image")
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("prepare image dir: %w", err)
	}

	ext := strings.ToLower(filepath.Ext(originalName))
	base := sanitizeImageBaseName(strings.TrimSuffix(originalName, ext))
	timestamp := time.Now().Format("20060102-150405")
	prefix := fmt.Sprintf("%s_%s", timestamp, base)

	data, err := io.ReadAll(src)
	if err != nil {
		return "", fmt.Errorf("read image data: %w", err)
	}

	baseIndex := 1
	originalSuffix := ""
	if ext == ".webp" {
		originalSuffix = "_source"
	}

	var originalFilename, webpFilename string
	for {
		baseName := prefix
		if baseIndex > 1 {
			baseName = fmt.Sprintf("%s_%02d", prefix, baseIndex)
		}
		originalFilename = baseName + originalSuffix + ext
		webpFilename = baseName + ".webp"
		if !fileExists(filepath.Join(destDir, originalFilename)) && !fileExists(filepath.Join(destDir, webpFilename)) {
			break
		}
		baseIndex++
	}

	originalDestPath := filepath.Join(destDir, originalFilename)
	if err := os.WriteFile(originalDestPath, data, 0o644); err != nil {
		return "", fmt.Errorf("write original image file: %w", err)
	}

	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("decode image for webp conversion: %w", err)
	}

	webpDestPath := filepath.Join(destDir, webpFilename)
	webpFile, err := os.OpenFile(webpDestPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return "", fmt.Errorf("create webp image file: %w", err)
	}

	webpOptions := &webp.Options{Lossless: format == "png" || format == "gif"}
	if !webpOptions.Lossless {
		webpOptions.Quality = 90
	}

	if err := webp.Encode(webpFile, img, webpOptions); err != nil {
		webpFile.Close()
		return "", fmt.Errorf("encode webp image: %w", err)
	}
	if err := webpFile.Close(); err != nil {
		return "", fmt.Errorf("close webp file: %w", err)
	}

	relOriginalPath, err := filepath.Rel(a.dataDir, originalDestPath)
	if err != nil {
		relOriginalPath = originalDestPath
	}
	relOriginalPath = filepath.ToSlash(relOriginalPath)

	relWebPPath, err := filepath.Rel(a.dataDir, webpDestPath)
	if err != nil {
		relWebPPath = webpDestPath
	}
	relWebPPath = filepath.ToSlash(relWebPPath)

	payload := map[string]interface{}{
		"path":          relWebPPath,
		"original_name": originalName,
		"saved_name":    webpFilename,
		"webp_path":     relWebPPath,
		"original_path": relOriginalPath,
	}
	runtime.EventsEmit(a.ctx, "image-imported", payload)
	a.logInfo(fmt.Sprintf("Image imported: %s -> webp=%s (original=%s)", originalName, relWebPPath, relOriginalPath))

	return relWebPPath, nil
}

// ImportAudioBase64 saves audio content provided as base64 (used when native paths are not available).
func (a *App) ImportAudioBase64(filename, base64Data string) (string, error) {
	if filename == "" {
		return "", fmt.Errorf("filename is required")
	}
	if base64Data == "" {
		return "", fmt.Errorf("audio data is empty")
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("decode audio data: %w", err)
	}
	return a.importAudioFromReader(filename, bytes.NewReader(data))
}

func (a *App) importAudioFromReader(originalName string, src io.Reader) (string, error) {
	if originalName == "" {
		return "", fmt.Errorf("original name is required")
	}
	if !audio.IsSupportedImportExt(originalName) {
		return "", fmt.Errorf("unsupported audio format: %s", filepath.Ext(originalName))
	}

	destDir := filepath.Join(a.dataDir, "data", "audio")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return "", fmt.Errorf("prepare audio dir: %w", err)
	}

	base := audio.SanitizeFileName(strings.TrimSuffix(originalName, filepath.Ext(originalName)))
	if base == "" {
		base = "audio"
	}
	ext := strings.ToLower(filepath.Ext(originalName))
	timestamp := time.Now().Format("20060102-150405")
	prefix := fmt.Sprintf("%s_%s", timestamp, base)
	filename := prefix + ext
	for i := 2; ; i++ {
		_, err := os.Stat(filepath.Join(destDir, filename))
		if os.IsNotExist(err) {
			break
		}
		filename = fmt.Sprintf("%s_%02d%s", prefix, i, ext)
	}

	destPath := filepath.Join(destDir, filename)
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("create audio file: %w", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return "", fmt.Errorf("write audio file: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("close audio file: %w", err)
	}

	relPath, err := filepath.Rel(a.dataDir, destPath)
	if err != nil {
		relPath = destPath
	}
	relPath = filepath.ToSlash(relPath)

	payload := map[string]interface{}{
		"path":          relPath,
		"original_name": originalName,
		"saved_name":    filename,
	}
	runtime.EventsEmit(a.ctx, "audio-imported", payload)
	a.logInfo(fmt.Sprintf("Audio imported: %s -> %s", originalName, relPath))

	a.startTranscriptionJob(destPath, relPath)
	return relPath, nil
}

// ImportPdfFile copies a PDF file into karte_data/content.
func (a *App) ImportPdfFile(src string) (string, error) {
	if src == "" {
		return "", fmt.Errorf("source path is required")
	}
	info, err := os.Stat(src)
	if err != nil {
		return "", fmt.Errorf("stat pdf: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("pdf path must be a file")
	}
	ext := strings.ToLower(filepath.Ext(src))
	if ext != ".pdf" {
		return "", fmt.Errorf("file is not a PDF: %s", ext)
	}
	f, err := os.Open(src)
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()
	return a.importPdfFromReader(info.Name(), f)
}

// ImportPdfBase64 saves PDF content provided as base64 (used when native paths are not available).
func (a *App) ImportPdfBase64(filename, base64Data string) (string, error) {
	if filename == "" {
		return "", fmt.Errorf("filename is required")
	}
	if base64Data == "" {
		return "", fmt.Errorf("pdf data is empty")
	}
	data, err := base64.StdEncoding.DecodeString(base64Data)
	if err != nil {
		return "", fmt.Errorf("decode pdf data: %w", err)
	}
	return a.importPdfFromReader(filename, bytes.NewReader(data))
}

func (a *App) importPdfFromReader(originalName string, src io.Reader) (string, error) {
	if originalName == "" {
		return "", fmt.Errorf("original name is required")
	}
	ext := strings.ToLower(filepath.Ext(originalName))
	if ext != ".pdf" {
		return "", fmt.Errorf("unsupported file format: %s", ext)
	}

	// Save PDF to content directory
	contentDir := filepath.Join(a.dataDir, "content")
	if err := os.MkdirAll(contentDir, 0o755); err != nil {
		return "", fmt.Errorf("prepare content dir: %w", err)
	}

	base := strings.TrimSuffix(originalName, ext)
	base = audio.SanitizeFileName(base)
	if base == "" {
		base = "document"
	}
	filename := base + ext

	// Check if file already exists, add number suffix if needed
	baseIndex := 1
	for {
		destPath := filepath.Join(contentDir, filename)
		if _, err := os.Stat(destPath); os.IsNotExist(err) {
			break
		}
		filename = fmt.Sprintf("%s_%02d%s", base, baseIndex, ext)
		baseIndex++
	}

	destPath := filepath.Join(contentDir, filename)
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("create pdf file: %w", err)
	}
	if _, err := io.Copy(out, src); err != nil {
		out.Close()
		return "", fmt.Errorf("write pdf file: %w", err)
	}
	if err := out.Close(); err != nil {
		return "", fmt.Errorf("close pdf file: %w", err)
	}

	relPath, err := filepath.Rel(a.dataDir, destPath)
	if err != nil {
		relPath = destPath
	}
	relPath = filepath.ToSlash(relPath)

	payload := map[string]interface{}{
		"path":          relPath,
		"original_name": originalName,
		"saved_name":    filename,
	}
	runtime.EventsEmit(a.ctx, "pdf-imported", payload)
	a.logInfo(fmt.Sprintf("PDF imported: %s -> %s", originalName, relPath))

	return relPath, nil
}

func (a *App) startTranscriptionJob(absAudioPath, relAudioPath string) {
	if ready := a.waitForASRReady(); !ready {
		a.logInfo("ASR service not configured; skipping transcription")
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()

		transcriptPath, err := a.startStreamingTranscription(ctx, absAudioPath, relAudioPath)
		if err != nil {
			a.logError(fmt.Sprintf("ASR failed for %s: %v", relAudioPath, err))
			runtime.EventsEmit(a.ctx, "audio-transcribed", map[string]interface{}{
				"audioPath": relAudioPath,
				"error":     err.Error(),
			})
			return
		}

		runtime.EventsEmit(a.ctx, "audio-transcribed", map[string]interface{}{
			"audioPath":      relAudioPath,
			"transcriptPath": transcriptPath,
		})
	}()
}

func (a *App) startStreamingTranscription(ctx context.Context, absAudioPath, relAudioPath string) (string, error) {
	transcriptPath, err := a.writeTranscriptDocument(relAudioPath, "")
	if err != nil {
		return "", err
	}

	progressHandler := func(line string, segmentIndex, totalSegments int, timestamp float64) {
		// Format timestamp as [HH:MM:SS] or [MM:SS]
		timestampStr := formatTimestamp(timestamp)
		timestampedLine := fmt.Sprintf("%s %s", timestampStr, line)

		a.appendTranscriptLine(transcriptPath, timestampedLine)
		runtime.EventsEmit(a.ctx, "audio-transcribe-progress", map[string]interface{}{
			"audioPath":      relAudioPath,
			"transcriptPath": transcriptPath,
			"text":           line,
			"segmentIndex":   segmentIndex,
			"totalSegments":  totalSegments,
			"timestamp":      timestamp,
		})
	}

	text, err := a.asrService.TranscribeFile(ctx, absAudioPath, progressHandler)
	if err != nil {
		return "", err
	}

	if strings.TrimSpace(text) == "" {
		a.appendTranscriptLine(transcriptPath, "_（ASRから有効な結果が得られませんでした）_")
	}
	return transcriptPath, nil
}

func (a *App) writeTranscriptDocument(audioRelPath, transcript string) (string, error) {
	baseName := audio.SanitizeFileName(strings.TrimSuffix(filepath.Base(audioRelPath), filepath.Ext(audioRelPath)))
	if baseName == "" {
		baseName = fmt.Sprintf("audio-%s", time.Now().Format("20060102-150405"))
	}

	dirRel := filepath.ToSlash(filepath.Join("content", "transcripts"))
	filename := baseName + ".md"
	contentRel := filepath.ToSlash(filepath.Join(dirRel, filename))

	makeAbs := func(rel string) (string, error) {
		abs, ok := a.resolveContentPath(rel)
		if !ok {
			return "", fmt.Errorf("invalid transcript path: %s", rel)
		}
		return abs, nil
	}

	absPath, err := makeAbs(contentRel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return "", fmt.Errorf("prepare transcript dir: %w", err)
	}

	for i := 2; ; i++ {
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			break
		}
		filename = fmt.Sprintf("%s-%d.md", baseName, i)
		contentRel = filepath.ToSlash(filepath.Join(dirRel, filename))
		absPath, err = makeAbs(contentRel)
		if err != nil {
			return "", err
		}
	}

	body := a.composeTranscriptMarkdown(audioRelPath, transcript)
	if err := a.SaveFile(contentRel, body); err != nil {
		return "", err
	}
	return contentRel, nil
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

// appendTranscriptPartial updates the partial text marker in the transcript file
func (a *App) appendTranscriptPartial(contentRel, partialText string) {
	absPath, ok := a.resolveContentPath(contentRel)
	if !ok {
		a.logError(fmt.Sprintf("Failed to resolve transcript path: %s", contentRel))
		return
	}

	// Read existing content
	content, err := os.ReadFile(absPath)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to read transcript file: %v", err))
		return
	}

	contentStr := string(content)
	partialMarkerStart := "<!-- ASR_PARTIAL -->"
	partialMarkerEnd := "<!-- /ASR_PARTIAL -->"

	// Find the "## Transcript" section
	transcriptHeader := "## Transcript"
	headerIndex := strings.Index(contentStr, transcriptHeader)

	if headerIndex == -1 {
		// If "## Transcript" not found, create it with partial text
		contentStr += "\n\n" + transcriptHeader + "\n\n" + partialMarkerStart + partialText + partialMarkerEnd + "\n"
	} else {
		// Find existing partial marker
		afterHeader := contentStr[headerIndex+len(transcriptHeader):]
		partialStartIndex := strings.Index(afterHeader, partialMarkerStart)
		partialEndIndex := strings.Index(afterHeader, partialMarkerEnd)

		if partialStartIndex != -1 && partialEndIndex != -1 && partialEndIndex > partialStartIndex {
			// Replace existing partial text
			beforePartial := contentStr[:headerIndex+len(transcriptHeader)+partialStartIndex+len(partialMarkerStart)]
			afterPartial := contentStr[headerIndex+len(transcriptHeader)+partialEndIndex+len(partialMarkerEnd):]
			contentStr = beforePartial + partialText + partialMarkerEnd + afterPartial
		} else {
			// Add new partial text marker at the end of Transcript section
			// Find the end of the section (next ## header or end of file)
			nextHeaderMatch := strings.Index(afterHeader, "\n## ")
			sectionEnd := len(afterHeader)
			if nextHeaderMatch != -1 {
				sectionEnd = nextHeaderMatch
			}
			sectionContent := strings.TrimRight(afterHeader[:sectionEnd], "\n")
			newSectionContent := sectionContent
			if sectionContent != "" {
				newSectionContent += "\n\n"
			}
			newSectionContent += partialMarkerStart + partialText + partialMarkerEnd + "\n"
			contentStr = contentStr[:headerIndex+len(transcriptHeader)] + "\n" + newSectionContent + afterHeader[sectionEnd:]
		}
	}

	// Write back to file
	if err := os.WriteFile(absPath, []byte(contentStr), 0644); err != nil {
		a.logError(fmt.Sprintf("Failed to write transcript: %v", err))
		return
	}

	// Sync to ensure data is written immediately
	f, err := os.OpenFile(absPath, os.O_RDONLY, 0644)
	if err == nil {
		f.Sync()
		f.Close()
	}
}

func (a *App) appendTranscriptLine(contentRel, line string) {
	absPath, ok := a.resolveContentPath(contentRel)
	if !ok {
		a.logError(fmt.Sprintf("Failed to resolve transcript path: %s", contentRel))
		return
	}

	// Read existing content
	content, err := os.ReadFile(absPath)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to read transcript file: %v", err))
		return
	}

	contentStr := string(content)
	partialMarkerStart := "<!-- ASR_PARTIAL -->"
	partialMarkerEnd := "<!-- /ASR_PARTIAL -->"

	// Find the "## Transcript" section
	transcriptHeader := "## Transcript"
	headerIndex := strings.Index(contentStr, transcriptHeader)

	if headerIndex == -1 {
		// If "## Transcript" not found, append at the end
		contentStr += "\n\n" + transcriptHeader + "\n\n" + line + "\n\n"
	} else {
		// Check if there's a partial text marker to replace
		afterHeader := contentStr[headerIndex+len(transcriptHeader):]
		partialStartIndex := strings.Index(afterHeader, partialMarkerStart)
		partialEndIndex := strings.Index(afterHeader, partialMarkerEnd)

		if partialStartIndex != -1 && partialEndIndex != -1 && partialEndIndex > partialStartIndex {
			// Replace partial marker with final text
			beforePartial := contentStr[:headerIndex+len(transcriptHeader)+partialStartIndex]
			afterPartial := contentStr[headerIndex+len(transcriptHeader)+partialEndIndex+len(partialMarkerEnd):]
			contentStr = beforePartial + line + "\n" + afterPartial
		} else {
			// Always append at the end of the file to maintain chronological order
			// This ensures new transcript segments are added in the correct time order
			contentStr += "\n\n" + line + "\n\n"
		}
	}

	// Write back to file
	if err := os.WriteFile(absPath, []byte(contentStr), 0644); err != nil {
		a.logError(fmt.Sprintf("Failed to write transcript: %v", err))
		return
	}

	// Sync to ensure data is written immediately
	f, err := os.OpenFile(absPath, os.O_RDONLY, 0644)
	if err == nil {
		f.Sync()
		f.Close()
	}
}

// updateTranscriptAudioPath updates the audio_path in the transcript file's frontmatter
func (a *App) updateTranscriptAudioPath(transcriptPath, audioRelPath string) error {
	absPath, ok := a.resolveContentPath(transcriptPath)
	if !ok {
		return fmt.Errorf("invalid transcript path: %s", transcriptPath)
	}

	// Read existing content
	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("failed to read transcript file: %w", err)
	}

	// Parse frontmatter
	frontMatter, markdownBody := fm.ParseFrontMatter(string(content))
	if frontMatter == nil {
		return fmt.Errorf("no frontmatter found in transcript file")
	}

	// Update audio_path in Raw map (since it's a custom field)
	if frontMatter.Raw == nil {
		frontMatter.Raw = make(map[string]any)
	}
	frontMatter.Raw["audio_path"] = audioRelPath
	// Preserve existing created_at if present, otherwise set it
	if _, exists := frontMatter.Raw["created_at"]; !exists {
		frontMatter.Raw["created_at"] = time.Now().Format(time.RFC3339)
	}

	// Format frontmatter and combine with body
	formattedFM := fm.FormatFrontMatter(frontMatter)
	updatedContent := formattedFM + markdownBody

	// Write directly to file to avoid SaveFile's ParseFrontMatter which might overwrite our changes
	// We still need to handle git operations, so we'll use SaveFile but ensure the content is correct
	// Actually, let's write directly and then commit via git if needed
	if err := os.WriteFile(absPath, []byte(updatedContent), 0644); err != nil {
		return fmt.Errorf("failed to write updated transcript: %w", err)
	}

	// Commit the change via git if VCS is available
	if a.vcs != nil {
		relPath, err := filepath.Rel(a.dataDir, absPath)
		if err == nil {
			if err := a.vcs.CommitFile(relPath, fmt.Sprintf("Update audio_path to %s", audioRelPath)); err != nil {
				// Log but don't fail - the file is already updated
				a.logError(fmt.Sprintf("Failed to commit audio_path update: %v", err))
			}
		}
	}

	return nil
}

func (a *App) initASRService() error {
	cfgPath := filepath.Join(a.dataDir, "data", "asr", "config.json")
	cfg, err := asr.LoadConfigFromFile(cfgPath)
	if err != nil {
		return err
	}
	if cfg == nil || !cfg.Enabled {
		a.logInfo("ASR disabled (config missing or enabled=false)")
		return nil
	}
	cfg.EnsureModelPathsAbsolute(a.dataDir)
	svc, err := asr.NewService(cfg)
	if err != nil {
		return err
	}
	a.asrService = svc
	return nil
}

func (a *App) waitForASRReady() bool {
	if a.asrService != nil {
		return true
	}
	if a.asrInitDone == nil {
		return false
	}
	select {
	case <-a.asrInitDone:
		return a.asrService != nil
	case <-time.After(30 * time.Second):
		return a.asrService != nil
	}
}

// ASRStatus represents the current status of the ASR service
type ASRStatus struct {
	Initialized  bool `json:"initialized"`
	Initializing bool `json:"initializing"`
}

// GetASRStatus returns the current initialization status of the ASR service
func (a *App) GetASRStatus() ASRStatus {
	initialized := a.asrService != nil

	initializing := false
	if a.asrInitDone != nil {
		select {
		case <-a.asrInitDone:
			// Initialization is complete (either succeeded or failed)
			initializing = false
		default:
			// Still initializing
			initializing = true
		}
	}

	return ASRStatus{
		Initialized:  initialized,
		Initializing: initializing,
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

// GetGraphData generates graph data from markdown files
func (a *App) GetGraphData() (*GraphData, error) {
	a.logInfo("Generating graph data...")

	contentDir := filepath.Join(a.dataDir, "content")
	if _, err := os.Stat(contentDir); os.IsNotExist(err) {
		return &GraphData{
			Nodes: []GraphNode{},
			Edges: []GraphEdge{},
			Meta:  GraphMeta{Directed: true},
		}, nil
	}

	// ファイル一覧を取得
	var files []string
	err := filepath.Walk(contentDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			// Store path relative to dataDir so it begins with "content/..."
			rel, _ := filepath.Rel(a.dataDir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk content directory: %v", err)
	}

	// ノードとエッジを生成
	nodes := make(map[string]*GraphNode)
	edges := make(map[string]*GraphEdge)
	edgeCounts := make(map[string]int) // source-target の参照回数

	// 各ファイルを処理
	for _, filePath := range files {
		nodeID := "doc:/" + strings.TrimPrefix(filePath, "content/")
		title := filepath.Base(filePath)
		title = strings.TrimSuffix(title, ".md")

		var fileHash string
		var fileContent string
		var frontMatter *fm.FrontMatter
		var docIDFromEnsure string // ensureDocIDで取得したdocIDを保持

		// ファイル内容を読み込み
		var tags []string
		var markdownBody string
		if content, err := os.ReadFile(filepath.Join(a.dataDir, filePath)); err == nil {
			fileContent = string(content)

			// Ensure doc_id exists (lazy assignment)
			contentWithDocID, docID, err := a.ensureDocID(fileContent)
			if err != nil {
				a.logError(fmt.Sprintf("Failed to ensure doc_id for %s: %v", filePath, err))
				// Continue with original content if doc_id generation fails
			} else {
				docIDFromEnsure = docID // ensureDocIDで取得したdocIDを保持
				if docID != "" && contentWithDocID != fileContent {
					// Save the updated content with doc_id if it was added
					absPath := filepath.Join(a.dataDir, filePath)
					if err := os.WriteFile(absPath, []byte(contentWithDocID), 0644); err != nil {
						a.logError(fmt.Sprintf("Failed to save file with doc_id: %v", err))
					} else {
						fileContent = contentWithDocID
						a.logInfo(fmt.Sprintf("Assigned doc_id to file: %s -> %s", filePath, docID))
					}
				}
			}

			title = a.extractTitleFromContent(fileContent, title)
			// タグを抽出
			tags = fm.ExtractTags(fileContent)
			// デバッグ: フロントマターのパース結果を確認
			frontMatter, body := fm.ParseFrontMatter(fileContent)
			if frontMatter != nil {
				a.logInfo(fmt.Sprintf("File %s: frontmatter parsed - title: %q, tags: %q, theme: %q, doc_id: %q", filePath, frontMatter.Title, frontMatter.Tags, frontMatter.Theme, frontMatter.DocID))
				a.logInfo(fmt.Sprintf("File %s: extracted tags: %v", filePath, tags))
			} else {
				a.logInfo(fmt.Sprintf("File %s: no frontmatter found", filePath))
			}
			markdownBody = body
			if markdownBody == "" {
				markdownBody = fileContent // フロントマターがない場合
			}
			// ハッシュを計算（フルコンテンツ）
			fileHash = gitvcs.CalculateHash(fileContent)
		}

		// doc_idを取得（frontMatterから取得、なければensureDocIDで取得したdocIDを使用）
		var docID string
		if frontMatter != nil && frontMatter.DocID != "" {
			docID = frontMatter.DocID
		} else if docIDFromEnsure != "" {
			// frontMatterにdocIDがない場合、ensureDocIDで取得したdocIDを使用
			docID = docIDFromEnsure
		}

		// ノードを作成
		nodes[nodeID] = &GraphNode{
			ID:     nodeID,
			DocID:  docID,
			Label:  title,
			Kind:   "note",
			Exists: true,
			DegIn:  0,
			DegOut: 0,
			Tags:   tags,
			Hash:   fileHash,
		}

		// ファイル内容を解析してリンクを抽出（フロントマターを除いた本文を使用）
		if markdownBody != "" {
			links := a.extractLinks(markdownBody)
			a.logInfo(fmt.Sprintf("File %s: found %d links", filePath, len(links)))
			for i, link := range links {
				targetID := a.resolveLinkTarget(link, filePath)
				a.logInfo(fmt.Sprintf("  Link %d: %s -> %s (kind: %s)", i+1, link.Target, targetID, link.Kind))
				if targetID != "" {
					// エッジの重みをカウント
					edgeKey := nodeID + "->" + targetID
					edgeCounts[edgeKey]++

					// ターゲットファイルのハッシュを取得（現在のバージョン）
					currentTargetHash := ""
					if targetNode, exists := nodes[targetID]; exists {
						currentTargetHash = targetNode.Hash
					} else {
						// ターゲットファイルがまだ処理されていない場合、ファイルを読み込んでハッシュを計算
						if strings.HasPrefix(targetID, "doc:/") {
							targetPath := strings.TrimPrefix(targetID, "doc:/")
							targetFilePath := filepath.Join(a.dataDir, "content", targetPath)
							if targetContent, err := os.ReadFile(targetFilePath); err == nil {
								currentTargetHash = gitvcs.CalculateHash(string(targetContent))
							}
						}
					}

					// エッジIDを生成
					edgeID := fmt.Sprintf("e_%s_%s", strings.ReplaceAll(nodeID, "/", "_"), strings.ReplaceAll(targetID, "/", "_"))

					// ターゲットノードのdoc_idを取得
					var targetDocID string
					if targetNode, exists := nodes[targetID]; exists {
						targetDocID = targetNode.DocID
					} else if strings.HasPrefix(targetID, "doc:/") {
						// ターゲットファイルがまだ処理されていない場合、ファイルを読み込んでdoc_idを取得
						targetPath := strings.TrimPrefix(targetID, "doc:/")
						targetFilePath := filepath.Join(a.dataDir, "content", targetPath)
						if targetContent, err := os.ReadFile(targetFilePath); err == nil {
							if targetFM, _ := fm.ParseFrontMatter(string(targetContent)); targetFM != nil {
								targetDocID = targetFM.DocID
							}
						}
					}

					// 既存のエッジがあるかチェックして、ターゲットの更新状況を判定
					targetUpdated := false
					storedTargetHash := currentTargetHash // デフォルトは現在のハッシュ

					if existingEdge, exists := edges[edgeID]; exists && existingEdge.TargetHash != "" {
						// 既存のエッジがあり、以前のターゲットハッシュが記録されている場合
						storedTargetHash = existingEdge.TargetHash // 以前記録されたハッシュを保持

						// 現在のターゲットハッシュと以前のハッシュを比較
						if currentTargetHash != "" && storedTargetHash != "" && currentTargetHash != storedTargetHash {
							targetUpdated = true
						}
					}

					// エッジを作成または更新
					// リンク作成時は常にpinnedバージョンとして記録（リンク作成時のターゲットのバージョンを固定）
					toVersionMode := "pinned"
					toVersionID := storedTargetHash // リンク作成時のターゲットのハッシュをバージョンIDとして保存
					if toVersionID == "" {
						// 初回リンク作成時は現在のハッシュを使用
						toVersionID = currentTargetHash
						toVersionMode = "pinned"
					}

					edges[edgeID] = &GraphEdge{
						ID:            edgeID,
						Source:        nodeID,
						Target:        targetID,
						SourceDocID:   docID,
						TargetDocID:   targetDocID,
						Kind:          link.Kind,
						Weight:        edgeCounts[edgeKey],
						SourceHash:    fileHash,
						TargetHash:    storedTargetHash, // 以前のハッシュを保持（初回は現在のハッシュ）
						TargetUpdated: targetUpdated,
						ToVersionMode: toVersionMode,
						ToVersionID:   toVersionID,
					}
					a.logInfo(fmt.Sprintf("    Created edge with SourceDocID=%s, TargetDocID=%s, ToVersionMode=%s, TargetUpdated=%v", docID, targetDocID, toVersionMode, targetUpdated))
					sourceHashShort := ""
					if len(fileHash) >= 8 {
						sourceHashShort = fileHash[:8]
					}
					targetHashShort := ""
					if len(storedTargetHash) >= 8 {
						targetHashShort = storedTargetHash[:8]
					}
					updateStatus := ""
					if targetUpdated {
						updateStatus = " [TARGET UPDATED]"
					}
					a.logInfo(fmt.Sprintf("    Created edge: %s -> %s (weight: %d, sourceHash: %s, targetHash: %s)%s", nodeID, targetID, edgeCounts[edgeKey], sourceHashShort, targetHashShort, updateStatus))
				}
			}
		} else {
			a.logError(fmt.Sprintf("Failed to read file %s: %v", filePath, err))
		}
	}

	// 存在しないファイルのノードを作成（エッジの作成後）
	for _, edge := range edges {
		// ターゲットノードが存在しない場合
		if _, exists := nodes[edge.Target]; !exists {
			if strings.HasPrefix(edge.Target, "doc:/") {
				path := strings.TrimPrefix(edge.Target, "doc:/")
				title := filepath.Base(path)
				title = strings.TrimSuffix(title, ".md")

				// ファイルが存在する場合はフロントマターからタイトルとタグを取得
				var tags []string
				filePath := filepath.Join(a.dataDir, "content", path)
				if content, err := os.ReadFile(filePath); err == nil {
					fileContent := string(content)
					title = a.extractTitleFromContent(fileContent, title)
					tags = fm.ExtractTags(fileContent)
					nodes[edge.Target] = &GraphNode{
						ID:     edge.Target,
						Label:  title,
						Kind:   "note",
						Exists: true, // ファイルが存在する
						DegIn:  0,
						DegOut: 0,
						Tags:   tags,
					}
				} else {
					nodes[edge.Target] = &GraphNode{
						ID:     edge.Target,
						Label:  title,
						Kind:   "note",
						Exists: false,
						DegIn:  0,
						DegOut: 0,
						Tags:   []string{},
					}
				}
				a.logInfo(fmt.Sprintf("Created missing target node: %s", edge.Target))
			} else if strings.HasPrefix(edge.Target, "img:/") {
				path := strings.TrimPrefix(edge.Target, "img:/")
				title := filepath.Base(path)

				// Read tags from WebP file if it's a WebP image
				var tags []string
				imageAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(path))
				if strings.ToLower(filepath.Ext(path)) == ".webp" {
					webpTags, err := webpchunk.ReadTagsFromWebP(imageAbsPath)
					if err != nil {
						a.logError(fmt.Sprintf("Failed to read tags from WebP chunk for %s: %v", path, err))
					} else {
						tags = webpTags
					}
				}

				nodes[edge.Target] = &GraphNode{
					ID:     edge.Target,
					Label:  title,
					Kind:   "asset:image",
					Exists: true, // 画像は存在すると仮定
					DegIn:  0,
					DegOut: 0,
					Tags:   tags,
				}
				a.logInfo(fmt.Sprintf("Created missing image node: %s (tags: %v)", edge.Target, tags))
			}
		}

		// ソースノードが存在しない場合（念のため）
		if _, exists := nodes[edge.Source]; !exists && strings.HasPrefix(edge.Source, "doc:/") {
			path := strings.TrimPrefix(edge.Source, "doc:/")
			title := filepath.Base(path)
			title = strings.TrimSuffix(title, ".md")

			// ファイルが存在する場合はフロントマターからタイトルとタグを取得
			var tags []string
			filePath := filepath.Join(a.dataDir, "content", path)
			if content, err := os.ReadFile(filePath); err == nil {
				fileContent := string(content)
				title = a.extractTitleFromContent(fileContent, title)
				tags = fm.ExtractTags(fileContent)
				nodes[edge.Source] = &GraphNode{
					ID:     edge.Source,
					Label:  title,
					Kind:   "note",
					Exists: true, // ファイルが存在する
					DegIn:  0,
					DegOut: 0,
					Tags:   tags,
				}
			} else {
				nodes[edge.Source] = &GraphNode{
					ID:     edge.Source,
					Label:  title,
					Kind:   "note",
					Exists: false,
					DegIn:  0,
					DegOut: 0,
					Tags:   []string{},
				}
			}
			a.logInfo(fmt.Sprintf("Created missing source node: %s", edge.Source))
		}
	}

	// 入出次数を計算
	for _, edge := range edges {
		if sourceNode, exists := nodes[edge.Source]; exists {
			sourceNode.DegOut++
		}
		if targetNode, exists := nodes[edge.Target]; exists {
			targetNode.DegIn++
		}
	}

	// 永続化されたリンク情報を読み込む
	linkInfoPath := filepath.Join(a.dataDir, ".mdsys", "links.json")
	persistedLinks := make(map[string]*GraphEdge)
	if linkData, err := os.ReadFile(linkInfoPath); err == nil {
		var persistedEdges []GraphEdge
		if err := json.Unmarshal(linkData, &persistedEdges); err == nil {
			for i := range persistedEdges {
				persistedLinks[persistedEdges[i].ID] = &persistedEdges[i]
			}
			a.logInfo(fmt.Sprintf("Loaded %d persisted link records", len(persistedLinks)))
		}
	}

	// 永続化された情報とマージして、ターゲット更新を検出
	for edgeID, edge := range edges {
		if persistedEdge, exists := persistedLinks[edgeID]; exists && persistedEdge.TargetHash != "" {
			// 永続化されたハッシュがある場合、それと比較
			currentHash := ""

			// まず、edge.Target（古いパス）で試す
			if targetNode, exists := nodes[edge.Target]; exists {
				currentHash = targetNode.Hash
				a.logInfo(fmt.Sprintf("Target updated check for edge %s: found hash from nodes[%s]: %s", edgeID, edge.Target, currentHash[:8]))
			} else if strings.HasPrefix(edge.Target, "doc:/") {
				targetPath := strings.TrimPrefix(edge.Target, "doc:/")
				targetFilePath := filepath.Join(a.dataDir, "content", targetPath)
				if targetContent, err := os.ReadFile(targetFilePath); err == nil {
					currentHash = gitvcs.CalculateHash(string(targetContent))
					a.logInfo(fmt.Sprintf("Target updated check for edge %s: found hash from file %s: %s", edgeID, targetPath, currentHash[:8]))
				} else {
					a.logInfo(fmt.Sprintf("Target updated check for edge %s: file not found at %s (may be renamed)", edgeID, targetPath))
				}
			}

			// edge.Targetでファイルが見つからない場合（リネーム後）、TargetDocIDを使って現在のパスを取得
			if currentHash == "" && edge.TargetDocID != "" {
				a.logInfo(fmt.Sprintf("Target updated check for edge %s: trying to resolve via TargetDocID %s", edgeID, edge.TargetDocID))
				docMapPath := filepath.Join(a.dataDir, ".mdsys", "doc_map.json")
				if docMapData, err := os.ReadFile(docMapPath); err == nil {
					var docMap map[string]string
					if err := json.Unmarshal(docMapData, &docMap); err == nil {
						if currentPath, exists := docMap[edge.TargetDocID]; exists {
							// doc_mapから取得したパスでファイルを読み込む
							// currentPathは "content/..." 形式なので、filepath.FromSlashで正規化してから結合
							normalizedPath := filepath.FromSlash(currentPath)
							targetFilePath := filepath.Join(a.dataDir, normalizedPath)
							a.logInfo(fmt.Sprintf("Target updated check: attempting to read file at resolved path (doc_id=%s): %s -> %s", edge.TargetDocID, currentPath, targetFilePath))
							if targetContent, err := os.ReadFile(targetFilePath); err == nil {
								currentHash = gitvcs.CalculateHash(string(targetContent))
								a.logInfo(fmt.Sprintf("Target updated check: resolved path via doc_map for doc_id %s: %s (hash: %s)", edge.TargetDocID, currentPath, currentHash[:8]))
							} else {
								a.logError(fmt.Sprintf("Target updated check: failed to read file at resolved path %s (normalized: %s): %v", currentPath, targetFilePath, err))
								// フォールバック: 直接パスを試す（マルチバイト文字の問題の可能性）
								if targetContent2, err2 := os.ReadFile(filepath.Join(a.dataDir, currentPath)); err2 == nil {
									currentHash = gitvcs.CalculateHash(string(targetContent2))
									a.logInfo(fmt.Sprintf("Target updated check: succeeded with direct path join for doc_id %s: %s (hash: %s)", edge.TargetDocID, currentPath, currentHash[:8]))
								} else {
									a.logError(fmt.Sprintf("Target updated check: fallback also failed for %s: %v", currentPath, err2))
								}
							}
						} else {
							a.logInfo(fmt.Sprintf("Target updated check: doc_id %s not found in doc_map", edge.TargetDocID))
						}
					} else {
						a.logError(fmt.Sprintf("Target updated check: failed to parse doc_map.json: %v", err))
					}
				} else {
					a.logError(fmt.Sprintf("Target updated check: failed to read doc_map.json: %v", err))
				}
			}

			if currentHash != "" && persistedEdge.TargetHash != currentHash {
				edge.TargetUpdated = true
				a.logInfo(fmt.Sprintf("Target updated detected for edge %s: old=%s, new=%s", edgeID, persistedEdge.TargetHash[:8], currentHash[:8]))
			} else if currentHash == "" {
				a.logInfo(fmt.Sprintf("Target updated check for edge %s: currentHash is empty, cannot determine if updated", edgeID))
			} else {
				a.logInfo(fmt.Sprintf("Target updated check for edge %s: hash unchanged (old=%s, new=%s)", edgeID, persistedEdge.TargetHash[:8], currentHash[:8]))
			}
			// 永続化されたハッシュを保持（リンク作成時のハッシュ）
			edge.TargetHash = persistedEdge.TargetHash
		}
		// 新しいエッジの場合、現在のハッシュが既にedge.TargetHashに設定されている
	}

	// 画像ファイルを列挙して、画像ノードを作成または更新（タグを読み取る）
	imageList := a.GetImageList()
	for _, imageItem := range imageList {
		imageNodeID := "img:/" + imageItem.Path
		title := imageItem.Name

		// Read tags from WebP file if it's a WebP image
		var tags []string
		imageAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(imageItem.Path))
		if strings.ToLower(filepath.Ext(imageItem.Path)) == ".webp" {
			webpTags, err := webpchunk.ReadTagsFromWebP(imageAbsPath)
			if err != nil {
				a.logError(fmt.Sprintf("Failed to read tags from WebP chunk for %s: %v", imageItem.Path, err))
			} else {
				tags = webpTags
			}
		}

		// Create or update image node
		if existingNode, exists := nodes[imageNodeID]; exists {
			// Update existing node with tags
			existingNode.Tags = tags
			a.logInfo(fmt.Sprintf("Updated image node tags: %s (tags: %v)", imageNodeID, tags))
		} else {
			// Create new image node
			nodes[imageNodeID] = &GraphNode{
				ID:     imageNodeID,
				Label:  title,
				Kind:   "asset:image",
				Exists: true,
				DegIn:  0,
				DegOut: 0,
				Tags:   tags,
			}
			a.logInfo(fmt.Sprintf("Created image node: %s (tags: %v)", imageNodeID, tags))
		}
	}

	// スライスに変換
	var nodeList []GraphNode
	for _, node := range nodes {
		nodeList = append(nodeList, *node)
	}

	var edgeList []GraphEdge
	for _, edge := range edges {
		edgeList = append(edgeList, *edge)
	}

	// タグノードを作成し、同じタグを持つドキュメントと接続
	tagNodes := make(map[string]*GraphNode)
	tagNodeCount := 0
	for _, node := range nodeList {
		if len(node.Tags) > 0 {
			a.logInfo(fmt.Sprintf("Node %s has %d tags: %v", node.ID, len(node.Tags), node.Tags))
		}
		for _, tag := range node.Tags {
			if tag == "" {
				continue
			}
			tagID := "tag:/" + tag

			// タグノードが存在しない場合は作成
			if _, exists := tagNodes[tagID]; !exists {
				tagNodes[tagID] = &GraphNode{
					ID:     tagID,
					Label:  "#" + tag,
					Kind:   "tag",
					Exists: true,
					DegIn:  0,
					DegOut: 0,
					Tags:   []string{},
				}
				tagNodeCount++
				a.logInfo(fmt.Sprintf("Creating tag node: %s (label: #%s)", tagID, tag))
			}

			// ドキュメントノードとタグノードを接続するエッジを作成
			edgeID := fmt.Sprintf("tag_edge_%s_%s", strings.ReplaceAll(node.ID, "/", "_"), strings.ReplaceAll(tagID, "/", "_"))
			edgeList = append(edgeList, GraphEdge{
				ID:     edgeID,
				Source: node.ID,
				Target: tagID,
				Kind:   "tag",
				Weight: 1,
			})
			a.logInfo(fmt.Sprintf("Created tag edge: %s -> %s", node.ID, tagID))

			// タグノードの入次数を増やす
			tagNodes[tagID].DegIn++
			// ドキュメントノードの出次数を増やす（既にカウントされている可能性があるが、念のため）
		}
	}

	// タグノードをノードリストに追加
	for _, tagNode := range tagNodes {
		nodeList = append(nodeList, *tagNode)
	}
	a.logInfo(fmt.Sprintf("Created %d tag nodes (total nodes: %d, total edges: %d)", tagNodeCount, len(nodeList), len(edgeList)))

	// リンク情報を永続化
	if linkInfoJSON, err := json.MarshalIndent(edgeList, "", "  "); err == nil {
		if err := os.MkdirAll(filepath.Dir(linkInfoPath), 0755); err == nil {
			if err := os.WriteFile(linkInfoPath, linkInfoJSON, 0644); err == nil {
				a.logInfo(fmt.Sprintf("Saved %d link records to %s", len(edgeList), linkInfoPath))
			}
		}
	}

	// doc_idからパスへのマッピングを保存
	docMapPath := filepath.Join(a.dataDir, ".mdsys", "doc_map.json")
	docMap := make(map[string]string)

	// 既存のマッピングを読み込む
	if data, err := os.ReadFile(docMapPath); err == nil {
		if err := json.Unmarshal(data, &docMap); err != nil {
			a.logError(fmt.Sprintf("Failed to parse doc_map.json: %v", err))
			docMap = make(map[string]string)
		}
	}

	// 各ノードのdoc_idとパスのマッピングを更新
	for _, node := range nodeList {
		if node.DocID != "" && strings.HasPrefix(node.ID, "doc:/") {
			path := strings.TrimPrefix(node.ID, "doc:/")
			contentPath := filepath.Join("content", path)
			docMap[node.DocID] = contentPath
		}
	}

	// マッピングを保存
	if docMapJSON, err := json.MarshalIndent(docMap, "", "  "); err == nil {
		if err := os.MkdirAll(filepath.Dir(docMapPath), 0755); err == nil {
			if err := os.WriteFile(docMapPath, docMapJSON, 0644); err == nil {
				a.logInfo(fmt.Sprintf("Saved %d doc_id mappings to %s", len(docMap), docMapPath))
			} else {
				a.logError(fmt.Sprintf("Failed to write doc_map.json: %v", err))
			}
		}
	} else {
		a.logError(fmt.Sprintf("Failed to marshal doc_map: %v", err))
	}

	// デバッグ用：ノードIDとエッジの詳細をログ出力
	a.logInfo(fmt.Sprintf("Generated graph with %d nodes and %d edges", len(nodeList), len(edgeList)))

	// ノードIDの一覧をログ出力
	nodeIds := make([]string, 0, len(nodes))
	for id := range nodes {
		nodeIds = append(nodeIds, id)
	}
	a.logInfo(fmt.Sprintf("Node IDs: %v", nodeIds))

	// エッジの詳細をログ出力
	for i, edge := range edgeList {
		if i < 5 { // 最初の5個のエッジのみログ出力
			a.logInfo(fmt.Sprintf("Edge %d: %s -> %s (kind: %s, weight: %d)",
				i+1, edge.Source, edge.Target, edge.Kind, edge.Weight))
		}
	}

	return &GraphData{
		Nodes: nodeList,
		Edges: edgeList,
		Meta:  GraphMeta{Directed: true},
	}, nil
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
func (a *App) ExportPDF(html string) (string, error) {
	if html == "" {
		return "", fmt.Errorf("empty html")
	}
	exportDir := filepath.Join(a.dataDir, "export")
	if err := os.MkdirAll(exportDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create export dir: %v", err)
	}
	base := fmt.Sprintf("export-%s.pdf", time.Now().Format("20060102-150405"))
	pdfPath := filepath.Join(exportDir, base)
	a.logInfo(fmt.Sprintf("ExportPDF start: out=%s html.len=%d", pdfPath, len(html)))
	if err := pdfexport.ExportHTMLToPDF(html, pdfPath); err != nil {
		a.logError(fmt.Sprintf("ExportPDF failed: %v", err))
		return "", err
	}
	a.logInfo(fmt.Sprintf("PDF exported: %s", pdfPath))
	url := strings.ReplaceAll(pdfPath, "\\", "/")
	a.logInfo("url --- " + url)
	return url, nil
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

// GetAudioFileURL returns a URL for the audio file that can be used in HTML audio elements.
// The audioPath should be relative to dataDir (e.g., "data/audio/xxx.wav").
// Returns a URL path that will be served by the HTTP handler.
func (a *App) GetAudioFileURL(audioPath string) (string, error) {
	if audioPath == "" {
		return "", fmt.Errorf("audio path is empty")
	}

	// Resolve to absolute path to verify file exists
	var absPath string
	if filepath.IsAbs(audioPath) {
		absPath = audioPath
	} else {
		// Assume relative to dataDir
		absPath = filepath.Join(a.dataDir, filepath.FromSlash(audioPath))
	}

	// Check if file exists
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("audio file not found: %s", audioPath)
		}
		return "", fmt.Errorf("failed to stat audio file: %w", err)
	}

	// Return URL path that will be handled by the HTTP handler
	// The handler will serve files from /audio/ path
	urlPath := "/audio/" + filepath.ToSlash(audioPath)

	// Log file size for debugging
	a.logInfo(fmt.Sprintf("Audio file URL: %s (size: %d bytes)", urlPath, info.Size()))

	return urlPath, nil
}

// GetImageFileURL returns a URL for the image file that can be used in HTML img elements.
// The imagePath should be relative to dataDir (e.g., "data/image/xxx.png").
func (a *App) GetImageFileURL(imagePath string) (string, error) {
	if imagePath == "" {
		return "", fmt.Errorf("image path is empty")
	}

	var absPath string
	if filepath.IsAbs(imagePath) {
		absPath = imagePath
	} else {
		absPath = filepath.Join(a.dataDir, filepath.FromSlash(imagePath))
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("image file not found: %s", imagePath)
		}
		return "", fmt.Errorf("failed to stat image file: %w", err)
	}

	urlPath := "/image/" + filepath.ToSlash(imagePath)
	a.logInfo(fmt.Sprintf("Image file URL: %s (size: %d bytes)", urlPath, info.Size()))
	return urlPath, nil
}

// GetPdfFileURL returns a URL for the PDF file that can be used in HTML embed/iframe elements.
// The pdfPath should be relative to dataDir (e.g., "content/example.pdf").
func (a *App) GetPdfFileURL(pdfPath string) (string, error) {
	if pdfPath == "" {
		return "", fmt.Errorf("pdf path is empty")
	}

	var absPath string
	if filepath.IsAbs(pdfPath) {
		absPath = pdfPath
	} else {
		absPath = filepath.Join(a.dataDir, filepath.FromSlash(pdfPath))
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("pdf file not found: %s", pdfPath)
		}
		return "", fmt.Errorf("failed to stat pdf file: %w", err)
	}

	urlPath := "/pdf/" + filepath.ToSlash(pdfPath)
	a.logInfo(fmt.Sprintf("PDF file URL: %s (size: %d bytes)", urlPath, info.Size()))
	return urlPath, nil
}

// createAudioHandler creates an HTTP handler that serves audio files.
// This handler wraps the default asset handler and adds support for /audio/ paths.
func (a *App) createAssetHandler() http.Handler {
	// Get default asset handler
	defaultHandler := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/audio/") {
			if a.serveMediaFile(w, r, "/audio/", "audio") {
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/image/") {
			if a.serveMediaFile(w, r, "/image/", "image") {
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/pdf/") {
			if a.serveMediaFile(w, r, "/pdf/", "pdf") {
				return
			}
		}

		// For all other paths, use default asset handler
		defaultHandler.ServeHTTP(w, r)
	})
}

func (a *App) serveMediaFile(w http.ResponseWriter, r *http.Request, prefix, mediaType string) bool {
	relPath := strings.TrimPrefix(r.URL.Path, prefix)

	a.logInfo(fmt.Sprintf("serveMediaFile: %s request for %s, relPath: %s", mediaType, r.URL.Path, relPath))

	var absPath string
	if filepath.IsAbs(relPath) {
		absPath = relPath
	} else {
		absPath = filepath.Join(a.dataDir, filepath.FromSlash(relPath))
	}

	a.logInfo(fmt.Sprintf("serveMediaFile: %s resolved path: %s", mediaType, absPath))

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			a.logError(fmt.Sprintf("serveMediaFile: %s file not found: %s (resolved: %s)", mediaType, relPath, absPath))
		} else {
			a.logError(fmt.Sprintf("serveMediaFile: %s stat error: %v (path: %s)", mediaType, err, absPath))
		}
		http.Error(w, fmt.Sprintf("%s file not found", strings.Title(mediaType)), http.StatusNotFound)
		return true
	}

	a.logInfo(fmt.Sprintf("serveMediaFile: %s file found: %s (size: %d bytes)", mediaType, absPath, info.Size()))

	file, err := os.Open(absPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to open %s file", mediaType), http.StatusInternalServerError)
		return true
	}
	defer file.Close()

	ext := strings.ToLower(filepath.Ext(absPath))
	var mimeType string

	if mediaType == "audio" {
		switch ext {
		case ".wav":
			mimeType = "audio/wav"
		case ".mp3":
			mimeType = "audio/mpeg"
		case ".m4a":
			mimeType = "audio/mp4"
		case ".ogg":
			mimeType = "audio/ogg"
		default:
			mimeType = "audio/mpeg"
		}
	} else {
		switch ext {
		case ".jpg", ".jpeg":
			mimeType = "image/jpeg"
		case ".png":
			mimeType = "image/png"
		case ".gif":
			mimeType = "image/gif"
		case ".webp":
			mimeType = "image/webp"
		case ".svg":
			mimeType = "image/svg+xml"
		default:
			mimeType = "application/octet-stream"
		}
	}

	w.Header().Set("Content-Type", mimeType)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	if mediaType == "audio" {
		w.Header().Set("Accept-Ranges", "bytes")
	}

	http.ServeContent(w, r, filepath.Base(absPath), info.ModTime(), file)
	return true
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
	if err := os.Rename(oldAbsPath, newAbsPath); err != nil {
		return fmt.Errorf("failed to rename file: %v", err)
	}

	// Update doc_map.json
	docMapPath := filepath.Join(a.dataDir, ".mdsys", "doc_map.json")
	docMap := make(map[string]string)

	// Read existing mapping
	if data, err := os.ReadFile(docMapPath); err == nil {
		if err := json.Unmarshal(data, &docMap); err != nil {
			a.logError(fmt.Sprintf("Failed to parse doc_map.json: %v", err))
			docMap = make(map[string]string)
		}
	}

	// Update mapping (doc_id -> content path)
	oldContentPath := filepath.ToSlash(oldPath)
	newContentPath := filepath.ToSlash(newPath)
	docMap[docID] = newContentPath
	a.logInfo(fmt.Sprintf("Updated doc_map: %s -> %s", docID, newContentPath))

	// Save mapping
	if docMapJSON, err := json.MarshalIndent(docMap, "", "  "); err == nil {
		if err := os.WriteFile(docMapPath, docMapJSON, 0644); err != nil {
			a.logError(fmt.Sprintf("Failed to write doc_map.json: %v", err))
		}
	}

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

	// Rebuild index
	if err := a.BuildSite(); err != nil {
		a.logError(fmt.Sprintf("Failed to rebuild site: %v", err))
	}

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

	// Emit file changed event
	runtime.EventsEmit(a.ctx, "file-renamed", map[string]interface{}{
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

	// Load links.json
	linkInfoPath := filepath.Join(a.dataDir, ".mdsys", "links.json")
	var edges []GraphEdge

	// Read existing links
	if linkData, err := os.ReadFile(linkInfoPath); err == nil {
		if err := json.Unmarshal(linkData, &edges); err != nil {
			a.logError(fmt.Sprintf("Failed to parse links.json: %v", err))
			return fmt.Errorf("failed to parse links.json: %v", err)
		}
	} else {
		return fmt.Errorf("failed to read links.json: %v", err)
	}

	// Find and update the matching edge
	found := false
	for i := range edges {
		if edges[i].SourceDocID == sourceDocID && edges[i].TargetDocID == targetDocID {
			edges[i].ToVersionMode = "latest"
			edges[i].TargetUpdated = false
			// Update ToVersionID to current target hash
			if strings.HasPrefix(edges[i].Target, "doc:/") {
				targetPath := strings.TrimPrefix(edges[i].Target, "doc:/")
				targetFilePath := filepath.Join(a.dataDir, "content", targetPath)
				if targetContent, err := os.ReadFile(targetFilePath); err == nil {
					currentHash := gitvcs.CalculateHash(string(targetContent))
					edges[i].ToVersionID = currentHash
					edges[i].TargetHash = currentHash
				}
			}
			found = true
			a.logInfo(fmt.Sprintf("Updated edge %s -> %s to latest version", edges[i].Source, edges[i].Target))
			break
		}
	}

	if !found {
		return fmt.Errorf("link not found: sourceDocID=%s, targetDocID=%s", sourceDocID, targetDocID)
	}

	// Save updated links
	if linkInfoJSON, err := json.MarshalIndent(edges, "", "  "); err == nil {
		if err := os.MkdirAll(filepath.Dir(linkInfoPath), 0755); err == nil {
			if err := os.WriteFile(linkInfoPath, linkInfoJSON, 0644); err == nil {
				a.logInfo(fmt.Sprintf("Saved updated link records to %s", linkInfoPath))
			} else {
				return fmt.Errorf("failed to write links.json: %v", err)
			}
		} else {
			return fmt.Errorf("failed to create directory: %v", err)
		}
	} else {
		return fmt.Errorf("failed to marshal links: %v", err)
	}

	// Emit event to refresh preview
	runtime.EventsEmit(a.ctx, "link-updated", map[string]interface{}{
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
	runtime.LogInfo(a.ctx, fmt.Sprintf("Found %d wiki links", len(matches)))
	for _, match := range matches {
		title := match[1]
		// .md拡張子を追加
		if !strings.HasSuffix(strings.ToLower(title), ".md") {
			title += ".md"
		}
		links = append(links, LinkInfo{Target: title, Kind: "wikilink"})
		runtime.LogInfo(a.ctx, fmt.Sprintf("  Wiki link: %s", title))
	}

	// Markdownリンク [text](url)
	markdownLinkRegex := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	matches = markdownLinkRegex.FindAllStringSubmatch(content, -1)
	runtime.LogInfo(a.ctx, fmt.Sprintf("Found %d markdown links", len(matches)))
	for _, match := range matches {
		url := match[2]
		if strings.HasSuffix(strings.ToLower(url), ".md") {
			links = append(links, LinkInfo{Target: url, Kind: "markdown_link"})
			runtime.LogInfo(a.ctx, fmt.Sprintf("  Markdown link: %s", url))
		}
	}

	// 画像リンク ![alt](src)
	imgLinkRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	matches = imgLinkRegex.FindAllStringSubmatch(content, -1)
	runtime.LogInfo(a.ctx, fmt.Sprintf("Found %d image links", len(matches)))
	for _, match := range matches {
		src := match[2]
		links = append(links, LinkInfo{Target: src, Kind: "img"})
		runtime.LogInfo(a.ctx, fmt.Sprintf("  Image link: %s", src))
	}

	// 引用 > text 内のWikiリンク
	quoteRegex := regexp.MustCompile(`(?m)^>\s*.*?\[\[([^|\]]+)(?:\|([^\]]+))?\]\].*$`)
	matches = quoteRegex.FindAllStringSubmatch(content, -1)
	runtime.LogInfo(a.ctx, fmt.Sprintf("Found %d quote blocks with wiki links", len(matches)))
	for _, match := range matches {
		title := match[1]
		if !strings.HasSuffix(strings.ToLower(title), ".md") {
			title += ".md"
		}
		links = append(links, LinkInfo{Target: title, Kind: "quote"})
		runtime.LogInfo(a.ctx, fmt.Sprintf("  Quote link: %s", title))
	}

	runtime.LogInfo(a.ctx, fmt.Sprintf("Total links extracted: %d", len(links)))
	return links
}

// resolveLinkTarget resolves a link target to a node ID
func (a *App) resolveLinkTarget(link LinkInfo, currentFile string) string {
	runtime.LogInfo(a.ctx, fmt.Sprintf("Resolving link: %s (kind: %s) from file: %s", link.Target, link.Kind, currentFile))

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
		runtime.LogInfo(a.ctx, fmt.Sprintf("  Resolved to: %s", result))
		return result
	case "img":
		result := "img:/" + link.Target
		runtime.LogInfo(a.ctx, fmt.Sprintf("  Resolved to: %s", result))
		return result
	default:
		runtime.LogInfo(a.ctx, fmt.Sprintf("  No resolution for kind: %s", link.Kind))
		return ""
	}
}

// recordingSegment holds state for a single speech segment during recording
type recordingSegment struct {
	samples          []float32
	startSampleIndex int
}

// StartRecording starts real-time recording with transcription
// Uses existing asr.Service (OfflineStream) instead of RealtimeService to avoid crashes
func (a *App) StartRecording() error {
	a.logInfo("[Recording] StartRecording called")
	a.recordingMu.Lock()
	defer a.recordingMu.Unlock()

	if a.isRecording {
		a.logError("[Recording] StartRecording called but already recording")
		return fmt.Errorf("recording already in progress")
	}

	// Wait for ASR to be ready
	a.logInfo("[Recording] Waiting for ASR service to be ready...")
	if !a.waitForASRReady() {
		a.logError("[Recording] ASR service not ready")
		return fmt.Errorf("ASR service not ready")
	}
	a.logInfo("[Recording] ASR service is ready")

	// Initialize RealtimeService for partial text support
	// Check if model is suitable for online recognition (streaming model)
	a.logInfo("[Recording] Initializing RealtimeService for partial text...")
	cfgPath := filepath.Join(a.dataDir, "data", "asr", "config.json")
	a.logInfo(fmt.Sprintf("[Recording] Loading ASR config from: %s", cfgPath))
	cfg, err := asr.LoadConfigFromFile(cfgPath)
	if err != nil {
		a.logError(fmt.Sprintf("[Recording] Failed to load ASR config: %v", err))
		return fmt.Errorf("failed to load ASR config: %w", err)
	}
	if cfg == nil {
		a.logError("[Recording] ASR config is nil")
		return fmt.Errorf("ASR config is nil")
	}
	a.logInfo(fmt.Sprintf("[Recording] ASR config loaded: enabled=%v, sampleRate=%d", cfg.Enabled, cfg.SampleRate))
	if !cfg.Enabled {
		a.logError("[Recording] ASR config not enabled")
		return fmt.Errorf("ASR not enabled")
	}
	a.logInfo("[Recording] Making model paths absolute...")
	cfg.EnsureModelPathsAbsolute(a.dataDir)
	a.logInfo(fmt.Sprintf("[Recording] Model paths: encoder=%s, decoder=%s, joiner=%s, tokens=%s",
		cfg.Model.Encoder, cfg.Model.Decoder, cfg.Model.Joiner, cfg.Model.Tokens))

	// Check if model is suitable for online recognition
	// Streaming models typically have "chunk", "left", "right", or "streaming" in the filename
	isStreamingModel := strings.Contains(cfg.Model.Encoder, "chunk") ||
		strings.Contains(cfg.Model.Encoder, "left") ||
		strings.Contains(cfg.Model.Encoder, "right") ||
		strings.Contains(cfg.Model.Encoder, "streaming") ||
		strings.Contains(cfg.Model.Decoder, "chunk") ||
		strings.Contains(cfg.Model.Decoder, "left") ||
		strings.Contains(cfg.Model.Decoder, "right") ||
		strings.Contains(cfg.Model.Decoder, "streaming")

	if !isStreamingModel {
		a.logInfo("[Recording] Model appears to be offline-only (no streaming indicators in filename). Skipping RealtimeService initialization.")
		a.logInfo("[Recording] Partial text will not be available, but recording will continue with offline ASR.")
		// Continue without RealtimeService - we'll use offline ASR only
		a.realtimeService = nil
	} else {
		a.logInfo("[Recording] Model appears to be streaming-capable. Initializing RealtimeService...")
		a.logInfo("[Recording] Calling asr.NewRealtimeService...")

		// Create a logger function that writes to app.log
		logFunc := func(format string, args ...interface{}) {
			msg := fmt.Sprintf(format, args...)
			a.logInfo(fmt.Sprintf("[Recording] [RealtimeASR] %s", msg))
		}

		// Call NewRealtimeService directly with panic recovery
		var realtimeService *asr.RealtimeService
		var serviceErr error

		func() {
			defer func() {
				if r := recover(); r != nil {
					errMsg := fmt.Sprintf("panic in NewRealtimeService: %v", r)
					a.logError(fmt.Sprintf("[Recording] %s", errMsg))
					serviceErr = fmt.Errorf(errMsg)
				}
			}()

			a.logInfo("[Recording] Entering NewRealtimeService (this may take a moment)...")
			realtimeService, serviceErr = asr.NewRealtimeServiceWithLogger(cfg, logFunc)
			a.logInfo(fmt.Sprintf("[Recording] NewRealtimeService returned: service=%v, err=%v",
				realtimeService != nil, serviceErr))
		}()

		if serviceErr != nil {
			a.logError(fmt.Sprintf("[Recording] Failed to initialize RealtimeService: %v", serviceErr))
			a.logInfo("[Recording] Continuing without RealtimeService - will use offline ASR only")
			// Don't fail - continue without RealtimeService
			a.realtimeService = nil
		} else if realtimeService == nil {
			a.logError("[Recording] NewRealtimeService returned nil (no error)")
			a.logInfo("[Recording] Continuing without RealtimeService - will use offline ASR only")
			a.realtimeService = nil
		} else {
			a.realtimeService = realtimeService
			a.logInfo("[Recording] RealtimeService initialized successfully")
		}
	}

	// Create recorder
	a.logInfo("[Recording] Creating audio recorder...")
	recorder, err := audio.NewRecorder()
	if err != nil {
		a.logError(fmt.Sprintf("[Recording] Failed to create recorder: %v", err))
		// Clean up RealtimeService if recorder creation fails
		if a.realtimeService != nil {
			a.realtimeService.Close()
			a.realtimeService = nil
		}
		return fmt.Errorf("failed to create recorder: %w", err)
	}
	a.logInfo("[Recording] Audio recorder created successfully")

	// Check if we can access microphone by trying to get default input device
	// This will trigger permission request on macOS if not already granted
	a.logInfo("[Recording] Checking microphone access...")

	// Initialize recording state
	a.recordingSamples = make([]float32, 0)
	a.recordingVAD = audio.DefaultSimpleVAD()
	a.recordingSegment = nil
	a.recordingStopCh = make(chan struct{})
	segmentIndex := 0

	// Create transcript file at start (before recording starts)
	// This ensures the file exists when we append transcript lines
	timestamp := time.Now().Format("20060102-150405")
	baseName := fmt.Sprintf("%s_recording", timestamp)
	dirRel := filepath.ToSlash(filepath.Join("content", "transcripts"))
	filename := baseName + ".md"
	contentRel := filepath.ToSlash(filepath.Join(dirRel, filename))

	// Resolve absolute path
	makeAbs := func(rel string) (string, error) {
		abs, ok := a.resolveContentPath(rel)
		if !ok {
			return "", fmt.Errorf("invalid transcript path: %s", rel)
		}
		return abs, nil
	}

	// Find available filename
	absPath, err := makeAbs(contentRel)
	if err != nil {
		a.logError(fmt.Sprintf("[Recording] Failed to resolve transcript path: %v", err))
		recorder.Close()
		return fmt.Errorf("failed to resolve transcript path: %w", err)
	}

	for i := 2; ; i++ {
		if _, err := os.Stat(absPath); os.IsNotExist(err) {
			break
		}
		filename = fmt.Sprintf("%s-%d.md", baseName, i)
		contentRel = filepath.ToSlash(filepath.Join(dirRel, filename))
		absPath, err = makeAbs(contentRel)
		if err != nil {
			a.logError(fmt.Sprintf("[Recording] Failed to resolve transcript path: %v", err))
			recorder.Close()
			return fmt.Errorf("failed to resolve transcript path: %w", err)
		}
	}

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		a.logError(fmt.Sprintf("[Recording] Failed to create transcript dir: %v", err))
		recorder.Close()
		return fmt.Errorf("failed to create transcript dir: %w", err)
	}

	// Create empty transcript file with frontmatter
	// We'll append transcript lines as they come in
	relAudioPath := filepath.ToSlash(filepath.Join("data", "audio", fmt.Sprintf("%s_recording.m4a", timestamp)))
	body := a.composeTranscriptMarkdown(relAudioPath, "")
	if err := a.SaveFile(contentRel, body); err != nil {
		a.logError(fmt.Sprintf("[Recording] Failed to create transcript file: %v", err))
		recorder.Close()
		return fmt.Errorf("failed to create transcript file: %w", err)
	}

	a.recordingTranscriptPath = contentRel
	a.logInfo(fmt.Sprintf("[Recording] Created transcript file: %s", contentRel))

	// Start recording with callback that buffers samples
	a.logInfo("[Recording] Starting audio recording...")
	err = recorder.Start(func(samples []float32) {
		// This callback runs in audio thread - only buffer samples, no CGO calls
		a.recordingMu.Lock()
		a.recordingSamples = append(a.recordingSamples, samples...)
		a.recordingMu.Unlock()

		// Calculate input level (RMS) for microphone indicator
		// This is safe to do in audio thread as it's just math
		rms := calculateRMS(samples)
		// Emit input level event (use goroutine to avoid blocking audio thread)
		go func() {
			runtime.EventsEmit(a.ctx, "recording-input-level", map[string]interface{}{
				"level": rms,
			})
		}()
	})

	if err != nil {
		recorder.Close()
		errMsg := fmt.Sprintf("[Recording] Failed to start recording: %v", err)
		a.logError(errMsg)
		// Provide helpful error message for permission issues
		if strings.Contains(err.Error(), "permission") || strings.Contains(err.Error(), "denied") || strings.Contains(err.Error(), "access") {
			a.logError("[Recording] Microphone permission may be denied. Please check System Settings > Privacy & Security > Microphone")
			runtime.EventsEmit(a.ctx, "recording-error", map[string]interface{}{
				"error":   "マイクの権限が拒否されています。システム設定 > プライバシーとセキュリティ > マイク でKarteにマイクへのアクセスを許可してください。",
				"details": err.Error(),
			})
		}
		return fmt.Errorf("failed to start recording: %w", err)
	}

	a.recorder = recorder
	a.isRecording = true

	// Start processing goroutine that handles ASR (runs outside audio thread)
	a.recordingWg.Add(1)
	go func() {
		defer a.recordingWg.Done()
		a.processRecordingSamples(&segmentIndex)
	}()

	runtime.EventsEmit(a.ctx, "recording-started", map[string]interface{}{
		"transcriptPath": contentRel,
	})
	a.logInfo("[Recording] Recording started successfully")

	return nil
}

// processRecordingSamples processes buffered samples using VAD and ASR
// This runs in a separate goroutine to avoid CGO calls in audio thread
func (a *App) processRecordingSamples(segmentIndex *int) {
	a.logInfo("[Recording] Processing goroutine started")
	defer a.logInfo("[Recording] Processing goroutine finished (defer)")

	chunkSize := 160                // 10ms @ 16kHz
	maxSegmentSamples := 16000 * 15 // 15 seconds max
	processedSamples := 0

	ticker := time.NewTicker(100 * time.Millisecond) // Process every 100ms
	defer func() {
		a.logInfo("[Recording] Stopping ticker...")
		ticker.Stop()
		a.logInfo("[Recording] Ticker stopped")
	}()

	for {
		select {
		case <-a.recordingStopCh:
			// Stop ticker first to avoid processing new samples
			a.logInfo("[Recording] Processing goroutine received stop signal in main select")
			ticker.Stop()
			a.logInfo("[Recording] Ticker stopped after stop signal")

			// Get segment data without holding lock for long
			var seg *recordingSegment
			var startIdx int
			var samplesCopy []float32
			func() {
				// Use a separate lock scope to minimize lock time
				a.recordingMu.Lock()
				defer a.recordingMu.Unlock()
				if a.recordingSegment != nil && len(a.recordingSegment.samples) > 0 {
					seg = a.recordingSegment
					startIdx = seg.startSampleIndex
					samplesCopy = make([]float32, len(seg.samples))
					copy(samplesCopy, seg.samples)
					a.recordingSegment = nil // Clear immediately
				}
			}()

			// Finalize any remaining segment in background to avoid blocking exit
			// Only process if segment is long enough (minimum 0.1 seconds = 1600 samples)
			if seg != nil && len(samplesCopy) > 0 {
				minSamples := 1600 // 0.1 seconds at 16kHz
				if len(samplesCopy) >= minSamples {
					a.logInfo(fmt.Sprintf("[Recording] Finalizing remaining segment before exit (async, %d samples)", len(samplesCopy)))
					// Run finalization in background to avoid blocking exit
					go func() {
						a.finalizeRecordingSegment(segmentIndex, startIdx, samplesCopy)
					}()
				} else {
					a.logInfo(fmt.Sprintf("[Recording] Skipping remaining segment (too short: %d samples, minimum: %d)", len(samplesCopy), minSamples))
				}
			} else {
				a.logInfo("[Recording] No remaining segment to finalize")
			}
			a.logInfo("[Recording] Processing goroutine exiting (return statement)")
			return
		case <-ticker.C:
			// Check if we should stop before processing
			select {
			case <-a.recordingStopCh:
				// Stop signal received, exit immediately
				a.logInfo("[Recording] Processing goroutine received stop signal in ticker select")
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
				a.logInfo("[Recording] Processing goroutine exiting from ticker select")
				return
			default:
				// Continue processing
			}

			// Process buffered samples (minimize lock time)
			var newSamples []float32
			func() {
				a.recordingMu.Lock()
				defer a.recordingMu.Unlock()
				if len(a.recordingSamples) <= processedSamples {
					return
				}
				// Get new samples to process (copy to avoid holding lock)
				newSamples = make([]float32, len(a.recordingSamples)-processedSamples)
				copy(newSamples, a.recordingSamples[processedSamples:])
			}()

			if len(newSamples) == 0 {
				continue
			}

			// Process with RealtimeService for partial text (if available)
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
