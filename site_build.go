package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	karterenderer "github.com/kirimine170/KarteRenderer"
)

const (
	siteBuildManifestSchema = 1
	siteBuildManifestName   = ".karte-build-manifest.json"
)

type siteBuildManifest struct {
	Schema  int                               `json:"schema"`
	Sources map[string]siteBuildManifestEntry `json:"sources"`
}

type siteBuildManifestEntry struct {
	Checksum string `json:"checksum"`
	Output   string `json:"output"`
}

type siteBuildIndex struct {
	Items []siteBuildIndexEntry `json:"items"`
}

type siteBuildIndexEntry struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type siteBuildSource struct {
	AbsolutePath string
	SourcePath   string
	IndexPath    string
	OutputPath   string
	Checksum     string
}

type siteBuildResult struct {
	Full     bool
	Rendered []string
	Deleted  []string
}

type siteBuildHooks struct {
	renderMarkdown func(context.Context, string, string) (string, error)
	encodeIndex    func(siteBuildIndex) ([]byte, error)
	encodeManifest func(siteBuildManifest) ([]byte, error)
	rename         func(string, string) error
	removeAll      func(string) error
	commitIndex    func(string, string) error
}

type siteBuilder struct {
	mu    sync.Mutex
	hooks siteBuildHooks
}

func newSiteBuilder(hooks siteBuildHooks) *siteBuilder {
	if hooks.renderMarkdown == nil {
		hooks.renderMarkdown = func(ctx context.Context, root, sourcePath string) (string, error) {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			html, _, err := karterenderer.RenderMarkdown(root, sourcePath)
			return html, err
		}
	}
	if hooks.encodeIndex == nil {
		hooks.encodeIndex = func(index siteBuildIndex) ([]byte, error) {
			return json.MarshalIndent(index, "", "  ")
		}
	}
	if hooks.encodeManifest == nil {
		hooks.encodeManifest = func(manifest siteBuildManifest) ([]byte, error) {
			return json.MarshalIndent(manifest, "", "  ")
		}
	}
	if hooks.rename == nil {
		hooks.rename = os.Rename
	}
	if hooks.removeAll == nil {
		hooks.removeAll = os.RemoveAll
	}
	if hooks.commitIndex == nil {
		hooks.commitIndex = atomicReplaceFile
	}
	return &siteBuilder{hooks: hooks}
}

func (b *siteBuilder) BuildFull(ctx context.Context, root string) (siteBuildResult, error) {
	return b.build(ctx, root, true)
}

func (b *siteBuilder) BuildIncremental(ctx context.Context, root string) (siteBuildResult, error) {
	return b.build(ctx, root, false)
}

func (b *siteBuilder) build(ctx context.Context, root string, forceFull bool) (siteBuildResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return siteBuildResult{}, err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	absRoot, err := filepath.Abs(strings.TrimSpace(root))
	if err != nil {
		return siteBuildResult{}, fmt.Errorf("resolve site root: %w", err)
	}
	if strings.TrimSpace(root) == "" {
		return siteBuildResult{}, fmt.Errorf("site root is empty")
	}
	root = absRoot
	if err := validateSiteBuildRoot(root); err != nil {
		return siteBuildResult{}, err
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return siteBuildResult{}, fmt.Errorf("resolve site root symlinks: %w", err)
	}
	root = canonicalRoot

	sources, sourceOrder, err := scanSiteBuildSources(ctx, root)
	if err != nil {
		return siteBuildResult{}, err
	}

	metadataDir, err := ensureSiteBuildMetadataDir(root)
	if err != nil {
		return siteBuildResult{}, err
	}
	publicDir := filepath.Join(root, "public")
	if err := recoverSiteBuildBackups(publicDir, metadataDir, b.hooks.rename); err != nil {
		return siteBuildResult{}, err
	}
	manifest, baselineValid, err := loadSiteBuildManifest(publicDir)
	if err != nil {
		return siteBuildResult{}, err
	}
	full := forceFull || !baselineValid
	result := siteBuildResult{Full: full}

	renderSet := make(map[string]struct{})
	deleteSet := make(map[string]struct{})
	if full {
		for _, sourcePath := range sourceOrder {
			renderSet[sourcePath] = struct{}{}
		}
	} else {
		for _, sourcePath := range sourceOrder {
			source := sources[sourcePath]
			previous, exists := manifest.Sources[sourcePath]
			outputExists := false
			if exists {
				outputInfo, statErr := os.Lstat(filepath.Join(publicDir, filepath.FromSlash(previous.Output)))
				outputExists = statErr == nil && outputInfo.Mode().IsRegular()
			}
			if !exists || previous.Checksum != source.Checksum || previous.Output != source.OutputPath || !outputExists {
				renderSet[sourcePath] = struct{}{}
			}
		}
		for sourcePath, previous := range manifest.Sources {
			if _, exists := sources[sourcePath]; !exists {
				deleteSet[previous.Output] = struct{}{}
			}
		}
	}

	stageDir, err := os.MkdirTemp(metadataDir, ".site-build-stage-")
	if err != nil {
		return siteBuildResult{}, fmt.Errorf("create site build stage: %w", err)
	}
	stageOwned := true
	defer func() {
		if stageOwned {
			_ = os.RemoveAll(stageDir)
		}
	}()

	if !full {
		if err := copySiteBuildTree(ctx, publicDir, stageDir); err != nil {
			return siteBuildResult{}, fmt.Errorf("copy previous public output: %w", err)
		}
	}

	deletedOutputs := sortedSetKeys(deleteSet)
	for _, outputPath := range deletedOutputs {
		if err := ctx.Err(); err != nil {
			return siteBuildResult{}, err
		}
		if err := removeSiteBuildOutput(stageDir, outputPath); err != nil {
			return siteBuildResult{}, fmt.Errorf("remove deleted site output %s: %w", outputPath, err)
		}
	}

	renderPaths := sortedSetKeys(renderSet)
	for _, sourcePath := range renderPaths {
		if err := ctx.Err(); err != nil {
			return siteBuildResult{}, err
		}
		source := sources[sourcePath]
		html, err := b.hooks.renderMarkdown(ctx, root, source.AbsolutePath)
		if err != nil {
			return siteBuildResult{}, fmt.Errorf("render %s: %w", sourcePath, err)
		}
		if err := ctx.Err(); err != nil {
			return siteBuildResult{}, err
		}
		outputPath, err := confinedSiteBuildPath(stageDir, source.OutputPath)
		if err != nil {
			return siteBuildResult{}, err
		}
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			return siteBuildResult{}, fmt.Errorf("create output directory for %s: %w", sourcePath, err)
		}
		if err := os.WriteFile(outputPath, []byte(html), 0o644); err != nil {
			return siteBuildResult{}, fmt.Errorf("write output for %s: %w", sourcePath, err)
		}
	}

	index := siteBuildIndex{Items: make([]siteBuildIndexEntry, 0, len(sourceOrder))}
	newManifest := siteBuildManifest{
		Schema:  siteBuildManifestSchema,
		Sources: make(map[string]siteBuildManifestEntry, len(sourceOrder)),
	}
	for _, sourcePath := range sourceOrder {
		source := sources[sourcePath]
		index.Items = append(index.Items, siteBuildIndexEntry{ID: source.IndexPath, Path: source.IndexPath})
		newManifest.Sources[sourcePath] = siteBuildManifestEntry{
			Checksum: source.Checksum,
			Output:   source.OutputPath,
		}
	}

	indexData, err := b.hooks.encodeIndex(index)
	if err != nil {
		return siteBuildResult{}, fmt.Errorf("encode site index: %w", err)
	}
	manifestData, err := b.hooks.encodeManifest(newManifest)
	if err != nil {
		return siteBuildResult{}, fmt.Errorf("encode site manifest: %w", err)
	}
	manifestPath := filepath.Join(stageDir, siteBuildManifestName)
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		return siteBuildResult{}, fmt.Errorf("write staged site manifest: %w", err)
	}

	indexTempPath, err := writeSiteBuildTempFile(metadataDir, ".site-index-", indexData, 0o644)
	if err != nil {
		return siteBuildResult{}, fmt.Errorf("stage site index: %w", err)
	}
	defer os.Remove(indexTempPath)

	if err := ctx.Err(); err != nil {
		return siteBuildResult{}, err
	}
	if err := b.publish(root, stageDir, indexTempPath); err != nil {
		return siteBuildResult{}, err
	}
	stageOwned = false
	result.Rendered = renderPaths
	result.Deleted = deletedOutputs
	return result, nil
}

func scanSiteBuildSources(ctx context.Context, root string) (map[string]siteBuildSource, []string, error) {
	contentDir := filepath.Join(root, "content")
	info, err := os.Lstat(contentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]siteBuildSource{}, []string{}, nil
		}
		return nil, nil, fmt.Errorf("inspect site content directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, nil, fmt.Errorf("site content path is not a directory: %s", contentDir)
	}

	sources := make(map[string]siteBuildSource)
	err = filepath.WalkDir(contentDir, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if sourcePath == contentDir {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("site source symlink is not allowed: %s", sourcePath)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		relativePath, err := filepath.Rel(contentDir, sourcePath)
		if err != nil {
			return err
		}
		indexPath := filepath.ToSlash(relativePath)
		sourceRelativePath := "content/" + indexPath
		outputPath := strings.TrimSuffix(indexPath, filepath.Ext(indexPath)) + ".html"
		if _, err := confinedSiteBuildPath(contentDir, indexPath); err != nil {
			return err
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		sources[sourceRelativePath] = siteBuildSource{
			AbsolutePath: sourcePath,
			SourcePath:   sourceRelativePath,
			IndexPath:    indexPath,
			OutputPath:   filepath.ToSlash(outputPath),
			Checksum:     hex.EncodeToString(digest[:]),
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan site sources: %w", err)
	}

	order := make([]string, 0, len(sources))
	for sourcePath := range sources {
		order = append(order, sourcePath)
	}
	sort.Strings(order)
	return sources, order, nil
}

func validateSiteBuildRoot(root string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("inspect site root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("site root is not a directory: %s", root)
	}
	return nil
}

func ensureSiteBuildMetadataDir(root string) (string, error) {
	metadataDir, err := confinedSiteBuildPath(root, ".mdsys")
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(metadataDir)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("site metadata path is not a directory: %s", metadataDir)
		}
		return metadataDir, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect site metadata directory: %w", err)
	}
	if err := os.Mkdir(metadataDir, 0o755); err != nil {
		return "", fmt.Errorf("create site metadata directory: %w", err)
	}
	return metadataDir, nil
}

func cleanupStaleSiteBuildBackups(metadataDir string) error {
	entries, err := os.ReadDir(metadataDir)
	if err != nil {
		return fmt.Errorf("list site metadata directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".site-build-stage-") || !strings.HasSuffix(name, "-public-backup") {
			continue
		}
		path := filepath.Join(metadataDir, name)
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect stale site backup: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("remove stale site backup symlink: %w", err)
			}
			continue
		}
		if !info.IsDir() {
			return fmt.Errorf("stale site backup is not a directory: %s", path)
		}
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("remove stale site backup: %w", err)
		}
	}
	return nil
}

type staleSiteBuildBackup struct {
	path    string
	modTime int64
}

func recoverSiteBuildBackups(publicDir, metadataDir string, rename func(string, string) error) error {
	publicInfo, publicErr := os.Lstat(publicDir)
	if publicErr == nil {
		if publicInfo.Mode()&os.ModeSymlink != 0 || !publicInfo.IsDir() {
			return fmt.Errorf("public path is not a directory: %s", publicDir)
		}
		return cleanupStaleSiteBuildBackups(metadataDir)
	}
	if !os.IsNotExist(publicErr) {
		return fmt.Errorf("inspect public directory for recovery: %w", publicErr)
	}

	entries, err := os.ReadDir(metadataDir)
	if err != nil {
		return fmt.Errorf("list site metadata directory for recovery: %w", err)
	}
	var candidates []staleSiteBuildBackup
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, ".site-build-stage-") || !strings.HasSuffix(name, "-public-backup") {
			continue
		}
		path := filepath.Join(metadataDir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		if _, valid, err := loadSiteBuildManifest(path); err != nil || !valid {
			continue
		}
		candidates = append(candidates, staleSiteBuildBackup{path: path, modTime: info.ModTime().UnixNano()})
	}
	if len(candidates) == 0 {
		// An invalid or partially deleted backup is not safe to restore.  Keep
		// it in place until a new public tree commits successfully.
		return nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].modTime == candidates[j].modTime {
			return candidates[i].path > candidates[j].path
		}
		return candidates[i].modTime > candidates[j].modTime
	})
	if err := rename(candidates[0].path, publicDir); err != nil {
		return fmt.Errorf("restore public output from interrupted build: %w", err)
	}
	return cleanupStaleSiteBuildBackups(metadataDir)
}

func loadSiteBuildManifest(publicDir string) (siteBuildManifest, bool, error) {
	publicInfo, err := os.Lstat(publicDir)
	if err != nil {
		if os.IsNotExist(err) {
			return siteBuildManifest{}, false, nil
		}
		return siteBuildManifest{}, false, fmt.Errorf("inspect public directory: %w", err)
	}
	if publicInfo.Mode()&os.ModeSymlink != 0 || !publicInfo.IsDir() {
		return siteBuildManifest{}, false, fmt.Errorf("public path is not a directory: %s", publicDir)
	}

	manifestPath := filepath.Join(publicDir, siteBuildManifestName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return siteBuildManifest{}, false, nil
		}
		return siteBuildManifest{}, false, fmt.Errorf("read site build manifest: %w", err)
	}
	var manifest siteBuildManifest
	if err := json.Unmarshal(data, &manifest); err != nil || manifest.Schema != siteBuildManifestSchema || manifest.Sources == nil {
		return siteBuildManifest{}, false, nil
	}
	for sourcePath, entry := range manifest.Sources {
		if !validSiteBuildManifestEntry(sourcePath, entry) {
			return siteBuildManifest{}, false, nil
		}
	}
	return manifest, true, nil
}

func validSiteBuildManifestEntry(sourcePath string, entry siteBuildManifestEntry) bool {
	if !strings.HasPrefix(sourcePath, "content/") || !strings.EqualFold(filepath.Ext(sourcePath), ".md") {
		return false
	}
	cleanSource := filepath.ToSlash(filepath.Clean(filepath.FromSlash(sourcePath)))
	if cleanSource != sourcePath || filepath.IsAbs(filepath.FromSlash(sourcePath)) || strings.Contains(sourcePath, "\\") {
		return false
	}
	expectedOutput := strings.TrimSuffix(strings.TrimPrefix(sourcePath, "content/"), filepath.Ext(sourcePath)) + ".html"
	if entry.Output != expectedOutput || !validSiteBuildRelativePath(entry.Output) {
		return false
	}
	decoded, err := hex.DecodeString(entry.Checksum)
	return err == nil && len(decoded) == sha256.Size
}

func validSiteBuildRelativePath(relativePath string) bool {
	if relativePath == "" || filepath.IsAbs(filepath.FromSlash(relativePath)) || strings.Contains(relativePath, "\\") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relativePath)))
	return clean == relativePath && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func confinedSiteBuildPath(root, relativePath string) (string, error) {
	if !validSiteBuildRelativePath(relativePath) {
		return "", fmt.Errorf("site build path is not confined: %q", relativePath)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	pathAbs, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(relativePath)))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil || rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") {
		return "", fmt.Errorf("site build path escapes root: %q", relativePath)
	}
	return pathAbs, nil
}

func copySiteBuildTree(ctx context.Context, sourceDir, destinationDir string) error {
	return filepath.WalkDir(sourceDir, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relativePath, err := filepath.Rel(sourceDir, sourcePath)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("public output symlink is not allowed: %s", sourcePath)
		}
		destinationPath, err := confinedSiteBuildPath(destinationDir, filepath.ToSlash(relativePath))
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destinationPath, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported public output type: %s", sourcePath)
		}
		if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
			return err
		}
		return copySiteBuildFile(ctx, sourcePath, destinationPath, info.Mode().Perm())
	})
}

func copySiteBuildFile(ctx context.Context, sourcePath, destinationPath string, mode fs.FileMode) (err error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := destination.Close(); err == nil {
			err = closeErr
		}
	}()
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		readCount, readErr := source.Read(buffer)
		if readCount > 0 {
			if _, err := destination.Write(buffer[:readCount]); err != nil {
				return err
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}
	return destination.Sync()
}

func removeSiteBuildOutput(stageDir, outputPath string) error {
	path, err := confinedSiteBuildPath(stageDir, outputPath)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func writeSiteBuildTempFile(directory, pattern string, data []byte, mode fs.FileMode) (path string, err error) {
	file, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return "", err
	}
	path = file.Name()
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(mode); err != nil {
		return "", err
	}
	if _, err := file.Write(data); err != nil {
		return "", err
	}
	if err := file.Sync(); err != nil {
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	keep = true
	return path, nil
}

type siteBuildFileSnapshot struct {
	data   []byte
	mode   fs.FileMode
	exists bool
}

func snapshotSiteBuildFile(path string) (siteBuildFileSnapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return siteBuildFileSnapshot{}, nil
		}
		return siteBuildFileSnapshot{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return siteBuildFileSnapshot{}, fmt.Errorf("site index is not a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return siteBuildFileSnapshot{}, err
	}
	return siteBuildFileSnapshot{data: data, mode: info.Mode().Perm(), exists: true}, nil
}

func restoreSiteBuildFile(path string, snapshot siteBuildFileSnapshot) error {
	if !snapshot.exists {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return atomicWriteDerivedFile(path, snapshot.data, snapshot.mode)
}

func (b *siteBuilder) publish(root, stageDir, indexTempPath string) error {
	publicDir := filepath.Join(root, "public")
	indexPath := filepath.Join(root, ".mdsys", "index.json")
	indexSnapshot, err := snapshotSiteBuildFile(indexPath)
	if err != nil {
		return fmt.Errorf("snapshot site index: %w", err)
	}

	backupDir := stageDir + "-public-backup"
	hadPublic := false
	if info, statErr := os.Lstat(publicDir); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("public path is not a directory: %s", publicDir)
		}
		if err := b.hooks.rename(publicDir, backupDir); err != nil {
			return fmt.Errorf("move previous public output to backup: %w", err)
		}
		hadPublic = true
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("inspect public output before publish: %w", statErr)
	}

	if err := b.hooks.rename(stageDir, publicDir); err != nil {
		if hadPublic {
			if rollbackErr := b.hooks.rename(backupDir, publicDir); rollbackErr != nil {
				return fmt.Errorf("publish staged public output: %w; restore previous public output: %v", err, rollbackErr)
			}
		}
		return fmt.Errorf("publish staged public output: %w", err)
	}

	rollback := func(cause error) error {
		var rollbackErrors []string
		if err := rollbackSiteBuildPublic(b.hooks, stageDir, publicDir, backupDir, hadPublic); err != nil {
			rollbackErrors = append(rollbackErrors, err.Error())
		}
		if err := restoreSiteBuildFile(indexPath, indexSnapshot); err != nil {
			rollbackErrors = append(rollbackErrors, "restore previous site index: "+err.Error())
		}
		if len(rollbackErrors) > 0 {
			return fmt.Errorf("%w; rollback failed: %s", cause, strings.Join(rollbackErrors, "; "))
		}
		return cause
	}

	if err := b.hooks.commitIndex(indexTempPath, indexPath); err != nil {
		return rollback(fmt.Errorf("publish site index: %w", err))
	}
	if hadPublic {
		if err := b.hooks.removeAll(backupDir); err != nil {
			// RemoveAll may have deleted only part of the backup before returning
			// an error.  At this point the new public directory and index are both
			// committed，so attempting to restore a partial backup would corrupt
			// otherwise valid output.  Leave any remainder for the next build's
			// confined stale-backup cleanup and surface the maintenance failure.
			return fmt.Errorf("remove previous public backup after commit: %w", err)
		}
	}
	return nil
}

func rollbackSiteBuildPublic(hooks siteBuildHooks, stageDir, publicDir, backupDir string, hadPublic bool) error {
	if err := hooks.rename(publicDir, stageDir); err != nil {
		return fmt.Errorf("move failed public output aside: %w", err)
	}
	if hadPublic {
		if err := hooks.rename(backupDir, publicDir); err != nil {
			_ = hooks.rename(stageDir, publicDir)
			return fmt.Errorf("restore previous public output: %w", err)
		}
	}
	if err := hooks.removeAll(stageDir); err != nil {
		return fmt.Errorf("remove failed public output: %w", err)
	}
	return nil
}

func sortedSetKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
