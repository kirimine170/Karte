package ephyoutbox

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func syntheticExperiment() (ExperimentRecord, map[string][]byte) {
	content := []byte("synthetic halted experiment evidence\n")
	return ExperimentRecord{
		SchemaVersion: ExperimentSchemaVersion, CandidateID: "candidate-experiment-001",
		ExperimentID: "synthetic-experiment", RunID: "synthetic-run", AttemptID: "synthetic-attempt",
		TargetCommit: Unacquired, PatchSHA256: Unacquired, Environment: "synthetic",
		Model: Unacquired, Checker: "synthetic-checker", Observations: []string{"Synthetic observation."},
		Interpretation: "Synthetic interpretation.", HaltReason: "Synthetic halt.",
		Evidence:     []ExperimentEvidence{{LogicalRef: "halt/stderr.txt", SHA256: SHA256Bytes(content)}},
		Verification: "verified", State: "experiment", Project: "ephy", Title: "Synthetic experiment",
		ReportedAt: "2026-08-31T23:30:00-02:00",
	}, map[string][]byte{"halt/stderr.txt": content}
}

func experimentSnapshot(t *testing.T, root string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			files[relative] = []byte("symlink:" + target)
			return err
		}
		files[relative], err = os.ReadFile(path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestExperimentTraversalTerminatesWithoutWriting(t *testing.T) {
	if root := os.Getenv("KARTE_SYNTHETIC_TRAVERSAL_ROOT"); root != "" {
		store, err := newExperimentTestStore(t, root)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.WriteEvidence("candidate-traversal", map[string][]byte{"logs/../../escaped.txt": []byte("synthetic")}); err == nil {
			t.Fatal("traversal was accepted")
		}
		return
	}
	root := t.TempDir()
	before := experimentSnapshot(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExperimentTraversalTerminatesWithoutWriting$")
	cmd.Env = append(os.Environ(), "KARTE_SYNTHETIC_TRAVERSAL_ROOT="+root)
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatal("traversal did not terminate at the filesystem root")
	}
	if err != nil {
		t.Fatalf("traversal subprocess failed: %v\n%s", err, output)
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Fatal("invalid reference mutated the store")
	}
}

func TestExperimentManifestIntegrity(t *testing.T) {
	for _, scenario := range []string{"candidate", "version", "date", "negative-size", "wrong-size", "missing-size", "null-size", "digest", "missing-entry", "duplicate-entry", "expected-duplicate", "unknown-field", "trailing-json"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			store, err := newExperimentTestStore(t, root)
			if err != nil {
				t.Fatal(err)
			}
			record, evidence := syntheticExperiment()
			if scenario == "missing-size" || scenario == "null-size" {
				evidence["halt/stderr.txt"] = []byte{}
				record.Evidence[0].SHA256 = SHA256Bytes(nil)
			}
			if err := store.WriteEvidence(record.CandidateID, evidence); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.CandidateDir(record.CandidateID), "manifest.json")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var manifest EvidenceManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				t.Fatal(err)
			}
			expected := append([]ExperimentEvidence(nil), record.Evidence...)
			switch scenario {
			case "candidate":
				manifest.CandidateID = "candidate-other"
			case "version":
				manifest.SchemaVersion = "9.9"
			case "date":
				manifest.WrittenAt = "invalid"
			case "negative-size":
				manifest.Entries[0].SizeBytes = -1
			case "wrong-size":
				manifest.Entries[0].SizeBytes++
			case "digest":
				manifest.Entries[0].SHA256 = strings.Repeat("0", 64)
			case "missing-entry":
				expected = append(expected, ExperimentEvidence{LogicalRef: "halt/missing.txt", SHA256: strings.Repeat("0", 64)})
			case "duplicate-entry":
				manifest.Entries = append(manifest.Entries, manifest.Entries[0])
			case "expected-duplicate":
				expected = append(expected, expected[0])
			}
			data, err = json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "unknown-field" {
				data = append(data[:len(data)-1], []byte(",\"unexpected\":true}")...)
			}
			if scenario == "trailing-json" {
				data = append(data, []byte(" {}")...)
			}
			if scenario == "missing-size" || scenario == "null-size" {
				var raw map[string]any
				if err := json.Unmarshal(data, &raw); err != nil {
					t.Fatal(err)
				}
				entry := raw["entries"].([]any)[0].(map[string]any)
				if scenario == "missing-size" {
					delete(entry, "size_bytes")
				} else {
					entry["size_bytes"] = nil
				}
				data, err = json.Marshal(raw)
				if err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			before := experimentSnapshot(t, root)
			if err := store.Verify(record.CandidateID, expected); err == nil {
				t.Error("invalid manifest/expected set was trusted")
			}
			if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
				t.Error("verification changed evidence")
			}
		})
	}
}

func TestExperimentVerifyRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	store, err := newExperimentTestStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	record, evidence := syntheticExperiment()
	if err := store.WriteEvidence(record.CandidateID, evidence); err != nil {
		t.Fatal(err)
	}
	content := []byte("synthetic evidence outside the candidate directory")
	if err := os.WriteFile(filepath.Join(store.root, "escaped.txt"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	ref := "logs/../../escaped.txt"
	manifest := EvidenceManifest{SchemaVersion: ExperimentSchemaVersion, CandidateID: record.CandidateID,
		WrittenAt: record.ReportedAt, Entries: []EvidenceEntry{{LogicalRef: ref, SHA256: SHA256Bytes(content), SizeBytes: int64(len(content))}}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.CandidateDir(record.CandidateID), "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	before := experimentSnapshot(t, root)
	if err := store.Verify(record.CandidateID, []ExperimentEvidence{{LogicalRef: ref, SHA256: SHA256Bytes(content)}}); err == nil {
		t.Error("verification accepted evidence outside candidate directory")
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Error("traversal verification mutated files")
	}
}

func TestExperimentInvalidRetryPreservesEvidence(t *testing.T) {
	for _, scenario := range []string{"wrong-hash", "missing-content", "extra-content", "reserved-manifest", "saved-state"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			publisher, err := newExperimentTestPublisher(t, root)
			if err != nil {
				t.Fatal(err)
			}
			record, evidence := syntheticExperiment()
			if _, err := publisher.Publish(record, evidence); err != nil {
				t.Fatal(err)
			}
			before := experimentSnapshot(t, root)
			switch scenario {
			case "wrong-hash":
				evidence["halt/stderr.txt"] = []byte("changed synthetic evidence")
			case "missing-content":
				delete(evidence, "halt/stderr.txt")
			case "extra-content":
				evidence["halt/extra.txt"] = []byte("synthetic extra")
			case "reserved-manifest":
				evidence["manifest.json"] = []byte("synthetic overwrite")
			case "saved-state":
				record.State = "saved"
				evidence["halt/stderr.txt"] = []byte("changed")
				record.Evidence[0].SHA256 = SHA256Bytes(evidence["halt/stderr.txt"])
			}
			if _, err := publisher.Publish(record, evidence); err == nil {
				t.Error("invalid retry succeeded")
			}
			if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
				t.Error("invalid retry destroyed previously stored evidence")
			}
		})
	}
}

func TestExperimentRetryIsImmutableAndDeterministic(t *testing.T) {
	root := t.TempDir()
	record, evidence := syntheticExperiment()
	publisher, err := newExperimentTestPublisher(t, root)
	if err != nil {
		t.Fatal(err)
	}
	first, err := publisher.Publish(record, evidence)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a manifest persisted before this process, without relying on a
	// sleep or on crossing the old writer's one-second clock resolution.
	manifestPath := filepath.Join(publisher.store.CandidateDir(record.CandidateID), "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var historical EvidenceManifest
	if err := json.Unmarshal(manifestBytes, &historical); err != nil {
		t.Fatal(err)
	}
	historical.WrittenAt = "2026-09-01T01:30:00Z"
	manifestBytes, err = json.Marshal(historical)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	before := experimentSnapshot(t, root)
	if first.CreatedAt != "2026-09-01T01:30:00Z" || first.Placement.YearMonth != "2026-09" {
		t.Error("proposal dates do not derive from reported_at in UTC")
	}
	publisher, err = newExperimentTestPublisher(t, root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := publisher.Publish(record, evidence)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Error("restart retry changed proposal")
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Error("restart retry changed evidence/manifest bytes")
	}
	one, err := BuildExperimentProposal(record, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	two, err := BuildExperimentProposal(record, time.Unix(999999, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(one, two) {
		t.Error("same record depends on wall-clock argument")
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("generated proposal is invalid: %v", err)
	}
	changed := []byte("different synthetic evidence")
	evidence["halt/stderr.txt"] = changed
	record.Evidence[0].SHA256 = SHA256Bytes(changed)
	if _, err := publisher.Publish(record, evidence); err == nil {
		t.Error("different content overwrote the same candidate")
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Error("different-content retry changed prior evidence")
	}
}

func TestExperimentSymlinkControls(t *testing.T) {
	for _, scenario := range []string{"candidate", "manifest", "evidence", "managed-root"} {
		t.Run(scenario, func(t *testing.T) {
			root, outside := t.TempDir(), t.TempDir()
			store, err := newExperimentTestStore(t, root)
			if err != nil {
				t.Fatal(err)
			}
			record, evidence := syntheticExperiment()
			if err := store.WriteEvidence(record.CandidateID, evidence); err != nil {
				t.Fatal(err)
			}
			candidate := store.CandidateDir(record.CandidateID)
			source, target := candidate, filepath.Join(outside, "candidate")
			switch scenario {
			case "manifest":
				source, target = filepath.Join(candidate, "manifest.json"), filepath.Join(outside, "manifest.json")
			case "evidence":
				source, target = filepath.Join(candidate, "halt", "stderr.txt"), filepath.Join(outside, "evidence.txt")
			case "managed-root":
				source, target = store.root, filepath.Join(outside, "experiments")
			}
			if err := os.Rename(source, target); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, source); err != nil {
				t.Fatal(err)
			}
			beforeRoot, beforeOutside := experimentSnapshot(t, root), experimentSnapshot(t, outside)
			if err := store.Verify(record.CandidateID, record.Evidence); err == nil {
				t.Error("verification followed symlink")
			}
			if err := store.WriteEvidence(record.CandidateID, evidence); err == nil {
				t.Error("write followed symlink")
			}
			if !reflect.DeepEqual(beforeRoot, experimentSnapshot(t, root)) || !reflect.DeepEqual(beforeOutside, experimentSnapshot(t, outside)) {
				t.Error("symlink operation changed bytes")
			}
		})
	}
}

func TestExperimentExistingChecksRemainEnforced(t *testing.T) {
	root := t.TempDir()
	store, err := newExperimentTestStore(t, root)
	if err != nil {
		t.Fatal(err)
	}
	record, evidence := syntheticExperiment()
	if err := store.WriteEvidence(record.CandidateID, evidence); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.CandidateDir(record.CandidateID), "halt", "stderr.txt")
	if err := os.WriteFile(path, []byte("corrupted synthetic content"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := experimentSnapshot(t, root)
	if err := store.Verify(record.CandidateID, record.Evidence); err == nil {
		t.Error("actual content corruption was trusted")
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Error("verification changed corrupted evidence")
	}
	if bytes.Equal(evidence["halt/stderr.txt"], before[filepath.Join(".mdsys", "ephy", "experiments", record.CandidateID, "halt", "stderr.txt")]) {
		t.Fatal("corruption fixture did not change bytes")
	}
}
