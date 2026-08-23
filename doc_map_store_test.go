package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	boardpkg "karte/internal/board"
	fm "karte/internal/frontmatter"
	gitvcs "karte/internal/git"
)

func TestDocumentMapStoreConcurrentUpdatesPreserveEveryMapping(t *testing.T) {
	app, _ := newDocumentMapTestApp(t)
	const updateCount = 64

	start := make(chan struct{})
	errorsByUpdate := make(chan error, updateCount)
	var wait sync.WaitGroup
	for index := 0; index < updateCount; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			docID := fmt.Sprintf("doc-%02d", index)
			path := fmt.Sprintf("content/並行/note-%02d.md", index)
			_, err := app.updateDocumentMapping(docID, path)
			errorsByUpdate <- err
		}(index)
	}
	close(start)
	wait.Wait()
	close(errorsByUpdate)
	for err := range errorsByUpdate {
		if err != nil {
			t.Fatalf("concurrent document map update: %v", err)
		}
	}

	mappings, _ := readDocumentMapTest(t, app.dataDir)
	if len(mappings) != updateCount {
		t.Fatalf("stored mappings = %d, want %d", len(mappings), updateCount)
	}
	for index := 0; index < updateCount; index++ {
		docID := fmt.Sprintf("doc-%02d", index)
		want := fmt.Sprintf("content/並行/note-%02d.md", index)
		if got := mappings[docID]; got != want {
			t.Errorf("mapping %s = %q, want %q", docID, got, want)
		}
	}
}

func TestDocumentMapStoreConcurrentGraphRefreshReadsValidJSON(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	documentPath := filepath.Join(dataDir, "content", "graph.md")
	if err := os.WriteFile(documentPath, []byte(documentMapTestMarkdown("doc-graph", "Graph", "body\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.updateDocumentMapping("doc-graph", "content/graph.md"); err != nil {
		t.Fatal(err)
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatal(err)
	}

	invalidJSON := errors.New("graph refresh observed invalid document map JSON")
	app.graphReadFile = func(path string) ([]byte, error) {
		data, err := os.ReadFile(path)
		if err == nil && filepath.Base(path) == documentMapFileName && !json.Valid(data) {
			return nil, invalidJSON
		}
		return data, err
	}
	const iterations = 32
	start := make(chan struct{})
	errorsByOperation := make(chan error, iterations*2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		for index := 0; index < iterations; index++ {
			_, err := app.updateDocumentMapping(
				fmt.Sprintf("doc-update-%02d", index),
				fmt.Sprintf("content/更新-%02d.md", index),
			)
			errorsByOperation <- err
		}
	}()
	go func() {
		defer wait.Done()
		<-start
		for index := 0; index < iterations; index++ {
			errorsByOperation <- app.RefreshGraphData()
		}
	}()
	close(start)
	wait.Wait()
	close(errorsByOperation)
	for err := range errorsByOperation {
		if err != nil {
			t.Fatalf("concurrent graph/store operation: %v", err)
		}
	}
	mappings, _ := readDocumentMapTest(t, dataDir)
	if len(mappings) != iterations+1 {
		t.Fatalf("mappings after concurrent graph refresh = %d, want %d", len(mappings), iterations+1)
	}
}

func TestDocumentMapStoreRejectsCorruptInputWithoutOverwrite(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	documentMapPath := filepath.Join(dataDir, documentMapDirectoryName, documentMapFileName)
	corrupt := []byte(`{"existing":"content/existing.md"`)
	if err := os.WriteFile(documentMapPath, corrupt, 0o640); err != nil {
		t.Fatal(err)
	}

	_, err := app.updateDocumentMapping("doc-new", "content/new.md")
	if !errors.Is(err, errCorruptDocumentMap) {
		t.Fatalf("corrupt document map update error = %v", err)
	}
	after, err := os.ReadFile(documentMapPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(corrupt) {
		t.Fatalf("corrupt input was overwritten\nwant: %q\n got: %q", corrupt, after)
	}
}

func TestDocumentMapStoreRejectsSymlinksAndUnconfinedPaths(t *testing.T) {
	t.Run("map symlink", func(t *testing.T) {
		app, dataDir := newDocumentMapTestApp(t)
		outsidePath := filepath.Join(t.TempDir(), "outside.json")
		outside := []byte("{\"outside\":\"content/outside.md\"}\n")
		if err := os.WriteFile(outsidePath, outside, 0o644); err != nil {
			t.Fatal(err)
		}
		documentMapPath := filepath.Join(dataDir, documentMapDirectoryName, documentMapFileName)
		if err := os.Symlink(outsidePath, documentMapPath); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := app.updateDocumentMapping("doc-new", "content/new.md"); err == nil || !strings.Contains(err.Error(), "not a confined regular file") {
			t.Fatalf("symlink document map error = %v", err)
		}
		after, err := os.ReadFile(outsidePath)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(outside) {
			t.Fatalf("outside symlink target changed: %q", after)
		}
	})

	t.Run("directory symlink", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dataDir, "content"), 0o755); err != nil {
			t.Fatal(err)
		}
		outsideDirectory := t.TempDir()
		outsidePath := filepath.Join(outsideDirectory, documentMapFileName)
		outside := []byte("{\"outside\":\"content/outside.md\"}\n")
		if err := os.WriteFile(outsidePath, outside, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outsideDirectory, filepath.Join(dataDir, documentMapDirectoryName)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		app := &App{dataDir: dataDir, root: dataDir}
		if _, err := app.updateDocumentMapping("doc-new", "content/new.md"); err == nil || !strings.Contains(err.Error(), "not a confined directory") {
			t.Fatalf("symlink document map directory error = %v", err)
		}
		after, err := os.ReadFile(outsidePath)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(outside) {
			t.Fatalf("outside directory content changed: %q", after)
		}
	})

	t.Run("path escape", func(t *testing.T) {
		app, dataDir := newDocumentMapTestApp(t)
		outsidePath := filepath.Join(filepath.Dir(dataDir), "escape.md")
		if _, err := app.updateDocumentMapping("doc-escape", "content/../../escape.md"); err == nil {
			t.Fatal("document map accepted a path escape")
		}
		if _, err := os.Stat(outsidePath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("path escape touched outside file: %v", err)
		}
	})
}

func TestDocumentMapStoreAtomicFailureKeepsOldBytes(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	if _, err := app.updateDocumentMapping("doc-existing", "content/existing.md"); err != nil {
		t.Fatal(err)
	}
	documentMapPath := filepath.Join(dataDir, documentMapDirectoryName, documentMapFileName)
	before, err := os.ReadFile(documentMapPath)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected document map replacement failure")
	replaceCalls := 0
	app.documentMapStore.operations.replace = func(tempPath, destinationPath string) error {
		replaceCalls++
		resolvedDestination, destinationErr := filepath.EvalSymlinks(destinationPath)
		resolvedExpected, expectedErr := filepath.EvalSymlinks(documentMapPath)
		if destinationErr != nil || expectedErr != nil || resolvedDestination != resolvedExpected {
			t.Fatalf("replace destination = %q, want %q", destinationPath, documentMapPath)
		}
		resolvedTempDirectory, tempDirectoryErr := filepath.EvalSymlinks(filepath.Dir(tempPath))
		resolvedDestinationDirectory, destinationDirectoryErr := filepath.EvalSymlinks(filepath.Dir(destinationPath))
		if tempDirectoryErr != nil || destinationDirectoryErr != nil || resolvedTempDirectory != resolvedDestinationDirectory {
			t.Fatalf("temporary file is not in destination directory: %q", tempPath)
		}
		pending, err := os.ReadFile(tempPath)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeDocumentMap(pending)
		if err != nil || decoded["doc-existing"] == "" || decoded["doc-new"] != "content/new.md" {
			t.Fatalf("pending document map = %#v, %v", decoded, err)
		}
		return injected
	}
	if _, err := app.updateDocumentMapping("doc-new", "content/new.md"); !errors.Is(err, injected) {
		t.Fatalf("atomic failure error = %v, want injected", err)
	}
	if replaceCalls != 1 {
		t.Fatalf("replace calls = %d, want 1", replaceCalls)
	}
	after, err := os.ReadFile(documentMapPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("atomic failure changed old bytes\nwant: %q\n got: %q", before, after)
	}
	temporaryFiles, err := filepath.Glob(filepath.Join(filepath.Dir(documentMapPath), "."+documentMapFileName+".tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temporaryFiles) != 0 {
		t.Fatalf("atomic failure left temporary files: %v", temporaryFiles)
	}
}

func TestDocumentMapStoreReadersSeeOnlyOldOrNewJSON(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	if _, err := app.updateDocumentMapping("doc-old", "content/old.md"); err != nil {
		t.Fatal(err)
	}
	documentMapPath := filepath.Join(dataDir, documentMapDirectoryName, documentMapFileName)
	oldBytes, err := os.ReadFile(documentMapPath)
	if err != nil {
		t.Fatal(err)
	}
	newBytes := encodeDocumentMapTest(t, map[string]string{
		"doc-new": "content/新規.md",
		"doc-old": "content/old.md",
	})

	replaceReady := make(chan struct{})
	allowReplace := make(chan struct{})
	app.documentMapStore.operations.replace = func(tempPath, destinationPath string) error {
		close(replaceReady)
		<-allowReplace
		return atomicReplaceFile(tempPath, destinationPath)
	}

	stopReader := make(chan struct{})
	readerDone := make(chan struct{})
	readerFailure := make(chan error, 1)
	oldSeen := make(chan struct{})
	newSeen := make(chan struct{})
	var oldOnce sync.Once
	var newOnce sync.Once
	go func() {
		defer close(readerDone)
		for {
			data, err := os.ReadFile(documentMapPath)
			if err != nil {
				readerFailure <- err
				return
			}
			if !json.Valid(data) {
				readerFailure <- fmt.Errorf("reader observed invalid JSON %q", data)
				return
			}
			switch string(data) {
			case string(oldBytes):
				oldOnce.Do(func() { close(oldSeen) })
			case string(newBytes):
				newOnce.Do(func() { close(newSeen) })
			default:
				readerFailure <- fmt.Errorf("reader observed neither old nor new bytes: %q", data)
				return
			}
			select {
			case <-stopReader:
				return
			default:
			}
		}
	}()

	waitDocumentMapSignal(t, oldSeen, "reader to observe old JSON")
	updateDone := make(chan error, 1)
	go func() {
		_, err := app.updateDocumentMapping("doc-new", "content/新規.md")
		updateDone <- err
	}()
	waitDocumentMapSignal(t, replaceReady, "atomic replacement to become ready")
	close(allowReplace)
	if err := <-updateDone; err != nil {
		t.Fatalf("document map update: %v", err)
	}
	waitDocumentMapSignal(t, newSeen, "reader to observe new JSON")
	close(stopReader)
	<-readerDone
	select {
	case err := <-readerFailure:
		t.Fatal(err)
	default:
	}
}

func TestRenameFileRollsBackWhenDocumentMapUpdateFails(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	oldRelative := "content/old.md"
	newRelative := "content/new.md"
	oldPath := filepath.Join(dataDir, "content", "old.md")
	newPath := filepath.Join(dataDir, "content", "new.md")
	original := documentMapTestMarkdown("doc-rename", "Old", "body\n")
	if err := os.WriteFile(oldPath, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := app.updateDocumentMappings(map[string]string{
		"doc-rename": "content/old.md",
		"doc-stable": "content/stable.md",
	}); err != nil {
		t.Fatal(err)
	}
	documentMapPath := filepath.Join(dataDir, documentMapDirectoryName, documentMapFileName)
	beforeMap, err := os.ReadFile(documentMapPath)
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected rename map failure")
	app.documentMapStore.operations.replace = func(string, string) error { return injected }
	err = app.RenameFile(oldRelative, newRelative)
	if !errors.Is(err, injected) || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("RenameFile error = %v", err)
	}
	after, err := os.ReadFile(oldPath)
	if err != nil || string(after) != original {
		t.Fatalf("old file after rollback = %q, %v", after, err)
	}
	if _, err := os.Stat(newPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rename target remained after rollback: %v", err)
	}
	afterMap, err := os.ReadFile(documentMapPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterMap) != string(beforeMap) {
		t.Fatal("rename map failure changed document map bytes")
	}
}

func TestRenameFileReturnsMapAndRollbackErrors(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	oldPath := filepath.Join(dataDir, "content", "old.md")
	newPath := filepath.Join(dataDir, "content", "new.md")
	if err := os.WriteFile(oldPath, []byte(documentMapTestMarkdown("doc-rename", "Old", "body\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.updateDocumentMapping("doc-rename", "content/old.md"); err != nil {
		t.Fatal(err)
	}
	mapFailure := errors.New("map replacement failed")
	rollbackFailure := errors.New("rename rollback failed")
	app.documentMapStore.operations.replace = func(string, string) error { return mapFailure }
	renameCalls := 0
	app.documentRenameFile = func(sourcePath, destinationPath string) error {
		renameCalls++
		if renameCalls == 1 {
			return os.Rename(sourcePath, destinationPath)
		}
		return rollbackFailure
	}

	err := app.RenameFile("content/old.md", "content/new.md")
	if !errors.Is(err, mapFailure) || !errors.Is(err, rollbackFailure) {
		t.Fatalf("RenameFile error did not preserve both failures: %v", err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old path after failed rollback = %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new path after failed rollback = %v", err)
	}
}

func TestSaveFileReturnsMappingFailureAndRollsBackContent(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	documentPath := filepath.Join(dataDir, "content", "save.md")
	before := documentMapTestMarkdown("doc-save", "Before", "old body\n")
	after := documentMapTestMarkdown("doc-save", "After", "new body\n")
	if err := os.WriteFile(documentPath, []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := app.updateDocumentMapping("doc-stable", "content/stable.md"); err != nil {
		t.Fatal(err)
	}
	documentMapPath := filepath.Join(dataDir, documentMapDirectoryName, documentMapFileName)
	beforeMap, err := os.ReadFile(documentMapPath)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected save map failure")
	app.documentMapStore.operations.replace = func(string, string) error { return injected }

	err = app.SaveFile("content/save.md", after)
	if !errors.Is(err, injected) || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("SaveFile error = %v", err)
	}
	stored, err := os.ReadFile(documentPath)
	if err != nil || string(stored) != before {
		t.Fatalf("SaveFile rollback content = %q, %v", stored, err)
	}
	afterMap, err := os.ReadFile(documentMapPath)
	if err != nil || string(afterMap) != string(beforeMap) {
		t.Fatalf("SaveFile map bytes changed: %q, %v", afterMap, err)
	}
}

func TestSaveFileDocIDGenerationFailureLeavesFileAndMapUnchanged(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	documentPath := filepath.Join(dataDir, "content", "save.md")
	before := "# Before\n\nold body\n"
	if err := os.WriteFile(documentPath, []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := app.updateDocumentMapping("doc-stable", "content/stable.md"); err != nil {
		t.Fatal(err)
	}
	_, beforeMap := readDocumentMapTest(t, dataDir)
	sequencePath := filepath.Join(dataDir, documentMapDirectoryName, "doc_seq.json")
	if err := os.Mkdir(sequencePath, 0o755); err != nil {
		t.Fatal(err)
	}

	err := app.SaveFile("content/save.md", "# After\n\nnew body\n")
	if err == nil || !strings.Contains(err.Error(), "failed to ensure doc_id") {
		t.Fatalf("SaveFile doc_id generation error = %v", err)
	}
	after, err := os.ReadFile(documentPath)
	if err != nil || string(after) != before {
		t.Fatalf("doc_id generation failure changed target: %q, %v", after, err)
	}
	_, afterMap := readDocumentMapTest(t, dataDir)
	if string(afterMap) != string(beforeMap) {
		t.Fatal("doc_id generation failure changed document map")
	}
}

func TestResolveConflictAssignsAndPersistsMissingDocumentID(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	relativePath := "content/conflict.md"
	absolutePath := filepath.Join(dataDir, "content", "conflict.md")
	base := "# Base\n\nhead content\n"
	if err := os.WriteFile(absolutePath, []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	vcs, err := gitvcs.NewVCS(nil, dataDir, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := vcs.CommitFile(relativePath, "add conflict fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte("# Local\n\nworking content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	app.vcs = vcs

	if err := app.ResolveConflict(relativePath, "remote"); err != nil {
		t.Fatalf("ResolveConflict: %v", err)
	}
	resolved, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	frontMatter, _ := fm.ParseFrontMatter(string(resolved))
	if frontMatter == nil || frontMatter.DocID == "" {
		t.Fatalf("resolved content has no doc_id: %s", resolved)
	}
	mappings, _ := readDocumentMapTest(t, dataDir)
	if got := mappings[frontMatter.DocID]; got != relativePath {
		t.Fatalf("resolved persistent mapping = %q, want %q", got, relativePath)
	}
}

func TestResolveConflictMappingFailureRollsBackResolvedContent(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	relativePath := "content/conflict.md"
	absolutePath := filepath.Join(dataDir, "content", "conflict.md")
	base := "# Base\n\nhead content\n"
	local := "# Local\n\nworking content\n"
	if err := os.WriteFile(absolutePath, []byte(base), 0o640); err != nil {
		t.Fatal(err)
	}
	vcs, err := gitvcs.NewVCS(nil, dataDir, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := vcs.CommitFile(relativePath, "add conflict fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte(local), 0o640); err != nil {
		t.Fatal(err)
	}
	app.vcs = vcs
	if _, err := app.updateDocumentMapping("doc-stable", "content/stable.md"); err != nil {
		t.Fatal(err)
	}
	_, beforeMap := readDocumentMapTest(t, dataDir)
	injected := errors.New("injected resolved map failure")
	app.documentMapStore.operations.replace = func(string, string) error { return injected }

	err = app.ResolveConflict(relativePath, "remote")
	if !errors.Is(err, injected) || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("ResolveConflict mapping error = %v", err)
	}
	after, err := os.ReadFile(absolutePath)
	if err != nil || string(after) != local {
		t.Fatalf("ResolveConflict rollback content = %q, %v", after, err)
	}
	_, afterMap := readDocumentMapTest(t, dataDir)
	if string(afterMap) != string(beforeMap) {
		t.Fatal("ResolveConflict mapping failure changed document map")
	}
}

func TestEnsureDocumentIDAtMutationReturnsMappingFailureAndRollsBack(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	documentPath := filepath.Join(dataDir, "content", "clip.md")
	before := "# Clip\n\nbody\n"
	if err := os.WriteFile(documentPath, []byte(before), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := app.updateDocumentMapping("doc-stable", "content/stable.md"); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected clip map failure")
	app.documentMapStore.operations.replace = func(string, string) error { return injected }

	if _, err := app.ensureDocumentIDAtMutation("content/clip.md"); !errors.Is(err, injected) {
		t.Fatalf("ensureDocumentIDAtMutation error = %v", err)
	}
	after, err := os.ReadFile(documentPath)
	if err != nil || string(after) != before {
		t.Fatalf("doc_id assignment was not rolled back: %q, %v", after, err)
	}
}

func TestConcurrentSaveRenameAndGraphReadsPreserveDocumentMappings(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	renamePath := filepath.Join(dataDir, "content", "rename-old.md")
	savePath := filepath.Join(dataDir, "content", "save.md")
	if err := os.WriteFile(renamePath, []byte(documentMapTestMarkdown("doc-rename", "Rename", "body\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(savePath, []byte(documentMapTestMarkdown("doc-save", "Save", "before\n")), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.updateDocumentMappings(map[string]string{
		"doc-rename": "content/rename-old.md",
		"doc-stable": "content/stable.md",
	}); err != nil {
		t.Fatal(err)
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatal(err)
	}

	stopReader := make(chan struct{})
	readerFailure := make(chan error, 1)
	var readerWait sync.WaitGroup
	readerWait.Add(1)
	go func() {
		defer readerWait.Done()
		documentMapPath := filepath.Join(dataDir, documentMapDirectoryName, documentMapFileName)
		for {
			data, err := os.ReadFile(documentMapPath)
			if err != nil || !json.Valid(data) {
				select {
				case readerFailure <- fmt.Errorf("document map reader: bytes=%q err=%v", data, err):
				default:
				}
				return
			}
			if _, err := app.GetGraphData(); err != nil {
				select {
				case readerFailure <- err:
				default:
				}
				return
			}
			select {
			case <-stopReader:
				return
			default:
			}
		}
	}()

	start := make(chan struct{})
	operationErrors := make(chan error, 2)
	var operations sync.WaitGroup
	operations.Add(2)
	go func() {
		defer operations.Done()
		<-start
		operationErrors <- app.RenameFile("content/rename-old.md", "content/rename-new.md")
	}()
	go func() {
		defer operations.Done()
		<-start
		operationErrors <- app.SaveFile("content/save.md", documentMapTestMarkdown("doc-save", "Save", "after!\n"))
	}()
	close(start)
	operations.Wait()
	close(operationErrors)
	close(stopReader)
	readerWait.Wait()
	for err := range operationErrors {
		if err != nil {
			t.Fatalf("concurrent mutation: %v", err)
		}
	}
	select {
	case err := <-readerFailure:
		t.Fatal(err)
	default:
	}

	mappings, _ := readDocumentMapTest(t, dataDir)
	want := map[string]string{
		"doc-rename": "content/rename-new.md",
		"doc-save":   "content/save.md",
		"doc-stable": "content/stable.md",
	}
	for docID, path := range want {
		if mappings[docID] != path {
			t.Errorf("final mapping %s = %q, want %q", docID, mappings[docID], path)
		}
	}
}

func TestCreateAndBoardMutationsPersistDocumentMappings(t *testing.T) {
	app, dataDir := newDocumentMapTestApp(t)
	created, err := app.CreateNewFile("created")
	if err != nil || !created {
		t.Fatalf("CreateNewFile = %v, %v", created, err)
	}
	createdBytes, err := os.ReadFile(filepath.Join(dataDir, "content", "created.md"))
	if err != nil {
		t.Fatal(err)
	}
	createdFrontMatter, _ := fm.ParseFrontMatter(string(createdBytes))
	if createdFrontMatter == nil || createdFrontMatter.DocID == "" {
		t.Fatalf("created document has no doc_id: %s", createdBytes)
	}

	board, err := app.SaveBoard("content/board.board.md", boardpkg.Document{
		Title:   "Board",
		Type:    boardpkg.BoardType,
		Version: 1,
		Cards:   []boardpkg.Card{},
		Edges:   []boardpkg.Edge{},
		Layout: boardpkg.Layout{
			Cards:    map[string]boardpkg.CardLayout{},
			Viewport: boardpkg.Viewport{Zoom: 1},
		},
	})
	if err != nil {
		t.Fatalf("SaveBoard: %v", err)
	}
	if board.DocID == "" {
		t.Fatal("saved board has no doc_id")
	}

	mappings, _ := readDocumentMapTest(t, dataDir)
	if got := mappings[createdFrontMatter.DocID]; got != "content/created.md" {
		t.Fatalf("created mapping = %q", got)
	}
	if got := mappings[board.DocID]; got != "content/board.board.md" {
		t.Fatalf("board mapping = %q", got)
	}
}

func newDocumentMapTestApp(t *testing.T) (*App, string) {
	t.Helper()
	dataDir := t.TempDir()
	for _, relativePath := range []string{"content", documentMapDirectoryName, filepath.Join("data", "image")} {
		if err := os.MkdirAll(filepath.Join(dataDir, relativePath), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &App{dataDir: dataDir, root: dataDir}, dataDir
}

func readDocumentMapTest(t *testing.T, dataDir string) (map[string]string, []byte) {
	t.Helper()
	path := filepath.Join(dataDir, documentMapDirectoryName, documentMapFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mappings, err := decodeDocumentMap(data)
	if err != nil {
		t.Fatal(err)
	}
	return mappings, data
}

func encodeDocumentMapTest(t *testing.T, mappings map[string]string) []byte {
	t.Helper()
	data, err := json.MarshalIndent(mappings, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(data, '\n')
}

func documentMapTestMarkdown(docID, title, body string) string {
	return fmt.Sprintf("---\ndoc_id: %s\ntitle: %s\n---\n%s", docID, title, body)
}

func waitDocumentMapSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}
