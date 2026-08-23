package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	resourceSearchDefaultLimit = 50
	resourceSearchMaxLimit     = 100
)

type ResourceKind string

const (
	ResourceKindMarkdown ResourceKind = "markdown"
	ResourceKindPDF      ResourceKind = "pdf"
	ResourceKindImage    ResourceKind = "image"
	ResourceKindCSV      ResourceKind = "csv"
)

var resourceSearchKindOrder = []ResourceKind{
	ResourceKindMarkdown,
	ResourceKindPDF,
	ResourceKindImage,
	ResourceKindCSV,
}

// ResourceSearchRequest is the shared paginated contract used by file search
// and the board material tray．Kinds is empty when all supported kinds are
// requested．Paths are always relative to the application data root．
type ResourceSearchRequest struct {
	Query        string         `json:"query"`
	Kinds        []ResourceKind `json:"kinds"`
	ExcludePaths []string       `json:"excludePaths,omitempty"`
	Page         int            `json:"page"`
	Limit        int            `json:"limit"`
}

type ResourceSearchMetadata struct {
	Name      string    `json:"name"`
	Extension string    `json:"extension"`
	Size      int64     `json:"size"`
	ModTime   time.Time `json:"modTime"`
}

type ResourceSearchItem struct {
	Kind     ResourceKind           `json:"kind"`
	Path     string                 `json:"path"`
	Title    string                 `json:"title"`
	Metadata ResourceSearchMetadata `json:"metadata"`
}

type ResourceSearchResult struct {
	Items   []ResourceSearchItem `json:"items"`
	Query   string               `json:"query"`
	Kinds   []ResourceKind       `json:"kinds"`
	Page    int                  `json:"page"`
	Limit   int                  `json:"limit"`
	Total   int                  `json:"total"`
	HasMore bool                 `json:"hasMore"`
}

type resourceSearchCandidate struct {
	item           ResourceSearchItem
	normalizedText string
}

type resourceSearchDiskFile struct {
	path          string
	info          os.FileInfo
	multipleLinks bool
}

// SearchResources returns one metadata-only page for every supported resource
// kind．Markdown body matching reuses the persisted file search index and body
// text never crosses the Wails boundary．
func (a *App) SearchResources(request ResourceSearchRequest) (ResourceSearchResult, error) {
	page, limit := normalizeResourceSearchPagination(request.Page, request.Limit)
	kinds, kindSet, err := normalizeResourceSearchKinds(request.Kinds)
	result := ResourceSearchResult{
		Items: []ResourceSearchItem{},
		Query: normalizeResourceSearchQuery(request.Query),
		Kinds: kinds,
		Page:  page,
		Limit: limit,
	}
	if err != nil {
		return result, err
	}

	excluded, err := normalizeResourceSearchExcludePaths(request.ExcludePaths)
	if err != nil {
		return result, err
	}

	candidates := make([]resourceSearchCandidate, 0)
	if resourceSearchNeedsDocuments(kindSet) {
		documents, documentErr := a.searchDocumentResources(kindSet)
		if documentErr != nil {
			return result, documentErr
		}
		candidates = append(candidates, documents...)
	}
	if _, include := kindSet[ResourceKindImage]; include {
		images, imageErr := a.searchImageResources()
		if imageErr != nil {
			return result, imageErr
		}
		candidates = append(candidates, images...)
	}
	if _, include := kindSet[ResourceKindCSV]; include {
		csvs, csvErr := a.searchCSVResources()
		if csvErr != nil {
			return result, csvErr
		}
		candidates = append(candidates, csvs...)
	}

	matches := make([]ResourceSearchItem, 0, len(candidates))
	for _, candidate := range candidates {
		if _, skip := excluded[candidate.item.Path]; skip {
			continue
		}
		if result.Query == "" || strings.Contains(candidate.normalizedText, result.Query) {
			matches = append(matches, candidate.item)
		}
	}
	sort.Slice(matches, func(left, right int) bool {
		leftPath := strings.ToLower(matches[left].Path)
		rightPath := strings.ToLower(matches[right].Path)
		if leftPath != rightPath {
			return leftPath < rightPath
		}
		if matches[left].Path != matches[right].Path {
			return matches[left].Path < matches[right].Path
		}
		return matches[left].Kind < matches[right].Kind
	})

	result.Total = len(matches)
	start := resourceSearchPageStart(page, limit, len(matches))
	end := start + limit
	if end > len(matches) {
		end = len(matches)
	}
	if start < end {
		result.Items = append(result.Items, matches[start:end]...)
	}
	result.HasMore = end < len(matches)
	return result, nil
}

func normalizeResourceSearchPagination(page, limit int) (int, int) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = resourceSearchDefaultLimit
	}
	if limit > resourceSearchMaxLimit {
		limit = resourceSearchMaxLimit
	}
	return page, limit
}

func resourceSearchPageStart(page, limit, total int) int {
	if total == 0 || page > 1+total/limit {
		return total
	}
	start := (page - 1) * limit
	if start < 0 || start > total {
		return total
	}
	return start
}

func normalizeResourceSearchKinds(requested []ResourceKind) ([]ResourceKind, map[ResourceKind]struct{}, error) {
	known := make(map[ResourceKind]struct{}, len(resourceSearchKindOrder))
	for _, kind := range resourceSearchKindOrder {
		known[kind] = struct{}{}
	}
	selected := make(map[ResourceKind]struct{}, len(resourceSearchKindOrder))
	if len(requested) == 0 {
		for _, kind := range resourceSearchKindOrder {
			selected[kind] = struct{}{}
		}
	} else {
		for _, rawKind := range requested {
			kind := ResourceKind(strings.ToLower(strings.TrimSpace(string(rawKind))))
			if _, ok := known[kind]; !ok {
				return nil, nil, fmt.Errorf("unsupported resource kind %q", rawKind)
			}
			selected[kind] = struct{}{}
		}
	}
	ordered := make([]ResourceKind, 0, len(selected))
	for _, kind := range resourceSearchKindOrder {
		if _, ok := selected[kind]; ok {
			ordered = append(ordered, kind)
		}
	}
	return ordered, selected, nil
}

func normalizeResourceSearchQuery(query string) string {
	return strings.ToLower(strings.TrimSpace(query))
}

func resourceSearchNeedsDocuments(kinds map[ResourceKind]struct{}) bool {
	for _, kind := range []ResourceKind{ResourceKindMarkdown, ResourceKindPDF} {
		if _, ok := kinds[kind]; ok {
			return true
		}
	}
	return false
}

func (a *App) searchDocumentResources(kinds map[ResourceKind]struct{}) ([]resourceSearchCandidate, error) {
	index, files, err := a.refreshFileSearchIndex()
	if err != nil {
		return nil, fmt.Errorf("refresh document search index: %w", err)
	}
	items := make([]resourceSearchCandidate, 0, len(files))
	for _, file := range files {
		kind := resourceKindForDocumentPath(file.Path)
		if _, include := kinds[kind]; !include {
			continue
		}
		entry, ok := index.Entries[file.Path]
		if !ok {
			continue
		}
		items = append(items, resourceSearchCandidate{
			item: ResourceSearchItem{
				Kind:  kind,
				Path:  file.Path,
				Title: file.Title,
				Metadata: ResourceSearchMetadata{
					Name:      filepath.Base(file.Path),
					Extension: strings.ToLower(filepath.Ext(file.Path)),
					Size:      file.Size,
					ModTime:   file.ModTime,
				},
			},
			normalizedText: entry.NormalizedText,
		})
	}
	return items, nil
}

func resourceKindForDocumentPath(path string) ResourceKind {
	normalized := strings.ToLower(path)
	if strings.HasSuffix(normalized, ".pdf") {
		return ResourceKindPDF
	}
	return ResourceKindMarkdown
}

func (a *App) searchImageResources() ([]resourceSearchCandidate, error) {
	managed, err := scanResourceSearchDirectory(a.dataDir, "data/image", map[string]struct{}{".webp": {}})
	if err != nil {
		return nil, fmt.Errorf("scan managed image resources: %w", err)
	}
	clipFiles, err := scanResourceSearchDirectory(a.dataDir, "content/clips/assets", map[string]struct{}{
		".jpg": {}, ".jpeg": {}, ".png": {}, ".gif": {}, ".webp": {},
	})
	if err != nil {
		return nil, fmt.Errorf("scan Web Clip image resources: %w", err)
	}

	deduplicatedClips := make(map[string]resourceSearchDiskFile, len(clipFiles))
	for _, file := range clipFiles {
		extension := strings.ToLower(filepath.Ext(file.path))
		key := strings.TrimSuffix(strings.ToLower(file.path), extension)
		current, exists := deduplicatedClips[key]
		currentExtension := strings.ToLower(filepath.Ext(current.path))
		if !exists || extension == ".webp" || currentExtension != ".webp" && file.path < current.path {
			deduplicatedClips[key] = file
		}
	}
	files := make([]resourceSearchDiskFile, 0, len(managed)+len(deduplicatedClips))
	files = append(files, managed...)
	for _, file := range deduplicatedClips {
		files = append(files, file)
	}
	return resourceSearchCandidatesFromDisk(ResourceKindImage, files), nil
}

func (a *App) searchCSVResources() ([]resourceSearchCandidate, error) {
	files, err := scanResourceSearchDirectory(a.dataDir, "data/csv", map[string]struct{}{".csv": {}})
	if err != nil {
		return nil, fmt.Errorf("scan CSV resources: %w", err)
	}
	// The editable CSV store is deliberately flat so same-directory temp，
	// replace，and Sync semantics have one unambiguous durability boundary．
	flat := make([]resourceSearchDiskFile, 0, len(files))
	for _, file := range files {
		relative := strings.TrimPrefix(file.path, "data/csv/")
		if relative == "" || strings.Contains(relative, "/") || file.multipleLinks || csvFileHasMultipleLinks(file.info) {
			continue
		}
		flat = append(flat, file)
	}
	return resourceSearchCandidatesFromDisk(ResourceKindCSV, flat), nil
}

func scanResourceSearchDirectory(dataDirectory, relativeDirectory string, extensions map[string]struct{}) ([]resourceSearchDiskFile, error) {
	root, err := openResourceSearchDirectory(dataDirectory, relativeDirectory)
	if errors.Is(err, os.ErrNotExist) {
		return []resourceSearchDiskFile{}, nil
	}
	if err != nil {
		return nil, err
	}
	defer root.Close()

	files := make([]resourceSearchDiskFile, 0)
	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == "." || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		before, lstatErr := root.Lstat(path)
		if lstatErr != nil {
			return lstatErr
		}
		if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if _, ok := extensions[strings.ToLower(filepath.Ext(before.Name()))]; !ok {
			return nil
		}
		file, openErr := root.Open(path)
		if openErr != nil {
			return openErr
		}
		opened, statErr := file.Stat()
		multipleLinks := false
		var linkErr error
		if relativeDirectory == "data/csv" && statErr == nil {
			multipleLinks, linkErr = csvOpenedFileHasMultipleLinks(file, opened)
		}
		closeErr := file.Close()
		after, afterErr := root.Lstat(path)
		if statErr != nil {
			return statErr
		}
		if linkErr != nil {
			return linkErr
		}
		if closeErr != nil {
			return closeErr
		}
		if afterErr != nil || !opened.Mode().IsRegular() || !after.Mode().IsRegular() ||
			after.Mode()&os.ModeSymlink != 0 || !os.SameFile(before, opened) || !os.SameFile(opened, after) {
			return errors.New("resource changed while its metadata was inspected")
		}
		relativePath := pathpkg.Join(relativeDirectory, filepath.ToSlash(path))
		files = append(files, resourceSearchDiskFile{path: relativePath, info: opened, multipleLinks: multipleLinks})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func normalizeResourceSearchExcludePaths(paths []string) (map[string]struct{}, error) {
	excluded := make(map[string]struct{}, len(paths))
	for _, rawPath := range paths {
		path := strings.TrimSpace(rawPath)
		if path == "" || path != rawPath || strings.ContainsRune(path, '\x00') || strings.Contains(path, "\\") || strings.Contains(path, ":") ||
			pathpkg.IsAbs(path) || filepath.IsAbs(filepath.FromSlash(path)) || pathpkg.Clean(path) != path ||
			path == "." || path == ".." || strings.HasPrefix(path, "../") {
			return nil, fmt.Errorf("invalid excluded resource path %q", rawPath)
		}
		excluded[path] = struct{}{}
	}
	return excluded, nil
}

func openResourceSearchDirectory(dataDirectory, relativeDirectory string) (*os.Root, error) {
	dataInfo, err := os.Lstat(dataDirectory)
	if err != nil {
		return nil, err
	}
	if !dataInfo.IsDir() || dataInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("resource data root is not a regular directory")
	}
	dataRoot, err := os.OpenRoot(dataDirectory)
	if err != nil {
		return nil, err
	}
	defer dataRoot.Close()
	openedDataInfo, err := dataRoot.Stat(".")
	if err != nil || !os.SameFile(dataInfo, openedDataInfo) {
		return nil, errors.New("resource data root changed while it was opened")
	}

	if err := validateResourceSearchDirectoryComponents(dataRoot, relativeDirectory); err != nil {
		return nil, err
	}

	directoryRoot, err := dataRoot.OpenRoot(filepath.FromSlash(relativeDirectory))
	if err != nil {
		return nil, err
	}
	openedDirectoryInfo, err := directoryRoot.Stat(".")
	if err != nil || !openedDirectoryInfo.IsDir() {
		directoryRoot.Close()
		return nil, errors.New("resource search path is not a directory")
	}
	if err := validateResourceSearchDirectoryComponents(dataRoot, relativeDirectory); err != nil {
		directoryRoot.Close()
		return nil, err
	}
	currentInfo, err := dataRoot.Lstat(filepath.FromSlash(relativeDirectory))
	if err != nil || currentInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedDirectoryInfo, currentInfo) {
		directoryRoot.Close()
		return nil, errors.New("resource search directory changed while it was opened")
	}
	return directoryRoot, nil
}

func validateResourceSearchDirectoryComponents(root *os.Root, relativeDirectory string) error {
	current := ""
	for _, component := range strings.Split(filepath.ToSlash(relativeDirectory), "/") {
		if component == "" || component == "." || component == ".." {
			return errors.New("invalid resource search directory")
		}
		current = filepath.Join(current, component)
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("resource search directory contains a symlink or non-directory component")
		}
	}
	return nil
}

func resourceSearchCandidatesFromDisk(kind ResourceKind, files []resourceSearchDiskFile) []resourceSearchCandidate {
	items := make([]resourceSearchCandidate, 0, len(files))
	for _, file := range files {
		name := file.info.Name()
		item := ResourceSearchItem{
			Kind:  kind,
			Path:  file.path,
			Title: name,
			Metadata: ResourceSearchMetadata{
				Name:      name,
				Extension: strings.ToLower(filepath.Ext(name)),
				Size:      file.info.Size(),
				ModTime:   file.info.ModTime(),
			},
		}
		items = append(items, resourceSearchCandidate{
			item:           item,
			normalizedText: normalizeFileSearchText(item.Path, item.Title, item.Metadata.Name),
		})
	}
	return items
}
