package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
)

const (
	documentMapDirectoryName = ".mdsys"
	documentMapFileName      = "doc_map.json"
)

var errCorruptDocumentMap = errors.New("document map is corrupt")

type documentMapStoreOperations struct {
	mkdirAll     func(string, fs.FileMode) error
	stat         func(string) (fs.FileInfo, error)
	lstat        func(string) (fs.FileInfo, error)
	readFile     func(string) ([]byte, error)
	abs          func(string) (string, error)
	evalSymlinks func(string) (string, error)
	replace      func(string, string) error
}

// documentMapStore owns the validated read-modify-write operation for
// .mdsys/doc_map.json. App supplies the per-instance mutex so independent
// mutation methods cannot lose each other's mappings.
type documentMapStore struct {
	operations documentMapStoreOperations
}

func (a *App) updateDocumentMapping(docID, documentPath string) (map[string]string, error) {
	return a.updateDocumentMappings(map[string]string{docID: documentPath})
}

func (a *App) updateDocumentMappings(updates map[string]string) (map[string]string, error) {
	if a == nil || strings.TrimSpace(a.dataDir) == "" {
		return nil, errors.New("document map data directory is not initialized")
	}
	canonicalUpdates := make(map[string]string, len(updates))
	for docID, documentPath := range updates {
		absPath, ok := a.resolveContentPath(documentPath)
		if !ok {
			return nil, fmt.Errorf("invalid document map path %q", documentPath)
		}
		relativePath, err := filepath.Rel(a.dataDir, absPath)
		if err != nil {
			return nil, fmt.Errorf("resolve document map path %q: %w", documentPath, err)
		}
		canonicalPath := filepath.ToSlash(relativePath)
		if _, err := validateDocumentMapEntry(docID, canonicalPath); err != nil {
			return nil, err
		}
		canonicalUpdates[docID] = canonicalPath
	}

	a.documentMapMu.Lock()
	defer a.documentMapMu.Unlock()
	return a.documentMapStore.update(a.dataDir, canonicalUpdates)
}

func (a *App) renameDocumentPath(oldPath, newPath string) error {
	if a.documentRenameFile != nil {
		return a.documentRenameFile(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

func (s *documentMapStore) update(dataDir string, updates map[string]string) (map[string]string, error) {
	if len(updates) == 0 {
		return map[string]string{}, nil
	}
	for docID, documentPath := range updates {
		if _, err := validateDocumentMapEntry(docID, documentPath); err != nil {
			return nil, err
		}
	}

	documentMapPath, err := s.preparePath(dataDir)
	if err != nil {
		return nil, err
	}
	mappings, exists, err := s.load(documentMapPath)
	if err != nil {
		return nil, err
	}

	changed := !exists
	for docID, documentPath := range updates {
		if mappings[docID] != documentPath {
			mappings[docID] = documentPath
			changed = true
		}
	}
	if !changed {
		return cloneDocumentMap(mappings), nil
	}

	encoded, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode document map: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := atomicWriteFileWithReplace(documentMapPath, encoded, 0o644, s.replaceFile); err != nil {
		return nil, fmt.Errorf("persist document map: %w", err)
	}
	return cloneDocumentMap(mappings), nil
}

func (s *documentMapStore) preparePath(dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", errors.New("document map data directory is empty")
	}
	root, err := s.abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve document map data directory: %w", err)
	}
	rootInfo, err := s.stat(root)
	if err != nil {
		return "", fmt.Errorf("stat document map data directory: %w", err)
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("document map data path is not a directory: %s", root)
	}
	resolvedRoot, err := s.evalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve document map data directory symlinks: %w", err)
	}

	directory := filepath.Join(root, documentMapDirectoryName)
	if !documentMapPathWithinRoot(root, directory) {
		return "", errors.New("document map directory escapes data directory")
	}
	info, err := s.lstat(directory)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := s.mkdirAll(directory, 0o755); err != nil {
			return "", fmt.Errorf("create document map directory: %w", err)
		}
		info, err = s.lstat(directory)
		if err != nil {
			return "", fmt.Errorf("inspect created document map directory: %w", err)
		}
	case err != nil:
		return "", fmt.Errorf("inspect document map directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("document map directory is not a confined directory")
	}
	resolvedDirectory, err := s.evalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("resolve document map directory symlinks: %w", err)
	}
	if !documentMapPathWithinRoot(resolvedRoot, resolvedDirectory) {
		return "", errors.New("document map directory resolves outside data directory")
	}

	documentMapPath := filepath.Join(resolvedDirectory, documentMapFileName)
	if !documentMapPathWithinRoot(resolvedDirectory, documentMapPath) {
		return "", errors.New("document map path escapes its directory")
	}
	return documentMapPath, nil
}

func (s *documentMapStore) load(documentMapPath string) (map[string]string, bool, error) {
	info, err := s.lstat(documentMapPath)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]string), false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("inspect document map: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, false, errors.New("document map path is not a confined regular file")
	}
	data, err := s.readFile(documentMapPath)
	if err != nil {
		return nil, false, fmt.Errorf("read document map: %w", err)
	}
	mappings, err := decodeDocumentMap(data)
	if err != nil {
		return nil, false, err
	}
	return mappings, true, nil
}

func decodeDocumentMap(data []byte) (map[string]string, error) {
	var decoded map[string]string
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("%w: decode JSON: %v", errCorruptDocumentMap, err)
	}
	if decoded == nil {
		return nil, fmt.Errorf("%w: expected a JSON object", errCorruptDocumentMap)
	}
	normalized := make(map[string]string, len(decoded))
	for docID, documentPath := range decoded {
		canonicalPath, err := validateDocumentMapEntry(docID, documentPath)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", errCorruptDocumentMap, err)
		}
		normalized[docID] = canonicalPath
	}
	return normalized, nil
}

func validateDocumentMapEntry(docID, documentPath string) (string, error) {
	if strings.TrimSpace(docID) == "" || docID != strings.TrimSpace(docID) {
		return "", fmt.Errorf("invalid document map doc_id %q", docID)
	}
	slashPath := strings.ReplaceAll(documentPath, "\\", "/")
	if slashPath == "" || strings.HasPrefix(slashPath, "/") {
		return "", fmt.Errorf("invalid document map path %q", documentPath)
	}
	canonicalPath := pathpkg.Clean(slashPath)
	if canonicalPath != slashPath || !strings.HasPrefix(canonicalPath, "content/") {
		return "", fmt.Errorf("invalid document map path %q", documentPath)
	}
	relativePath := strings.TrimPrefix(canonicalPath, "content/")
	if relativePath == "" || relativePath == "." || relativePath == ".." ||
		strings.HasPrefix(relativePath, "../") || !strings.EqualFold(pathpkg.Ext(relativePath), ".md") {
		return "", fmt.Errorf("invalid document map path %q", documentPath)
	}
	return canonicalPath, nil
}

func cloneDocumentMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for docID, documentPath := range source {
		result[docID] = documentPath
	}
	return result
}

func documentMapPathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *documentMapStore) mkdirAll(path string, perm fs.FileMode) error {
	if s.operations.mkdirAll != nil {
		return s.operations.mkdirAll(path, perm)
	}
	return os.MkdirAll(path, perm)
}

func (s *documentMapStore) stat(path string) (fs.FileInfo, error) {
	if s.operations.stat != nil {
		return s.operations.stat(path)
	}
	return os.Stat(path)
}

func (s *documentMapStore) lstat(path string) (fs.FileInfo, error) {
	if s.operations.lstat != nil {
		return s.operations.lstat(path)
	}
	return os.Lstat(path)
}

func (s *documentMapStore) readFile(path string) ([]byte, error) {
	if s.operations.readFile != nil {
		return s.operations.readFile(path)
	}
	return os.ReadFile(path)
}

func (s *documentMapStore) abs(path string) (string, error) {
	if s.operations.abs != nil {
		return s.operations.abs(path)
	}
	return filepath.Abs(path)
}

func (s *documentMapStore) evalSymlinks(path string) (string, error) {
	if s.operations.evalSymlinks != nil {
		return s.operations.evalSymlinks(path)
	}
	return filepath.EvalSymlinks(path)
}

func (s *documentMapStore) replaceFile(sourcePath, destinationPath string) error {
	if s.operations.replace != nil {
		return s.operations.replace(sourcePath, destinationPath)
	}
	return atomicReplaceFile(sourcePath, destinationPath)
}
