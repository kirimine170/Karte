package ephyoutbox

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestExperimentVerificationUsesOpenedDirectory(t *testing.T) {
	dataDir := t.TempDir()
	parent, err := os.OpenRoot(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := parent.Mkdir("original", 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := parent.OpenRoot("original")
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	record, evidence := syntheticExperiment()
	if err := root.Mkdir("halt", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeEvidenceFile(root, "halt/stderr.txt", evidence["halt/stderr.txt"]); err != nil {
		t.Fatal(err)
	}
	manifest := EvidenceManifest{
		SchemaVersion: ExperimentSchemaVersion, CandidateID: record.CandidateID,
		WrittenAt: record.ReportedAt,
		Entries: []EvidenceEntry{{LogicalRef: record.Evidence[0].LogicalRef,
			SHA256: record.Evidence[0].SHA256, SizeBytes: int64(len(evidence["halt/stderr.txt"]))}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeEvidenceFile(root, "manifest.json", content); err != nil {
		t.Fatal(err)
	}
	if err := parent.Rename("original", "moved"); err != nil {
		t.Fatal(err)
	}
	// Recreate the old pathname with conflicting bytes. Verification must use
	// the opened directory, never cwd or the directory's obsolete display path.
	if err := parent.MkdirAll("original/halt", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := parent.WriteFile("original/halt/stderr.txt", []byte("different"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := experimentSnapshot(t, dataDir)
	if err := verifyEvidenceDirectory(root, record.CandidateID, record.Evidence); err != nil {
		t.Fatalf("verification escaped the opened directory: %v", err)
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, dataDir)) {
		t.Fatal("verification changed evidence bytes")
	}
}
