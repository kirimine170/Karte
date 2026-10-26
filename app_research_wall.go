package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	boardpkg "karte/internal/board"
	fm "karte/internal/frontmatter"
)

// ResearchResource contains managed references only．Media bytes stay in Karte's data directory．
type ResearchResource struct {
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
	EditPath string `json:"editPath"`
}

func researchKind(path string) string {
	if strings.HasSuffix(strings.ToLower(path), ".board.md") {
		return ""
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md":
		return "markdown"
	case ".pdf":
		return "pdf"
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return "image"
	case ".wav", ".mp3", ".m4a", ".ogg", ".flac", ".aac":
		return "audio"
	}
	return ""
}

// resolveResearchResource rejects traversal and symlinks outside the managed media roots．
func (a *App) resolveResearchResource(path string) (string, error) {
	if strings.Contains(path, "\\") || filepath.IsAbs(path) || filepath.ToSlash(filepath.Clean(path)) != path {
		return "", fmt.Errorf("invalid resource path")
	}
	kind := researchKind(path)
	allowed := strings.HasPrefix(path, "content/") && (kind == "markdown" || kind == "pdf" || kind == "image") || strings.HasPrefix(path, "data/image/") && kind == "image" || strings.HasPrefix(path, "data/audio/") && kind == "audio"
	if !allowed {
		return "", fmt.Errorf("unsupported managed resource")
	}
	abs, err := filepath.EvalSymlinks(filepath.Join(a.dataDir, filepath.FromSlash(path)))
	if err != nil {
		return "", err
	}
	rootName := "content"
	if strings.HasPrefix(path, "data/image/") {
		rootName = "data/image"
	}
	if strings.HasPrefix(path, "data/audio/") {
		rootName = "data/audio"
	}
	root, err := filepath.EvalSymlinks(filepath.Join(a.dataDir, filepath.FromSlash(rootName)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("resource escapes managed directory")
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("resource is not a file")
	}
	return abs, nil
}

// ListResearchResources uses the existing document visibility rules，including managed v2 records．
func (a *App) ListResearchResources() []ResearchResource {
	resources := []ResearchResource{}
	for _, item := range a.GetFileList() {
		kind := researchKind(item.Path)
		if kind == "" {
			continue
		}
		if _, err := a.resolveResearchResource(item.Path); err != nil {
			continue
		}
		resources = append(resources, ResearchResource{Path: item.Path, Kind: kind, Title: item.Title, Revision: item.ModTime.String()})
	}
	for _, item := range a.GetImageList() {
		if _, err := a.resolveResearchResource(item.Path); err != nil {
			continue
		}
		resources = append(resources, ResearchResource{Path: item.Path, Kind: "image", Title: item.Name, Revision: item.ModTime.String()})
	}
	_ = filepath.WalkDir(filepath.Join(a.dataDir, "data", "audio"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || researchKind(path) != "audio" {
			return nil
		}
		rel, e := filepath.Rel(a.dataDir, path)
		if e != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if _, e = a.resolveResearchResource(rel); e != nil {
			return nil
		}
		info, e := d.Info()
		if e != nil {
			return nil
		}
		resources = append(resources, ResearchResource{Path: rel, Kind: "audio", Title: d.Name(), Revision: info.ModTime().String()})
		return nil
	})
	sort.Slice(resources, func(i, j int) bool { return resources[i].Path < resources[j].Path })
	return resources
}

func (a *App) researchTranscript(audioPath string) string {
	for _, file := range a.GetFileList() {
		if researchKind(file.Path) != "markdown" {
			continue
		}
		text, err := a.LoadFile(file.Path)
		if err != nil {
			continue
		}
		front, _ := fm.ParseFrontMatter(text)
		if front != nil && strings.TrimSpace(fmt.Sprint(front.Raw["audio_path"])) == audioPath {
			return file.Path
		}
	}
	return ""
}

func (a *App) LoadResearchResource(path string) (*ResearchResource, error) {
	abs, err := a.resolveResearchResource(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	r := &ResearchResource{Path: path, Kind: researchKind(path), Title: filepath.Base(path), Revision: fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())}
	switch r.Kind {
	case "markdown":
		if info.Size() > 8*1024*1024 {
			return nil, fmt.Errorf("Markdown is too large for a board preview")
		}
		text, e := a.LoadFile(path)
		if e != nil {
			return nil, e
		}
		r.Body = text
		r.EditPath = path
		front, _ := fm.ParseFrontMatter(text)
		if front != nil && front.Title != "" {
			r.Title = front.Title
		}
	case "image":
		r.URL = (&url.URL{Path: "/image/" + path}).EscapedPath()
		r.Body, _ = a.GetImageMetadata(path)
	case "pdf":
		r.URL = (&url.URL{Path: "/pdf/" + path}).EscapedPath()
		r.EditPath = path
	case "audio":
		r.URL = (&url.URL{Path: "/audio/" + path}).EscapedPath()
		r.EditPath = a.researchTranscript(path)
		if r.EditPath != "" {
			r.Body, _ = a.LoadFile(r.EditPath)
		}
	}
	if current, e := os.Stat(abs); e == nil {
		r.Revision = fmt.Sprintf("%d:%d", current.ModTime().UnixNano(), current.Size())
	}
	return r, nil
}

// EditResearchResource opens the existing text editor for audio transcripts．An empty linked
// transcript is created only on this explicit edit operation，without inventing speech content．
func (a *App) EditResearchResource(path string) (string, error) {
	r, err := a.LoadResearchResource(path)
	if err != nil {
		return "", err
	}
	if r.Kind != "audio" {
		return r.EditPath, nil
	}
	if r.EditPath != "" {
		return r.EditPath, nil
	}
	dest := "content/audio-" + uuid.NewString() + ".md"
	content := fmt.Sprintf("---\ntitle: 音声メモ\naudio_path: %q\n---\n\n# 音声メモ\n\n", path)
	if err = a.SaveFile(dest, content); err != nil {
		return "", err
	}
	return dest, nil
}

func (a *App) CreateResearchBoard(title string) (*boardpkg.Document, error) {
	if strings.TrimSpace(title) == "" {
		title = "リサーチボード"
	}
	doc := &boardpkg.Document{Path: "content/research-" + uuid.NewString() + ".board.md", Title: title, Cards: []boardpkg.Card{}, Edges: []boardpkg.Edge{}}
	return a.saveBoardDocument(doc)
}

// ImportResearchResource delegates media conversion，metadata and ASR to the same importers
// as the rest of Karte．Markdown uses SaveFile for document IDs and canonical storage．
func (a *App) ImportResearchResource(filename, encoded string) (*ResearchResource, error) {
	if filename == "" || strings.ContainsAny(filename, "/\\") {
		return nil, fmt.Errorf("invalid filename")
	}
	if len(encoded) > 28*1024*1024 {
		return nil, fmt.Errorf("file exceeds 20MB")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	if len(data) > 20*1024*1024 {
		return nil, fmt.Errorf("file exceeds 20MB")
	}
	kind := researchKind(filename)
	var path string
	switch kind {
	case "markdown":
		if len(data) > 1024*1024 {
			return nil, fmt.Errorf("Markdown import exceeds 1MB")
		}
		path = "content/import-" + uuid.NewString() + "-" + filename
		text := string(data)
		// A portable import creates a new managed document，so retain user metadata
		// while letting SaveFile allocate a fresh canonical identity．
		if front, body := fm.ParseFrontMatter(text); front != nil {
			front.DocID = ""
			delete(front.Raw, "doc_id")
			text = fm.FormatFrontMatter(front) + body
		}
		err = a.SaveFile(path, text)
	case "image":
		path, err = a.ImportImageBase64(filename, encoded)
	case "audio":
		path, err = a.ImportAudioBase64(filename, encoded)
	case "pdf":
		path, err = a.ImportPdfBase64(filename, encoded)
	default:
		return nil, fmt.Errorf("unsupported file type")
	}
	if err != nil {
		return nil, err
	}
	return a.LoadResearchResource(path)
}

// SaveResearchBoardExport gives explicit portable exports the standard native save dialog．
func (a *App) SaveResearchBoardExport(filename, content string) (string, error) {
	if len(content) > 115*1024*1024 {
		return "", fmt.Errorf("board export exceeds 115MB")
	}
	var header struct {
		Schema string            `json:"schema"`
		Cards  []json.RawMessage `json:"cards"`
		Edges  []json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal([]byte(content), &header); err != nil {
		return "", err
	}
	if header.Schema != "karte-research-wall/1" || header.Cards == nil || header.Edges == nil || len(header.Cards) > 250 || len(header.Edges) > 1000 {
		return "", fmt.Errorf("invalid research board export")
	}
	filename = filepath.Base(strings.ReplaceAll(filename, "\\", "/"))
	if filename == "." || filename == "" {
		filename = "research.wall.json"
	}
	if !strings.HasSuffix(filename, ".json") {
		filename += ".wall.json"
	}
	return a.savePostLabFile(filename, "Research Wall JSON", "*.json", []byte(content))
}
