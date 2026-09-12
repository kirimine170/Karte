package git

import (
	"fmt"
	"github.com/go-git/go-git/v5"
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

func TestAutomaticCommitRejectsV2StorageOnlyFilesAndPreservesStaging(t *testing.T) {
	vcs, root, relative, _ := newConflictTestRepository(t, "original\n")
	protected := "content/ephy-v2-synthetic.md"
	if err := os.WriteFile(filepath.Join(root, protected), []byte("---\nruntime_record: {}\n---\nsynthetic private record\n"), 0600); err != nil {
		t.Fatal(err)
	}
	head, err := vcs.Repository().Head()
	if err != nil {
		t.Fatal(err)
	}
	if err = vcs.CommitFile(protected, "should not commit"); err == nil {
		t.Fatal("storage-only record entered Git")
	}
	worktree, err := vcs.Repository().Worktree()
	if err != nil {
		t.Fatal(err)
	}
	// Existing user staging is preserved，but an unrelated automatic save must
	// not incidentally commit it together with its own file．
	if _, err = worktree.Add(protected); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, relative), []byte("ordinary changed file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = vcs.CommitFile(relative, "automatic ordinary update"); err == nil {
		t.Fatal("automatic commit swept up protected staging")
	}
	after, err := vcs.Repository().Head()
	if err != nil {
		t.Fatal(err)
	}
	if after.Hash() != head.Hash() {
		t.Fatal("commit occurred despite storage-only staging")
	}
	status, err := worktree.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status[protected].Staging == git.Untracked {
		t.Fatal("removed user staging")
	}
}
