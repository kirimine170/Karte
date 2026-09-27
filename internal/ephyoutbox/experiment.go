package ephyoutbox

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	ExperimentSchemaVersion = "0.1"
	Unacquired              = "unacquired"
)

var (
	logicalRefPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._/]*$`)
)

// ExperimentEvidence represents a piece of evidence for an experiment.
type ExperimentEvidence struct {
	LogicalRef string `json:"logical_ref"`
	SHA256     string `json:"sha256"`
}

// ExperimentRecord describes a halted experiment.
type ExperimentRecord struct {
	SchemaVersion  string               `json:"schema_version"`
	CandidateID    string               `json:"candidate_id"`
	ExperimentID   string               `json:"experiment_id"`
	RunID          string               `json:"run_id"`
	AttemptID      string               `json:"attempt_id"`
	TargetCommit   string               `json:"target_commit"`
	PatchSHA256    string               `json:"patch_sha256"`
	Environment    string               `json:"environment"`
	Model          string               `json:"model"`
	Checker        string               `json:"checker"`
	Observations   []string             `json:"observations"`
	Interpretation string               `json:"interpretation"`
	HaltReason     string               `json:"halt_reason"`
	Evidence       []ExperimentEvidence `json:"evidence"`
	Verification   string               `json:"verification"`
	State          string               `json:"state"` // saved | experiment | adopted
	Project        string               `json:"project"`
	Title          string               `json:"title"`
	ReportedAt     string               `json:"reported_at"`
}

// EvidenceManifest is the manifest for evidence stored for an experiment
type EvidenceManifest struct {
	SchemaVersion string               `json:"schema_version"`
	CandidateID   string               `json:"candidate_id"`
	Entries       []EvidenceEntry      `json:"entries"`
	WrittenAt     string               `json:"written_at"`
}

// EvidenceEntry is a single entry in an evidence manifest
type EvidenceEntry struct {
	LogicalRef string `json:"logical_ref"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
}

// ExperimentEvidenceStore manages experiment evidence in the secret managed area
type ExperimentEvidenceStore struct {
	dataRoot string
	root     string
}

// NewExperimentEvidenceStore creates a new evidence store
func NewExperimentEvidenceStore(dataDir string) (*ExperimentEvidenceStore, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	store := &ExperimentEvidenceStore{
		dataRoot: abs,
		root:     filepath.Join(abs, ".mdsys", "ephy", "experiments"),
	}
	return store, nil
}

// CandidateDir returns the directory for a candidate's evidence
func (s *ExperimentEvidenceStore) CandidateDir(candidateID string) string {
	return filepath.Join(s.root, candidateID)
}

// WriteEvidence writes experiment evidence to the managed area
func (s *ExperimentEvidenceStore) WriteEvidence(candidateID string, entries map[string][]byte) error {
	// Validate candidate ID
	if !candidateIDPattern.MatchString(candidateID) {
		return fmt.Errorf("invalid candidate_id: %s", candidateID)
	}

	candidateDir := s.CandidateDir(candidateID)
	if err := os.MkdirAll(candidateDir, 0700); err != nil {
		return fmt.Errorf("failed to create evidence directory: %w", err)
	}

	manifest := EvidenceManifest{
		SchemaVersion: ExperimentSchemaVersion,
		CandidateID:   candidateID,
		Entries:       make([]EvidenceEntry, 0, len(entries)),
		WrittenAt:     time.Now().UTC().Format(time.RFC3339),
	}

	for logicalRef, content := range entries {
		// Validate logical reference
		if !isValidLogicalRef(logicalRef) {
			return fmt.Errorf("invalid logical reference: %s", logicalRef)
		}

		// Write the evidence file
		filePath := filepath.Join(candidateDir, logicalRef)
		if err := os.MkdirAll(filepath.Dir(filePath), 0700); err != nil {
			return fmt.Errorf("failed to create evidence subdirectory: %w", err)
		}

		// Check for symlinks before writing
		if fileInfo, err := os.Lstat(filePath); err == nil {
			if fileInfo.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink detected in evidence path: %s", filePath)
			}
		}

		if err := os.WriteFile(filePath, content, 0600); err != nil {
			return fmt.Errorf("failed to write evidence file %s: %w", logicalRef, err)
		}

		// Calculate SHA256 hash
		hash := sha256.Sum256(content)
		sha256Hex := hex.EncodeToString(hash[:])

		manifest.Entries = append(manifest.Entries, EvidenceEntry{
			LogicalRef: logicalRef,
			SHA256:     sha256Hex,
			SizeBytes:  int64(len(content)),
		})
	}

	// Write the manifest
	manifestPath := filepath.Join(candidateDir, "manifest.json")
	// Reserve manifest.json from evidence references
	if _, exists := entries["manifest.json"]; exists {
		return fmt.Errorf("manifest.json is reserved and cannot be used as evidence")
	}
	manifestBytes, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0600); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	return nil
}

// Verify verifies that evidence files exist and match expected hashes
func (s *ExperimentEvidenceStore) Verify(candidateID string, expected []ExperimentEvidence) error {
	candidateDir := s.CandidateDir(candidateID)
	manifestPath := filepath.Join(candidateDir, "manifest.json")
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("evidence manifest not found: %w", err)
	}

	var manifest EvidenceManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return fmt.Errorf("failed to unmarshal manifest: %w", err)
	}

	// Create a map for quick lookup
	expectedMap := make(map[string]ExperimentEvidence)
	for _, e := range expected {
		expectedMap[e.LogicalRef] = e
	}

	// Verify each expected evidence
	for _, entry := range manifest.Entries {
		expectedEntry, exists := expectedMap[entry.LogicalRef]
		if !exists {
			return fmt.Errorf("unexpected evidence file: %s", entry.LogicalRef)
		}

		// Read the file content
		filePath := filepath.Join(candidateDir, entry.LogicalRef)
		content, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("evidence file missing: %s", entry.LogicalRef)
		}

		// Calculate hash of the content
		hash := sha256.Sum256(content)
		actualSHA256 := hex.EncodeToString(hash[:])

		if actualSHA256 != expectedEntry.SHA256 {
			return fmt.Errorf("evidence hash mismatch for %s: expected %s, got %s", entry.LogicalRef, expectedEntry.SHA256, actualSHA256)
		}
	}

	// Verify that all expected evidence was processed
	for _, expectedEntry := range expected {
		if _, exists := expectedMap[expectedEntry.LogicalRef]; !exists {
			return fmt.Errorf("expected evidence file missing: %s", expectedEntry.LogicalRef)
		}
	}

	return nil
}

// isValidLogicalRef checks if a logical reference is valid
func isValidLogicalRef(ref string) bool {
	// Must not be empty
	if ref == "" {
		return false
	}
	// Must not start with "/"
	if strings.HasPrefix(ref, "/") {
		return false
	}
	// Must be within the allowed character set
	return logicalRefPattern.MatchString(ref)
}

// DecodeExperimentRecord decodes a JSON byte array into an ExperimentRecord.
func DecodeExperimentRecord(raw []byte) (*ExperimentRecord, error) {
	var record ExperimentRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, fmt.Errorf("failed to unmarshal experiment record: %w", err)
	}

	if record.SchemaVersion != ExperimentSchemaVersion {
		return nil, fmt.Errorf("unsupported schema version: %s", record.SchemaVersion)
	}

	return &record, nil
}

// Validate validates an experiment record.
func (r *ExperimentRecord) Validate() error {
	if r.SchemaVersion != ExperimentSchemaVersion {
		return fmt.Errorf("schema_version must be %s", ExperimentSchemaVersion)
	}

	if !candidateIDPattern.MatchString(r.CandidateID) {
		return fmt.Errorf("candidate_id is invalid")
	}

	if r.ExperimentID == "" || len(r.ExperimentID) > 128 {
		return fmt.Errorf("experiment_id must be 1-128 characters")
	}

	if r.RunID == "" || len(r.RunID) > 128 {
		return fmt.Errorf("run_id must be 1-128 characters")
	}

	if r.AttemptID == "" || len(r.AttemptID) > 128 {
		return fmt.Errorf("attempt_id must be 1-128 characters")
	}

	if r.TargetCommit == "" {
		return fmt.Errorf("target_commit must not be empty")
	}
	if r.TargetCommit != Unacquired {
		if len(r.TargetCommit) != 40 {
			return fmt.Errorf("target_commit must be 40-character hex string")
		}
		if !isValidHex(r.TargetCommit) {
			return fmt.Errorf("target_commit must be valid hex string")
		}
	}

	if r.PatchSHA256 == "" {
		return fmt.Errorf("patch_sha256 must not be empty")
	}
	if r.PatchSHA256 != Unacquired {
		if len(r.PatchSHA256) != 64 {
			return fmt.Errorf("patch_sha256 must be 64-character hex string")
		}
		if !isValidHex(r.PatchSHA256) {
			return fmt.Errorf("patch_sha256 must be valid hex string")
		}
	}

	if r.Environment == "" && r.Environment != Unacquired {
		return fmt.Errorf("environment must not be empty or 'unacquired'")
	}
	if r.Model == "" && r.Model != Unacquired {
		return fmt.Errorf("model must not be empty or 'unacquired'")
	}
	if r.Checker == "" && r.Checker != Unacquired {
		return fmt.Errorf("checker must not be empty or 'unacquired'")
	}

	if len(r.Observations) == 0 || len(r.Observations) > 64 {
		return fmt.Errorf("observations must be 1-64 items")
	}
	for _, obs := range r.Observations {
		if len(obs) > 1024 {
			return fmt.Errorf("observation must be <= 1024 characters")
		}
	}

	if r.Interpretation == "" || len(r.Interpretation) > 2048 {
		return fmt.Errorf("interpretation must not be empty and <= 2048 characters")
	}

	if r.HaltReason == "" || len(r.HaltReason) > 1024 {
		return fmt.Errorf("halt_reason must not be empty and <= 1024 characters")
	}

	if len(r.Evidence) == 0 || len(r.Evidence) > 64 {
		return fmt.Errorf("evidence must be 1-64 items")
	}
	for _, ev := range r.Evidence {
		if ev.LogicalRef == "" || len(ev.LogicalRef) > 2048 {
			return fmt.Errorf("evidence logical_ref must not be empty and <= 2048 characters")
		}
		if !isValidLogicalRef(ev.LogicalRef) {
			return fmt.Errorf("evidence logical_ref contains invalid characters: %s", ev.LogicalRef)
		}
		if len(ev.SHA256) != 64 {
			return fmt.Errorf("evidence sha256 must be 64-character hex string")
		}
		if !isValidHex(ev.SHA256) {
			return fmt.Errorf("evidence sha256 must be valid hex string")
		}
	}

	if r.Verification == "" {
		return fmt.Errorf("verification must not be empty")
	}
	if r.State == "" {
		return fmt.Errorf("state must not be empty")
	}
	if r.State != "saved" && r.State != "experiment" && r.State != "adopted" {
		return fmt.Errorf("state must be 'saved', 'experiment', or 'adopted'")
	}
	if r.State == "adopted" {
		return fmt.Errorf("experiment record state must be 'experiment' for proposal")
	}

	if r.Project == "" || !projectPattern.MatchString(r.Project) {
		return fmt.Errorf("project must be a valid project name")
	}

	if r.Title == "" || len(r.Title) > 256 {
		return fmt.Errorf("title must not be empty and <= 256 characters")
	}

	if r.ReportedAt == "" {
		return fmt.Errorf("reported_at must not be empty")
	}
	if _, err := time.Parse(time.RFC3339, r.ReportedAt); err != nil {
		return fmt.Errorf("reported_at must be valid RFC3339 timestamp")
	}

	return nil
}

// isValidHex checks if a string is a valid hexadecimal string
func isValidHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// RenderExperimentReport renders an experiment record into a Markdown report.
func RenderExperimentReport(record *ExperimentRecord) (string, error) {
	if err := record.Validate(); err != nil {
		return "", fmt.Errorf("invalid experiment record: %w", err)
	}

	var buf strings.Builder

	buf.WriteString(fmt.Sprintf("# %s\n\n", record.Title))

	buf.WriteString("## Status\n\n")
	buf.WriteString(fmt.Sprintf("- Record: experiment v0.1 (halted, not re-run, not adopted)\n"))
	buf.WriteString(fmt.Sprintf("- State: %s\n", record.State))
	buf.WriteString(fmt.Sprintf("- Verification: %s\n\n", record.Verification))

	buf.WriteString("## Identity\n\n")
	buf.WriteString(fmt.Sprintf("- Experiment: %s\n", record.ExperimentID))
	buf.WriteString(fmt.Sprintf("- Run: %s\n", record.RunID))
	buf.WriteString(fmt.Sprintf("- Attempt: %s\n\n", record.AttemptID))

	buf.WriteString("## Target\n\n")
	buf.WriteString(fmt.Sprintf("- Commit: %s\n", record.TargetCommit))
	buf.WriteString(fmt.Sprintf("- Patch SHA-256: %s\n\n", record.PatchSHA256))

	buf.WriteString("## Environment\n\n")
	buf.WriteString(fmt.Sprintf("- Environment: %s\n", record.Environment))
	buf.WriteString(fmt.Sprintf("- Model: %s\n", record.Model))
	buf.WriteString(fmt.Sprintf("- Checker: %s\n\n", record.Checker))

	buf.WriteString("## Observed facts\n\n")
	for _, obs := range record.Observations {
		buf.WriteString(fmt.Sprintf("- %s\n", obs))
	}
	buf.WriteString("\n")

	buf.WriteString("## Interpretation\n\n")
	buf.WriteString(fmt.Sprintf("%s\n\n", record.Interpretation))

	buf.WriteString("## Halt reason\n\n")
	buf.WriteString(fmt.Sprintf("%s\n\n", record.HaltReason))

	buf.WriteString("## Evidence\n\n")
	for _, ev := range record.Evidence {
		buf.WriteString(fmt.Sprintf("- `%s` — sha256:%s\n", ev.LogicalRef, ev.SHA256))
		buf.WriteString(fmt.Sprintf("  (stored under .mdsys/ephy/experiments/%s/%s; not embedded in this document)\n\n", record.CandidateID, ev.LogicalRef))
	}

	buf.WriteString("## Verification\n\n")
	buf.WriteString(fmt.Sprintf("%s\n", record.Verification))

	return buf.String(), nil
}

// BuildExperimentProposal converts an experiment record into an ephy outbox proposal.
func BuildExperimentProposal(record ExperimentRecord, now time.Time) (Proposal, error) {
	if err := record.Validate(); err != nil {
		return Proposal{}, fmt.Errorf("invalid experiment record: %w", err)
	}

	if record.State != "experiment" {
		return Proposal{}, fmt.Errorf("experiment record state must be 'experiment' for proposal")
	}

	// Reject unknown properties in the record to prevent invalid JSON
	// This maintains compatibility with the expected schema
	if record.SchemaVersion != "0.1" {
		return Proposal{}, fmt.Errorf("unsupported schema version: %s", record.SchemaVersion)
	}

	// Determine the filename
	filename := slugify(record.ExperimentID) + ".md"
	if !filenamePattern.MatchString(filename) {
		filename = "experiment-report.md"
	}

	// Create the proposal
	proposal := Proposal{
		SchemaVersion: "1.1",
		CandidateID:   record.CandidateID,
		Operation:     "create",
		ProposedFrontmatter: map[string]any{
			"title": record.Title,
			"tags":  "ephy, experiment, halted",
		},
		ProposedBody: func() string {
			body, err := RenderExperimentReport(&record)
			if err != nil {
				panic(err)
			}
			return body
		}(),
		Placement: PlacementHint{
			Project:              record.Project,
			Kind:                 "report",
			YearMonth:            time.Now().UTC().Format("2006-01"),
			Confidence:           float64Ptr(0.9),
			PreferredFilename:    filename,
			Candidates:           []PlacementCandidate{{Project: record.Project, Kind: "report", Confidence: float64Ptr(0.9), Reason: "Halted experiment record v0.1 maps to a human-readable report."}},
			ConsultationRequired: false,
		},
		SourceRefs: []SourceRef{
			// The record itself is the proposal
			{
				Type:      "experiment-record",
				Reference: "experiment://" + record.CandidateID,
				SHA256:    "", // Not used for the record itself
			},
		},
		Sensitivity: "internal",
		CreatedAt:   now.UTC().Format(time.RFC3339),
	}

	// Add evidence source references
	for _, ev := range record.Evidence {
		proposal.SourceRefs = append(proposal.SourceRefs, SourceRef{
			Type:      "experiment-evidence",
			Reference: ev.LogicalRef,
			SHA256:    ev.SHA256,
		})
	}

	return proposal, nil
}

// slugify converts a string to a valid filename
func slugify(s string) string {
	if s == "" {
		return "experiment-report"
	}
	s = strings.ToLower(s)
	var buf strings.Builder
	prevIsDash := false
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			buf.WriteRune(r)
			prevIsDash = false
		} else if !prevIsDash {
			buf.WriteRune('-')
			prevIsDash = true
		}
	}
	result := buf.String()
	result = strings.Trim(result, "-")
	if result == "" {
		return "experiment-report"
	}
	return result
}

// float64Ptr returns a pointer to a float64
func float64Ptr(v float64) *float64 {
	return &v
}

// ExperimentPublisher is a helper for publishing experiment records
type ExperimentPublisher struct {
	dataDir string
	store   *ExperimentEvidenceStore
}

// NewExperimentPublisher creates a new experiment publisher
func NewExperimentPublisher(dataDir string) (*ExperimentPublisher, error) {
	store, err := NewExperimentEvidenceStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &ExperimentPublisher{
		dataDir: dataDir,
		store:   store,
	}, nil
}

// Publish converts an experiment record to a proposal and stores evidence
func (p *ExperimentPublisher) Publish(record ExperimentRecord, evidence map[string][]byte) (Proposal, error) {
	// Validate the record
	if err := record.Validate(); err != nil {
		return Proposal{}, fmt.Errorf("invalid experiment record: %w", err)
	}

	// Store the evidence
	if err := p.store.WriteEvidence(record.CandidateID, evidence); err != nil {
		return Proposal{}, fmt.Errorf("failed to store evidence: %w", err)
	}

	// Verify the evidence
	if err := p.store.Verify(record.CandidateID, record.Evidence); err != nil {
		return Proposal{}, fmt.Errorf("failed to verify evidence: %w", err)
	}

	// Build the proposal
	proposal, err := BuildExperimentProposal(record, time.Now())
	if err != nil {
		return Proposal{}, fmt.Errorf("failed to build proposal: %w", err)
	}

	return proposal, nil
}