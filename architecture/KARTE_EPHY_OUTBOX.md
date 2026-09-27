# KARTE EPHY Outbox Architecture

This document describes the architecture for the EPHY outbox integration for halted experiments reporting.

## Overview

The EPHY outbox is responsible for managing proposals for human-readable reports that document halted experiments. These reports are stored in the `.mdsys/ephy/reports` directory and are integrated with the KARTE outbox system.

## Key Components

### ExperimentRecord

An `ExperimentRecord` represents a halted experiment that needs to be documented in a human-readable report.

```go
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
```

### ExperimentEvidence

Evidence represents a piece of evidence for an experiment.

```go
type ExperimentEvidence struct {
	LogicalRef string `json:"logical_ref"`
	SHA256     string `json:"sha256"`
}
```

### ExperimentEvidenceStore

The `ExperimentEvidenceStore` manages experiment evidence in the secret managed area.

```go
type ExperimentEvidenceStore struct {
	dataRoot string
	root     string
}
```

### Proposal Structure

The proposal generated from an experiment record follows the KARTE outbox schema:

```go
type Proposal struct {
	SchemaVersion       string         `json:"schema_version"`
	CandidateID         string         `json:"candidate_id"`
	Operation           string         `json:"operation"`
	ProposedFrontmatter map[string]any `json:"proposed_frontmatter"`
	ProposedBody        string         `json:"proposed_body"`
	Placement           PlacementHint  `json:"placement"`
	SourceRefs          []SourceRef    `json:"source_refs"`
	Sensitivity         string         `json:"sensitivity"`
	CreatedAt           string         `json:"created_at"`
}
```

## Flow

1. An experiment halts and creates an `ExperimentRecord`
2. The `ExperimentPublisher` stores evidence in the managed area
3. Evidence is verified to ensure integrity
4. A `Proposal` is created and converted from the `ExperimentRecord`
5. The proposal is stored in the KARTE outbox for human review

## Evidence Storage

Evidence is stored in the managed area under `.mdsys/ephy/experiments/{candidateID}/` with a manifest file `manifest.json` that contains references to all evidence files.

## Report Generation

The `RenderExperimentReport` function transforms an `ExperimentRecord` into a human-readable Markdown report that includes:
- Status information
- Identity details
- Target commit and patch information
- Environment details
- Observed facts
- Interpretation
- Halt reason
- Evidence references

## Validation

All records are validated before processing to ensure they meet the required schema and constraints.