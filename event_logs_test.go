package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestSaveEventLogsAppendsValidatedJSONLines(t *testing.T) {
	dataDir := t.TempDir()
	app := &App{dataDir: dataDir}

	legacy := `[{
		"component":"EditorLayout",
		"action":"editor-input",
		"state":{"length":42},
		"timestamp":1
	}]`
	if saved, err := app.SaveEventLogs(legacy); err != nil || !saved {
		t.Fatalf("SaveEventLogs legacy payload = (%v, %v), want (true, nil)", saved, err)
	}
	if saved, err := app.SaveEventLogs(eventLogPayload("save-error", 2)); err != nil || !saved {
		t.Fatalf("SaveEventLogs current payload = (%v, %v), want (true, nil)", saved, err)
	}

	records := readStoredEventLogs(t, filepath.Join(dataDir, eventLogDirectoryName, eventLogActiveFileName))
	if len(records) != 2 {
		t.Fatalf("stored event logs = %d, want 2", len(records))
	}
	if records[0].Level != "debug" || records[1].Level != "info" {
		t.Fatalf("stored levels = %q, %q, want debug, info", records[0].Level, records[1].Level)
	}
	if string(records[0].State) != `{"length":42}` {
		t.Fatalf("stored state = %s", records[0].State)
	}
}

func TestEventLogStoreRejectsInvalidOrUnboundedInput(t *testing.T) {
	dataDir := t.TempDir()
	store := &eventLogStore{
		maxInputBytes: 1024,
		maxEntries:    2,
		maxFieldBytes: 16,
		maxStateBytes: 8,
	}
	tests := map[string]string{
		"empty":           "",
		"invalid json":    `{`,
		"empty batch":     `[]`,
		"unknown field":   `[{"component":"App","action":"init","timestamp":1,"extra":true}]`,
		"missing field":   `[{"component":"App","timestamp":1}]`,
		"control field":   `[{"component":"App\nInjected","action":"init","timestamp":1}]`,
		"bad timestamp":   `[{"component":"App","action":"init","timestamp":0}]`,
		"bad level":       `[{"component":"App","action":"init","timestamp":1,"level":"trace"}]`,
		"oversized state": `[{"component":"App","action":"init","timestamp":1,"state":"123456789"}]`,
		"too many entries": `[
			{"component":"App","action":"one","timestamp":1},
			{"component":"App","action":"two","timestamp":2},
			{"component":"App","action":"three","timestamp":3}
		]`,
		"oversized input": strings.Repeat("x", 1025),
	}

	for name, payload := range tests {
		t.Run(name, func(t *testing.T) {
			if err := store.append(dataDir, payload); err == nil {
				t.Fatal("append accepted invalid payload")
			}
		})
	}
	activePath := filepath.Join(dataDir, eventLogDirectoryName, eventLogActiveFileName)
	if _, err := os.Stat(activePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid input created active event log: %v", err)
	}
}

func TestEventLogStoreSerializesConcurrentAppends(t *testing.T) {
	dataDir := t.TempDir()
	store := &eventLogStore{}
	const calls = 64

	var wait sync.WaitGroup
	errorsByCall := make(chan error, calls)
	for index := 0; index < calls; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errorsByCall <- store.append(dataDir, eventLogPayload(fmt.Sprintf("event-%02d", index), int64(index+1)))
		}(index)
	}
	wait.Wait()
	close(errorsByCall)
	for err := range errorsByCall {
		if err != nil {
			t.Fatalf("concurrent append: %v", err)
		}
	}

	records := readStoredEventLogs(t, filepath.Join(dataDir, eventLogDirectoryName, eventLogActiveFileName))
	if len(records) != calls {
		t.Fatalf("stored event logs = %d, want %d", len(records), calls)
	}
	seen := make(map[string]bool, calls)
	for _, record := range records {
		seen[record.Action] = true
	}
	for index := 0; index < calls; index++ {
		action := fmt.Sprintf("event-%02d", index)
		if !seen[action] {
			t.Errorf("missing concurrent event %q", action)
		}
	}
}

func TestEventLogStoreRotatesAtSizeBoundaryAndRetainsBoundedGenerations(t *testing.T) {
	dataDir := t.TempDir()
	store := &eventLogStore{backupCount: 2}
	oneLine, err := store.validateAndEncode(eventLogPayload("event-01", 1))
	if err != nil {
		t.Fatal(err)
	}
	store.maxFileBytes = int64(len(oneLine) * 2)
	directory := filepath.Join(dataDir, eventLogDirectoryName)
	activePath := filepath.Join(directory, eventLogActiveFileName)

	for index := 1; index <= 2; index++ {
		if err := store.append(dataDir, eventLogPayload(fmt.Sprintf("event-%02d", index), int64(index))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(eventLogBackupPath(directory, 1)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("exact size boundary rotated early: %v", err)
	}
	if records := readStoredEventLogs(t, activePath); len(records) != 2 {
		t.Fatalf("records at exact boundary = %d, want 2", len(records))
	}

	for index := 3; index <= 7; index++ {
		if err := store.append(dataDir, eventLogPayload(fmt.Sprintf("event-%02d", index), int64(index))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(eventLogBackupPath(directory, 3)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("retention left generation 3: %v", err)
	}
	wantGenerations := map[string][]string{
		activePath:                       {"event-07"},
		eventLogBackupPath(directory, 1): {"event-05", "event-06"},
		eventLogBackupPath(directory, 2): {"event-03", "event-04"},
	}
	for path, wantActions := range wantGenerations {
		records := readStoredEventLogs(t, path)
		gotActions := make([]string, len(records))
		for index, record := range records {
			gotActions[index] = record.Action
		}
		if strings.Join(gotActions, ",") != strings.Join(wantActions, ",") {
			t.Errorf("%s actions = %v, want %v", filepath.Base(path), gotActions, wantActions)
		}
	}
}

func TestEventLogStoreRollsBackPartialAppendWithTruncateThenSync(t *testing.T) {
	dataDir := t.TempDir()
	store := &eventLogStore{}
	if err := store.append(dataDir, eventLogPayload("existing", 1)); err != nil {
		t.Fatal(err)
	}
	activePath := filepath.Join(dataDir, eventLogDirectoryName, eventLogActiveFileName)
	before, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}

	var operations []string
	store.operations.openFile = func(path string, flag int, perm fs.FileMode) (eventLogAppendFile, error) {
		file, err := os.OpenFile(path, flag, perm)
		if err != nil {
			return nil, err
		}
		return &partialEventLogFile{File: file, operations: &operations}, nil
	}
	if err := store.append(dataDir, eventLogPayload("partial", 2)); err == nil {
		t.Fatal("partial append succeeded")
	}
	after, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("partial append changed active file\nbefore: %q\n after: %q", before, after)
	}
	if got := strings.Join(operations, ","); got != "write,truncate,sync" {
		t.Fatalf("rollback operations = %q, want write,truncate,sync", got)
	}
}

func TestEventLogStoreKeepsRotatedLogWhenNewActiveCreationFails(t *testing.T) {
	dataDir := t.TempDir()
	store := &eventLogStore{backupCount: 2}
	firstPayload := eventLogPayload("first", 1)
	firstLine, err := store.validateAndEncode(firstPayload)
	if err != nil {
		t.Fatal(err)
	}
	store.maxFileBytes = int64(len(firstLine))
	if err := store.append(dataDir, firstPayload); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected active creation failure")
	store.operations.openFile = func(string, int, fs.FileMode) (eventLogAppendFile, error) {
		return nil, injected
	}
	if err := store.append(dataDir, eventLogPayload("retry", 2)); !errors.Is(err, injected) {
		t.Fatalf("append error = %v, want injected failure", err)
	}
	directory := filepath.Join(dataDir, eventLogDirectoryName)
	activePath := filepath.Join(directory, eventLogActiveFileName)
	if _, err := os.Stat(activePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed active creation left unexpected active file: %v", err)
	}
	rotated := readStoredEventLogs(t, eventLogBackupPath(directory, 1))
	if len(rotated) != 1 || rotated[0].Action != "first" {
		t.Fatalf("rotated records after active creation failure = %+v", rotated)
	}

	store.operations.openFile = nil
	if err := store.append(dataDir, eventLogPayload("retry", 2)); err != nil {
		t.Fatalf("retry append: %v", err)
	}
	active := readStoredEventLogs(t, activePath)
	if len(active) != 1 || active[0].Action != "retry" {
		t.Fatalf("active records after retry = %+v", active)
	}
}

func TestEventLogStoreRotatesFullGenerationsWithWindowsRenameSemantics(t *testing.T) {
	dataDir := t.TempDir()
	store := &eventLogStore{backupCount: 5}
	oneLine, err := store.validateAndEncode(eventLogPayload("event-01", 1))
	if err != nil {
		t.Fatal(err)
	}
	store.maxFileBytes = int64(len(oneLine))
	store.operations.rename = func(oldPath, newPath string) error {
		if _, err := os.Lstat(newPath); err == nil {
			return fmt.Errorf("windows rename destination exists: %s", newPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Rename(oldPath, newPath)
	}

	for index := 1; index <= 8; index++ {
		if err := store.append(dataDir, eventLogPayload(fmt.Sprintf("event-%02d", index), int64(index))); err != nil {
			t.Fatalf("append event %d: %v", index, err)
		}
	}
	directory := filepath.Join(dataDir, eventLogDirectoryName)
	want := map[string]string{
		filepath.Join(directory, eventLogActiveFileName): "event-08",
		eventLogBackupPath(directory, 1):                 "event-07",
		eventLogBackupPath(directory, 2):                 "event-06",
		eventLogBackupPath(directory, 3):                 "event-05",
		eventLogBackupPath(directory, 4):                 "event-04",
		eventLogBackupPath(directory, 5):                 "event-03",
	}
	for path, wantAction := range want {
		records := readStoredEventLogs(t, path)
		if len(records) != 1 || records[0].Action != wantAction {
			t.Errorf("%s records = %+v, want %s", filepath.Base(path), records, wantAction)
		}
	}
}

func TestEventLogStoreKeepsActiveLogWhenGenerationShiftFails(t *testing.T) {
	dataDir := t.TempDir()
	store := &eventLogStore{backupCount: 2}
	oneLine, err := store.validateAndEncode(eventLogPayload("event-01", 1))
	if err != nil {
		t.Fatal(err)
	}
	store.maxFileBytes = int64(len(oneLine))
	for index := 1; index <= 3; index++ {
		if err := store.append(dataDir, eventLogPayload(fmt.Sprintf("event-%02d", index), int64(index))); err != nil {
			t.Fatal(err)
		}
	}
	directory := filepath.Join(dataDir, eventLogDirectoryName)
	activePath := filepath.Join(directory, eventLogActiveFileName)
	before, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected generation shift failure")
	store.operations.rename = func(string, string) error { return injected }
	if err := store.append(dataDir, eventLogPayload("event-04", 4)); !errors.Is(err, injected) {
		t.Fatalf("append error = %v, want injected failure", err)
	}
	after, err := os.ReadFile(activePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("generation shift failure changed active log\nbefore: %q\n after: %q", before, after)
	}
}

func TestEventLogStoreRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available without elevated privileges")
	}
	dataDir := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dataDir, eventLogDirectoryName)); err != nil {
		t.Fatal(err)
	}

	store := &eventLogStore{}
	if err := store.append(dataDir, eventLogPayload("escape", 1)); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("symlink escape error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, eventLogActiveFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink escape wrote outside data directory: %v", err)
	}
}

func TestEventLogStoreRejectsSymlinkActiveFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation is not reliably available without elevated privileges")
	}
	dataDir := t.TempDir()
	directory := filepath.Join(dataDir, eventLogDirectoryName)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.jsonl")
	const original = "outside must remain unchanged\n"
	if err := os.WriteFile(outsidePath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(directory, eventLogActiveFileName)); err != nil {
		t.Fatal(err)
	}

	store := &eventLogStore{}
	if err := store.append(dataDir, eventLogPayload("escape", 1)); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("active symlink error = %v", err)
	}
	content, err := os.ReadFile(outsidePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Fatalf("active symlink changed outside file: %q", content)
	}
}

type partialEventLogFile struct {
	*os.File
	operations *[]string
}

func (f *partialEventLogFile) Write(data []byte) (int, error) {
	*f.operations = append(*f.operations, "write")
	written, err := f.File.Write(data[:len(data)/2])
	if err != nil {
		return written, err
	}
	return written, errors.New("injected partial write failure")
}

func (f *partialEventLogFile) Truncate(size int64) error {
	*f.operations = append(*f.operations, "truncate")
	return f.File.Truncate(size)
}

func (f *partialEventLogFile) Sync() error {
	*f.operations = append(*f.operations, "sync")
	return f.File.Sync()
}

func eventLogPayload(action string, timestamp int64) string {
	return fmt.Sprintf(
		`[{"component":"Test","action":%q,"timestamp":%d,"level":"info"}]`,
		action,
		timestamp,
	)
}

func readStoredEventLogs(t *testing.T, path string) []storedEventLog {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	records := make([]storedEventLog, 0, len(lines))
	for _, line := range lines {
		var record storedEventLog
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode JSONL line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}
