package ephyoutbox

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	WorkerExperimentAdapterVersion = "karte.worker-experiment.v1"
	WorkerEvidenceManifestVersion  = "ephy.evidence-manifest.v1"
	producerBindingVersion         = "karte.experiment-payload.v1"
	producerMetadataRef            = "adapter/metadata.json"
	producerManifestRef            = "worker/evidence-manifest.json"
	producerMaxJSON                = 256 * 1024
	producerMaxArtifact            = 8 * 1024 * 1024
	producerMaxEvidence            = 32 * 1024 * 1024
)

// WorkerExperimentMetadata is an explicit, synthetic bridge contract, not a
// Worker audit result or adoption decision. No status can promote verification.
type WorkerExperimentMetadata struct {
	AdapterVersion string   `json:"adapter_version"`
	SyntheticOnly  *bool    `json:"synthetic_only"`
	CandidateID    string   `json:"candidate_id"`
	ExperimentID   string   `json:"experiment_id"`
	RunID          string   `json:"run_id"`
	AttemptID      string   `json:"attempt_id"`
	TargetCommit   string   `json:"target_commit"`
	Environment    string   `json:"environment"`
	Model          string   `json:"model"`
	Checker        string   `json:"checker"`
	WorkerResult   string   `json:"worker_result"`
	Observations   []string `json:"observations"`
	Interpretation string   `json:"interpretation"`
	HaltReason     string   `json:"halt_reason"`
	Project        string   `json:"project"`
	Title          string   `json:"title"`
	ReportedAt     string   `json:"reported_at"`
}

type workerArtifact struct {
	ArtifactID string `json:"artifact_id"`
	Path       string `json:"path"`
	SizeBytes  *int64 `json:"size_bytes"`
	MediaType  string `json:"media_type"`
	Producer   string `json:"producer"`
	SHA256     string `json:"sha256"`
}

type workerManifest struct {
	SchemaVersion string           `json:"schema_version"`
	AuditID       string           `json:"audit_id"`
	JobID         string           `json:"job_id"`
	CreatedAt     string           `json:"created_at"`
	Artifacts     []workerArtifact `json:"artifacts"`
}

// Ordered IDs are pinned to the Worker v1 schema; arbitrary evidence subsets
// must not masquerade as that complete contract.
var workerArtifactIDs = []string{
	"system_development_policy", "task_spec", "required_skill",
	"evaluation_contract", "environment_contract", "audit_contract", "audit_prompt",
	"audit_input_schema", "evidence_manifest_schema", "audit_result_schema",
	"preflight_result", "lead_plan", "workflow_events", "model_provenance",
	"candidate_patch", "candidate_changed_files", "candidate_snapshot_manifest",
	"verification_plan", "verification_results", "checker_source",
	"checker_control_results", "command_transcripts",
}

var producerSourcePathPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

func validProducerSourcePath(value string) bool {
	if len(value) == 0 || len(value) > 500 || !producerSourcePathPattern.MatchString(value) || path.Clean(value) != value || strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if !portableEvidenceSegment(part) {
			return false
		}
	}
	return true
}

// Reject duplicate object keys as well as unknown fields, trailing JSON, and
// invalid UTF-8. json.Unmarshal alone silently accepts duplicate identity keys.
func decodeProducerJSON(raw []byte, value any) error {
	if len(raw) > producerMaxJSON || !utf8.Valid(raw) {
		return fmt.Errorf("invalid or oversized producer JSON")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func() error
	walk = func() error {
		token, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := map[string]bool{}
			for d.More() {
				t, err := d.Token()
				if err != nil {
					return err
				}
				key, ok := t.(string)
				if !ok || keys[key] {
					return fmt.Errorf("duplicate or invalid JSON key")
				}
				keys[key] = true
				if err := walk(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err := walk(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(value)
}

func decodeWorkerInputs(metadataRaw, manifestRaw []byte) (WorkerExperimentMetadata, workerManifest, error) {
	var metadata WorkerExperimentMetadata
	var manifest workerManifest
	if err := decodeProducerJSON(metadataRaw, &metadata); err != nil {
		return metadata, manifest, fmt.Errorf("metadata: %w", err)
	}
	if err := decodeProducerJSON(manifestRaw, &manifest); err != nil {
		return metadata, manifest, fmt.Errorf("manifest: %w", err)
	}
	if metadata.AdapterVersion != WorkerExperimentAdapterVersion || metadata.SyntheticOnly == nil || !*metadata.SyntheticOnly {
		return metadata, manifest, fmt.Errorf("adapter requires version %s and synthetic_only=true", WorkerExperimentAdapterVersion)
	}
	switch metadata.WorkerResult {
	case "external_review_pending", "strict_pass", "halted":
	default:
		return metadata, manifest, fmt.Errorf("unsupported worker_result")
	}
	if manifest.SchemaVersion != WorkerEvidenceManifestVersion || manifest.AuditID != metadata.ExperimentID || manifest.JobID != metadata.RunID {
		return metadata, manifest, fmt.Errorf("Worker manifest/metadata version or identity mismatch")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.CreatedAt); err != nil {
		return metadata, manifest, fmt.Errorf("invalid manifest created_at")
	}
	if len(manifest.Artifacts) != len(workerArtifactIDs) {
		return metadata, manifest, fmt.Errorf("Worker v1 requires all 22 ordered artifacts")
	}
	seen := map[string]bool{}
	var total int64
	for i, artifact := range manifest.Artifacts {
		if artifact.ArtifactID != workerArtifactIDs[i] || !validProducerSourcePath(artifact.Path) || strings.EqualFold(artifact.Path, "evidence-manifest.json") {
			return metadata, manifest, fmt.Errorf("invalid Worker artifact identity or path at index %d", i)
		}
		folded := strings.ToLower(artifact.Path)
		if seen[folded] {
			return metadata, manifest, fmt.Errorf("duplicate artifact path")
		}
		seen[folded] = true
		if artifact.SizeBytes == nil || *artifact.SizeBytes < 0 || *artifact.SizeBytes > producerMaxArtifact || !isSHA256(artifact.SHA256) {
			return metadata, manifest, fmt.Errorf("invalid artifact size or SHA256")
		}
		for _, v := range []string{artifact.MediaType, artifact.Producer} {
			if strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > 160 {
				return metadata, manifest, fmt.Errorf("invalid artifact provenance metadata")
			}
		}
		total += *artifact.SizeBytes
	}
	if total > producerMaxEvidence {
		return metadata, manifest, fmt.Errorf("Worker evidence exceeds total size limit")
	}
	for ref := range seen {
		for parent := path.Dir(ref); parent != "."; parent = path.Dir(parent) {
			if seen[parent] {
				return metadata, manifest, fmt.Errorf("artifact file/directory collision")
			}
		}
	}
	return metadata, manifest, nil
}

type producerBinding struct {
	SchemaVersion string           `json:"schema_version"`
	Record        ExperimentRecord `json:"record"`
	Proposal      Proposal         `json:"proposal"`
	Entries       []EvidenceEntry  `json:"entries"`
}

func buildProducerBinding(contents map[string][]byte) (producerBinding, error) {
	metadata, manifest, err := decodeWorkerInputs(contents[producerMetadataRef], contents[producerManifestRef])
	if err != nil {
		return producerBinding{}, err
	}
	if len(contents) != len(workerArtifactIDs)+2 {
		return producerBinding{}, fmt.Errorf("producer evidence set is incomplete or contains extras")
	}
	patchSHA := ""
	for _, artifact := range manifest.Artifacts {
		raw, ok := contents["worker/artifacts/"+artifact.ArtifactID]
		if !ok || int64(len(raw)) != *artifact.SizeBytes || SHA256Bytes(raw) != artifact.SHA256 {
			return producerBinding{}, fmt.Errorf("artifact digest/size mismatch: %s", artifact.ArtifactID)
		}
		if artifact.ArtifactID == "candidate_patch" {
			patchSHA = artifact.SHA256
		}
	}
	record := ExperimentRecord{SchemaVersion: ExperimentSchemaVersion, CandidateID: metadata.CandidateID,
		ExperimentID: metadata.ExperimentID, RunID: metadata.RunID, AttemptID: metadata.AttemptID,
		TargetCommit: metadata.TargetCommit, PatchSHA256: patchSHA, Environment: metadata.Environment,
		Model: metadata.Model, Checker: metadata.Checker, Interpretation: metadata.Interpretation,
		HaltReason: metadata.HaltReason, State: "experiment", Verification: "unverified",
		Project: metadata.Project, Title: metadata.Title, ReportedAt: metadata.ReportedAt,
		Observations: append([]string{"Worker result (unverified): " + metadata.WorkerResult}, metadata.Observations...),
	}
	if len(metadata.Observations) == 0 {
		return producerBinding{}, fmt.Errorf("metadata requires observed facts")
	}
	refs := make([]string, 0, len(contents))
	for ref := range contents {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	binding := producerBinding{SchemaVersion: producerBindingVersion}
	for _, ref := range refs {
		hash := SHA256Bytes(contents[ref])
		record.Evidence = append(record.Evidence, ExperimentEvidence{LogicalRef: ref, SHA256: hash})
		binding.Entries = append(binding.Entries, EvidenceEntry{LogicalRef: ref, SHA256: hash, SizeBytes: int64(len(contents[ref]))})
	}
	proposal, err := BuildExperimentProposal(record, time.Time{})
	if err != nil {
		return producerBinding{}, err
	}
	binding.Record, binding.Proposal = record, proposal
	return binding, nil
}

func loadWorkerBundle(bundleDir, metadataPath string) (producerBinding, map[string][]byte, error) {
	root, err := openProducerInputRoot(bundleDir)
	if err != nil {
		return producerBinding{}, nil, err
	}
	defer root.Close()
	return loadWorkerBundleRoot(root, metadataPath)
}

func loadWorkerBundleRoot(root *os.Root, metadataPath string) (producerBinding, map[string][]byte, error) {
	metadataRaw, err := readProducerInputFile(metadataPath, producerMaxJSON)
	if err != nil {
		return producerBinding{}, nil, err
	}
	manifestRaw, err := readProducerRootFile(root, "evidence-manifest.json", producerMaxJSON)
	if err != nil {
		return producerBinding{}, nil, err
	}
	_, manifest, err := decodeWorkerInputs(metadataRaw, manifestRaw)
	if err != nil {
		return producerBinding{}, nil, err
	}
	contents := map[string][]byte{producerMetadataRef: metadataRaw, producerManifestRef: manifestRaw}
	for _, artifact := range manifest.Artifacts {
		raw, err := readProducerRootFile(root, artifact.Path, *artifact.SizeBytes)
		if err != nil {
			return producerBinding{}, nil, err
		}
		contents["worker/artifacts/"+artifact.ArtifactID] = raw
	}
	binding, err := buildProducerBinding(contents)
	return binding, contents, err
}

func producerJSON(value any) []byte { raw, _ := json.Marshal(value); return append(raw, '\n') }

func openProducerInputRoot(dir string) (*os.Root, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("input root must be a directory, not a symlink")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fmt.Errorf("input root changed while opening")
	}
	return root, nil
}
