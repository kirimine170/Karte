package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	fm "karte/internal/frontmatter"
)

const (
	fileSearchIndexVersion = 1
	fileSearchDefaultLimit = 50
	fileSearchMaxLimit     = 100
	fileSearchIndexName    = "file_search_index.json"
)

type fileSearchIndex struct {
	Version  int                             `json:"version"`
	Entries  map[string]fileSearchIndexEntry `json:"entries"`
	Checksum string                          `json:"checksum"`
}

type fileSearchIndexEntry struct {
	Path           string `json:"path"`
	Title          string `json:"title"`
	ModTimeNS      int64  `json:"modTimeNs"`
	Size           int64  `json:"size"`
	ChangeToken    string `json:"changeToken"`
	ContentHash    string `json:"contentHash,omitempty"`
	NormalizedText string `json:"normalizedText"`
	Markdown       bool   `json:"markdown"`
}

// FileSearchResult is a single metadata-only page returned by SearchFiles.
type FileSearchResult struct {
	Items   []FileItem `json:"items"`
	Page    int        `json:"page"`
	Limit   int        `json:"limit"`
	Total   int        `json:"total"`
	HasMore bool       `json:"hasMore"`
}

// SearchFiles searches title, path, and indexed Markdown content without
// sending the indexed body to the frontend.
func (a *App) SearchFiles(query string, page, limit int) FileSearchResult {
	resourceResult, err := a.SearchResources(ResourceSearchRequest{
		Query: query,
		Kinds: []ResourceKind{ResourceKindMarkdown, ResourceKindPDF},
		Page:  page,
		Limit: limit,
	})
	result := FileSearchResult{
		Items: []FileItem{},
		Page:  resourceResult.Page,
		Limit: resourceResult.Limit,
	}
	if err != nil {
		a.logError(fmt.Sprintf("SearchFiles failed: %v", err))
		return result
	}
	for _, item := range resourceResult.Items {
		result.Items = append(result.Items, FileItem{
			Path:    item.Path,
			Title:   item.Title,
			ModTime: item.Metadata.ModTime,
			Size:    item.Metadata.Size,
		})
	}
	result.Total = resourceResult.Total
	result.HasMore = resourceResult.HasMore
	return result
}

func normalizeFileSearchPagination(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = fileSearchDefaultLimit
	}
	if limit > fileSearchMaxLimit {
		limit = fileSearchMaxLimit
	}
	return page, limit
}

func fileSearchPageStart(page, limit, total int) int {
	if total == 0 || page-1 > total/limit {
		return total
	}
	start := (page - 1) * limit
	if start > total {
		return total
	}
	return start
}

func (a *App) refreshFileSearchIndex() (fileSearchIndex, []FileItem, error) {
	a.fileSearchMu.Lock()
	defer a.fileSearchMu.Unlock()

	indexPath, err := a.prepareFileSearchIndexPath()
	if err != nil {
		return emptyFileSearchIndex(), nil, err
	}

	previous, valid := loadFileSearchIndex(indexPath)
	if !valid {
		previous = emptyFileSearchIndex()
	}

	index, files, changed, err := a.scanFileSearchEntries(previous)
	if err != nil {
		return emptyFileSearchIndex(), nil, err
	}
	if !valid || changed {
		if err := persistFileSearchIndex(indexPath, index); err != nil {
			return emptyFileSearchIndex(), nil, err
		}
	}
	return index, files, nil
}

func emptyFileSearchIndex() fileSearchIndex {
	return fileSearchIndex{
		Version: fileSearchIndexVersion,
		Entries: make(map[string]fileSearchIndexEntry),
	}
}

func (a *App) prepareFileSearchIndexPath() (string, error) {
	mdsysPath := filepath.Join(a.dataDir, ".mdsys")
	info, err := os.Lstat(mdsysPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(mdsysPath, 0o755); err != nil {
			return "", fmt.Errorf("create file search index directory: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect file search index directory: %w", err)
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("file search index directory is not a confined directory")
	}
	return filepath.Join(mdsysPath, fileSearchIndexName), nil
}

func loadFileSearchIndex(path string) (fileSearchIndex, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return emptyFileSearchIndex(), false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return emptyFileSearchIndex(), false
	}
	var index fileSearchIndex
	if err := json.Unmarshal(data, &index); err != nil || index.Version != fileSearchIndexVersion || index.Entries == nil {
		return emptyFileSearchIndex(), false
	}
	if index.Checksum == "" || index.Checksum != fileSearchEntriesChecksum(index.Entries) {
		return emptyFileSearchIndex(), false
	}
	for path, entry := range index.Entries {
		isMarkdown := strings.ToLower(filepath.Ext(path)) == ".md"
		if entry.Path != path || !isConfinedSearchPath(path) || entry.Markdown != isMarkdown ||
			entry.NormalizedText == "" {
			return emptyFileSearchIndex(), false
		}
		if isMarkdown {
			hash, err := hex.DecodeString(entry.ContentHash)
			if err != nil || len(hash) != sha256.Size {
				return emptyFileSearchIndex(), false
			}
		}
		if !isMarkdown && entry.ContentHash != "" {
			return emptyFileSearchIndex(), false
		}
	}
	return index, true
}

func isConfinedSearchPath(path string) bool {
	if path != filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) || !strings.HasPrefix(path, "content/") {
		return false
	}
	rel := strings.TrimPrefix(path, "content/")
	if rel == "" || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".pdf"
}

func (a *App) scanFileSearchEntries(previous fileSearchIndex) (fileSearchIndex, []FileItem, bool, error) {
	contentPath := filepath.Join(a.dataDir, "content")
	contentInfo, err := os.Lstat(contentPath)
	if errors.Is(err, os.ErrNotExist) {
		return emptyFileSearchIndex(), []FileItem{}, len(previous.Entries) > 0, nil
	}
	if err != nil {
		return emptyFileSearchIndex(), nil, false, fmt.Errorf("inspect content directory: %w", err)
	}
	if !contentInfo.IsDir() || contentInfo.Mode()&os.ModeSymlink != 0 {
		return emptyFileSearchIndex(), nil, false, fmt.Errorf("content directory is not a confined directory")
	}

	next := emptyFileSearchIndex()
	files := make([]FileItem, 0, len(previous.Entries))
	changed := false
	err = filepath.Walk(contentPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(info.Name()))
		isMarkdown := ext == ".md"
		if !isMarkdown && ext != ".pdf" {
			return nil
		}

		relativePath, err := filepath.Rel(a.dataDir, path)
		if err != nil {
			return fmt.Errorf("resolve indexed path %q: %w", path, err)
		}
		relativePath = filepath.ToSlash(relativePath)
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return fmt.Errorf("resolve absolute indexed path %q: %w", path, err)
		}
		resolvedPath, ok := a.resolveContentPath(relativePath)
		if !ok || filepath.Clean(resolvedPath) != filepath.Clean(absolutePath) || !isConfinedSearchPath(relativePath) {
			return fmt.Errorf("refusing to index unconfined path %q", path)
		}

		changeToken := a.changeTokenForSearchFile(absolutePath, info)
		entry, reused, err := a.buildFileSearchEntry(
			absolutePath,
			relativePath,
			info,
			isMarkdown,
			changeToken,
			previous.Entries[relativePath],
		)
		if err != nil {
			return err
		}
		if !reused || entry != previous.Entries[relativePath] {
			changed = true
		}
		next.Entries[relativePath] = entry
		files = append(files, FileItem{
			Path:    relativePath,
			Title:   entry.Title,
			ModTime: info.ModTime(),
			Size:    info.Size(),
		})
		return nil
	})
	if err != nil {
		return emptyFileSearchIndex(), nil, false, fmt.Errorf("scan content files: %w", err)
	}
	if len(next.Entries) != len(previous.Entries) {
		changed = true
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return next, files, changed, nil
}

func (a *App) buildFileSearchEntry(
	absolutePath string,
	relativePath string,
	info os.FileInfo,
	isMarkdown bool,
	changeToken string,
	previous fileSearchIndexEntry,
) (fileSearchIndexEntry, bool, error) {
	entry := fileSearchIndexEntry{
		Path:        relativePath,
		ModTimeNS:   info.ModTime().UnixNano(),
		Size:        info.Size(),
		ChangeToken: changeToken,
		Markdown:    isMarkdown,
	}
	metadataUnchanged := previous.Path == relativePath &&
		previous.Markdown == isMarkdown &&
		previous.ModTimeNS == entry.ModTimeNS &&
		previous.Size == entry.Size &&
		changeToken != "" &&
		previous.ChangeToken == changeToken
	if metadataUnchanged {
		return previous, true, nil
	}

	if !isMarkdown {
		entry.Title = strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))
		entry.NormalizedText = normalizeFileSearchText(relativePath, entry.Title, "")
		return entry, false, nil
	}

	readFile := os.ReadFile
	if a.fileSearchReadFile != nil {
		readFile = a.fileSearchReadFile
	}
	content, err := readFile(absolutePath)
	if err != nil {
		return fileSearchIndexEntry{}, false, fmt.Errorf("read indexed Markdown %q: %w", relativePath, err)
	}
	hashBytes := sha256.Sum256(content)
	entry.ContentHash = hex.EncodeToString(hashBytes[:])
	if previous.Path == relativePath && previous.ContentHash == entry.ContentHash {
		entry.Title = previous.Title
		entry.NormalizedText = previous.NormalizedText
		return entry, false, nil
	}
	entry.Title = fm.ExtractTitle(string(content), info.Name())
	entry.NormalizedText = normalizeFileSearchText(relativePath, entry.Title, string(content))
	return entry, false, nil
}

func normalizeFileSearchText(path, title, content string) string {
	return strings.ToLower(strings.Join([]string{path, title, content}, "\n"))
}

func (a *App) changeTokenForSearchFile(path string, info os.FileInfo) string {
	if a.fileSearchChangeToken != nil {
		return a.fileSearchChangeToken(path, info)
	}
	return platformFileChangeToken(path, info)
}

func persistFileSearchIndex(path string, index fileSearchIndex) error {
	index.Checksum = fileSearchEntriesChecksum(index.Entries)
	data, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("encode file search index: %w", err)
	}
	if err := atomicWriteSaveFile(path, data, 0o644); err != nil {
		return fmt.Errorf("persist file search index: %w", err)
	}
	return nil
}

func fileSearchEntriesChecksum(entries map[string]fileSearchIndexEntry) string {
	data, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
