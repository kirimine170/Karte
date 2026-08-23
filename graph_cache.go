package main

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	fm "karte/internal/frontmatter"
	gitvcs "karte/internal/git"
	"karte/internal/webpchunk"

	"gopkg.in/yaml.v3"
)

const (
	graphCacheVersion          = 1
	graphCacheName             = "graph_cache.json"
	graphDocIDMigrationVersion = 1
	graphDocIDMigrationName    = "graph_doc_id_migration_v1.json"
)

type graphDocIDMigrationMarker struct {
	Version int `json:"version"`
}

type graphCache struct {
	Version         int                                `json:"version"`
	Documents       map[string]graphDocumentCacheEntry `json:"documents"`
	Images          map[string]graphImageCacheEntry    `json:"images"`
	PersistedDocMap map[string]string                  `json:"persistedDocMap"`
	DocMap          map[string]string                  `json:"docMap"`
	DocMapStamp     graphFileStamp                     `json:"docMapStamp"`
	PersistedLinks  []GraphEdge                        `json:"persistedLinks"`
	LinksStamp      graphFileStamp                     `json:"linksStamp"`
	Snapshot        GraphData                          `json:"snapshot"`
	Checksum        string                             `json:"checksum"`
}

type graphFileStamp struct {
	Exists      bool   `json:"exists"`
	ModTimeNS   int64  `json:"modTimeNs,omitempty"`
	Size        int64  `json:"size,omitempty"`
	ChangeToken string `json:"changeToken,omitempty"`
}

type graphDocumentCacheEntry struct {
	Path        string            `json:"path"`
	Stamp       graphFileStamp    `json:"stamp"`
	ContentHash string            `json:"contentHash"`
	Node        GraphNode         `json:"node"`
	Links       []graphCachedLink `json:"links"`
}

type graphImageCacheEntry struct {
	Path          string         `json:"path"`
	Stamp         graphFileStamp `json:"stamp"`
	ContentHash   string         `json:"contentHash,omitempty"`
	MetadataPath  string         `json:"metadataPath,omitempty"`
	MetadataStamp graphFileStamp `json:"metadataStamp"`
	MetadataHash  string         `json:"metadataHash,omitempty"`
	EmbeddedTags  []string       `json:"embeddedTags"`
	MetadataTags  []string       `json:"metadataTags"`
	Node          GraphNode      `json:"node"`
}

type graphCachedLink struct {
	Target string `json:"target"`
	Kind   string `json:"kind"`
}

type graphParsedDocument struct {
	Node  GraphNode
	Links []graphCachedLink
}

// GetGraphData returns a deep copy of the current immutable graph snapshot.
// Rebuilding and all graph-related I/O happen only in RefreshGraphData.
func (a *App) GetGraphData() (*GraphData, error) {
	if a == nil {
		return emptyGraphData(), nil
	}
	a.graphSnapshotMu.RLock()
	defer a.graphSnapshotMu.RUnlock()
	if !a.graphCacheLoaded {
		return emptyGraphData(), nil
	}
	return cloneGraphData(&a.graphCacheState.Snapshot), nil
}

func emptyGraphData() *GraphData {
	return &GraphData{
		Nodes: []GraphNode{},
		Edges: []GraphEdge{},
		Meta:  GraphMeta{Directed: true},
	}
}

func cloneGraphData(source *GraphData) *GraphData {
	if source == nil {
		return emptyGraphData()
	}
	clone := &GraphData{
		Nodes: make([]GraphNode, len(source.Nodes)),
		Edges: append([]GraphEdge(nil), source.Edges...),
		Meta:  source.Meta,
	}
	copy(clone.Nodes, source.Nodes)
	for i := range clone.Nodes {
		clone.Nodes[i].Tags = append([]string(nil), source.Nodes[i].Tags...)
	}
	if clone.Nodes == nil {
		clone.Nodes = []GraphNode{}
	}
	if clone.Edges == nil {
		clone.Edges = []GraphEdge{}
	}
	return clone
}

func (a *App) graphDocMapSnapshot() map[string]string {
	result := make(map[string]string)
	if a == nil {
		return result
	}
	a.graphSnapshotMu.RLock()
	defer a.graphSnapshotMu.RUnlock()
	if !a.graphCacheLoaded {
		return result
	}
	for docID, path := range a.graphCacheState.DocMap {
		result[docID] = path
	}
	return result
}

// RefreshGraphData updates derived graph state at startup or a mutation
// boundary. It scans file metadata, but only reads and parses changed files.
// The new snapshot becomes visible only after its cache is durably replaced.
func (a *App) RefreshGraphData() error {
	if a == nil || strings.TrimSpace(a.dataDir) == "" {
		return fmt.Errorf("graph data directory is not initialized")
	}

	a.graphRefreshMu.Lock()
	defer a.graphRefreshMu.Unlock()

	cachePath, err := a.prepareGraphCachePath()
	if err != nil {
		return err
	}

	previous := emptyGraphCache()
	valid := false
	if a.graphCacheLoaded {
		previous = a.graphCacheState
		valid = true
	} else {
		previous, valid = a.loadGraphCache(cachePath)
	}

	documents, err := a.scanGraphDocuments(previous.Documents)
	if err != nil {
		return err
	}
	images, err := a.scanGraphImages(previous.Images)
	if err != nil {
		return err
	}
	docMap, docMapStamp, err := a.refreshGraphDocMap(previous.PersistedDocMap, previous.DocMapStamp)
	if err != nil {
		return err
	}
	persistedLinks, linksStamp, err := a.refreshPersistedGraphLinks(previous.PersistedLinks, previous.LinksStamp)
	if err != nil {
		return err
	}

	snapshot, mergedDocMap := a.buildGraphSnapshot(
		documents,
		images,
		docMap,
		persistedLinks,
		previous.Snapshot.Edges,
	)
	next := graphCache{
		Version:         graphCacheVersion,
		Documents:       documents,
		Images:          images,
		PersistedDocMap: docMap,
		DocMap:          mergedDocMap,
		DocMapStamp:     docMapStamp,
		PersistedLinks:  persistedLinks,
		LinksStamp:      linksStamp,
		Snapshot:        snapshot,
	}
	next.Checksum = graphCacheChecksum(next)
	if next.Checksum == "" {
		return fmt.Errorf("calculate graph cache checksum")
	}
	if !valid || previous.Checksum != next.Checksum {
		if err := a.persistGraphCache(cachePath, next); err != nil {
			return err
		}
	}

	a.graphSnapshotMu.Lock()
	a.graphCacheState = next
	a.graphCacheLoaded = true
	a.graphSnapshotMu.Unlock()
	return nil
}

func (a *App) refreshGraphAfterMutation(operation string) {
	if err := a.RefreshGraphData(); err != nil {
		a.logError(fmt.Sprintf("Refresh graph after %s failed: %v", operation, err))
	}
}

// ensureDocumentIDAtMutation assigns doc_id only at an explicit write
// boundary. Graph and preview read APIs intentionally never call this helper.
func (a *App) ensureDocumentIDAtMutation(path string) (string, error) {
	absPath, ok := a.resolveContentPath(path)
	if !ok || !strings.EqualFold(filepath.Ext(path), ".md") {
		return "", fmt.Errorf("invalid Markdown path: %s", path)
	}
	previous := captureDocumentFileSnapshot(absPath)
	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("read Markdown for doc_id assignment: %w", err)
	}
	updated, docID, err := a.ensureDocID(string(content))
	if err != nil {
		return "", err
	}
	changed := updated != string(content)
	if changed {
		if err := atomicWriteDerivedFile(absPath, []byte(updated), 0o644); err != nil {
			return "", fmt.Errorf("persist assigned doc_id: %w", err)
		}
	}
	if _, err := a.updateDocumentMapping(docID, path); err != nil {
		if changed {
			return "", documentMappingFailure("assign doc_id", absPath, previous, err)
		}
		return "", fmt.Errorf("persist assigned doc_id mapping: %w", err)
	}
	return docID, nil
}

// MigrateLegacyGraphDocumentIDs is an explicit, restart-safe mutation used at
// startup. Each changed Markdown file is atomically replaced, while symlinks
// and paths outside content/ are never followed. A later retry skips files
// already migrated.
func (a *App) MigrateLegacyGraphDocumentIDs() (int, error) {
	if a == nil || strings.TrimSpace(a.dataDir) == "" {
		return 0, fmt.Errorf("graph data directory is not initialized")
	}
	markerPath, complete, err := a.graphDocIDMigrationMarker()
	if err != nil {
		return 0, err
	}
	if complete {
		return 0, nil
	}
	contentRoot := filepath.Join(a.dataDir, "content")
	rootInfo, err := os.Lstat(contentRoot)
	if errors.Is(err, os.ErrNotExist) {
		if err := persistGraphDocIDMigrationMarker(markerPath); err != nil {
			return 0, err
		}
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("inspect legacy Markdown directory: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("legacy Markdown directory is not confined")
	}

	migrated := 0
	documentMappings := make(map[string]string)
	err = filepath.Walk(contentRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(path), ".md") {
			return nil
		}
		relativePath, err := filepath.Rel(a.dataDir, path)
		if err != nil {
			return err
		}
		relativePath = filepath.ToSlash(relativePath)
		if !isConfinedGraphDocumentPath(relativePath) {
			return fmt.Errorf("refusing to migrate unconfined Markdown %q", relativePath)
		}
		content, err := a.readGraphFile(path)
		if err != nil {
			return fmt.Errorf("read legacy Markdown %q: %w", relativePath, err)
		}
		var frontMatter *fm.FrontMatter
		if a.graphMigrationParse != nil {
			frontMatter = a.graphMigrationParse(content)
		} else {
			frontMatter, _ = fm.ParseFrontMatter(string(content))
		}
		if frontMatter != nil && frontMatter.DocID != "" {
			documentMappings[frontMatter.DocID] = relativePath
			return nil
		}
		updated, docID, err := a.ensureDocID(string(content))
		if err != nil {
			return fmt.Errorf("assign doc_id to %q: %w", relativePath, err)
		}
		if err := atomicWriteDerivedFile(path, []byte(updated), info.Mode().Perm()); err != nil {
			return fmt.Errorf("persist doc_id migration for %q: %w", relativePath, err)
		}
		documentMappings[docID] = relativePath
		migrated++
		return nil
	})
	if err != nil {
		return migrated, err
	}
	if len(documentMappings) > 0 {
		if _, err := a.updateDocumentMappings(documentMappings); err != nil {
			return migrated, fmt.Errorf("persist migrated document mappings: %w", err)
		}
	}
	if err := persistGraphDocIDMigrationMarker(markerPath); err != nil {
		return migrated, err
	}
	return migrated, nil
}

func (a *App) graphDocIDMigrationMarker() (string, bool, error) {
	mdsysPath := filepath.Join(a.dataDir, ".mdsys")
	info, err := os.Lstat(mdsysPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(mdsysPath, 0o755); err != nil {
			return "", false, fmt.Errorf("create graph migration directory: %w", err)
		}
	} else if err != nil {
		return "", false, fmt.Errorf("inspect graph migration directory: %w", err)
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("graph migration directory is not confined")
	}

	markerPath := filepath.Join(mdsysPath, graphDocIDMigrationName)
	info, err = os.Lstat(markerPath)
	if errors.Is(err, os.ErrNotExist) {
		return markerPath, false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("inspect graph migration marker: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", false, fmt.Errorf("graph migration marker is not a confined regular file")
	}
	data, err := a.readGraphFile(markerPath)
	if err != nil {
		return "", false, fmt.Errorf("read graph migration marker: %w", err)
	}
	var marker graphDocIDMigrationMarker
	if err := json.Unmarshal(data, &marker); err != nil || marker.Version != graphDocIDMigrationVersion {
		return markerPath, false, nil
	}
	return markerPath, true, nil
}

func persistGraphDocIDMigrationMarker(path string) error {
	data, err := json.Marshal(graphDocIDMigrationMarker{Version: graphDocIDMigrationVersion})
	if err != nil {
		return fmt.Errorf("encode graph migration marker: %w", err)
	}
	if err := atomicWriteDerivedFile(path, data, 0o644); err != nil {
		return fmt.Errorf("persist graph migration marker: %w", err)
	}
	return nil
}

func emptyGraphCache() graphCache {
	return graphCache{
		Version:         graphCacheVersion,
		Documents:       make(map[string]graphDocumentCacheEntry),
		Images:          make(map[string]graphImageCacheEntry),
		PersistedDocMap: make(map[string]string),
		DocMap:          make(map[string]string),
		PersistedLinks:  []GraphEdge{},
		Snapshot:        *emptyGraphData(),
	}
}

func (a *App) prepareGraphCachePath() (string, error) {
	mdsysPath := filepath.Join(a.dataDir, ".mdsys")
	info, err := os.Lstat(mdsysPath)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(mdsysPath, 0o755); err != nil {
			return "", fmt.Errorf("create graph cache directory: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("inspect graph cache directory: %w", err)
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("graph cache directory is not a confined directory")
	}

	cachePath := filepath.Join(mdsysPath, graphCacheName)
	if info, err := os.Lstat(cachePath); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("graph cache path is not a confined regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect graph cache file: %w", err)
	}
	return cachePath, nil
}

func (a *App) loadGraphCache(path string) (graphCache, bool) {
	data, err := a.readGraphFile(path)
	if err != nil {
		return emptyGraphCache(), false
	}
	var cache graphCache
	if err := json.Unmarshal(data, &cache); err != nil || !validGraphCache(cache) {
		return emptyGraphCache(), false
	}
	return cache, true
}

func validGraphCache(cache graphCache) bool {
	if cache.Version != graphCacheVersion || cache.Documents == nil || cache.Images == nil || cache.PersistedDocMap == nil || cache.DocMap == nil {
		return false
	}
	if cache.Checksum == "" || cache.Checksum != graphCacheChecksum(cache) {
		return false
	}
	for path, entry := range cache.Documents {
		if entry.Path != path || !isConfinedGraphDocumentPath(path) || !validGraphHash(entry.ContentHash) {
			return false
		}
		if entry.Node.ID != graphDocumentNodeID(path) || !entry.Node.Exists {
			return false
		}
	}
	for path, entry := range cache.Images {
		if entry.Path != path || !isConfinedGraphImagePath(path) || entry.Node.ID != "img:/"+path || !entry.Node.Exists {
			return false
		}
		if entry.ContentHash != "" && !validGraphHash(entry.ContentHash) {
			return false
		}
		if entry.MetadataHash != "" && !validGraphHash(entry.MetadataHash) {
			return false
		}
	}
	for _, path := range cache.PersistedDocMap {
		if !isConfinedGraphDocumentPath(path) {
			return false
		}
	}
	for _, path := range cache.DocMap {
		if !isConfinedGraphDocumentPath(path) {
			return false
		}
	}
	return true
}

func validGraphHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func graphCacheChecksum(cache graphCache) string {
	cache.Checksum = ""
	data, err := json.Marshal(cache)
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func (a *App) persistGraphCache(path string, cache graphCache) error {
	data, err := json.Marshal(cache)
	if err != nil {
		return fmt.Errorf("encode graph cache: %w", err)
	}
	writeFile := atomicWriteDerivedFile
	if a.graphPersistFile != nil {
		writeFile = a.graphPersistFile
	}
	if err := writeFile(path, data, 0o644); err != nil {
		return fmt.Errorf("persist graph cache: %w", err)
	}
	return nil
}

func (a *App) readGraphFile(path string) ([]byte, error) {
	if a.graphReadFile != nil {
		return a.graphReadFile(path)
	}
	return os.ReadFile(path)
}

func (a *App) scanGraphDocuments(previous map[string]graphDocumentCacheEntry) (map[string]graphDocumentCacheEntry, error) {
	if previous == nil {
		previous = make(map[string]graphDocumentCacheEntry)
	}
	next := make(map[string]graphDocumentCacheEntry)
	contentRoot := filepath.Join(a.dataDir, "content")
	rootInfo, err := os.Lstat(contentRoot)
	if errors.Is(err, os.ErrNotExist) {
		return next, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect graph content directory: %w", err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("graph content directory is not confined")
	}

	err = filepath.Walk(contentRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || strings.ToLower(filepath.Ext(info.Name())) != ".md" {
			return nil
		}
		relativePath, err := filepath.Rel(a.dataDir, path)
		if err != nil {
			return fmt.Errorf("resolve graph document path: %w", err)
		}
		relativePath = filepath.ToSlash(relativePath)
		if !isConfinedGraphDocumentPath(relativePath) {
			return fmt.Errorf("refusing to index unconfined graph document %q", relativePath)
		}
		resolvedPath, ok := a.resolveContentPath(relativePath)
		if !ok || filepath.Clean(resolvedPath) != filepath.Clean(path) {
			return fmt.Errorf("refusing to index unresolved graph document %q", relativePath)
		}

		stamp := a.graphStampForInfo(path, info)
		prior, hasPrior := previous[relativePath]
		if hasPrior && reusableGraphStamp(prior.Stamp, stamp) {
			next[relativePath] = prior
			return nil
		}

		content, err := a.readGraphFile(path)
		if err != nil {
			return fmt.Errorf("read graph document %q: %w", relativePath, err)
		}
		hash := gitvcs.CalculateHash(string(content))
		if hasPrior && prior.ContentHash == hash {
			prior.Stamp = stamp
			next[relativePath] = prior
			return nil
		}

		parsed := a.parseGraphDocument(relativePath, content)
		parsed.Node.ID = graphDocumentNodeID(relativePath)
		parsed.Node.Kind = graphNodeKindForPath(relativePath)
		parsed.Node.Exists = true
		parsed.Node.DegIn = 0
		parsed.Node.DegOut = 0
		parsed.Node.Hash = hash
		parsed.Node.Tags = append([]string(nil), parsed.Node.Tags...)
		next[relativePath] = graphDocumentCacheEntry{
			Path:        relativePath,
			Stamp:       stamp,
			ContentHash: hash,
			Node:        parsed.Node,
			Links:       append([]graphCachedLink(nil), parsed.Links...),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan graph documents: %w", err)
	}
	return next, nil
}

func (a *App) parseGraphDocument(path string, content []byte) graphParsedDocument {
	if a.graphParseDocument != nil {
		return a.graphParseDocument(path, content)
	}
	return defaultGraphParseDocument(a, path, content)
}

func defaultGraphParseDocument(a *App, path string, content []byte) graphParsedDocument {
	text := string(content)
	frontMatter, body := fm.ParseFrontMatter(text)
	if body == "" {
		body = text
	}
	docID := ""
	if frontMatter != nil {
		docID = frontMatter.DocID
	}
	links := a.extractLinks(body)
	cachedLinks := make([]graphCachedLink, 0, len(links))
	for _, link := range links {
		cachedLinks = append(cachedLinks, graphCachedLink{Target: link.Target, Kind: link.Kind})
	}
	return graphParsedDocument{
		Node: GraphNode{
			ID:     graphDocumentNodeID(path),
			DocID:  docID,
			Label:  fm.ExtractTitle(text, graphNodeDefaultTitleForPath(path)),
			Kind:   graphNodeKindForPath(path),
			Exists: true,
			Tags:   fm.ExtractTags(text),
		},
		Links: cachedLinks,
	}
}

func graphDocumentNodeID(path string) string {
	return "doc:/" + strings.TrimPrefix(path, "content/")
}

func isConfinedGraphDocumentPath(path string) bool {
	if path != filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) || !strings.HasPrefix(path, "content/") {
		return false
	}
	relative := strings.TrimPrefix(path, "content/")
	return relative != "" && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, "../") && strings.EqualFold(filepath.Ext(relative), ".md")
}

type graphImageCandidate struct {
	Path string
	Info os.FileInfo
}

func (a *App) scanGraphImages(previous map[string]graphImageCacheEntry) (map[string]graphImageCacheEntry, error) {
	if previous == nil {
		previous = make(map[string]graphImageCacheEntry)
	}
	candidates, err := a.graphImageCandidates()
	if err != nil {
		return nil, err
	}
	next := make(map[string]graphImageCacheEntry, len(candidates))
	paths := make([]string, 0, len(candidates))
	for path := range candidates {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, relativePath := range paths {
		candidate := candidates[relativePath]
		absolutePath := filepath.Join(a.dataDir, filepath.FromSlash(relativePath))
		stamp := a.graphStampForInfo(absolutePath, candidate.Info)
		metadataPath := metadataPathFromImage(relativePath)
		metadataAbsPath := filepath.Join(a.dataDir, filepath.FromSlash(metadataPath))
		metadataStamp, err := a.graphRegularFileStamp(metadataAbsPath)
		if err != nil {
			return nil, fmt.Errorf("inspect graph image metadata %q: %w", metadataPath, err)
		}

		prior, hasPrior := previous[relativePath]
		entry := graphImageCacheEntry{
			Path:          relativePath,
			Stamp:         stamp,
			MetadataPath:  metadataPath,
			MetadataStamp: metadataStamp,
			Node: GraphNode{
				ID:     "img:/" + relativePath,
				Label:  filepath.Base(relativePath),
				Kind:   "asset:image",
				Exists: true,
			},
		}

		if hasPrior && reusableGraphStamp(prior.Stamp, stamp) {
			entry.ContentHash = prior.ContentHash
			entry.EmbeddedTags = append([]string(nil), prior.EmbeddedTags...)
		} else if strings.EqualFold(filepath.Ext(relativePath), ".webp") {
			data, err := a.readGraphFile(absolutePath)
			if err != nil {
				return nil, fmt.Errorf("read graph image %q: %w", relativePath, err)
			}
			entry.ContentHash = gitvcs.CalculateHash(string(data))
			if hasPrior && prior.ContentHash == entry.ContentHash {
				entry.EmbeddedTags = append([]string(nil), prior.EmbeddedTags...)
			} else {
				entry.EmbeddedTags = extractGraphWebPTags(data)
			}
		}

		if hasPrior && prior.MetadataPath == metadataPath && reusableMissingGraphStamp(prior.MetadataStamp, metadataStamp) {
			entry.MetadataHash = prior.MetadataHash
			entry.MetadataTags = append([]string(nil), prior.MetadataTags...)
		} else if metadataStamp.Exists {
			data, err := a.readGraphFile(metadataAbsPath)
			if err != nil {
				return nil, fmt.Errorf("read graph image metadata %q: %w", metadataPath, err)
			}
			entry.MetadataHash = gitvcs.CalculateHash(string(data))
			if hasPrior && prior.MetadataHash == entry.MetadataHash {
				entry.MetadataTags = append([]string(nil), prior.MetadataTags...)
			} else {
				entry.MetadataTags = a.graphMetadataTags(data)
			}
		}

		entry.Node.Tags = mergeGraphTags(entry.MetadataTags, entry.EmbeddedTags)
		next[relativePath] = entry
	}
	return next, nil
}

func (a *App) graphImageCandidates() (map[string]graphImageCandidate, error) {
	candidates := make(map[string]graphImageCandidate)
	if err := a.walkGraphImageRoot(filepath.Join(a.dataDir, "data", "image"), func(path string, info os.FileInfo) {
		if !strings.EqualFold(filepath.Ext(info.Name()), ".webp") {
			return
		}
		relative, err := filepath.Rel(a.dataDir, path)
		if err != nil {
			return
		}
		relative = filepath.ToSlash(relative)
		if isConfinedGraphImagePath(relative) {
			candidates[relative] = graphImageCandidate{Path: relative, Info: info}
		}
	}); err != nil {
		return nil, err
	}

	clipCandidates := make(map[string]graphImageCandidate)
	if err := a.walkGraphImageRoot(filepath.Join(a.dataDir, "content", "clips", "assets"), func(path string, info os.FileInfo) {
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if !isGalleryImageExt(ext) {
			return
		}
		relative, err := filepath.Rel(a.dataDir, path)
		if err != nil {
			return
		}
		relative = filepath.ToSlash(relative)
		if !isConfinedGraphImagePath(relative) {
			return
		}
		key := strings.TrimSuffix(relative, filepath.Ext(relative))
		existing, exists := clipCandidates[key]
		if !exists || ext == ".webp" || !strings.EqualFold(filepath.Ext(existing.Path), ".webp") {
			clipCandidates[key] = graphImageCandidate{Path: relative, Info: info}
		}
	}); err != nil {
		return nil, err
	}
	for _, candidate := range clipCandidates {
		candidates[candidate.Path] = candidate
	}
	return candidates, nil
}

func (a *App) walkGraphImageRoot(root string, visit func(string, os.FileInfo)) error {
	rootInfo, err := os.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect graph image directory %q: %w", root, err)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("graph image directory %q is not confined", root)
	}
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil
		}
		visit(path, info)
		return nil
	})
}

func isConfinedGraphImagePath(path string) bool {
	if path != filepath.ToSlash(filepath.Clean(filepath.FromSlash(path))) {
		return false
	}
	if !(strings.HasPrefix(path, "data/image/") || strings.HasPrefix(path, "content/clips/assets/")) {
		return false
	}
	return isGalleryImageExt(filepath.Ext(path))
}

func extractGraphWebPTags(data []byte) []string {
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return []string{}
	}
	for offset := 12; offset+8 <= len(data); {
		chunkSize := int(binary.LittleEndian.Uint32(data[offset+4 : offset+8]))
		chunkStart := offset + 8
		chunkEnd := chunkStart + chunkSize
		if chunkSize < 0 || chunkEnd > len(data) {
			return []string{}
		}
		if string(data[offset:offset+4]) == webpchunk.ChunkID {
			tags, err := webpchunk.ExtractTagsFromChunk(data[chunkStart:chunkEnd])
			if err != nil {
				return []string{}
			}
			return tags
		}
		offset = chunkEnd
		if chunkSize%2 != 0 {
			offset++
		}
	}
	return []string{}
}

func (a *App) graphMetadataTags(data []byte) []string {
	var value interface{}
	if err := yaml.Unmarshal(data, &value); err != nil {
		if err := json.Unmarshal(data, &value); err != nil {
			return []string{}
		}
	}
	metadata, ok := normalizeMetadataMap(value)
	if !ok {
		return []string{}
	}
	return a.extractTagsFromMetadata(metadata)
}

func mergeGraphTags(groups ...[]string) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0)
	for _, group := range groups {
		for _, tag := range group {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			if _, exists := seen[tag]; exists {
				continue
			}
			seen[tag] = struct{}{}
			result = append(result, tag)
		}
	}
	sort.Strings(result)
	return result
}

func (a *App) graphStampForInfo(path string, info os.FileInfo) graphFileStamp {
	return graphFileStamp{
		Exists:      true,
		ModTimeNS:   info.ModTime().UnixNano(),
		Size:        info.Size(),
		ChangeToken: a.changeTokenForSearchFile(path, info),
	}
}

func (a *App) graphRegularFileStamp(path string) (graphFileStamp, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return graphFileStamp{}, nil
	}
	if err != nil {
		return graphFileStamp{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return graphFileStamp{}, fmt.Errorf("path is not a confined regular file")
	}
	return a.graphStampForInfo(path, info), nil
}

func reusableGraphStamp(previous, current graphFileStamp) bool {
	return current.Exists && previous.Exists && current.ChangeToken != "" &&
		previous.ModTimeNS == current.ModTimeNS && previous.Size == current.Size &&
		previous.ChangeToken == current.ChangeToken
}

func reusableMissingGraphStamp(previous, current graphFileStamp) bool {
	if !previous.Exists && !current.Exists {
		return true
	}
	return reusableGraphStamp(previous, current)
}

func (a *App) refreshGraphDocMap(previous map[string]string, previousStamp graphFileStamp) (map[string]string, graphFileStamp, error) {
	path := filepath.Join(a.dataDir, documentMapDirectoryName, documentMapFileName)
	stamp, err := a.graphRegularFileStamp(path)
	if err != nil {
		return nil, graphFileStamp{}, fmt.Errorf("inspect graph doc map: %w", err)
	}
	if reusableMissingGraphStamp(previousStamp, stamp) {
		return cloneGraphDocMap(previous), stamp, nil
	}
	result := make(map[string]string)
	if !stamp.Exists {
		return result, stamp, nil
	}
	data, err := a.readGraphFile(path)
	if err != nil {
		return nil, graphFileStamp{}, fmt.Errorf("read graph doc map: %w", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return result, stamp, nil
	}
	for docID, documentPath := range decoded {
		documentPath = filepath.ToSlash(documentPath)
		if docID != "" && isConfinedGraphDocumentPath(documentPath) {
			result[docID] = documentPath
		}
	}
	return result, stamp, nil
}

func cloneGraphDocMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (a *App) refreshPersistedGraphLinks(previous []GraphEdge, previousStamp graphFileStamp) ([]GraphEdge, graphFileStamp, error) {
	path := filepath.Join(a.dataDir, ".mdsys", "links.json")
	stamp, err := a.graphRegularFileStamp(path)
	if err != nil {
		return nil, graphFileStamp{}, fmt.Errorf("inspect persisted graph links: %w", err)
	}
	if reusableMissingGraphStamp(previousStamp, stamp) {
		return append([]GraphEdge(nil), previous...), stamp, nil
	}
	if !stamp.Exists {
		return []GraphEdge{}, stamp, nil
	}
	data, err := a.readGraphFile(path)
	if err != nil {
		return nil, graphFileStamp{}, fmt.Errorf("read persisted graph links: %w", err)
	}
	var edges []GraphEdge
	if err := json.Unmarshal(data, &edges); err != nil {
		return []GraphEdge{}, stamp, nil
	}
	result := make([]GraphEdge, 0, len(edges))
	for _, edge := range edges {
		if validGraphLinkNodeID(edge.Source) && validGraphLinkNodeID(edge.Target) {
			result = append(result, edge)
		}
	}
	return result, stamp, nil
}

func validGraphLinkNodeID(id string) bool {
	switch {
	case strings.HasPrefix(id, "doc:/"):
		return isConfinedGraphDocumentPath("content/" + strings.TrimPrefix(id, "doc:/"))
	case strings.HasPrefix(id, "img:/"):
		path := strings.TrimPrefix(id, "img:/")
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
		return path != "" && path == clean && path != ".." && !strings.HasPrefix(path, "../")
	case strings.HasPrefix(id, "tag:/"):
		return strings.TrimPrefix(id, "tag:/") != ""
	default:
		return false
	}
}

func (a *App) buildGraphSnapshot(
	documents map[string]graphDocumentCacheEntry,
	images map[string]graphImageCacheEntry,
	persistedDocMap map[string]string,
	persistedLinks []GraphEdge,
	previousEdges []GraphEdge,
) (GraphData, map[string]string) {
	nodes := make(map[string]*GraphNode, len(documents)+len(images))
	// doc_map.json is an input hint only. The published map contains current
	// documents exclusively, so deletion cannot retain a stale doc_id path.
	docMap := make(map[string]string)
	for docID, path := range persistedDocMap {
		if entry, exists := documents[path]; exists && entry.Node.DocID == docID {
			docMap[docID] = path
		}
	}
	documentPaths := sortedGraphDocumentPaths(documents)
	for _, path := range documentPaths {
		node := documents[path].Node
		node.DegIn = 0
		node.DegOut = 0
		node.Tags = append([]string(nil), node.Tags...)
		nodes[node.ID] = &node
		if node.DocID != "" {
			docMap[node.DocID] = path
		}
	}
	imagePaths := make([]string, 0, len(images))
	for path := range images {
		imagePaths = append(imagePaths, path)
	}
	sort.Strings(imagePaths)
	for _, path := range imagePaths {
		node := images[path].Node
		node.DegIn = 0
		node.DegOut = 0
		node.Tags = append([]string(nil), node.Tags...)
		nodes[node.ID] = &node
	}

	baselineByID := make(map[string]GraphEdge)
	baselineByDocPair := make(map[string]GraphEdge)
	addBaselines := func(edges []GraphEdge) {
		for _, edge := range edges {
			if edge.Kind == "tag" || !validGraphLinkNodeID(edge.Source) || !validGraphLinkNodeID(edge.Target) {
				continue
			}
			baselineByID[edge.ID] = edge
			if edge.SourceDocID != "" && edge.TargetDocID != "" {
				baselineByDocPair[graphDocPairKey(edge.SourceDocID, edge.TargetDocID, edge.Kind)] = edge
			}
		}
	}
	addBaselines(previousEdges)
	addBaselines(persistedLinks)

	edges := make(map[string]*GraphEdge)
	edgeCounts := make(map[string]int)
	for _, path := range documentPaths {
		entry := documents[path]
		sourceID := entry.Node.ID
		for _, cachedLink := range entry.Links {
			targetID := resolveGraphLinkTarget(cachedLink, path)
			if targetID == "" {
				continue
			}
			edgeKey := sourceID + "->" + targetID
			edgeCounts[edgeKey]++
			edgeID := fmt.Sprintf("e_%s_%s", strings.ReplaceAll(sourceID, "/", "_"), strings.ReplaceAll(targetID, "/", "_"))

			targetDocID := ""
			currentTargetHash := ""
			if targetNode, exists := nodes[targetID]; exists {
				targetDocID = targetNode.DocID
				currentTargetHash = targetNode.Hash
			}
			baseline, hasBaseline := baselineByID[edgeID]
			if !hasBaseline && entry.Node.DocID != "" && targetDocID != "" {
				baseline, hasBaseline = baselineByDocPair[graphDocPairKey(entry.Node.DocID, targetDocID, cachedLink.Kind)]
			}
			if targetDocID == "" && hasBaseline {
				targetDocID = baseline.TargetDocID
			}
			if currentTargetHash == "" && targetDocID != "" {
				if currentPath := docMap[targetDocID]; currentPath != "" {
					if currentNode := nodes[graphDocumentNodeID(currentPath)]; currentNode != nil {
						currentTargetHash = currentNode.Hash
					}
				}
			}

			targetHash := currentTargetHash
			versionMode := "pinned"
			versionID := currentTargetHash
			if hasBaseline {
				if baseline.TargetHash != "" {
					targetHash = baseline.TargetHash
				}
				if baseline.ToVersionMode != "" {
					versionMode = baseline.ToVersionMode
				}
				if baseline.ToVersionID != "" {
					versionID = baseline.ToVersionID
				}
			}
			edges[edgeID] = &GraphEdge{
				ID:            edgeID,
				Source:        sourceID,
				Target:        targetID,
				SourceDocID:   entry.Node.DocID,
				TargetDocID:   targetDocID,
				Kind:          cachedLink.Kind,
				Weight:        edgeCounts[edgeKey],
				SourceHash:    entry.ContentHash,
				TargetHash:    targetHash,
				TargetUpdated: currentTargetHash != "" && targetHash != "" && currentTargetHash != targetHash,
				ToVersionMode: versionMode,
				ToVersionID:   versionID,
			}
		}
	}

	for _, edge := range edges {
		if _, exists := nodes[edge.Target]; !exists {
			switch {
			case strings.HasPrefix(edge.Target, "doc:/"):
				path := strings.TrimPrefix(edge.Target, "doc:/")
				nodes[edge.Target] = &GraphNode{
					ID:     edge.Target,
					DocID:  edge.TargetDocID,
					Label:  graphNodeDefaultTitleForPath(path),
					Kind:   graphNodeKindForPath("content/" + path),
					Exists: false,
					Tags:   []string{},
				}
			case strings.HasPrefix(edge.Target, "img:/"):
				path := strings.TrimPrefix(edge.Target, "img:/")
				nodes[edge.Target] = &GraphNode{
					ID:     edge.Target,
					Label:  filepath.Base(path),
					Kind:   "asset:image",
					Exists: true,
					Tags:   []string{},
				}
			}
		}
		if source := nodes[edge.Source]; source != nil {
			source.DegOut++
		}
		if target := nodes[edge.Target]; target != nil {
			target.DegIn++
		}
	}

	baseNodeIDs := make([]string, 0, len(nodes))
	for id := range nodes {
		baseNodeIDs = append(baseNodeIDs, id)
	}
	sort.Strings(baseNodeIDs)
	for _, nodeID := range baseNodeIDs {
		node := nodes[nodeID]
		for _, tag := range node.Tags {
			if tag == "" {
				continue
			}
			tagID := "tag:/" + tag
			tagNode := nodes[tagID]
			if tagNode == nil {
				tagNode = &GraphNode{ID: tagID, Label: "#" + tag, Kind: "tag", Exists: true, Tags: []string{}}
				nodes[tagID] = tagNode
			}
			tagEdgeID := fmt.Sprintf("tag_edge_%s_%s", strings.ReplaceAll(node.ID, "/", "_"), strings.ReplaceAll(tagID, "/", "_"))
			edges[tagEdgeID] = &GraphEdge{ID: tagEdgeID, Source: node.ID, Target: tagID, Kind: "tag", Weight: 1}
			tagNode.DegIn++
		}
	}

	nodeIDs := make([]string, 0, len(nodes))
	for id := range nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	nodeList := make([]GraphNode, 0, len(nodeIDs))
	for _, id := range nodeIDs {
		node := *nodes[id]
		node.Tags = append([]string(nil), node.Tags...)
		nodeList = append(nodeList, node)
	}
	edgeIDs := make([]string, 0, len(edges))
	for id := range edges {
		edgeIDs = append(edgeIDs, id)
	}
	sort.Strings(edgeIDs)
	edgeList := make([]GraphEdge, 0, len(edgeIDs))
	for _, id := range edgeIDs {
		edgeList = append(edgeList, *edges[id])
	}
	return GraphData{Nodes: nodeList, Edges: edgeList, Meta: GraphMeta{Directed: true}}, docMap
}

func sortedGraphDocumentPaths(documents map[string]graphDocumentCacheEntry) []string {
	paths := make([]string, 0, len(documents))
	for path := range documents {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

func graphDocPairKey(sourceDocID, targetDocID, kind string) string {
	return sourceDocID + "\x00" + targetDocID + "\x00" + kind
}

func resolveGraphLinkTarget(link graphCachedLink, currentFile string) string {
	switch link.Kind {
	case "wikilink", "markdown_link", "quote":
		target := strings.TrimSpace(link.Target)
		if target == "" || strings.HasPrefix(strings.ToLower(target), "http://") || strings.HasPrefix(strings.ToLower(target), "https://") {
			return ""
		}
		if strings.HasPrefix(target, "/") {
			target = "content/" + strings.TrimPrefix(filepath.ToSlash(target), "/")
		} else {
			target = filepath.ToSlash(filepath.Join(filepath.Dir(currentFile), filepath.FromSlash(target)))
		}
		if !isConfinedGraphDocumentPath(target) {
			return ""
		}
		return graphDocumentNodeID(target)
	case "img":
		target := strings.TrimSpace(filepath.ToSlash(link.Target))
		lower := strings.ToLower(target)
		if target == "" || strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.HasPrefix(lower, "data:") {
			return ""
		}
		target = strings.TrimPrefix(target, "/")
		target = filepath.ToSlash(filepath.Clean(filepath.FromSlash(target)))
		if target == "." || target == ".." || strings.HasPrefix(target, "../") {
			return ""
		}
		return "img:/" + target
	default:
		return ""
	}
}
