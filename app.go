package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"karte/internal/site"
	"karte/internal/sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App struct
type App struct {
	ctx         context.Context
	root        string
	syncManager *sync.SyncManager
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
}

// GraphEdge represents an edge in the graph
type GraphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Weight int    `json:"weight"`
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
	// Get the current working directory as the project root
	wd, err := os.Getwd()
	if err != nil {
		runtime.LogError(ctx, fmt.Sprintf("Failed to get working directory: %v", err))
		return
	}
	a.root = wd
	runtime.LogInfo(ctx, fmt.Sprintf("Karte started with root: %s", a.root))

	// Initialize sync manager (disabled for now - will be implemented with git integration)
	// a.syncManager = sync.NewSyncManager(ctx, a.root)
	// if err := a.syncManager.Start(); err != nil {
	// 	runtime.LogError(ctx, fmt.Sprintf("Failed to start sync manager: %v", err))
	// }
}

// GetFileList returns a list of markdown files in the content directory
func (a *App) GetFileList() []FileItem {
	var files []FileItem
	contentDir := filepath.Join(a.root, "content")

	runtime.LogInfo(a.ctx, fmt.Sprintf("Looking for content directory: %s", contentDir))

	// Check if content directory exists
	if _, err := os.Stat(contentDir); os.IsNotExist(err) {
		runtime.LogError(a.ctx, fmt.Sprintf("Content directory does not exist: %s", contentDir))
		return []FileItem{}
	}

	err := filepath.Walk(contentDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			runtime.LogError(a.ctx, fmt.Sprintf("Error walking path %s: %v", p, err))
			return nil
		}
		if info.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			rel, _ := filepath.Rel(a.root, p)
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
			runtime.LogInfo(a.ctx, fmt.Sprintf("Found file: %s -> %s", fileItem.Path, fileItem.Title))
		}
		return nil
	})

	if err != nil {
		runtime.LogError(a.ctx, fmt.Sprintf("Error walking content directory: %v", err))
		return []FileItem{}
	}

	runtime.LogInfo(a.ctx, fmt.Sprintf("Found %d markdown files", len(files)))
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files
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

	err := os.WriteFile(absPath, []byte(content), 0o644)
	if err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	// Build the site after saving
	if err := a.BuildSite(); err != nil {
		runtime.LogError(a.ctx, fmt.Sprintf("Failed to build site after save: %v", err))
	}

	// Broadcast file change to peers (disabled for now - will be implemented with git integration)
	// if a.syncManager != nil {
	// 	if err := a.syncManager.BroadcastFileChange(path, content); err != nil {
	// 		runtime.LogError(a.ctx, fmt.Sprintf("Failed to broadcast file change: %v", err))
	// 	}
	// }

	// Emit file changed event
	runtime.EventsEmit(a.ctx, "file-changed", path)

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
	abs := filepath.Join(a.root, filepath.FromSlash(rel))
	canonical, err := filepath.Abs(abs)
	if err != nil {
		return "", false
	}
	contentRoot, _ := filepath.Abs(filepath.Join(a.root, "content"))
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
	runtime.LogInfo(a.ctx, "Generating graph data...")

	contentDir := filepath.Join(a.root, "content")
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
			rel, _ := filepath.Rel(a.root, p)
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

		// フロントマターからタイトルを抽出
		if content, err := os.ReadFile(filepath.Join(a.root, filePath)); err == nil {
			title = a.extractTitleFromContent(string(content), title)
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
		}

		// ファイル内容を解析してリンクを抽出
		if content, err := os.ReadFile(filepath.Join(a.root, filePath)); err == nil {
			links := a.extractLinks(string(content))
			runtime.LogInfo(a.ctx, fmt.Sprintf("File %s: found %d links", filePath, len(links)))
			for i, link := range links {
				targetID := a.resolveLinkTarget(link, filePath)
				runtime.LogInfo(a.ctx, fmt.Sprintf("  Link %d: %s -> %s (kind: %s)", i+1, link.Target, targetID, link.Kind))
				if targetID != "" {
					// エッジの重みをカウント
					edgeKey := nodeID + "->" + targetID
					edgeCounts[edgeKey]++

					// エッジを作成
					edgeID := fmt.Sprintf("e_%s_%s", strings.ReplaceAll(nodeID, "/", "_"), strings.ReplaceAll(targetID, "/", "_"))
					edges[edgeID] = &GraphEdge{
						ID:     edgeID,
						Source: nodeID,
						Target: targetID,
						Kind:   link.Kind,
						Weight: edgeCounts[edgeKey],
					}
					runtime.LogInfo(a.ctx, fmt.Sprintf("    Created edge: %s -> %s (weight: %d)", nodeID, targetID, edgeCounts[edgeKey]))
				}
			}
		} else {
			runtime.LogError(a.ctx, fmt.Sprintf("Failed to read file %s: %v", filePath, err))
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
				runtime.LogInfo(a.ctx, fmt.Sprintf("Created missing target node: %s", edge.Target))
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
				runtime.LogInfo(a.ctx, fmt.Sprintf("Created missing image node: %s", edge.Target))
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
			runtime.LogInfo(a.ctx, fmt.Sprintf("Created missing source node: %s", edge.Source))
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

	// スライスに変換
	var nodeList []GraphNode
	for _, node := range nodes {
		nodeList = append(nodeList, *node)
	}

	var edgeList []GraphEdge
	for _, edge := range edges {
		edgeList = append(edgeList, *edge)
	}

	// デバッグ用：ノードIDとエッジの詳細をログ出力
	runtime.LogInfo(a.ctx, fmt.Sprintf("Generated graph with %d nodes and %d edges", len(nodeList), len(edgeList)))

	// ノードIDの一覧をログ出力
	nodeIds := make([]string, 0, len(nodes))
	for id := range nodes {
		nodeIds = append(nodeIds, id)
	}
	runtime.LogInfo(a.ctx, fmt.Sprintf("Node IDs: %v", nodeIds))

	// エッジの詳細をログ出力
	for i, edge := range edgeList {
		if i < 5 { // 最初の5個のエッジのみログ出力
			runtime.LogInfo(a.ctx, fmt.Sprintf("Edge %d: %s -> %s (kind: %s, weight: %d)",
				i+1, edge.Source, edge.Target, edge.Kind, edge.Weight))
		}
	}

	return &GraphData{
		Nodes: nodeList,
		Edges: edgeList,
		Meta:  GraphMeta{Directed: true},
	}, nil
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
