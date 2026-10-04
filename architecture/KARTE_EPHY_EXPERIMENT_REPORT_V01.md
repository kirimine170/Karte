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
- `created_at`: Derived from `record.reported_at` and normalized to UTC RFC3339 with fractional seconds retained
- `placement`:
  - `project`: Copied from record
  - `kind`: `"report"`
  - `year_month`: Derived from the UTC `record.reported_at` (YYYY-MM)
  - `confidence`: `0.9`
  - `preferred_filename`: Derived from `record.experiment_id`
  - `candidates`: One candidate with the same project/kind and reason `"Halted experiment record v0.1 maps to a human-readable report."`
- `source_refs`:
  - One entry per evidence entry: `{type: "experiment-evidence", reference: logical_ref, sha256: sha256}`
- `proposed_body`: Rendered Markdown report from `RenderExperimentReport(record)`

## Verification

The extension schema and fixtures live under `schemas/experiment/v0.1/`．The existing `schemas/karte-ephy/v1/` and V1.1 review contract are retained．

## Evidence integrity and retries

The data directory must already exist．Evidence operations use rooted directory handles，reject symlinks in the managed path，and compare actual directory-entry spelling so Windows case aliases cannot select another candidate or file．Logical references are normalized relative forward-slash paths with no empty，`.`，or `..` segments，no trailing-dot or Windows device-name segments，and no `manifest.json` file or subtree．File/directory prefix collisions and duplicate references are rejected before creating any managed path．

`Publish` validates the record，the generated V1.1 proposal，and the complete supplied evidence reference/digest set before persistence．A new candidate is assembled in a private staging directory，all files are flushed and verified，and the complete directory is installed with an atomic no-replace operation．Linux uses `RENAME_NOREPLACE`，macOS uses `RENAME_EXCL`，and Windows uses handle-relative rename with `ReplaceIfExists=false`．Existing destinations，including empty directories and links，are never replaced．Concurrent identical writers verify the immutable winner and succeed；different bytes or reference sets fail while preserving the old evidence and manifest．Interrupted staging directories are uncommitted and cannot be mistaken for candidates；a retry does not delete unrelated staging directories．

Verification requires an exact candidate/schema identity，a valid RFC3339 manifest date，unique reference sets equal to the caller's expected set，explicit nonnegative integer sizes，and identical manifest/expected/file SHA-256 digests and sizes．Actual file inventory must equal the manifest plus `manifest.json`；unreferenced files，directories，and links fail verification．Reads and failed retries preserve bytes．Manifest entries are sorted at first installation，and `written_at` records that first write without being refreshed on retry．

The same record and evidence produce the same proposal across retries and restart．The UTC dates derive only from `reported_at`，and `Proposal.Validate` runs before returning a proposal．The optional legacy clock argument is ignored．The foundation returns a proposal and saves evidence；it does not save record/proposal JSON，publish pending candidates，run worker adapters，approve reports，or write canonical documents．Windows flushes evidence files but does not claim POSIX directory-fsync or power-loss guarantees；POSIX directory handles are synced before and after installation．Unsupported no-replace platforms/filesystems fail without a replacement fallback．

### Verification before proposal preparation

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
