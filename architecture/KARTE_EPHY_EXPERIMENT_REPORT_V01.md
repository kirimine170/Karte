# Karte–Ephy Experiment Report V0.1

## Overview

This document defines the contract for reporting halted experiments from the Ephy worker to Karte. A halted experiment is a run that was stopped before completion, with a reason and evidence. The Ephy worker proposes the experiment record as a human-readable Markdown document with `kind=report` to the existing V1.1 outbox, and Karte reviews it before saving.

## Contract

### Experiment Record v0.1

An experiment record describes a halted experiment with the following fields:

- `schema_version` (string): Must be `"0.1"`
- `candidate_id` (string): Unique identifier for the proposal (same as V1.1 proposal)
- `experiment_id` (string): Identifier of the experiment
- `run_id` (string): Identifier of the run
- `attempt_id` (string): Identifier of the attempt
- `target_commit` (string): Commit hash of the target (40-digit hex) or `"unacquired"`
- `patch_sha256` (string): SHA-256 hash of the patch (64-digit hex) or `"unacquired"`
- `environment` (string): Execution environment description or `"unacquired"`
- `model` (string): Model version or `"unacquired"`
- `checker` (string): Checker version or `"unacquired"`
- `observations` (array of string): Observed facts (1-64 entries, each ≤ 1024 characters)
- `interpretation` (string): Interpretation of observations (≤ 2048 characters)
- `halt_reason` (string): Reason for halting (required, non-empty, ≤ 1024 characters)
- `evidence` (array of object): Evidence entries (1-64 entries)
  - `logical_ref` (string): Logical reference to evidence (no traversal, ≤ 2048 characters)
  - `sha256` (string): SHA-256 hash of evidence (64-digit hex)
- `verification` (string): Result of verification ("verified", "unverified", or "unacquired")
- `state` (string): State of the experiment ("saved", "experiment", or "adopted")
- `project` (string): Project name (valid project pattern)
- `title` (string): Title of the report (non-empty, ≤ 256 characters)
- `reported_at` (string): RFC3339 timestamp when the experiment was reported

### Evidence

Evidence is stored in the managed area under `.mdsys/ephy/experiments/<candidate_id>/` to ensure it can be verified after restart, rather than depending only on absolute paths in the temporary worktree.

## Mapping to V1.1 Proposal

The mapping from experiment record v0.1 to V1.1 proposal follows these rules:

- `operation`: `"create"`
- `candidate_id`: Copied from the record
- `proposed_frontmatter`:
  - `title`: Copied from record
  - `tags`: `"ephy, experiment, halted"`
- `sensitivity`: `"internal"` (fixed)
- `created_at`: Set to `record.reported_at`
- `placement`:
  - `project`: Copied from record
  - `kind`: `"report"`
  - `year_month`: Derived from `record.reported_at` (YYYY-MM)
  - `confidence`: `0.9`
  - `preferred_filename`: Derived from `record.experiment_id`
  - `candidates`: One candidate with the same project/kind and reason `"Halted experiment record v0.1 maps to a human-readable report."`
- `source_refs`: 
  - One entry per evidence entry: `{type: "experiment-evidence", reference: logical_ref, sha256: sha256}`
- `proposed_body`: Rendered Markdown report from `RenderExperimentReport(record)`

## Verification

The experiment record must be verified before proposing:

1. All evidence entries must have valid logical references and SHA-256 hashes
2. The evidence must exist in the managed store and match the recorded hashes
3. The record's state must be `"experiment"` (not `"adopted"`)
4. All other fields must conform to the contract

## Example

```json
{
  "schema_version": "0.1",
  "candidate_id": "exp-001",
  "experiment_id": "my-experiment",
  "run_id": "run-123",
  "attempt_id": "attempt-456",
  "target_commit": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
  "patch_sha256": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
  "environment": "test-env",
  "model": "gpt-4",
  "checker": "test-checker",
  "observations": [
    "The experiment encountered an error during processing.",
    "No further information is available."
  ],
  "interpretation": "The experiment failed due to a runtime error that needs investigation.",
  "halt_reason": "Runtime error: failed to process input",
  "evidence": [
    {
      "logical_ref": "halt/stderr",
      "sha256": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
    }
  ],
  "verification": "verified",
  "state": "experiment",
  "project": "ephy",
  "title": "Halted Experiment Report: my-experiment",
  "reported_at": "2026-09-01T00:00:00Z"
}
```