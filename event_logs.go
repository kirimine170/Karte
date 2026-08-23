package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
)

const (
	eventLogDirectoryName        = ".mdsys"
	eventLogActiveFileName       = "event-logs.jsonl"
	defaultEventLogMaxInputBytes = 8 << 20
	defaultEventLogMaxEntries    = 20000
	defaultEventLogMaxFieldBytes = 256
	defaultEventLogMaxStateBytes = 64 << 10
	defaultEventLogMaxFileBytes  = 16 << 20
	defaultEventLogBackupCount   = 5
	eventLogAppendBufferSize     = 64 << 10
)

type storedEventLog struct {
	Component string          `json:"component"`
	Action    string          `json:"action"`
	State     json.RawMessage `json:"state,omitempty"`
	Timestamp int64           `json:"timestamp"`
	Level     string          `json:"level"`
}

type eventLogAppendFile interface {
	io.Writer
	Stat() (fs.FileInfo, error)
	Sync() error
	Truncate(size int64) error
	Close() error
}

type eventLogFileOperations struct {
	mkdirAll     func(string, fs.FileMode) error
	stat         func(string) (fs.FileInfo, error)
	lstat        func(string) (fs.FileInfo, error)
	openFile     func(string, int, fs.FileMode) (eventLogAppendFile, error)
	rename       func(string, string) error
	remove       func(string) error
	abs          func(string) (string, error)
	evalSymlinks func(string) (string, error)
}

type eventLogStore struct {
	mu sync.Mutex

	maxInputBytes int
	maxEntries    int
	maxFieldBytes int
	maxStateBytes int
	maxFileBytes  int64
	backupCount   int
	operations    eventLogFileOperations
}

var defaultEventLogStore eventLogStore

func (s *eventLogStore) append(dataDir, raw string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	batch, err := s.validateAndEncode(raw)
	if err != nil {
		return err
	}
	maxFileBytes := s.configuredMaxFileBytes()
	if int64(len(batch)) > maxFileBytes {
		return fmt.Errorf("event log batch is %d bytes, limit is %d", len(batch), maxFileBytes)
	}

	directory, err := s.prepareDirectory(dataDir)
	if err != nil {
		return err
	}
	activePath := filepath.Join(directory, eventLogActiveFileName)
	info, err := s.lstat(activePath)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() {
			return fmt.Errorf("event log path is not a regular file: %s", activePath)
		}
		if info.Size() > maxFileBytes-int64(len(batch)) {
			if err := s.rotate(directory); err != nil {
				return fmt.Errorf("rotate event logs: %w", err)
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("stat event log file: %w", err)
	}

	if err := s.appendBatch(activePath, batch); err != nil {
		return fmt.Errorf("append event logs: %w", err)
	}
	return nil
}

func (s *eventLogStore) validateAndEncode(raw string) ([]byte, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("event log payload is empty")
	}
	if len(raw) > s.configuredMaxInputBytes() {
		return nil, fmt.Errorf(
			"event log payload is %d bytes, limit is %d",
			len(raw),
			s.configuredMaxInputBytes(),
		)
	}

	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	var records []storedEventLog
	if err := decoder.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode event log payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("event log payload contains multiple JSON values")
		}
		return nil, fmt.Errorf("decode trailing event log data: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("event log payload must contain at least one entry")
	}
	if len(records) > s.configuredMaxEntries() {
		return nil, fmt.Errorf(
			"event log payload contains %d entries, limit is %d",
			len(records),
			s.configuredMaxEntries(),
		)
	}

	var encoded bytes.Buffer
	for index := range records {
		record := &records[index]
		if err := s.validateRecord(record); err != nil {
			return nil, fmt.Errorf("event log entry %d: %w", index, err)
		}
		line, err := json.Marshal(record)
		if err != nil {
			return nil, fmt.Errorf("encode event log entry %d: %w", index, err)
		}
		encoded.Write(line)
		encoded.WriteByte('\n')
	}
	return encoded.Bytes(), nil
}

func (s *eventLogStore) validateRecord(record *storedEventLog) error {
	if err := validateEventLogField("component", record.Component, s.configuredMaxFieldBytes()); err != nil {
		return err
	}
	if err := validateEventLogField("action", record.Action, s.configuredMaxFieldBytes()); err != nil {
		return err
	}
	if record.Timestamp <= 0 {
		return errors.New("timestamp must be a positive Unix millisecond value")
	}
	if len(record.State) > s.configuredMaxStateBytes() {
		return fmt.Errorf(
			"state is %d bytes, limit is %d",
			len(record.State),
			s.configuredMaxStateBytes(),
		)
	}

	if record.Level == "" {
		// Accept the pre-T-065 frontend shape during rolling upgrades．
		record.Level = "debug"
	} else {
		record.Level = strings.ToLower(strings.TrimSpace(record.Level))
	}
	switch record.Level {
	case "debug", "info", "warn", "error":
		return nil
	default:
		return fmt.Errorf("unsupported level %q", record.Level)
	}
}

func validateEventLogField(name, value string, limit int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	if len(value) > limit {
		return fmt.Errorf("%s is %d bytes, limit is %d", name, len(value), limit)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("%s contains control characters", name)
	}
	return nil
}

func (s *eventLogStore) prepareDirectory(dataDir string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", errors.New("event log data directory is empty")
	}
	root, err := s.abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve event log data directory: %w", err)
	}
	info, err := s.stat(root)
	if err != nil {
		return "", fmt.Errorf("stat event log data directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("event log data path is not a directory: %s", root)
	}
	resolvedRoot, err := s.evalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve event log data directory symlinks: %w", err)
	}

	directory := filepath.Join(root, eventLogDirectoryName)
	if !pathWithinRoot(root, directory) {
		return "", errors.New("event log directory escapes data directory")
	}
	if err := s.mkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("create event log directory: %w", err)
	}
	resolvedDirectory, err := s.evalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("resolve event log directory symlinks: %w", err)
	}
	if !pathWithinRoot(resolvedRoot, resolvedDirectory) {
		return "", errors.New("event log directory resolves outside data directory")
	}
	return resolvedDirectory, nil
}

func pathWithinRoot(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func (s *eventLogStore) rotate(directory string) error {
	backupCount := s.configuredBackupCount()
	activePath := filepath.Join(directory, eventLogActiveFileName)
	oldestPath := eventLogBackupPath(directory, backupCount)
	if err := s.remove(oldestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove oldest event log: %w", err)
	}

	for generation := backupCount - 1; generation >= 1; generation-- {
		source := eventLogBackupPath(directory, generation)
		if _, err := s.stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("stat event log generation %d: %w", generation, err)
		}
		destination := eventLogBackupPath(directory, generation+1)
		if err := s.remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("prepare event log generation %d: %w", generation+1, err)
		}
		if err := s.rename(source, destination); err != nil {
			return fmt.Errorf("shift event log generation %d: %w", generation, err)
		}
	}
	firstBackup := eventLogBackupPath(directory, 1)
	if err := s.remove(firstBackup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("prepare first event log generation: %w", err)
	}
	if err := s.rename(activePath, firstBackup); err != nil {
		return fmt.Errorf("archive active event log: %w", err)
	}
	return nil
}

func eventLogBackupPath(directory string, generation int) string {
	return filepath.Join(directory, fmt.Sprintf("event-logs.%d.jsonl", generation))
}

func (s *eventLogStore) appendBatch(path string, batch []byte) error {
	file, err := s.openFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open active event log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("stat active event log: %w", err)
	}
	startOffset := info.Size()

	writer := bufio.NewWriterSize(file, eventLogAppendBufferSize)
	_, writeErr := writer.Write(batch)
	if writeErr == nil {
		writeErr = writer.Flush()
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if writeErr != nil {
		truncateErr := file.Truncate(startOffset)
		var rollbackSyncErr error
		if truncateErr == nil {
			rollbackSyncErr = file.Sync()
		}
		closeErr := file.Close()
		return errors.Join(
			writeErr,
			wrapOptionalError("truncate partial event log append", truncateErr),
			wrapOptionalError("sync event log rollback", rollbackSyncErr),
			wrapOptionalError("close event log after rollback", closeErr),
		)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close active event log: %w", err)
	}
	return nil
}

func wrapOptionalError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", message, err)
}

func (s *eventLogStore) configuredMaxInputBytes() int {
	if s.maxInputBytes > 0 {
		return s.maxInputBytes
	}
	return defaultEventLogMaxInputBytes
}

func (s *eventLogStore) configuredMaxEntries() int {
	if s.maxEntries > 0 {
		return s.maxEntries
	}
	return defaultEventLogMaxEntries
}

func (s *eventLogStore) configuredMaxFieldBytes() int {
	if s.maxFieldBytes > 0 {
		return s.maxFieldBytes
	}
	return defaultEventLogMaxFieldBytes
}

func (s *eventLogStore) configuredMaxStateBytes() int {
	if s.maxStateBytes > 0 {
		return s.maxStateBytes
	}
	return defaultEventLogMaxStateBytes
}

func (s *eventLogStore) configuredMaxFileBytes() int64 {
	if s.maxFileBytes > 0 {
		return s.maxFileBytes
	}
	return defaultEventLogMaxFileBytes
}

func (s *eventLogStore) configuredBackupCount() int {
	if s.backupCount > 0 {
		return s.backupCount
	}
	return defaultEventLogBackupCount
}

func (s *eventLogStore) mkdirAll(path string, perm fs.FileMode) error {
	if s.operations.mkdirAll != nil {
		return s.operations.mkdirAll(path, perm)
	}
	return os.MkdirAll(path, perm)
}

func (s *eventLogStore) stat(path string) (fs.FileInfo, error) {
	if s.operations.stat != nil {
		return s.operations.stat(path)
	}
	return os.Stat(path)
}

func (s *eventLogStore) lstat(path string) (fs.FileInfo, error) {
	if s.operations.lstat != nil {
		return s.operations.lstat(path)
	}
	return os.Lstat(path)
}

func (s *eventLogStore) openFile(path string, flag int, perm fs.FileMode) (eventLogAppendFile, error) {
	if s.operations.openFile != nil {
		return s.operations.openFile(path, flag, perm)
	}
	return os.OpenFile(path, flag, perm)
}

func (s *eventLogStore) rename(oldPath, newPath string) error {
	if s.operations.rename != nil {
		return s.operations.rename(oldPath, newPath)
	}
	return os.Rename(oldPath, newPath)
}

func (s *eventLogStore) remove(path string) error {
	if s.operations.remove != nil {
		return s.operations.remove(path)
	}
	return os.Remove(path)
}

func (s *eventLogStore) abs(path string) (string, error) {
	if s.operations.abs != nil {
		return s.operations.abs(path)
	}
	return filepath.Abs(path)
}

func (s *eventLogStore) evalSymlinks(path string) (string, error) {
	if s.operations.evalSymlinks != nil {
		return s.operations.evalSymlinks(path)
	}
	return filepath.EvalSymlinks(path)
}
