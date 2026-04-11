package kartecore

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"karte/internal/docid"
	fm "karte/internal/frontmatter"
	gitvcs "karte/internal/git"
	"karte/internal/site"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
)

var (
	wikiLinkRegex     = regexp.MustCompile(`\[\[([^|\]]+)(?:\|([^\]]+))?\]\]`)
	markdownLinkRegex = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	imgLinkRegex      = regexp.MustCompile(`!\[[^\]]*\]\(([^)]+)\)`)
)

type Service struct {
	Root    string
	DataDir string
	vcs     *gitvcs.VCS
	logf    func(string)
}

type FileItem struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

type GraphNode struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Kind   string   `json:"kind"`
	Exists bool     `json:"exists"`
	DegIn  int      `json:"degIn"`
	DegOut int      `json:"degOut"`
	Tags   []string `json:"tags"`
}

type GraphEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Weight int    `json:"weight"`
}

type GraphData struct {
	Nodes []GraphNode `json:"nodes"`
	Edges []GraphEdge `json:"edges"`
	Meta  GraphMeta   `json:"meta"`
}

type GraphMeta struct {
	Directed bool `json:"directed"`
}

type ErrorCode string

const (
	ErrCodeInvalidInput ErrorCode = "invalid_input"
	ErrCodeNotFound     ErrorCode = "not_found"
	ErrCodeConflict     ErrorCode = "conflict"
	ErrCodeInternal     ErrorCode = "internal"
)

type Error struct {
	Code    ErrorCode
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return e.Message
	}
	if e.Message == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func New(root string, logger func(string)) *Service {
	if logger == nil {
		logger = func(string) {}
	}
	return &Service{
		Root:    root,
		DataDir: filepath.Join(root, "karte_data"),
		logf:    logger,
	}
}

func (s *Service) EnsureInitialized() error {
	if fi, err := os.Stat(s.DataDir); err != nil || !fi.IsDir() {
		return &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf("karte_data not found: %s", s.DataDir), Err: err}
	}
	return nil
}

func (s *Service) Init() error {
	subdirs := []string{
		"content",
		"data",
		filepath.Join("themes", "default"),
		"public",
		".mdsys",
	}
	if err := os.MkdirAll(s.DataDir, 0o755); err != nil {
		return &Error{Code: ErrCodeInternal, Message: "failed to create karte_data", Err: err}
	}
	for _, sub := range subdirs {
		if err := os.MkdirAll(filepath.Join(s.DataDir, sub), 0o755); err != nil {
			return &Error{Code: ErrCodeInternal, Message: fmt.Sprintf("failed to create %s", sub), Err: err}
		}
	}

	defaults := map[string]string{
		filepath.Join(s.DataDir, "content", "README.md"): "# Welcome to Karte\n\nThis is your first document.\n",
		filepath.Join(s.DataDir, ".mdsys", "index.json"): "{}",
		filepath.Join(s.DataDir, ".mdsys", "graph.json"): "{}",
	}
	for path, content := range defaults {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return &Error{Code: ErrCodeInternal, Message: fmt.Sprintf("failed to write %s", path), Err: err}
			}
		}
	}

	if err := s.ensureVCS(); err != nil {
		return err
	}
	if err := s.ensureInitialCommit(); err != nil {
		s.logf(fmt.Sprintf("initial commit skipped: %v", err))
	}
	return nil
}

func (s *Service) ListFiles() ([]FileItem, error) {
	if err := s.EnsureInitialized(); err != nil {
		return nil, err
	}
	contentDir := filepath.Join(s.DataDir, "content")
	items := make([]FileItem, 0)
	err := filepath.Walk(contentDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || strings.ToLower(filepath.Ext(info.Name())) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(s.DataDir, path)
		if err != nil {
			return nil
		}
		title := strings.TrimSuffix(info.Name(), ".md")
		if body, err := os.ReadFile(path); err == nil {
			title = fm.ExtractTitle(string(body), title)
		}
		items = append(items, FileItem{Path: filepath.ToSlash(rel), Title: title})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, &Error{Code: ErrCodeInternal, Message: "failed to list files", Err: err}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items, nil
}

func (s *Service) Read(path string) (string, error) {
	if err := s.EnsureInitialized(); err != nil {
		return "", err
	}
	abs, err := s.resolveContentPath(path)
	if err != nil {
		return "", err
	}
	body, readErr := os.ReadFile(abs)
	if os.IsNotExist(readErr) {
		return "", &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf("file not found: %s", path), Err: readErr}
	}
	if readErr != nil {
		return "", &Error{Code: ErrCodeInternal, Message: "failed to read file", Err: readErr}
	}
	return string(body), nil
}

func (s *Service) Create(path, title string) (string, error) {
	if err := s.EnsureInitialized(); err != nil {
		return "", err
	}
	abs, err := s.resolveContentPath(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs); err == nil {
		return "", &Error{Code: ErrCodeInvalidInput, Message: fmt.Sprintf("file already exists: %s", path)}
	}
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(filepath.Base(abs), ".md")
	}
	content := fmt.Sprintf("---\ntitle: \"%s\"\n---\n\n# %s\n", escapeYAMLString(title), title)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", &Error{Code: ErrCodeInternal, Message: "failed to create directory", Err: err}
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return "", &Error{Code: ErrCodeInternal, Message: "failed to create file", Err: err}
	}
	rel, _ := filepath.Rel(s.DataDir, abs)
	return filepath.ToSlash(rel), nil
}

func (s *Service) Write(path, content string, createIfMissing bool) error {
	if err := s.EnsureInitialized(); err != nil {
		return err
	}
	abs, err := s.resolveContentPath(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return &Error{Code: ErrCodeInternal, Message: "failed to create parent dir", Err: err}
	}

	current, readErr := os.ReadFile(abs)
	if os.IsNotExist(readErr) && !createIfMissing {
		return &Error{Code: ErrCodeNotFound, Message: fmt.Sprintf("file not found: %s", path), Err: readErr}
	}
	if readErr != nil && !os.IsNotExist(readErr) {
		return &Error{Code: ErrCodeInternal, Message: "failed to read existing file", Err: readErr}
	}

	oldHash := ""
	if readErr == nil {
		oldHash = gitvcs.CalculateHash(string(current))
	}

	normalized, _, normErr := s.ensureDocID(content)
	if normErr != nil {
		return normErr
	}
	frontMatter, markdownBody := fm.ParseFrontMatter(normalized)
	if frontMatter != nil {
		normalized = fm.FormatFrontMatter(frontMatter) + markdownBody
	}

	if err := s.ensureVCS(); err != nil {
		return err
	}
	if readErr == nil {
		if conflictErr := s.detectWriteConflict(abs, normalized); conflictErr != nil {
			return conflictErr
		}
	}

	if err := os.WriteFile(abs, []byte(normalized), 0o644); err != nil {
		return &Error{Code: ErrCodeInternal, Message: "failed to write file", Err: err}
	}

	newHash := gitvcs.CalculateHash(normalized)
	if s.vcs != nil && newHash != oldHash {
		if relPath, relErr := filepath.Rel(s.DataDir, abs); relErr == nil {
			if commitErr := s.vcs.CommitFile(relPath, fmt.Sprintf("Update: %s", filepath.ToSlash(relPath))); commitErr != nil {
				s.logf(fmt.Sprintf("git commit failed: %v", commitErr))
			}
		}
	}
	return s.Build()
}

func (s *Service) Preview(path string) (string, error) {
	if err := s.EnsureInitialized(); err != nil {
		return "", err
	}
	abs, err := s.resolveContentPath(path)
	if err != nil {
		return "", err
	}
	html, _, err := site.RenderMarkdown(s.DataDir, abs)
	if err != nil {
		return "", &Error{Code: ErrCodeInternal, Message: "failed to render markdown", Err: err}
	}
	return html, nil
}

func (s *Service) Build() error {
	if err := s.EnsureInitialized(); err != nil {
		return err
	}
	root := s.DataDir
	pub := filepath.Join(root, "public")
	tmp := filepath.Join(root, ".mdsys", "_public_tmp")
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return &Error{Code: ErrCodeInternal, Message: "failed to create temp dir", Err: err}
	}

	type indexEntry struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	idx := struct {
		Items []indexEntry `json:"items"`
	}{Items: make([]indexEntry, 0)}

	contentDir := filepath.Join(root, "content")
	walkErr := fs.WalkDir(os.DirFS(contentDir), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(p) != ".md" {
			return nil
		}
		src := filepath.Join(contentDir, p)
		dst := filepath.Join(tmp, strings.TrimSuffix(p, ".md")+".html")
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
		idx.Items = append(idx.Items, indexEntry{ID: id, Path: id})
		return nil
	})
	if walkErr != nil {
		_ = os.RemoveAll(tmp)
		return &Error{Code: ErrCodeInternal, Message: "build failed", Err: walkErr}
	}

	if body, err := json.MarshalIndent(idx, "", "  "); err == nil {
		_ = os.WriteFile(filepath.Join(root, ".mdsys", "index.json"), body, 0o644)
	}
	_ = os.RemoveAll(pub)
	if err := os.Rename(tmp, pub); err != nil {
		_ = os.RemoveAll(tmp)
		return &Error{Code: ErrCodeInternal, Message: "failed to publish public dir", Err: err}
	}
	return nil
}

func (s *Service) Graph() (*GraphData, error) {
	if err := s.EnsureInitialized(); err != nil {
		return nil, err
	}
	contentDir := filepath.Join(s.DataDir, "content")
	nodes := map[string]*GraphNode{}
	edges := map[string]*GraphEdge{}
	edgeCounts := map[string]int{}

	files := make([]string, 0)
	_ = filepath.Walk(contentDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || strings.ToLower(filepath.Ext(info.Name())) != ".md" {
			return nil
		}
		if rel, relErr := filepath.Rel(s.DataDir, path); relErr == nil {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(files)

	for _, rel := range files {
		abs := filepath.Join(s.DataDir, filepath.FromSlash(rel))
		content, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		body := string(content)
		nodeID := "doc:/" + strings.TrimPrefix(rel, "content/")
		title := fm.ExtractTitle(body, strings.TrimSuffix(filepath.Base(rel), ".md"))
		nodes[nodeID] = &GraphNode{
			ID:     nodeID,
			Label:  title,
			Kind:   "note",
			Exists: true,
			Tags:   fm.ExtractTags(body),
		}

		_, markdownBody := fm.ParseFrontMatter(body)
		if strings.TrimSpace(markdownBody) == "" {
			markdownBody = body
		}
		for _, link := range extractLinks(markdownBody) {
			targetID := resolveLinkTarget(link, rel)
			if targetID == "" {
				continue
			}
			key := nodeID + "->" + targetID
			edgeCounts[key]++
			edgeID := fmt.Sprintf("e_%s_%s", strings.ReplaceAll(nodeID, "/", "_"), strings.ReplaceAll(targetID, "/", "_"))
			edges[edgeID] = &GraphEdge{
				ID:     edgeID,
				Source: nodeID,
				Target: targetID,
				Kind:   link.Kind,
				Weight: edgeCounts[key],
			}
		}
	}

	for _, edge := range edges {
		if source, ok := nodes[edge.Source]; ok {
			source.DegOut++
		}
		if target, ok := nodes[edge.Target]; ok {
			target.DegIn++
		} else if strings.HasPrefix(edge.Target, "doc:/") {
			path := strings.TrimPrefix(edge.Target, "doc:/")
			nodes[edge.Target] = &GraphNode{
				ID:     edge.Target,
				Label:  strings.TrimSuffix(filepath.Base(path), ".md"),
				Kind:   "note",
				Exists: false,
				DegIn:  1,
				Tags:   []string{},
			}
		} else if strings.HasPrefix(edge.Target, "img:/") {
			nodes[edge.Target] = &GraphNode{
				ID:     edge.Target,
				Label:  filepath.Base(strings.TrimPrefix(edge.Target, "img:/")),
				Kind:   "asset:image",
				Exists: true,
				DegIn:  1,
				Tags:   []string{},
			}
		}
	}

	nodeList := make([]GraphNode, 0, len(nodes))
	for _, node := range nodes {
		nodeList = append(nodeList, *node)
	}
	sort.Slice(nodeList, func(i, j int) bool { return nodeList[i].ID < nodeList[j].ID })

	edgeList := make([]GraphEdge, 0, len(edges))
	for _, edge := range edges {
		edgeList = append(edgeList, *edge)
	}
	sort.Slice(edgeList, func(i, j int) bool { return edgeList[i].ID < edgeList[j].ID })

	return &GraphData{Nodes: nodeList, Edges: edgeList, Meta: GraphMeta{Directed: true}}, nil
}

func (s *Service) resolveContentPath(rel string) (string, error) {
	normalizedRel, err := normalizeDocumentPath(rel)
	if err != nil {
		return "", err
	}
	abs := filepath.Join(s.DataDir, filepath.FromSlash(normalizedRel))
	canonical, err := filepath.Abs(abs)
	if err != nil {
		return "", &Error{Code: ErrCodeInternal, Message: "failed to resolve path", Err: err}
	}
	contentRoot, err := filepath.Abs(filepath.Join(s.DataDir, "content"))
	if err != nil {
		return "", &Error{Code: ErrCodeInternal, Message: "failed to resolve content root", Err: err}
	}
	relToRoot, err := filepath.Rel(contentRoot, canonical)
	if err != nil {
		return "", &Error{Code: ErrCodeInvalidInput, Message: "invalid path", Err: err}
	}
	relToRoot = filepath.ToSlash(relToRoot)
	if relToRoot == ".." || strings.HasPrefix(relToRoot, "../") || filepath.IsAbs(relToRoot) {
		return "", &Error{Code: ErrCodeInvalidInput, Message: "path escapes content root"}
	}
	return canonical, nil
}

func normalizeDocumentPath(path string) (string, error) {
	rel := filepath.ToSlash(strings.TrimSpace(path))
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return "", &Error{Code: ErrCodeInvalidInput, Message: "path is required"}
	}
	if !strings.HasPrefix(rel, "content/") {
		rel = "content/" + rel
	}
	if strings.ToLower(filepath.Ext(rel)) != ".md" {
		return "", &Error{Code: ErrCodeInvalidInput, Message: "path must be a markdown file (.md)"}
	}
	return rel, nil
}

func (s *Service) ensureVCS() error {
	if s.vcs != nil {
		return nil
	}
	vcs, err := gitvcs.NewVCS(nil, s.DataDir, s.logf)
	if err != nil {
		return &Error{Code: ErrCodeInternal, Message: "failed to init vcs", Err: err}
	}
	s.vcs = vcs
	return nil
}

func (s *Service) ensureInitialCommit() error {
	if s.vcs == nil {
		return nil
	}
	if _, err := s.vcs.Repository().Head(); err == nil {
		return nil
	}
	worktree, err := s.vcs.Repository().Worktree()
	if err != nil {
		return err
	}
	if _, err := worktree.Add("."); err != nil {
		return err
	}
	_, err = worktree.Commit("Initial commit", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Karte User",
			Email: "karte@localhost",
			When:  time.Now(),
		},
	})
	return err
}

func (s *Service) ensureDocID(content string) (string, string, error) {
	frontMatter, body := fm.ParseFrontMatter(content)
	if frontMatter != nil && frontMatter.DocID != "" {
		return content, frontMatter.DocID, nil
	}
	if frontMatter == nil {
		frontMatter = &fm.FrontMatter{}
	}
	seqFile := filepath.Join(s.DataDir, ".mdsys", "doc_seq.json")
	id, err := docid.GenerateDocID(seqFile)
	if err != nil {
		return "", "", &Error{Code: ErrCodeInternal, Message: "failed to generate doc_id", Err: err}
	}
	frontMatter.DocID = id
	formatted := fm.FormatFrontMatter(frontMatter)
	if body == "" {
		return formatted, id, nil
	}
	return formatted + body, id, nil
}

func (s *Service) detectWriteConflict(absPath, candidate string) error {
	if s.vcs == nil {
		return nil
	}
	original, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	if err := os.WriteFile(absPath, []byte(candidate), 0o644); err != nil {
		return nil
	}
	defer func() {
		_ = os.WriteFile(absPath, original, 0o644)
	}()
	rel, err := filepath.Rel(s.DataDir, absPath)
	if err != nil {
		return nil
	}
	conflict, err := gitvcs.DetectConflict(s.vcs, s.DataDir, rel)
	if err != nil || conflict == nil {
		return nil
	}
	if conflict.Severity == gitvcs.ConflictCritical {
		return &Error{Code: ErrCodeConflict, Message: "write conflict detected", Err: fmt.Errorf("manual resolution required")}
	}
	return nil
}

type linkInfo struct {
	Target string
	Kind   string
}

func extractLinks(content string) []linkInfo {
	links := make([]linkInfo, 0)
	for _, match := range wikiLinkRegex.FindAllStringSubmatch(content, -1) {
		title := strings.TrimSpace(match[1])
		if !strings.HasSuffix(strings.ToLower(title), ".md") {
			title += ".md"
		}
		links = append(links, linkInfo{Target: title, Kind: "wikilink"})
	}
	for _, match := range markdownLinkRegex.FindAllStringSubmatch(content, -1) {
		url := strings.TrimSpace(match[2])
		if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
			continue
		}
		if strings.HasSuffix(strings.ToLower(url), ".md") {
			links = append(links, linkInfo{Target: url, Kind: "markdown_link"})
		}
	}
	for _, match := range imgLinkRegex.FindAllStringSubmatch(content, -1) {
		target := strings.TrimSpace(match[1])
		if target != "" {
			links = append(links, linkInfo{Target: target, Kind: "img"})
		}
	}
	return links
}

func resolveLinkTarget(link linkInfo, currentFile string) string {
	switch link.Kind {
	case "wikilink", "markdown_link":
		target := link.Target
		if !strings.HasPrefix(target, "/") && !strings.HasPrefix(target, "http") {
			target = filepath.ToSlash(filepath.Join(filepath.Dir(currentFile), target))
		}
		return "doc:/" + strings.TrimPrefix(target, "content/")
	case "img":
		return "img:/" + link.Target
	default:
		return ""
	}
}

func escapeYAMLString(s string) string {
	return strings.ReplaceAll(s, `"`, `\"`)
}
