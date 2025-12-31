package sync

import (
	"errors"
	"io/fs"
	"testing"
)

type mockFileManager struct {
	mkdirErr  error
	writeErr  error
	mkdirPath string
	writePath string
	content   []byte
}

func (m *mockFileManager) MkdirAll(path string, perm fs.FileMode) error {
	m.mkdirPath = path
	return m.mkdirErr
}

func (m *mockFileManager) WriteFile(name string, data []byte, perm fs.FileMode) error {
	m.writePath = name
	m.content = data
	return m.writeErr
}

func TestApplyFileChangeSuccess(t *testing.T) {
	fm := &mockFileManager{}
	sm := NewSyncManagerWithFileManager(nil, "/repo", fm)

	change := FileChange{Path: "notes/test.txt", Content: "hello"}
	if err := sm.applyFileChange(change); err != nil {
		t.Fatalf("applyFileChange returned error: %v", err)
	}

	if fm.mkdirPath == "" || fm.writePath == "" {
		t.Fatalf("file manager not called correctly: %+v", fm)
	}
	if string(fm.content) != "hello" {
		t.Fatalf("unexpected content: %s", string(fm.content))
	}
}

func TestApplyFileChangeMkdirError(t *testing.T) {
	fm := &mockFileManager{mkdirErr: errors.New("mkdir fail")}
	sm := NewSyncManagerWithFileManager(nil, "/repo", fm)

	err := sm.applyFileChange(FileChange{Path: "notes/bad.txt"})
	if err == nil || err.Error() != "failed to create directory: mkdir fail" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestApplyFileChangeUnsafePath(t *testing.T) {
	fm := &mockFileManager{}
	sm := NewSyncManagerWithFileManager(nil, "/repo", fm)

	tests := []struct {
		name string
		path string
	}{
		{name: "absolute path", path: "/etc/passwd"},
		{name: "parent directory", path: "../secret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := sm.applyFileChange(FileChange{Path: tc.path})
			if err == nil || err.Error() != "unsafe file path: "+tc.path {
				t.Fatalf("expected unsafe path error, got: %v", err)
			}
			if fm.mkdirPath != "" || fm.writePath != "" {
				t.Fatalf("file manager should not be called for unsafe path: %+v", fm)
			}
		})
	}
}

func TestApplyFileChangeWriteError(t *testing.T) {
	fm := &mockFileManager{writeErr: errors.New("write fail")}
	sm := NewSyncManagerWithFileManager(nil, "/repo", fm)

	err := sm.applyFileChange(FileChange{Path: "notes/fail.txt"})
	if err == nil || err.Error() != "failed to write file: write fail" {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.mkdirPath == "" || fm.writePath == "" {
		t.Fatalf("file manager not called correctly: %+v", fm)
	}
}
