package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fileSearchFixture struct {
	app    *App
	root   string
	tokens map[string]string
	reads  map[string]int
}

func newFileSearchFixture(t *testing.T) *fileSearchFixture {
	t.Helper()
	root := t.TempDir()
	for _, directory := range []string{filepath.Join(root, "content"), filepath.Join(root, ".mdsys")} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fixture := &fileSearchFixture{
		app:    &App{dataDir: root},
		root:   root,
		tokens: make(map[string]string),
		reads:  make(map[string]int),
	}
	fixture.app.fileSearchChangeToken = func(path string, _ os.FileInfo) string {
		return fixture.tokens[filepath.Clean(path)]
	}
	fixture.app.fileSearchReadFile = func(path string) ([]byte, error) {
		fixture.reads[filepath.Clean(path)]++
		return os.ReadFile(path)
	}
	return fixture
}

func (fixture *fileSearchFixture) writeFile(t *testing.T, relativePath, content, token string, modified time.Time) string {
	t.Helper()
	absolutePath := filepath.Join(fixture.root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(absolutePath, modified, modified); err != nil {
		t.Fatal(err)
	}
	fixture.tokens[filepath.Clean(absolutePath)] = token
	return absolutePath
}

func (fixture *fileSearchFixture) readCount(relativePath string) int {
	return fixture.reads[filepath.Clean(filepath.Join(fixture.root, filepath.FromSlash(relativePath)))]
}

func (fixture *fileSearchFixture) loadIndex(t *testing.T) fileSearchIndex {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixture.root, ".mdsys", fileSearchIndexName))
	if err != nil {
		t.Fatal(err)
	}
	var index fileSearchIndex
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("search index is not valid JSON: %v", err)
	}
	return index
}

func TestGetFileListReturnsMetadataAndReusesUnchangedIndex(t *testing.T) {
	fixture := newFileSearchFixture(t)
	modified := time.Unix(1_700_000_000, 123)
	markdownPath := fixture.writeFile(
		t,
		"content/alpha.md",
		"---\ntitle: \"Alpha title\"\n---\nbody-only-secret\n",
		"alpha-v1",
		modified,
	)
	fixture.writeFile(t, "content/reference.pdf", "not-read-as-markdown", "pdf-v1", modified)

	originalReplace := saveFileAtomicReplace
	replaceCalls := 0
	saveFileAtomicReplace = func(temporaryPath, destinationPath string) error {
		replaceCalls++
		wantDestination := filepath.Join(fixture.root, ".mdsys", fileSearchIndexName)
		if destinationPath != wantDestination {
			t.Fatalf("index destination = %q, want %q", destinationPath, wantDestination)
		}
		if filepath.Dir(temporaryPath) != filepath.Dir(destinationPath) {
			t.Fatalf("index temporary file is not in destination directory: %q", temporaryPath)
		}
		data, err := os.ReadFile(temporaryPath)
		if err != nil {
			t.Fatal(err)
		}
		var pending fileSearchIndex
		if err := json.Unmarshal(data, &pending); err != nil {
			t.Fatalf("pending index is invalid: %v", err)
		}
		return originalReplace(temporaryPath, destinationPath)
	}
	t.Cleanup(func() { saveFileAtomicReplace = originalReplace })

	files := fixture.app.GetFileList()
	if len(files) != 2 {
		t.Fatalf("GetFileList returned %d files, want 2: %#v", len(files), files)
	}
	if files[0].Path != "content/alpha.md" || files[0].Title != "Alpha title" || files[0].Size == 0 {
		t.Fatalf("unexpected Markdown metadata: %#v", files[0])
	}
	if files[1].Path != "content/reference.pdf" || files[1].Title != "reference" {
		t.Fatalf("unexpected PDF metadata: %#v", files[1])
	}
	payload, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "body-only-secret") || strings.Contains(string(payload), "searchText") {
		t.Fatalf("GetFileList leaked indexed content over IPC: %s", payload)
	}
	if fixture.reads[filepath.Clean(markdownPath)] != 1 {
		t.Fatalf("initial Markdown reads = %d, want 1", fixture.reads[filepath.Clean(markdownPath)])
	}

	for iteration := 0; iteration < 25; iteration++ {
		if got := fixture.app.GetFileList(); len(got) != 2 {
			t.Fatalf("unchanged GetFileList iteration %d returned %d files", iteration, len(got))
		}
		search := fixture.app.SearchFiles("body-only-secret", 1, 10)
		if search.Total != 1 || len(search.Items) != 1 || search.Items[0].Path != "content/alpha.md" {
			t.Fatalf("content search iteration %d = %#v", iteration, search)
		}
	}
	if got := fixture.readCount("content/alpha.md"); got != 1 {
		t.Fatalf("unchanged Markdown was read %d times, want 1", got)
	}
	if replaceCalls != 1 {
		t.Fatalf("unchanged index was atomically replaced %d times, want 1", replaceCalls)
	}

	searchPayload, err := json.Marshal(fixture.app.SearchFiles("body-only-secret", 1, 10))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(searchPayload), "body-only-secret") || strings.Contains(string(searchPayload), "searchText") {
		t.Fatalf("SearchFiles leaked indexed content over IPC: %s", searchPayload)
	}
}

func TestFileSearchUpdatesOnlyChangedMarkdownWithSameMtimeAndSize(t *testing.T) {
	fixture := newFileSearchFixture(t)
	modified := time.Unix(1_710_000_000, 456)
	alphaPath := fixture.writeFile(t, "content/alpha.md", "# Alpha\nold-token\n", "alpha-v1", modified)
	fixture.writeFile(t, "content/beta.md", "# Beta\nstable-text\n", "beta-v1", modified)

	fixture.app.GetFileList()
	before := fixture.loadIndex(t).Entries["content/alpha.md"].ContentHash
	fixture.writeFile(t, "content/alpha.md", "# Alpha\nnew-token\n", "alpha-v2", modified)
	if info, err := os.Stat(alphaPath); err != nil {
		t.Fatal(err)
	} else if info.ModTime().UnixNano() != modified.UnixNano() {
		t.Fatalf("mtime changed: got %v, want %v", info.ModTime(), modified)
	}

	result := fixture.app.SearchFiles("new-token", 1, 10)
	if result.Total != 1 || result.Items[0].Path != "content/alpha.md" {
		t.Fatalf("same-mtime content update was not indexed: %#v", result)
	}
	if old := fixture.app.SearchFiles("old-token", 1, 10); old.Total != 0 {
		t.Fatalf("old content remained searchable: %#v", old)
	}
	if got := fixture.readCount("content/alpha.md"); got != 2 {
		t.Fatalf("changed alpha reads = %d, want 2", got)
	}
	if got := fixture.readCount("content/beta.md"); got != 1 {
		t.Fatalf("unchanged beta reads = %d, want 1", got)
	}
	after := fixture.loadIndex(t).Entries["content/alpha.md"].ContentHash
	if before == after {
		t.Fatal("content hash did not change after same-size rewrite")
	}
}

func TestFileSearchRemovesDeletedPathsAndReindexesRename(t *testing.T) {
	fixture := newFileSearchFixture(t)
	modified := time.Unix(1_720_000_000, 789)
	oldRelativePath := "content/original.md"
	oldPath := fixture.writeFile(t, oldRelativePath, "# Original\nrename-content\n", "rename-v1", modified)
	fixture.app.GetFileList()

	newRelativePath := "content/日本語/改名.md"
	newPath := filepath.Join(fixture.root, filepath.FromSlash(newRelativePath))
	if err := os.MkdirAll(filepath.Dir(newPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		t.Fatal(err)
	}
	delete(fixture.tokens, filepath.Clean(oldPath))
	fixture.tokens[filepath.Clean(newPath)] = "rename-v2"

	files := fixture.app.GetFileList()
	if len(files) != 1 || files[0].Path != newRelativePath {
		t.Fatalf("renamed metadata = %#v", files)
	}
	if result := fixture.app.SearchFiles("rename-content", 1, 10); result.Total != 1 || result.Items[0].Path != newRelativePath {
		t.Fatalf("renamed content search = %#v", result)
	}
	index := fixture.loadIndex(t)
	if _, exists := index.Entries[oldRelativePath]; exists {
		t.Fatalf("old renamed path remains in index: %#v", index.Entries)
	}
	if fixture.readCount(newRelativePath) != 1 {
		t.Fatalf("renamed Markdown reads = %d, want 1", fixture.readCount(newRelativePath))
	}

	if err := os.Remove(newPath); err != nil {
		t.Fatal(err)
	}
	if files := fixture.app.GetFileList(); len(files) != 0 {
		t.Fatalf("deleted file remains in metadata: %#v", files)
	}
	if entries := fixture.loadIndex(t).Entries; len(entries) != 0 {
		t.Fatalf("deleted file remains in index: %#v", entries)
	}
}

func TestFileSearchRebuildsCorruptIndex(t *testing.T) {
	fixture := newFileSearchFixture(t)
	fixture.writeFile(t, "content/rebuild.md", "# Rebuild\nrecoverable-body\n", "rebuild-v1", time.Now())
	fixture.app.GetFileList()
	indexPath := filepath.Join(fixture.root, ".mdsys", fileSearchIndexName)
	if err := os.WriteFile(indexPath, []byte(`{"version":`), 0o644); err != nil {
		t.Fatal(err)
	}

	result := fixture.app.SearchFiles("recoverable-body", 1, 10)
	if result.Total != 1 || result.Items[0].Path != "content/rebuild.md" {
		t.Fatalf("rebuilt search result = %#v", result)
	}
	if got := fixture.readCount("content/rebuild.md"); got != 2 {
		t.Fatalf("corrupt-index rebuild reads = %d, want 2", got)
	}
	index := fixture.loadIndex(t)
	if index.Version != fileSearchIndexVersion || len(index.Entries) != 1 {
		t.Fatalf("rebuilt index = %#v", index)
	}
	entry := index.Entries["content/rebuild.md"]
	entry.NormalizedText = "semantically-corrupt"
	index.Entries["content/rebuild.md"] = entry
	data, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if result := fixture.app.SearchFiles("recoverable-body", 1, 10); result.Total != 1 {
		t.Fatalf("checksum-corrupt index was not rebuilt: %#v", result)
	}
	if got := fixture.readCount("content/rebuild.md"); got != 3 {
		t.Fatalf("checksum-corrupt rebuild reads = %d, want 3", got)
	}
}

func TestFileSearchAtomicFailurePreservesPriorIndexAndRetries(t *testing.T) {
	fixture := newFileSearchFixture(t)
	modified := time.Unix(1_725_000_000, 0)
	fixture.writeFile(t, "content/atomic.md", "# Atomic\nold-indexed-body\n", "atomic-v1", modified)
	fixture.app.GetFileList()
	indexPath := filepath.Join(fixture.root, ".mdsys", fileSearchIndexName)
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}

	fixture.writeFile(t, "content/atomic.md", "# Atomic\nnew-indexed-body\n", "atomic-v2", modified)
	originalReplace := saveFileAtomicReplace
	saveFileAtomicReplace = func(_, _ string) error {
		return errors.New("injected search index replacement failure")
	}
	t.Cleanup(func() { saveFileAtomicReplace = originalReplace })
	if files := fixture.app.GetFileList(); len(files) != 0 {
		t.Fatalf("failed index persistence returned partial metadata: %#v", files)
	}
	afterFailure, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterFailure) != string(before) {
		t.Fatal("atomic replacement failure changed the prior index")
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(filepath.Dir(indexPath), "."+filepath.Base(indexPath)+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("failed index replacement left temporary files: %v", temporaryFiles)
	}

	saveFileAtomicReplace = originalReplace
	result := fixture.app.SearchFiles("new-indexed-body", 1, 10)
	if result.Total != 1 || result.Items[0].Path != "content/atomic.md" {
		t.Fatalf("retry did not persist the changed index: %#v", result)
	}
}

func TestFileSearchRejectsUnconfinedPersistedAndSymlinkPaths(t *testing.T) {
	fixture := newFileSearchFixture(t)
	fixture.writeFile(t, "content/safe.md", "# Safe\nsafe-content\n", "safe-v1", time.Now())
	outsidePath := filepath.Join(fixture.root, "outside.md")
	if err := os.WriteFile(outsidePath, []byte("TOP-SECRET-OUTSIDE"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(fixture.root, "content", "linked.md")
	symlinkCreated := os.Symlink(outsidePath, linkPath) == nil

	malicious := fileSearchIndex{
		Version: fileSearchIndexVersion,
		Entries: map[string]fileSearchIndexEntry{
			"content/../../outside.md": {
				Path:           "content/../../outside.md",
				Title:          "Outside",
				NormalizedText: "top-secret-outside",
				Markdown:       true,
			},
		},
	}
	malicious.Checksum = fileSearchEntriesChecksum(malicious.Entries)
	data, err := json.Marshal(malicious)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.root, ".mdsys", fileSearchIndexName), data, 0o644); err != nil {
		t.Fatal(err)
	}

	if result := fixture.app.SearchFiles("TOP-SECRET-OUTSIDE", 1, 10); result.Total != 0 {
		t.Fatalf("unconfined content was searchable: %#v", result)
	}
	files := fixture.app.GetFileList()
	if len(files) != 1 || files[0].Path != "content/safe.md" {
		t.Fatalf("confined file list = %#v", files)
	}
	if symlinkCreated {
		for _, file := range files {
			if file.Path == "content/linked.md" {
				t.Fatalf("symlink escaped confinement: %#v", files)
			}
		}
	}
	for path := range fixture.loadIndex(t).Entries {
		if strings.Contains(path, "..") || path == "content/linked.md" {
			t.Fatalf("unsafe path persisted in rebuilt index: %q", path)
		}
	}
}

func TestFileSearchUnicodeAndPaginationLimits(t *testing.T) {
	fixture := newFileSearchFixture(t)
	modified := time.Unix(1_730_000_000, 0)
	for index := 0; index < 125; index++ {
		path := fmt.Sprintf("content/notes/note-%03d.md", index)
		body := fmt.Sprintf("# Note %03d\n共通検索語 item-%03d\n", index, index)
		fixture.writeFile(t, path, body, fmt.Sprintf("token-%03d", index), modified)
	}
	fixture.writeFile(t, "content/日本語/猫.md", "---\ntitle: \"東京ノート\"\n---\n固有本文検索語\n", "unicode-v1", modified)

	first := fixture.app.SearchFiles("共通検索語", -5, 1_000)
	if first.Page != 1 || first.Limit != fileSearchMaxLimit || first.Total != 125 || len(first.Items) != 100 || !first.HasMore {
		t.Fatalf("first bounded page = %#v", first)
	}
	second := fixture.app.SearchFiles("共通検索語", 2, 100)
	if second.Total != 125 || len(second.Items) != 25 || second.HasMore {
		t.Fatalf("second page = %#v", second)
	}
	defaultLimit := fixture.app.SearchFiles("共通検索語", 1, 0)
	if defaultLimit.Limit != fileSearchDefaultLimit || len(defaultLimit.Items) != fileSearchDefaultLimit {
		t.Fatalf("default page = %#v", defaultLimit)
	}
	beyond := fixture.app.SearchFiles("共通検索語", 1_000_000, 100)
	if beyond.Total != 125 || len(beyond.Items) != 0 || beyond.HasMore {
		t.Fatalf("out-of-range page = %#v", beyond)
	}
	unicodeTitle := fixture.app.SearchFiles("東京", 1, 10)
	if unicodeTitle.Total != 1 || unicodeTitle.Items[0].Path != "content/日本語/猫.md" {
		t.Fatalf("Unicode title search = %#v", unicodeTitle)
	}
	unicodeBody := fixture.app.SearchFiles("固有本文検索語", 1, 10)
	if unicodeBody.Total != 1 || unicodeBody.Items[0].Path != "content/日本語/猫.md" {
		t.Fatalf("Unicode body search = %#v", unicodeBody)
	}
}

func TestFileSearchConcurrentCalls(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 10; index++ {
		path := filepath.Join(root, "content", fmt.Sprintf("note-%02d.md", index))
		if err := os.WriteFile(path, []byte(fmt.Sprintf("# Note\nconcurrent-%02d\n", index)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{dataDir: root}
	var markdownReads atomic.Int64
	app.fileSearchReadFile = func(path string) ([]byte, error) {
		markdownReads.Add(1)
		return os.ReadFile(path)
	}
	if files := app.GetFileList(); len(files) != 10 {
		t.Fatalf("initial files = %d, want 10", len(files))
	}
	if got := markdownReads.Load(); got != 10 {
		t.Fatalf("initial platform-index reads = %d, want 10", got)
	}

	var waitGroup sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			for iteration := 0; iteration < 10; iteration++ {
				if files := app.GetFileList(); len(files) != 10 {
					t.Errorf("concurrent files = %d, want 10", len(files))
				}
				if result := app.SearchFiles("concurrent", 1, 20); result.Total != 10 {
					t.Errorf("concurrent search total = %d, want 10", result.Total)
				}
			}
		}()
	}
	waitGroup.Wait()
	if got := markdownReads.Load(); got != 10 {
		t.Fatalf("unchanged platform-index reads = %d, want 10", got)
	}
}
