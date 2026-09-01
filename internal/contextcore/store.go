package contextcore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const maxRequestBytes = 128 * 1024

type Store struct {
	dataRoot     string
	protocolRoot string
	requestsDir  string
	responsesDir string
	processedDir string
}

type PendingRequest struct {
	Filename string
	Path     string
	Data     []byte
	SHA256   string
}

func NewStore(dataDir string) (*Store, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve KARTE_DATA_DIR: %w", err)
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve KARTE_DATA_DIR symlinks: %w", err)
	}
	protocolRoot := filepath.Join(realRoot, ".mdsys", "context", "v1")
	store := &Store{
		dataRoot: realRoot, protocolRoot: protocolRoot,
		requestsDir: filepath.Join(protocolRoot, "requests"), responsesDir: filepath.Join(protocolRoot, "responses"),
		processedDir: filepath.Join(protocolRoot, "processed"),
	}
	for _, path := range []string{store.protocolRoot, store.requestsDir, store.responsesDir, store.processedDir} {
		if err := store.assertNoSymlinkEscape(path); err != nil {
			return nil, err
		}
	}
	return store, nil
}

func (store *Store) EnsureLayout() error {
	for _, path := range []string{store.requestsDir, store.responsesDir, store.processedDir} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create context protocol directory: %w", err)
		}
		if err := store.assertExistingWithinDataRoot(path); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) ListPending(limit int) ([]PendingRequest, error) {
	if limit < 1 {
		limit = 1
	}
	if err := store.EnsureLayout(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(store.requestsDir)
	if err != nil {
		return nil, fmt.Errorf("list context requests: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	requests := make([]PendingRequest, 0, limit)
	for _, entry := range entries {
		if len(requests) >= limit {
			break
		}
		name := entry.Name()
		if entry.IsDir() || strings.HasPrefix(name, ".") || filepath.Ext(name) != ".json" {
			continue
		}
		path := filepath.Join(store.requestsDir, name)
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxRequestBytes {
			continue
		}
		if err := store.assertExistingWithinDataRoot(path); err != nil {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		digest := sha256.Sum256(data)
		requests = append(requests, PendingRequest{Filename: name, Path: path, Data: data, SHA256: hex.EncodeToString(digest[:])})
	}
	return requests, nil
}

func DecodeRequest(data []byte) (Request, error) {
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, validationError("invalid_json", "request is not valid protocol JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Request{}, validationError("invalid_json", "request contains trailing JSON")
	}
	if err := request.Validate(); err != nil {
		return Request{}, err
	}
	return request, nil
}

func (store *Store) ReadResponse(requestID string) (*Response, error) {
	if !requestIDPattern.MatchString(requestID) {
		return nil, fmt.Errorf("invalid request_id")
	}
	path := filepath.Join(store.responsesDir, requestID+".json")
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("inspect context response: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, fmt.Errorf("context response must be a regular file")
	}
	if err := store.assertExistingWithinDataRoot(path); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read context response: %w", err)
	}
	var response Response
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return nil, fmt.Errorf("decode context response: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("decode context response: trailing JSON")
	}
	return &response, nil
}

func (store *Store) WriteResponse(response Response) error {
	if !requestIDPattern.MatchString(response.RequestID) || response.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("invalid context response identity")
	}
	if err := store.EnsureLayout(); err != nil {
		return err
	}
	destination := filepath.Join(store.responsesDir, response.RequestID+".json")
	existing, err := store.ReadResponse(response.RequestID)
	if err != nil {
		return err
	}
	if existing != nil {
		left, _ := json.Marshal(existing)
		right, _ := json.Marshal(response)
		if bytes.Equal(left, right) {
			return nil
		}
		return fmt.Errorf("context response already exists with different content")
	}
	return atomicWriteJSON(destination, response)
}

func (store *Store) ArchiveRequest(pending PendingRequest, requestID string) error {
	if !requestIDPattern.MatchString(requestID) || strings.TrimSuffix(pending.Filename, ".json") != requestID {
		return fmt.Errorf("request filename does not match request_id")
	}
	if err := store.EnsureLayout(); err != nil {
		return err
	}
	destination := filepath.Join(store.processedDir, pending.Filename)
	if _, err := os.Stat(destination); err == nil {
		existing, readErr := os.ReadFile(destination)
		if readErr != nil {
			return readErr
		}
		if bytes.Equal(existing, pending.Data) {
			if err := os.Remove(pending.Path); err != nil {
				return err
			}
			return syncDirectory(store.requestsDir)
		}
		return fmt.Errorf("processed request already exists with different content")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(pending.Path, destination); err != nil {
		return fmt.Errorf("archive context request: %w", err)
	}
	if err := syncDirectory(store.processedDir); err != nil {
		return err
	}
	return syncDirectory(store.requestsDir)
}

func atomicWriteJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	temporary, err := os.CreateTemp(dir, ".context-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func (store *Store) assertNoSymlinkEscape(path string) error {
	relative, err := filepath.Rel(store.dataRoot, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("context protocol path escapes KARTE_DATA_DIR")
	}
	current := store.dataRoot
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("context protocol path contains a symlink")
		}
	}
	return nil
}

func (store *Store) assertExistingWithinDataRoot(path string) error {
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(store.dataRoot, realPath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("context protocol path escapes KARTE_DATA_DIR")
	}
	return nil
}
