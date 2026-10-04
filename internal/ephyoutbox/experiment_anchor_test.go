package ephyoutbox

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
)

func TestExperimentStoreKeepsDataDirectoryIdentity(t *testing.T) {
	for _, replacement := range []string{"symlink", "directory"} {
		t.Run(replacement, func(t *testing.T) {
			parent := t.TempDir()
			dataDir := filepath.Join(parent, "original")
			moved := filepath.Join(parent, "moved")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			store, err := newExperimentTestStore(t, dataDir)
			if err != nil {
				t.Fatal(err)
			}
			closedBeforeReplacement := false
			if err := os.Rename(dataDir, moved); err != nil {
				// Windows' top-level OpenRoot handle blocks rename with a sharing
				// violation (32). Prove both that protection and fail-closed behavior
				// after Close releases it, rather than skipping the swap regression.
				if runtime.GOOS != "windows" || !errors.Is(err, syscall.Errno(32)) {
					t.Fatal(err)
				}
				t.Log("retained Windows root blocked pathname replacement until Close")
				if err := store.Close(); err != nil {
					t.Fatal(err)
				}
				closedBeforeReplacement = true
				if err := os.Rename(dataDir, moved); err != nil {
					t.Fatal("Close did not release the directory handle:", err)
				}
			}
			target := dataDir
			if replacement == "symlink" {
				target = filepath.Join(parent, "outside")
			}
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "sentinel"), []byte("preserve this synthetic data"), 0o600); err != nil {
				t.Fatal(err)
			}
			if replacement == "symlink" {
				if err := os.Symlink(target, dataDir); err != nil {
					t.Fatal(err)
				}
			}
			before := experimentSnapshot(t, target)
			record, evidence := syntheticExperiment()
			if closedBeforeReplacement {
				if err := store.WriteEvidence(record.CandidateID, evidence); err == nil {
					t.Error("closed store followed a replacement pathname")
				}
				if err := store.Verify(record.CandidateID, record.Evidence); err == nil {
					t.Error("closed store verified a replacement pathname")
				}
				if !reflect.DeepEqual(before, experimentSnapshot(t, target)) {
					t.Error("closed store modified replacement target")
				}
				return
			}
			if err := store.WriteEvidence(record.CandidateID, evidence); err != nil {
				t.Fatal(err)
			}
			if err := store.Verify(record.CandidateID, record.Evidence); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, experimentSnapshot(t, target)) {
				t.Error("replacement target was modified instead of the original directory")
			}
			path := filepath.Join(moved, ".mdsys", "ephy", "experiments", record.CandidateID, "halt", "stderr.txt")
			content, err := os.ReadFile(path)
			if err != nil || !reflect.DeepEqual(content, evidence["halt/stderr.txt"]) {
				t.Fatalf("evidence did not remain in the opened original directory: %v", err)
			}
		})
	}
}

func TestExperimentStoreCloseLifecycle(t *testing.T) {
	root := t.TempDir()
	store, err := newExperimentTestStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal("repeated close failed:", err)
	}
	record, evidence := syntheticExperiment()
	before := experimentSnapshot(t, root)
	if err := store.WriteEvidence(record.CandidateID, evidence); err == nil {
		t.Error("closed store wrote evidence")
	}
	if err := store.Verify(record.CandidateID, record.Evidence); err == nil {
		t.Error("closed store verified evidence")
	}
	publisher, err := newExperimentTestPublisher(t, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(record, evidence); err == nil {
		t.Error("closed publisher wrote evidence")
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Error("closed owner mutated storage")
	}
}

func newExperimentTestStore(t *testing.T, dataDir string) (*ExperimentEvidenceStore, error) {
	t.Helper()
	store, err := NewExperimentEvidenceStore(dataDir)
	if err == nil {
		t.Cleanup(func() {
			if err := store.Close(); err != nil {
				t.Error("close synthetic store:", err)
			}
		})
	}
	return store, err
}

func newExperimentTestPublisher(t *testing.T, dataDir string) (*ExperimentPublisher, error) {
	t.Helper()
	publisher, err := NewExperimentPublisher(dataDir)
	if err == nil {
		t.Cleanup(func() {
			if err := publisher.Close(); err != nil {
				t.Error("close synthetic publisher:", err)
			}
		})
	}
	return publisher, err
}
