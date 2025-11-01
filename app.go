package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	gitvcs "karte/internal/git"
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
}

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

	a.logInfo(fmt.Sprintf("Karte started. root=%s dataDir=%s exeDir=%s", a.root, a.dataDir, exeDir))

	// Initialize sync manager (disabled for now - will be implemented with git integration)
	// a.syncManager = sync.NewSyncManager(ctx, a.root)
	// if err := a.syncManager.Start(); err != nil {
	// 	runtime.LogError(ctx, fmt.Sprintf("Failed to start sync manager: %v", err))
	// }
}

// initializeDataDirectory creates and initializes the karte_data directory structure
func (a *App) initializeDataDirectory() error {
	// Create karte_data directory if it doesn't exist
	if err := os.MkdirAll(a.dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %v", err)
	}

	// Create subdirectories
	subdirs := []string{"content", "data", "themes", "public", ".mdsys"}
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
	}

	for filePath, content := range defaultFiles {
		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to create default file %s: %v", filePath, err)
			}
		}
	}

	// Initialize Git repository
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
				s := string(b)
				if strings.HasPrefix(s, "---") {
					if i := strings.Index(s, "\n---"); i > 0 {
						fm := s[:i]
						for _, ln := range strings.Split(fm, "\n") {
							if strings.HasPrefix(strings.TrimSpace(ln), "title:") {
								title = strings.TrimSpace(strings.TrimPrefix(ln, "title:"))
								title = strings.Trim(title, `"' `)
								break
							}
						}
					}
				}
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
	// Create a temporary file for rendering
	tmpFile := filepath.Join(a.root, ".mdsys", "temp_preview.md")
	err := os.MkdirAll(filepath.Dir(tmpFile), 0o755)
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %v", err)
	}

	err = os.WriteFile(tmpFile, []byte(content), 0o644)
	if err != nil {
		return "", fmt.Errorf("failed to write temp file: %v", err)
	}
	defer os.Remove(tmpFile)

	html, _, err := site.RenderMarkdown(a.root, tmpFile)
	if err != nil {
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
		if content, err := os.ReadFile(filepath.Join(a.dataDir, filePath)); err == nil {
			fileContent = string(content)
			title = a.extractTitleFromContent(fileContent, title)
			// ハッシュを計算
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
			Tags:   []string{},
			Hash:   fileHash,
		}

		// ファイル内容を解析してリンクを抽出
		if fileContent != "" {
			links := a.extractLinks(fileContent)
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

				nodes[edge.Target] = &GraphNode{
					ID:     edge.Target,
					Label:  title,
					Kind:   "note",
					Exists: false,
					DegIn:  0,
					DegOut: 0,
					Tags:   []string{},
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

			nodes[edge.Source] = &GraphNode{
				ID:     edge.Source,
				Label:  title,
				Kind:   "note",
				Exists: false,
				DegIn:  0,
				DegOut: 0,
				Tags:   []string{},
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

// LinkInfo represents a link found in markdown content
type LinkInfo struct {
	Target string
	Kind   string
}

// extractTitleFromContent extracts title from frontmatter
func (a *App) extractTitleFromContent(content, defaultTitle string) string {
	if strings.HasPrefix(content, "---") {
		if i := strings.Index(content, "\n---"); i > 0 {
			fm := content[:i]
			for _, line := range strings.Split(fm, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "title:") {
					title := strings.TrimSpace(strings.TrimPrefix(line, "title:"))
					title = strings.Trim(title, `"' `)
					return title
				}
			}
		}
	}
	return defaultTitle
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
