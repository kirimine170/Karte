package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	boardpkg "karte/internal/board"
	fm "karte/internal/frontmatter"
)

func TestGraphHotPathsAreIOFreeAndImmutable(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	sourceContent := graphTestMarkdown("doc-source", "Source", "project", "[Target](target.md)\n")
	targetContent := graphTestMarkdown("doc-target", "Target", "project", "Initial body\n")
	writeGraphTestFile(t, filepath.Join(dataDir, "content", "source.md"), sourceContent)
	writeGraphTestFile(t, filepath.Join(dataDir, "content", "target.md"), targetContent)
	imagePath := filepath.Join(dataDir, "content", "clips", "assets", "例", "図.png")
	metadataPath := strings.TrimSuffix(imagePath, ".png") + ".yaml"
	writeGraphTestFile(t, imagePath, "png")
	writeGraphTestFile(t, metadataPath, "tags: image\n")
	docMapPath := filepath.Join(dataDir, ".mdsys", "doc_map.json")
	linksPath := filepath.Join(dataDir, ".mdsys", "links.json")
	docMapBytes := `{"doc-source":"content/source.md","doc-target":"content/target.md"}`
	linksBytes := `[]`
	writeGraphTestFile(t, docMapPath, docMapBytes)
	writeGraphTestFile(t, linksPath, linksBytes)

	var graphReads atomic.Int64
	var graphWrites atomic.Int64
	app.graphReadFile = func(path string) ([]byte, error) {
		graphReads.Add(1)
		return os.ReadFile(path)
	}
	app.graphPersistFile = func(path string, data []byte, perm fs.FileMode) error {
		graphWrites.Add(1)
		return atomicWriteDerivedFile(path, data, perm)
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("initial RefreshGraphData: %v", err)
	}
	if got, err := os.ReadFile(docMapPath); err != nil || string(got) != docMapBytes {
		t.Fatalf("refresh modified doc_map.json: bytes=%q err=%v", got, err)
	}
	if got, err := os.ReadFile(linksPath); err != nil || string(got) != linksBytes {
		t.Fatalf("refresh modified links.json: bytes=%q err=%v", got, err)
	}

	// Move the target away from its initially pinned hash so Preview exercises
	// both the graph edge and the in-memory doc map resolution path.
	targetPath := filepath.Join(dataDir, "content", "target.md")
	writeGraphTestFile(t, targetPath, graphTestMarkdown("doc-target", "Target", "project", "Updated body\n"))
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("refresh updated target: %v", err)
	}
	updatedGraph, _ := app.GetGraphData()
	pinnedEdge := graphTestEdge(updatedGraph, "doc:/source.md", "doc:/target.md")
	if pinnedEdge == nil || pinnedEdge.ToVersionMode != "pinned" || !pinnedEdge.TargetUpdated || pinnedEdge.TargetHash == "" {
		t.Fatalf("pinned target state was not preserved: %#v", pinnedEdge)
	}
	if tagNode := graphTestNode(updatedGraph, "tag:/project"); tagNode == nil || tagNode.Kind != "tag" {
		t.Fatalf("tag node missing: %#v", tagNode)
	}

	trackedPaths := []string{
		filepath.Join(dataDir, "content", "source.md"),
		targetPath,
		imagePath,
		metadataPath,
		docMapPath,
		linksPath,
		filepath.Join(dataDir, ".mdsys", graphCacheName),
		filepath.Join(dataDir, ".mdsys"),
	}
	before := graphTestFileStates(t, trackedPaths)
	graphReads.Store(0)
	graphWrites.Store(0)

	first, err := app.GetGraphData()
	if err != nil {
		t.Fatalf("GetGraphData: %v", err)
	}
	if len(first.Nodes) == 0 || len(first.Edges) == 0 {
		t.Fatalf("expected populated graph: %#v", first)
	}
	first.Nodes[0].Label = "mutated by caller"
	if len(first.Nodes[0].Tags) > 0 {
		first.Nodes[0].Tags[0] = "mutated"
	}
	first.Edges[0].Target = "doc:/mutated.md"

	for i := 0; i < 50; i++ {
		snapshot, err := app.GetGraphData()
		if err != nil {
			t.Fatalf("GetGraphData call %d: %v", i, err)
		}
		if graphNodeWithLabel(snapshot, "mutated by caller") {
			t.Fatal("caller mutation escaped into immutable graph snapshot")
		}
	}
	for i := 0; i < 50; i++ {
		if _, err := app.PreviewMarkdownForPath("content/source.md", sourceContent); err != nil {
			t.Fatalf("PreviewMarkdownForPath call %d: %v", i, err)
		}
	}

	if got := graphReads.Load(); got != 0 {
		t.Fatalf("graph hot paths performed %d reads, want 0", got)
	}
	if got := graphWrites.Load(); got != 0 {
		t.Fatalf("graph hot paths performed %d writes, want 0", got)
	}
	after := graphTestFileStates(t, trackedPaths)
	for path, state := range before {
		if after[path] != state {
			t.Fatalf("hot path changed tracked file %s: before=%+v after=%+v", path, state, after[path])
		}
	}
}

func TestRefreshGraphDataReadsAndParsesOnlyChangedMarkdown(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	paths := []string{
		filepath.Join(dataDir, "content", "alpha.md"),
		filepath.Join(dataDir, "content", "beta.md"),
		filepath.Join(dataDir, "content", "日本語.md"),
	}
	for i, path := range paths {
		writeGraphTestFile(t, path, graphTestMarkdown(fmt.Sprintf("doc-%d", i), filepath.Base(path), "", "same body\n"))
	}

	tokens := make(map[string]string, len(paths))
	for _, path := range paths {
		tokens[filepath.Clean(path)] = "v1"
	}
	app.fileSearchChangeToken = func(path string, _ os.FileInfo) string {
		if token := tokens[filepath.Clean(path)]; token != "" {
			return token
		}
		return "stable"
	}
	var reads atomic.Int64
	var parses atomic.Int64
	app.graphReadFile = func(path string) ([]byte, error) {
		if strings.HasSuffix(strings.ToLower(path), ".md") {
			reads.Add(1)
		}
		return os.ReadFile(path)
	}
	app.graphParseDocument = func(path string, content []byte) graphParsedDocument {
		parses.Add(1)
		return defaultGraphParseDocument(app, path, content)
	}

	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if reads.Load() != 3 || parses.Load() != 3 {
		t.Fatalf("initial reads/parses = %d/%d, want 3/3", reads.Load(), parses.Load())
	}
	reads.Store(0)
	parses.Store(0)
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("unchanged refresh: %v", err)
	}
	if reads.Load() != 0 || parses.Load() != 0 {
		t.Fatalf("unchanged reads/parses = %d/%d, want 0/0", reads.Load(), parses.Load())
	}

	changedPath := paths[1]
	before, err := os.Stat(changedPath)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(changedPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(string(original), "same body", "new! body", 1)
	if len(changed) != len(original) {
		t.Fatalf("test fixture must preserve size: before=%d after=%d", len(original), len(changed))
	}
	writeGraphTestFile(t, changedPath, changed)
	if err := os.Chtimes(changedPath, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	tokens[filepath.Clean(changedPath)] = "v2"
	reads.Store(0)
	parses.Store(0)
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("same-mtime same-size refresh: %v", err)
	}
	if reads.Load() != 1 || parses.Load() != 1 {
		t.Fatalf("changed reads/parses = %d/%d, want 1/1", reads.Load(), parses.Load())
	}
	info, err := os.Stat(changedPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != before.Size() || !info.ModTime().Equal(before.ModTime()) {
		t.Fatalf("fixture did not preserve mtime/size: before=%v/%d after=%v/%d", before.ModTime(), before.Size(), info.ModTime(), info.Size())
	}
}

func TestRefreshGraphDataHandlesRenameDeleteAndPrunesDocMap(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	sourcePath := filepath.Join(dataDir, "content", "source.md")
	oldTargetPath := filepath.Join(dataDir, "content", "旧名.md")
	newTargetPath := filepath.Join(dataDir, "content", "新名.md")
	writeGraphTestFile(t, sourcePath, graphTestMarkdown("doc-source", "Source", "", "[Target](旧名.md)\n"))
	writeGraphTestFile(t, oldTargetPath, graphTestMarkdown("doc-target", "対象", "", "body\n"))
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	if got := app.graphDocMapSnapshot()["doc-target"]; got != "content/旧名.md" {
		t.Fatalf("initial doc map = %q", got)
	}
	var reads atomic.Int64
	var parses atomic.Int64
	app.graphReadFile = func(path string) ([]byte, error) {
		if strings.EqualFold(filepath.Ext(path), ".md") {
			reads.Add(1)
		}
		return os.ReadFile(path)
	}
	app.graphParseDocument = func(path string, content []byte) graphParsedDocument {
		parses.Add(1)
		return defaultGraphParseDocument(app, path, content)
	}

	if err := os.Rename(oldTargetPath, newTargetPath); err != nil {
		t.Fatal(err)
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("rename refresh: %v", err)
	}
	if reads.Load() != 1 || parses.Load() != 1 {
		t.Fatalf("rename reads/parses = %d/%d, want 1/1", reads.Load(), parses.Load())
	}
	snapshot, _ := app.GetGraphData()
	if node := graphTestNode(snapshot, "doc:/新名.md"); node == nil || !node.Exists {
		t.Fatalf("renamed node missing: %#v", node)
	}
	if got := app.graphDocMapSnapshot()["doc-target"]; got != "content/新名.md" {
		t.Fatalf("renamed doc map = %q", got)
	}

	if err := os.Remove(newTargetPath); err != nil {
		t.Fatal(err)
	}
	reads.Store(0)
	parses.Store(0)
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("delete refresh: %v", err)
	}
	if reads.Load() != 0 || parses.Load() != 0 {
		t.Fatalf("delete reads/parses = %d/%d, want 0/0", reads.Load(), parses.Load())
	}
	if _, exists := app.graphDocMapSnapshot()["doc-target"]; exists {
		t.Fatal("deleted document retained a stale doc map entry")
	}
	snapshot, _ = app.GetGraphData()
	if node := graphTestNode(snapshot, "doc:/新名.md"); node != nil && node.Exists {
		t.Fatalf("deleted document remained as an existing node: %#v", node)
	}
	missing := graphTestNode(snapshot, "doc:/旧名.md")
	if missing == nil || missing.Exists {
		t.Fatalf("link target should remain as a missing node: %#v", missing)
	}
}

func TestRefreshGraphDataUpdatesImageMetadataIncrementally(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	imagePath := filepath.Join(dataDir, "content", "clips", "assets", "日本語", "画像.png")
	metadataPath := strings.TrimSuffix(imagePath, ".png") + ".yaml"
	writeGraphTestFile(t, imagePath, "png")
	writeGraphTestFile(t, metadataPath, "tags: alpha\n")
	tokens := map[string]string{
		filepath.Clean(imagePath):    "image-v1",
		filepath.Clean(metadataPath): "metadata-v1",
	}
	app.fileSearchChangeToken = func(path string, _ os.FileInfo) string {
		if token := tokens[filepath.Clean(path)]; token != "" {
			return token
		}
		return "stable"
	}
	var imageReads atomic.Int64
	var metadataReads atomic.Int64
	app.graphReadFile = func(path string) ([]byte, error) {
		switch filepath.Clean(path) {
		case filepath.Clean(imagePath):
			imageReads.Add(1)
		case filepath.Clean(metadataPath):
			metadataReads.Add(1)
		}
		return os.ReadFile(path)
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	snapshot, _ := app.GetGraphData()
	nodeID := "img:/content/clips/assets/日本語/画像.png"
	if node := graphTestNode(snapshot, nodeID); node == nil || !containsGraphTestString(node.Tags, "alpha") {
		t.Fatalf("initial image tags missing: %#v", node)
	}

	before, err := os.Stat(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	writeGraphTestFile(t, metadataPath, "tags: bravo\n")
	if err := os.Chtimes(metadataPath, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	tokens[filepath.Clean(metadataPath)] = "metadata-v2"
	imageReads.Store(0)
	metadataReads.Store(0)
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("metadata refresh: %v", err)
	}
	if imageReads.Load() != 0 || metadataReads.Load() != 1 {
		t.Fatalf("image/metadata reads = %d/%d, want 0/1", imageReads.Load(), metadataReads.Load())
	}
	snapshot, _ = app.GetGraphData()
	if node := graphTestNode(snapshot, nodeID); node == nil || !containsGraphTestString(node.Tags, "bravo") || containsGraphTestString(node.Tags, "alpha") {
		t.Fatalf("updated image tags incorrect: %#v", node)
	}

	renamedImagePath := filepath.Join(filepath.Dir(imagePath), "改名.png")
	renamedMetadataPath := strings.TrimSuffix(renamedImagePath, ".png") + ".yaml"
	if err := os.Rename(imagePath, renamedImagePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(metadataPath, renamedMetadataPath); err != nil {
		t.Fatal(err)
	}
	tokens[filepath.Clean(renamedImagePath)] = "image-v2"
	tokens[filepath.Clean(renamedMetadataPath)] = "metadata-v3"
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("image rename refresh: %v", err)
	}
	snapshot, _ = app.GetGraphData()
	if graphTestNode(snapshot, nodeID) != nil {
		t.Fatal("old image node remained after rename")
	}
	renamedNodeID := "img:/content/clips/assets/日本語/改名.png"
	if node := graphTestNode(snapshot, renamedNodeID); node == nil || !containsGraphTestString(node.Tags, "bravo") {
		t.Fatalf("renamed image node incorrect: %#v", node)
	}
	if err := os.Remove(renamedImagePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(renamedMetadataPath); err != nil {
		t.Fatal(err)
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("image deletion refresh: %v", err)
	}
	snapshot, _ = app.GetGraphData()
	if graphTestNode(snapshot, renamedNodeID) != nil {
		t.Fatal("deleted image node remained in graph")
	}
}

func TestRefreshGraphDataRebuildsCorruptCacheWithoutWritingMarkdown(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	markdownPath := filepath.Join(dataDir, "content", "plain.md")
	original := "# Plain\n\nNo front matter.\n"
	writeGraphTestFile(t, markdownPath, original)
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	cachePath := filepath.Join(dataDir, ".mdsys", graphCacheName)
	writeGraphTestFile(t, cachePath, "{corrupt")

	restarted := &App{dataDir: dataDir, root: dataDir}
	var markdownReads atomic.Int64
	restarted.graphReadFile = func(path string) ([]byte, error) {
		if filepath.Clean(path) == filepath.Clean(markdownPath) {
			markdownReads.Add(1)
		}
		return os.ReadFile(path)
	}
	if err := restarted.RefreshGraphData(); err != nil {
		t.Fatalf("rebuild corrupt cache: %v", err)
	}
	if markdownReads.Load() != 1 {
		t.Fatalf("corrupt rebuild markdown reads = %d, want 1", markdownReads.Load())
	}
	after, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("graph rebuild modified Markdown:\nwant=%q\ngot=%q", original, after)
	}
	if _, valid := restarted.loadGraphCache(cachePath); !valid {
		t.Fatal("rebuilt graph cache is not valid")
	}
}

func TestRefreshGraphDataConfinesSymlinkedMarkdown(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "secret.md")
	writeGraphTestFile(t, outsidePath, graphTestMarkdown("secret", "Secret", "", "outside\n"))
	symlinkPath := filepath.Join(dataDir, "content", "escape.md")
	if err := os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var outsideReads atomic.Int64
	app.graphReadFile = func(path string) ([]byte, error) {
		if filepath.Clean(path) == filepath.Clean(outsidePath) {
			outsideReads.Add(1)
		}
		return os.ReadFile(path)
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("RefreshGraphData: %v", err)
	}
	if outsideReads.Load() != 0 {
		t.Fatalf("read outside confined root %d times", outsideReads.Load())
	}
	snapshot, _ := app.GetGraphData()
	if node := graphTestNode(snapshot, "doc:/escape.md"); node != nil {
		t.Fatalf("symlinked Markdown was indexed: %#v", node)
	}
}

func TestRefreshGraphDataRejectsSymlinkCachePath(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	outsideCache := filepath.Join(t.TempDir(), "outside.json")
	writeGraphTestFile(t, outsideCache, "outside")
	cachePath := filepath.Join(dataDir, ".mdsys", graphCacheName)
	if err := os.Symlink(outsideCache, cachePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := app.RefreshGraphData(); err == nil || !strings.Contains(err.Error(), "not a confined regular file") {
		t.Fatalf("symlink cache error = %v", err)
	}
	outside, err := os.ReadFile(outsideCache)
	if err != nil {
		t.Fatal(err)
	}
	if string(outside) != "outside" {
		t.Fatalf("symlink target was modified: %q", outside)
	}
}

func TestRefreshGraphDataPersistenceFailureKeepsOldCacheAndSnapshot(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	markdownPath := filepath.Join(dataDir, "content", "note.md")
	writeGraphTestFile(t, markdownPath, graphTestMarkdown("doc-note", "Before", "", "body\n"))
	tokens := map[string]string{filepath.Clean(markdownPath): "v1"}
	app.fileSearchChangeToken = func(path string, _ os.FileInfo) string {
		if token := tokens[filepath.Clean(path)]; token != "" {
			return token
		}
		return "stable"
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	cachePath := filepath.Join(dataDir, ".mdsys", graphCacheName)
	beforeCache, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	beforeSnapshot, _ := app.GetGraphData()

	writeGraphTestFile(t, markdownPath, graphTestMarkdown("doc-note", "After!", "", "body\n"))
	tokens[filepath.Clean(markdownPath)] = "v2"
	injected := errors.New("injected atomic replacement failure")
	app.graphPersistFile = func(string, []byte, fs.FileMode) error { return injected }
	if err := app.RefreshGraphData(); !errors.Is(err, injected) {
		t.Fatalf("RefreshGraphData error = %v, want injected failure", err)
	}
	afterCache, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterCache) != string(beforeCache) {
		t.Fatal("failed persistence changed graph cache bytes")
	}
	afterSnapshot, _ := app.GetGraphData()
	if graphTestNode(afterSnapshot, "doc:/note.md").Label != graphTestNode(beforeSnapshot, "doc:/note.md").Label {
		t.Fatal("failed persistence published an uncommitted graph snapshot")
	}

	app.graphPersistFile = nil
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("retry refresh: %v", err)
	}
	retried, _ := app.GetGraphData()
	if got := graphTestNode(retried, "doc:/note.md").Label; got != "After!" {
		t.Fatalf("retry label = %q, want After!", got)
	}
}

func TestConcurrentGetGraphDataDuringRefresh(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	markdownPath := filepath.Join(dataDir, "content", "race.md")
	writeGraphTestFile(t, markdownPath, graphTestMarkdown("doc-race", "Race 0", "race", "body\n"))
	var token atomic.Int64
	token.Store(1)
	app.fileSearchChangeToken = func(path string, _ os.FileInfo) string {
		if filepath.Clean(path) == filepath.Clean(markdownPath) {
			return fmt.Sprintf("v%d", token.Load())
		}
		return "stable"
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				snapshot, err := app.GetGraphData()
				if err != nil || len(snapshot.Nodes) == 0 {
					t.Errorf("concurrent snapshot invalid: nodes=%d err=%v", len(snapshot.Nodes), err)
					return
				}
				snapshot.Nodes[0].Label = "caller mutation"
			}
		}()
	}
	for i := 1; i <= 10; i++ {
		writeGraphTestFile(t, markdownPath, graphTestMarkdown("doc-race", fmt.Sprintf("Race %d", i), "race", "body\n"))
		token.Add(1)
		if err := app.RefreshGraphData(); err != nil {
			t.Fatalf("refresh %d: %v", i, err)
		}
	}
	wg.Wait()
	finalSnapshot, _ := app.GetGraphData()
	if got := graphTestNode(finalSnapshot, "doc:/race.md").Label; got != "Race 10" {
		t.Fatalf("final label = %q, want Race 10", got)
	}
}

func TestLegacyDocIDMigrationIsExplicitAtomicAndRestartSafe(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	legacyPath := filepath.Join(dataDir, "content", "legacy.md")
	writeGraphTestFile(t, legacyPath, "# Legacy\n\nbody\n")

	// A graph refresh is a derived-data read boundary and must not modify the
	// legacy document.
	before, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("read-only graph refresh: %v", err)
	}
	afterRefresh, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterRefresh) != string(before) {
		t.Fatal("RefreshGraphData assigned doc_id as a read side effect")
	}
	if node := graphTestNodeMust(t, app, "doc:/legacy.md"); node.DocID != "" {
		t.Fatalf("read-only refresh assigned optional docID %q", node.DocID)
	}
	var migrationReads atomic.Int64
	var migrationParses atomic.Int64
	app.graphReadFile = func(path string) ([]byte, error) {
		if strings.EqualFold(filepath.Ext(path), ".md") {
			migrationReads.Add(1)
		}
		return os.ReadFile(path)
	}
	app.graphMigrationParse = func(content []byte) *fm.FrontMatter {
		migrationParses.Add(1)
		frontMatter, _ := fm.ParseFrontMatter(string(content))
		return frontMatter
	}

	migrated, err := app.MigrateLegacyGraphDocumentIDs()
	if err != nil {
		t.Fatalf("MigrateLegacyGraphDocumentIDs: %v", err)
	}
	if migrated != 1 {
		t.Fatalf("migrated = %d, want 1", migrated)
	}
	if migrationReads.Load() != 1 || migrationParses.Load() != 1 {
		t.Fatalf("initial migration reads/parses = %d/%d, want 1/1", migrationReads.Load(), migrationParses.Load())
	}
	migratedContent, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	frontMatter, _ := fm.ParseFrontMatter(string(migratedContent))
	if frontMatter == nil || frontMatter.DocID == "" {
		t.Fatalf("migration did not assign doc_id: %s", migratedContent)
	}
	firstDocID := frontMatter.DocID
	persistedMappings, _ := readDocumentMapTest(t, dataDir)
	if got := persistedMappings[firstDocID]; got != "content/legacy.md" {
		t.Fatalf("migrated persistent mapping = %q", got)
	}

	migrationReads.Store(0)
	migrationParses.Store(0)
	migrated, err = app.MigrateLegacyGraphDocumentIDs()
	if err != nil {
		t.Fatalf("retry migration: %v", err)
	}
	if migrated != 0 {
		t.Fatalf("retry migrated = %d, want 0", migrated)
	}
	if migrationReads.Load() != 0 || migrationParses.Load() != 0 {
		t.Fatalf("warm migration reads/parses = %d/%d, want 0/0", migrationReads.Load(), migrationParses.Load())
	}
	retriedContent, err := os.ReadFile(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(retriedContent) != string(migratedContent) {
		t.Fatal("retry rewrote an already migrated document")
	}
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("refresh migrated graph: %v", err)
	}
	node := graphTestNodeMust(t, app, "doc:/legacy.md")
	if node.DocID != firstDocID {
		t.Fatalf("graph docID = %q, want %q", node.DocID, firstDocID)
	}

	if err := os.Remove(filepath.Join(dataDir, ".mdsys", graphCacheName)); err != nil {
		t.Fatal(err)
	}
	restarted := &App{dataDir: dataDir, root: dataDir}
	var restartedMarkdownReads atomic.Int64
	restarted.graphReadFile = func(path string) ([]byte, error) {
		if strings.EqualFold(filepath.Ext(path), ".md") {
			restartedMarkdownReads.Add(1)
		}
		return os.ReadFile(path)
	}
	if migrated, err := restarted.MigrateLegacyGraphDocumentIDs(); err != nil || migrated != 0 {
		t.Fatalf("migration after cache deletion = %d, %v", migrated, err)
	}
	if restartedMarkdownReads.Load() != 0 {
		t.Fatalf("cache deletion reran migration with %d Markdown reads", restartedMarkdownReads.Load())
	}
}

func TestLegacyDocIDMigrationRetriesAfterPartialFailure(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	firstPath := filepath.Join(dataDir, "content", "a-first.md")
	secondPath := filepath.Join(dataDir, "content", "z-second.md")
	writeGraphTestFile(t, firstPath, "# First\n\nbody\n")
	writeGraphTestFile(t, secondPath, "# Second\n\nbody\n")

	injected := errors.New("injected migration interruption")
	app.graphReadFile = func(path string) ([]byte, error) {
		if filepath.Clean(path) == filepath.Clean(secondPath) {
			return nil, injected
		}
		return os.ReadFile(path)
	}
	migrated, err := app.MigrateLegacyGraphDocumentIDs()
	if migrated != 1 || !errors.Is(err, injected) {
		t.Fatalf("interrupted migration = %d, %v, want 1 and injected error", migrated, err)
	}
	markerPath := filepath.Join(dataDir, ".mdsys", graphDocIDMigrationName)
	if _, err := os.Lstat(markerPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("interrupted migration installed completion marker: %v", err)
	}
	firstAfterFailure, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	firstFrontMatter, _ := fm.ParseFrontMatter(string(firstAfterFailure))
	if firstFrontMatter == nil || firstFrontMatter.DocID == "" {
		t.Fatalf("first document was not durably migrated before interruption: %s", firstAfterFailure)
	}
	firstDocID := firstFrontMatter.DocID
	secondAfterFailure, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	secondFrontMatter, _ := fm.ParseFrontMatter(string(secondAfterFailure))
	if secondFrontMatter != nil && secondFrontMatter.DocID != "" {
		t.Fatalf("unreached document was unexpectedly migrated: %s", secondAfterFailure)
	}

	app.graphReadFile = nil
	migrated, err = app.MigrateLegacyGraphDocumentIDs()
	if err != nil || migrated != 1 {
		t.Fatalf("migration retry = %d, %v, want 1, nil", migrated, err)
	}
	firstAfterRetry, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	firstFrontMatter, _ = fm.ParseFrontMatter(string(firstAfterRetry))
	if firstFrontMatter == nil || firstFrontMatter.DocID != firstDocID {
		t.Fatalf("retry rewrote the completed document ID: got %#v, want %q", firstFrontMatter, firstDocID)
	}
	secondAfterRetry, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	secondFrontMatter, _ = fm.ParseFrontMatter(string(secondAfterRetry))
	if secondFrontMatter == nil || secondFrontMatter.DocID == "" {
		t.Fatalf("retry did not migrate the remaining document: %s", secondAfterRetry)
	}
	persistedMappings, _ := readDocumentMapTest(t, dataDir)
	if persistedMappings[firstDocID] != "content/a-first.md" || persistedMappings[secondFrontMatter.DocID] != "content/z-second.md" {
		t.Fatalf("retry did not retain every migrated mapping: %#v", persistedMappings)
	}
	if _, complete, err := app.graphDocIDMigrationMarker(); err != nil || !complete {
		t.Fatalf("retry did not install completion marker: complete=%v err=%v", complete, err)
	}
}

func TestLegacyDocIDMigrationRejectsSymlinkMarker(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	outsideMarker := filepath.Join(t.TempDir(), "marker.json")
	writeGraphTestFile(t, outsideMarker, `{"version":1}`)
	markerPath := filepath.Join(dataDir, ".mdsys", graphDocIDMigrationName)
	if err := os.Symlink(outsideMarker, markerPath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := app.MigrateLegacyGraphDocumentIDs(); err == nil || !strings.Contains(err.Error(), "not a confined regular file") {
		t.Fatalf("symlink marker error = %v", err)
	}
}

func TestCreateNewFileImmediatelyIndexesStableDocIDAcrossRename(t *testing.T) {
	app, _ := newGraphTestApp(t)
	created, err := app.CreateNewFile("new-note")
	if err != nil || !created {
		t.Fatalf("CreateNewFile = %v, %v", created, err)
	}
	createdNode := graphTestNodeMust(t, app, "doc:/new-note.md")
	if createdNode.DocID == "" {
		t.Fatal("newly created graph node has no doc_id")
	}
	docID := createdNode.DocID
	if got := app.graphDocMapSnapshot()[docID]; got != "content/new-note.md" {
		t.Fatalf("created doc map = %q", got)
	}
	persistedMappings, _ := readDocumentMapTest(t, app.dataDir)
	if got := persistedMappings[docID]; got != "content/new-note.md" {
		t.Fatalf("created persistent mapping = %q", got)
	}

	if err := app.RenameFile("content/new-note.md", "content/renamed-note.md"); err != nil {
		t.Fatalf("RenameFile: %v", err)
	}
	renamedNode := graphTestNodeMust(t, app, "doc:/renamed-note.md")
	if renamedNode.DocID != docID {
		t.Fatalf("renamed docID = %q, want %q", renamedNode.DocID, docID)
	}
	if got := app.graphDocMapSnapshot()[docID]; got != "content/renamed-note.md" {
		t.Fatalf("renamed doc map = %q", got)
	}
}

func TestLoadFileAndLoadBoardDoNotAssignDocID(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	markdownPath := filepath.Join(dataDir, "content", "external.md")
	markdownContent := "# External\n\nAdded after migration.\n"
	writeGraphTestFile(t, markdownPath, markdownContent)
	boardPath := filepath.Join(dataDir, "content", "external.board.md")
	boardContent, err := boardpkg.Serialize(&boardpkg.Document{
		Path:    "content/external.board.md",
		Title:   "External Board",
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
		t.Fatal(err)
	}
	writeGraphTestFile(t, boardPath, boardContent)
	tracked := []string{markdownPath, boardPath}
	beforeStates := graphTestFileStates(t, tracked)

	loadedMarkdown, err := app.LoadFile("content/external.md")
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if loadedMarkdown != markdownContent {
		t.Fatal("LoadFile changed returned Markdown")
	}
	loadedBoard, err := app.LoadBoard("content/external.board.md")
	if err != nil {
		t.Fatalf("LoadBoard: %v", err)
	}
	if loadedBoard.DocID != "" {
		t.Fatalf("LoadBoard assigned doc_id %q", loadedBoard.DocID)
	}

	afterStates := graphTestFileStates(t, tracked)
	for path, state := range beforeStates {
		if afterStates[path] != state {
			t.Fatalf("read API changed %s: before=%+v after=%+v", path, state, afterStates[path])
		}
	}
	markdownAfter, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatal(err)
	}
	boardAfter, err := os.ReadFile(boardPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(markdownAfter) != markdownContent || string(boardAfter) != boardContent {
		t.Fatal("read API changed file bytes")
	}
}

func TestUpdateLinkToLatestUsesSnapshotWithoutPreexistingLinksFile(t *testing.T) {
	app, dataDir := newGraphTestApp(t)
	sourcePath := filepath.Join(dataDir, "content", "source.md")
	targetPath := filepath.Join(dataDir, "content", "target.md")
	writeGraphTestFile(t, sourcePath, graphTestMarkdown("doc-source", "Source", "", "[Target](target.md)\n"))
	writeGraphTestFile(t, targetPath, graphTestMarkdown("doc-target", "Target", "", "before\n"))
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	linksPath := filepath.Join(dataDir, ".mdsys", "links.json")
	if _, err := os.Stat(linksPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only graph refresh unexpectedly created links.json: %v", err)
	}
	writeGraphTestFile(t, targetPath, graphTestMarkdown("doc-target", "Target", "", "after\n"))
	if err := app.RefreshGraphData(); err != nil {
		t.Fatalf("updated refresh: %v", err)
	}
	before, _ := app.GetGraphData()
	if edge := graphTestEdge(before, "doc:/source.md", "doc:/target.md"); edge == nil || !edge.TargetUpdated {
		t.Fatalf("target update not detected before latest mutation: %#v", edge)
	}

	if err := app.UpdateLinkToLatest("doc-source", "doc-target"); err != nil {
		t.Fatalf("UpdateLinkToLatest: %v", err)
	}
	after, _ := app.GetGraphData()
	edge := graphTestEdge(after, "doc:/source.md", "doc:/target.md")
	if edge == nil || edge.ToVersionMode != "latest" || edge.TargetUpdated {
		t.Fatalf("latest edge state incorrect: %#v", edge)
	}
	if _, err := os.Stat(linksPath); err != nil {
		t.Fatalf("explicit link mutation did not persist links.json: %v", err)
	}
}

func newGraphTestApp(t *testing.T) (*App, string) {
	t.Helper()
	dataDir := t.TempDir()
	for _, relative := range []string{"content", filepath.Join("data", "image"), ".mdsys"} {
		if err := os.MkdirAll(filepath.Join(dataDir, relative), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &App{dataDir: dataDir, root: dataDir}, dataDir
}

func graphTestMarkdown(docID, title, tags, body string) string {
	tagLine := ""
	if tags != "" {
		tagLine = "tags: " + tags + "\n"
	}
	return fmt.Sprintf("---\ndoc_id: %s\ntitle: %s\n%s---\n%s", docID, title, tagLine, body)
}

func writeGraphTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func graphTestNode(graph *GraphData, id string) *GraphNode {
	if graph == nil {
		return nil
	}
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == id {
			return &graph.Nodes[i]
		}
	}
	return nil
}

func graphTestEdge(graph *GraphData, source, target string) *GraphEdge {
	if graph == nil {
		return nil
	}
	for i := range graph.Edges {
		if graph.Edges[i].Source == source && graph.Edges[i].Target == target {
			return &graph.Edges[i]
		}
	}
	return nil
}

func graphTestNodeMust(t *testing.T, app *App, id string) *GraphNode {
	t.Helper()
	snapshot, err := app.GetGraphData()
	if err != nil {
		t.Fatalf("GetGraphData: %v", err)
	}
	node := graphTestNode(snapshot, id)
	if node == nil {
		t.Fatalf("graph node %s not found in %#v", id, snapshot.Nodes)
	}
	return node
}

func graphNodeWithLabel(graph *GraphData, label string) bool {
	if graph == nil {
		return false
	}
	for _, node := range graph.Nodes {
		if node.Label == label || containsGraphTestString(node.Tags, "mutated") {
			return true
		}
	}
	for _, edge := range graph.Edges {
		if edge.Target == "doc:/mutated.md" {
			return true
		}
	}
	return false
}

func containsGraphTestString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type graphTestFileState struct {
	Size    int64
	ModTime time.Time
}

func graphTestFileStates(t *testing.T, paths []string) map[string]graphTestFileState {
	t.Helper()
	result := make(map[string]graphTestFileState, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat tracked file %s: %v", path, err)
		}
		result[path] = graphTestFileState{Size: info.Size(), ModTime: info.ModTime()}
	}
	return result
}
