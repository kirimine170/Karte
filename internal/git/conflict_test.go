package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectConflictWithContentDoesNotModifyCriticalRemote(t *testing.T) {
	base := divergentConflictContent("base")
	local := divergentConflictContent("local")
	remote := divergentConflictContent("remote")
	vcs, root, relativePath, absolutePath := newConflictTestRepository(t, base)
	if err := os.WriteFile(absolutePath, []byte(remote), 0o640); err != nil {
		t.Fatal(err)
	}

	before, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	conflict, err := DetectConflictWithContent(vcs, root, relativePath, local)
	if err != nil {
		t.Fatal(err)
	}
	if conflict == nil {
		t.Fatal("expected a conflict")
	}
	if conflict.Severity != ConflictCritical {
		t.Fatalf("severity = %v, want %v", conflict.Severity, ConflictCritical)
	}
	if conflict.BaseContent != base || conflict.LocalContent != local || conflict.RemoteContent != remote {
		t.Fatalf("unexpected three-way conflict contents: %#v", conflict)
	}
	if conflict.BaseHash != CalculateHash(base) || conflict.LocalHash != CalculateHash(local) || conflict.RemoteHash != CalculateHash(remote) {
		t.Fatalf("unexpected three-way conflict hashes: %#v", conflict)
	}

	after, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("conflict detection modified the working-tree file\nwant: %q\n got: %q", before, after)
	}
}

func TestDetectConflictWithContentIgnoresOrdinaryEditorChange(t *testing.T) {
	base := "base content\n"
	vcs, root, relativePath, absolutePath := newConflictTestRepository(t, base)

	conflict, err := DetectConflictWithContent(vcs, root, relativePath, "editor content\n")
	if err != nil {
		t.Fatal(err)
	}
	if conflict != nil {
		t.Fatalf("disk matching HEAD should not conflict: %#v", conflict)
	}
	content, err := os.ReadFile(absolutePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != base {
		t.Fatalf("conflict detection modified ordinary save source: %q", content)
	}
}

func newConflictTestRepository(t *testing.T, baseContent string) (*VCS, string, string, string) {
	t.Helper()
	root := t.TempDir()
	relativePath := filepath.ToSlash(filepath.Join("content", "conflict.md"))
	absolutePath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolutePath, []byte(baseContent), 0o640); err != nil {
		t.Fatal(err)
	}
	vcs, err := NewVCS(nil, root, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if err := vcs.CommitFile(relativePath, "Add conflict fixture"); err != nil {
		t.Fatal(err)
	}
	return vcs, root, relativePath, absolutePath
}

func divergentConflictContent(prefix string) string {
	lines := make([]string, 12)
	for index := range lines {
		lines[index] = fmt.Sprintf("%s line %02d", prefix, index)
	}
	return strings.Join(lines, "\n") + "\n"
}
