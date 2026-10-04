# Experiment Record v0.1

This document describes the schema for an experiment record that captures the state of a halted experiment.

## Overview

An experiment record captures all the relevant information about a halted experiment, including its target commit, patch hash, environment, model, checker, observations, interpretation, halt reason, and associated evidence.

## Schema

```json
{
  "schema_version": "0.1",
  "candidate_id": "string",
  "experiment_id": "string",
  "run_id": "string",
  "attempt_id": "string",
  "target_commit": "string",
  "patch_sha256": "string",
  "environment": "string",
  "model": "string",
  "checker": "string",
  "observations": ["string"],
  "interpretation": "string",
  "halt_reason": "string",
  "evidence": [
    {
      "logical_ref": "string",
      "sha256": "string"
    }
  ],
  "verification": "string",
  "state": "string",
  "project": "string",
  "title": "string",
  "reported_at": "string"
}
```

## Fields

### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| schema_version | string | Must be "0.1" |
| candidate_id | string | Unique identifier for the candidate |
| experiment_id | string | Identifier for the experiment |
| run_id | string | Identifier for the run |
| attempt_id | string | Identifier for the attempt |
| target_commit | string | Commit hash that was targeted |
| patch_sha256 | string | SHA-256 hash of the patch |
| environment | string | Execution environment |
| model | string | Model used for the experiment |
| checker | string | Checker used for validation |
| observations | array of strings | Observed facts |
| interpretation | string | Interpretation of observations |
| halt_reason | string | Reason for halting |
| evidence | array of objects | Evidence references |
| verification | string | Verification status |
| state | string | Current state (saved, experiment, adopted) |
| project | string | Project name |
| title | string | Report title |
| reported_at | string | RFC3339 timestamp when reported |

### Evidence Field

Each evidence item contains:

| Field | Type | Description |
|-------|------|-------------|
| logical_ref | string | Logical reference to the evidence |
| sha256 | string | SHA-256 hash of the evidence |

## Validation Rules

- All required fields must be present
- `schema_version` must be "0.1"
- `candidate_id` must match pattern `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`
- `experiment_id`, `run_id`, `attempt_id` must be 1-128 characters
- `target_commit` must be 40-character hex string or "unacquired"
- `patch_sha256` must be 64-character hex string or "unacquired"
- `environment`, `model`, `checker` must not be empty or "unacquired"
- `observations` must be 1-64 items, each <= 1024 characters
- `interpretation` must not be empty and <= 2048 characters
- `halt_reason` must not be empty and <= 1024 characters
- `evidence` must be 1-64 items
- `verification` must be one of "verified", "unverified", "unacquired"
- `state` must be one of "saved", "experiment", "adopted"
- `project` must be a valid project name
- `title` must not be empty and <= 256 characters
- `reported_at` must be valid RFC3339 timestamp

## Example

```json
{
  "schema_version": "0.1",
  "candidate_id": "experiment-abc-123",
  "experiment_id": "test-experiment",
  "run_id": "run-456",
  "attempt_id": "attempt-789",
  "target_commit": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
  "patch_sha256": "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
  "environment": "test-env",
  "model": "gpt-4",
  "checker": "test-checker",
  "observations": [
    "The experiment encountered an error during processing."
  ],
  "interpretation": "The experiment failed due to a runtime error that needs investigation.",
  "halt_reason": "Runtime error: failed to process input",
  "evidence": [
    {
      "logical_ref": "halt/stderr",
      "sha256": "8276fdcd6ceeb245c6246c521c886cea8592bab3cdc9fd69ba41414d08f53b46"
    }
  ],
  "verification": "verified",
  "state": "experiment",
  "project": "ephy",
  "title": "Halted Experiment Report: test-experiment",
  "reported_at": "2026-09-01T00:00:00Z"
}
```
