package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"karte/internal/asr"
	"karte/internal/audio"
	fm "karte/internal/frontmatter"
	gitvcs "karte/internal/git"
	"karte/internal/marp"
	pdfexport "karte/internal/pdf"
	"karte/internal/site"
	"karte/internal/sync"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx         context.Context
	root        string
	dataDir     string
	logFilePath string
	syncManager *sync.SyncManager
	vcs         *gitvcs.VCS
	asrService  *asr.Service
	asrInitDone chan struct{}
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

// GraphNode represents a node in the graph
type GraphNode struct {
	ID     string   `json:"id"`
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
	Source        string `json:"source"`
	Target        string `json:"target"`
	Kind          string `json:"kind"`
	Weight        int    `json:"weight"`
	TargetHash    string `json:"targetHash,omitempty"`    // Hash of target file when link was created
	SourceHash    string `json:"sourceHash,omitempty"`    // Hash of source file when link was created
	LinkVersion   int    `json:"linkVersion,omitempty"`   // Version number when link was created
	TargetUpdated bool   `json:"targetUpdated,omitempty"` // True if target file has been updated since link creation
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
	// a.syncManager = sync.NewSyncManager(ctx, a.root)
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
		if strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			// Generate path relative to dataDir so that it starts with "content/..."
			rel, _ := filepath.Rel(a.dataDir, p)
			title := info.Name()

			// Try to extract title from frontmatter
			if b, err := os.ReadFile(p); err == nil {
				title = fm.ExtractTitle(string(b), title)
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

	a.logInfo(fmt.Sprintf("Found %d markdown files", len(files)))
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
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
func (a *App) LoadFile(path string) (string, error) {
	runtime.LogInfo(a.ctx, fmt.Sprintf("LoadFile called with path: %s", path))

	absPath, ok := a.resolveContentPath(path)
	if !ok {
		runtime.LogError(a.ctx, fmt.Sprintf("Invalid path: %s", path))
		return "", fmt.Errorf("invalid path: %s", path)
	}

	runtime.LogInfo(a.ctx, fmt.Sprintf("Resolved path: %s", absPath))

	content, err := os.ReadFile(absPath)
	if err != nil {
		runtime.LogError(a.ctx, fmt.Sprintf("Failed to read file %s: %v", absPath, err))
		return "", fmt.Errorf("failed to read file: %v", err)
	}

	runtime.LogInfo(a.ctx, fmt.Sprintf("Successfully loaded file, content length: %d", len(content)))
	return string(content), nil
}

// SaveFile saves content to a markdown file
func (a *App) SaveFile(path, content string) error {
	absPath, ok := a.resolveContentPath(path)
	if !ok {
		return fmt.Errorf("invalid path: %s", path)
	}

	// Calculate hash before saving
	var oldHash string
	if existingContent, err := os.ReadFile(absPath); err == nil {
		oldHash = gitvcs.CalculateHash(string(existingContent))
	}

	// Parse and format frontmatter before saving
	frontMatter, markdownBody := fm.ParseFrontMatter(content)
	if frontMatter != nil {
		// Format frontmatter with normalized tags
		formattedFM := fm.FormatFrontMatter(frontMatter)
		content = formattedFM + markdownBody
	}

	// Detect conflict before saving
	if a.vcs != nil {
		relPath, err := filepath.Rel(a.dataDir, absPath)
		if err == nil {
			conflict, err := gitvcs.DetectConflict(a.vcs, a.dataDir, relPath)
			if err != nil {
				a.logError(fmt.Sprintf("Failed to detect conflict: %v", err))
			} else if conflict != nil {
				// Create backup before handling conflict
				if err := a.createBackup(path, content); err != nil {
					a.logError(fmt.Sprintf("Failed to create backup: %v", err))
				}

				// Try auto-merge for auto-resolvable or warning conflicts
				if conflict.Severity == gitvcs.ConflictAutoResolvable || conflict.Severity == gitvcs.ConflictWarning {
					merged, severity, err := gitvcs.AutoMergeMarkdown(conflict.BaseContent, conflict.LocalContent, conflict.RemoteContent)
					if err == nil && severity != gitvcs.ConflictCritical {
						// Auto-merge successful - use merged content
						content = merged
						runtime.EventsEmit(a.ctx, "auto-merge-success", map[string]interface{}{
							"path":        path,
							"merged_hash": gitvcs.CalculateHash(merged),
						})
						a.logInfo(fmt.Sprintf("Auto-merged conflict for file: %s", path))
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

	// Save file
	err := os.WriteFile(absPath, []byte(content), 0o644)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	// Calculate new hash
	newHash := gitvcs.CalculateHash(content)

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

func (a *App) appendTranscriptLine(contentRel, line string) {
	absPath, ok := a.resolveContentPath(contentRel)
	if !ok {
		return
	}
	f, err := os.OpenFile(absPath, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		a.logError(fmt.Sprintf("Failed to append transcript line: %v", err))
		return
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n\n"); err != nil {
		a.logError(fmt.Sprintf("Failed to write transcript: %v", err))
	}
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
func (a *App) GetConnectedPeers() []sync.Peer {
	// TODO: Implement with git integration
	return []sync.Peer{}
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

		// ファイル内容を読み込み
		var tags []string
		var markdownBody string
		if content, err := os.ReadFile(filepath.Join(a.dataDir, filePath)); err == nil {
			fileContent = string(content)
			title = a.extractTitleFromContent(fileContent, title)
			// タグを抽出
			tags = fm.ExtractTags(fileContent)
			// デバッグ: フロントマターのパース結果を確認
			frontMatter, body := fm.ParseFrontMatter(fileContent)
			if frontMatter != nil {
				a.logInfo(fmt.Sprintf("File %s: frontmatter parsed - title: %q, tags: %q, theme: %q", filePath, frontMatter.Title, frontMatter.Tags, frontMatter.Theme))
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

		// ノードを作成
		nodes[nodeID] = &GraphNode{
			ID:     nodeID,
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
					edges[edgeID] = &GraphEdge{
						ID:            edgeID,
						Source:        nodeID,
						Target:        targetID,
						Kind:          link.Kind,
						Weight:        edgeCounts[edgeKey],
						SourceHash:    fileHash,
						TargetHash:    storedTargetHash, // 以前のハッシュを保持（初回は現在のハッシュ）
						TargetUpdated: targetUpdated,
					}
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

				nodes[edge.Target] = &GraphNode{
					ID:     edge.Target,
					Label:  title,
					Kind:   "asset:image",
					Exists: true, // 画像は存在すると仮定
					DegIn:  0,
					DegOut: 0,
					Tags:   []string{},
				}
				a.logInfo(fmt.Sprintf("Created missing image node: %s", edge.Target))
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
			if targetNode, exists := nodes[edge.Target]; exists {
				currentHash = targetNode.Hash
			} else if strings.HasPrefix(edge.Target, "doc:/") {
				targetPath := strings.TrimPrefix(edge.Target, "doc:/")
				targetFilePath := filepath.Join(a.dataDir, "content", targetPath)
				if targetContent, err := os.ReadFile(targetFilePath); err == nil {
					currentHash = gitvcs.CalculateHash(string(targetContent))
				}
			}

			if currentHash != "" && persistedEdge.TargetHash != currentHash {
				edge.TargetUpdated = true
				a.logInfo(fmt.Sprintf("Target updated detected for edge %s: old=%s, new=%s", edgeID, persistedEdge.TargetHash[:8], currentHash[:8]))
			}
			// 永続化されたハッシュを保持（リンク作成時のハッシュ）
			edge.TargetHash = persistedEdge.TargetHash
		}
		// 新しいエッジの場合、現在のハッシュが既にedge.TargetHashに設定されている
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
	return pdfPath, nil
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

// createAudioHandler creates an HTTP handler that serves audio files.
// This handler wraps the default asset handler and adds support for /audio/ paths.
func (a *App) createAudioHandler() http.Handler {
	// Get default asset handler
	defaultHandler := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Handle /audio/ paths
		if strings.HasPrefix(r.URL.Path, "/audio/") {
			// Extract audio path from URL
			audioPath := strings.TrimPrefix(r.URL.Path, "/audio/")

			// Resolve to absolute path
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
				http.Error(w, "Audio file not found", http.StatusNotFound)
				return
			}

			// Determine MIME type from file extension
			ext := strings.ToLower(filepath.Ext(absPath))
			var mimeType string
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
				mimeType = "audio/mpeg" // default fallback
			}

			// Open file
			file, err := os.Open(absPath)
			if err != nil {
				http.Error(w, "Failed to open audio file", http.StatusInternalServerError)
				return
			}
			defer file.Close()

			// Set headers
			w.Header().Set("Content-Type", mimeType)
			w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
			w.Header().Set("Accept-Ranges", "bytes") // Enable range requests for seeking

			// Serve file
			http.ServeContent(w, r, filepath.Base(absPath), info.ModTime(), file)
			return
		}

		// For all other paths, use default asset handler
		defaultHandler.ServeHTTP(w, r)
	})
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

// LinkInfo represents a link found in markdown content
type LinkInfo struct {
	Target string
	Kind   string
}

// extractTitleFromContent extracts title from frontmatter
func (a *App) extractTitleFromContent(content, defaultTitle string) string {
	return fm.ExtractTitle(content, defaultTitle)
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
