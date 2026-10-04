package ephyoutbox

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExperimentEvidenceStore(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "experiment_evidence_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	store, err := NewExperimentEvidenceStore(tempDir)
	require.NoError(t, err)

	// Test writing evidence
	evidence := map[string][]byte{
		"test/evidence1.txt": []byte("test evidence content 1"),
		"test/evidence2.txt": []byte("test evidence content 2"),
	}

	err = store.WriteEvidence("test-candidate", evidence)
	require.NoError(t, err)

	// Verify that the files were written
	manifestPath := filepath.Join(store.CandidateDir("test-candidate"), "manifest.json")
	assert.FileExists(t, manifestPath)

	// Read and verify the manifest
	manifestBytes, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest EvidenceManifest
	err = json.Unmarshal(manifestBytes, &manifest)
	require.NoError(t, err)
	assert.Equal(t, ExperimentSchemaVersion, manifest.SchemaVersion)
	assert.Equal(t, "test-candidate", manifest.CandidateID)
	assert.Len(t, manifest.Entries, 2)

	// Test that the files were actually written to disk
	assert.FileExists(t, filepath.Join(store.CandidateDir("test-candidate"), "test", "evidence1.txt"))
	assert.FileExists(t, filepath.Join(store.CandidateDir("test-candidate"), "test", "evidence2.txt"))
}

func TestSlugify(t *testing.T) {
	testCases := []struct {
		input    string
		expected string
	}{
		{"test experiment", "test-experiment"},
		{"Test_Experiment", "test-experiment"},
		{"Test:Experiment", "test-experiment"},
		{"test/experiment", "test-experiment"},
		{"", "experiment-report"},
		{"a", "a"},
		{"test experiment with many words", "test-experiment-with-many-words"},
	}

	for _, tc := range testCases {
		result := slugify(tc.input)
		assert.Equal(t, tc.expected, result, "slugify for input: %s", tc.input)
	}
}

func TestExperimentPublisher(t *testing.T) {
	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "publisher_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	publisher, err := NewExperimentPublisher(tempDir)
	require.NoError(t, err)

	// Test a valid record
	record := ExperimentRecord{
		SchemaVersion:  "0.1",
		CandidateID:    "test-candidate-001",
		ExperimentID:   "test-experiment",
		RunID:          "test-run",
		AttemptID:      "test-attempt",
		TargetCommit:   "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		PatchSHA256:    "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		Environment:    "test-env",
		Model:          "gpt-4",
		Checker:        "test-checker",
		Observations:   []string{"Observation 1"},
		Interpretation: "Interpretation of the observations",
		HaltReason:     "Reason for halting",
		Evidence: []ExperimentEvidence{
			{
				LogicalRef: "test/evidence.txt",
				SHA256:     "d960609511962d52cd97168d8f128849f532f33b86da2e4804b1a8385630f689",
			},
		},
		Verification: "verified",
		State:        "experiment",
		Project:      "ephy",
		Title:        "Test Experiment Report",
		ReportedAt:   "2026-09-01T00:00:00Z",
	}

	evidence := map[string][]byte{
		"test/evidence.txt": []byte("test evidence content"),
	}

	proposal, err := publisher.Publish(record, evidence)
	assert.NoError(t, err)
	assert.NotNil(t, proposal)
	assert.Equal(t, "test-candidate-001", proposal.CandidateID)
}
