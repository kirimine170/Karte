package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSearchResourcesUsesOneTypedContractAcrossKinds(t *testing.T) {
	fixture := newFileSearchFixture(t)
	modified := time.Unix(1_800_000_000, 0)
	fixture.writeFile(t, "content/shared.md", "---\ntitle: Shared Markdown\n---\nneedle in body only\n", "shared-v1", modified)
	fixture.writeFile(t, "content/project.board.md", "---\ntitle: Project Board\n---\nboard body\n", "board-v1", modified)
	fixture.writeFile(t, "content/needle-reference.pdf", "%PDF-1.7\n", "pdf-v1", modified)
	writeResourceSearchFixtureFile(t, fixture.root, "data/image/Needle-Cover.webp", "RIFF0000WEBP", modified)
	writeResourceSearchFixtureFile(t, fixture.root, "content/clips/assets/article/Needle-Clip.png", "png", modified)
	writeResourceSearchFixtureFile(t, fixture.root, "content/clips/assets/article/Needle-Clip.webp", "webp", modified)
	writeResourceSearchFixtureFile(t, fixture.root, "data/csv/Needle-Data.csv", "a,b\n1,2\n", modified)

	markdown, err := fixture.app.SearchResources(ResourceSearchRequest{
		Query: "  NEEDLE in BODY  ",
		Kinds: []ResourceKind{ResourceKindMarkdown},
		Page:  1,
		Limit: 50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if markdown.Query != "needle in body" || markdown.Total != 1 || len(markdown.Items) != 1 || markdown.Items[0].Path != "content/shared.md" {
		t.Fatalf("Markdown body result = %#v", markdown)
	}
	if markdown.Items[0].Kind != ResourceKindMarkdown || markdown.Items[0].Metadata.Name != "shared.md" || markdown.Items[0].Metadata.Extension != ".md" {
		t.Fatalf("Markdown typed metadata = %#v", markdown.Items[0])
	}
	payload, err := json.Marshal(markdown)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "needle in body only") || strings.Contains(string(payload), "normalizedText") {
		t.Fatalf("resource result leaked indexed Markdown body: %s", payload)
	}

	images, err := fixture.app.SearchResources(ResourceSearchRequest{Query: "needle", Kinds: []ResourceKind{ResourceKindImage}, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if images.Total != 2 || len(images.Items) != 2 {
		t.Fatalf("image results = %#v", images)
	}
	if images.Items[0].Path != "content/clips/assets/article/Needle-Clip.webp" || images.Items[1].Path != "data/image/Needle-Cover.webp" {
		t.Fatalf("image roots or stable ordering = %#v", images.Items)
	}
	for _, item := range images.Items {
		if item.Kind != ResourceKindImage || item.Metadata.Size <= 0 {
			t.Fatalf("image typed metadata = %#v", item)
		}
	}

	csvs, err := fixture.app.SearchResources(ResourceSearchRequest{Query: "needle-data", Kinds: []ResourceKind{ResourceKindCSV}, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if csvs.Total != 1 || csvs.Items[0].Path != "data/csv/Needle-Data.csv" || csvs.Items[0].Kind != ResourceKindCSV {
		t.Fatalf("CSV result = %#v", csvs)
	}

	all, err := fixture.app.SearchResources(ResourceSearchRequest{
		Kinds:        []ResourceKind{ResourceKindMarkdown, ResourceKindPDF, ResourceKindImage, ResourceKindCSV},
		ExcludePaths: []string{"content/project.board.md"},
		Page:         1,
		Limit:        2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 5 || len(all.Items) != 2 || !all.HasMore {
		t.Fatalf("exclude-adjusted pagination = %#v", all)
	}
	if all.Items[0].Path != "content/clips/assets/article/Needle-Clip.webp" || all.Items[1].Path != "content/needle-reference.pdf" {
		t.Fatalf("stable resource ordering = %#v", all.Items)
	}
}

func TestSearchResourcesRejectsInvalidKindsAndExcludePathsAndNormalizesPages(t *testing.T) {
	fixture := newFileSearchFixture(t)
	fixture.writeFile(t, "content/only.md", "# Only\n", "only-v1", time.Now())

	if _, err := fixture.app.SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{"unknown"}}); err == nil {
		t.Fatal("unknown resource kind was accepted")
	}
	for _, excluded := range []string{"../outside.md", "/absolute.md", "content/../outside.md", `content\outside.md`, " content/only.md", "C:/outside.md"} {
		if _, err := fixture.app.SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{ResourceKindMarkdown}, ExcludePaths: []string{excluded}}); err == nil {
			t.Fatalf("invalid exclude path %q was accepted", excluded)
		}
	}

	normalized, err := fixture.app.SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{ResourceKindMarkdown}, Page: 0, Limit: 10_000})
	if err != nil {
		t.Fatal(err)
	}
	if normalized.Page != 1 || normalized.Limit != resourceSearchMaxLimit {
		t.Fatalf("normalized pagination = %#v", normalized)
	}

	maxInt := int(^uint(0) >> 1)
	overflowPage, err := fixture.app.SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{ResourceKindMarkdown}, Page: maxInt, Limit: resourceSearchMaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	if overflowPage.Page != maxInt || len(overflowPage.Items) != 0 || overflowPage.Total != 1 || overflowPage.HasMore {
		t.Fatalf("overflow-safe page = %#v", overflowPage)
	}
}

func TestSearchResourcesRejectsImageRootAncestorSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated Windows privileges")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "content"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "image"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "image", "escape.webp"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "data")); err != nil {
		t.Fatal(err)
	}
	app := &App{dataDir: root}
	if _, err := app.SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{ResourceKindImage}, Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ancestor symlink error = %v", err)
	}
}

func TestSearchResourcesSkipsSymlinkAndNonRegularEntries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated Windows privileges")
	}
	root := t.TempDir()
	imageRoot := filepath.Join(root, "data", "image")
	if err := os.MkdirAll(imageRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.webp")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(imageRoot, "linked.webp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(imageRoot, "directory.webp"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := (&App{dataDir: root}).SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{ResourceKindImage}, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 || len(result.Items) != 0 {
		t.Fatalf("non-regular image entries were searchable: %#v", result)
	}
}

func TestSearchResourcesSkipsCSVHardLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hard-link creation requires platform-specific privileges on some Windows hosts")
	}
	root := t.TempDir()
	csvRoot := filepath.Join(root, "data", "csv")
	if err := os.MkdirAll(csvRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(csvRoot, "original.csv")
	if err := os.WriteFile(original, []byte("h\nv\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(original, filepath.Join(csvRoot, "alias.csv")); err != nil {
		t.Fatal(err)
	}

	result, err := (&App{dataDir: root}).SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{ResourceKindCSV}, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if result.Total != 0 || len(result.Items) != 0 {
		t.Fatalf("hard-linked CSV entries were searchable: %#v", result)
	}
}

func TestSearchResourcesPagesOneThousandItemsWithoutReturningAllMetadata(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data", "csv"), 0o755); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 1_000; index++ {
		name := fmt.Sprintf("item-%04d.csv", index)
		if err := os.WriteFile(filepath.Join(root, "data", "csv", name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	app := &App{dataDir: root}
	first, err := app.SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{ResourceKindCSV}, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if first.Total != 1_000 || len(first.Items) != 50 || !first.HasMore || first.Items[0].Path != "data/csv/item-0000.csv" {
		t.Fatalf("first thousand-item page = total %d，items %d，hasMore %v，first %#v", first.Total, len(first.Items), first.HasMore, first.Items[0])
	}
	last, err := app.SearchResources(ResourceSearchRequest{Kinds: []ResourceKind{ResourceKindCSV}, Page: 20, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Items) != 50 || last.HasMore || last.Items[49].Path != "data/csv/item-0999.csv" {
		t.Fatalf("last thousand-item page = %#v", last)
	}
}

func writeResourceSearchFixtureFile(t *testing.T, root, relativePath, content string, modified time.Time) {
	t.Helper()
	absolutePath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(absolutePath, modified, modified); err != nil {
		t.Fatal(err)
	}
}
