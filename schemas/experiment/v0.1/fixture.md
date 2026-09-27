# Experiment Record Fixtures v0.1

This document contains example fixtures for experiment records, both valid and invalid.

## Valid Fixture

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

## Invalid Fixtures

### Missing Required Fields

```json
{
  "schema_version": "0.1",
  "candidate_id": "experiment-abc-123",
  "experiment_id": "test-experiment"
  // Missing required fields
}
```

### Invalid Schema Version

```json
{
  "schema_version": "0.2",
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

### Invalid Target Commit

```json
{
  "schema_version": "0.1",
  "candidate_id": "experiment-abc-123",
  "experiment_id": "test-experiment",
  "run_id": "run-456",
  "attempt_id": "attempt-789",
  "target_commit": "invalid-commit",
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

### Invalid Evidence

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
      "logical_ref": "",
      "sha256": "invalid-sha256"
    }
  ],
  "verification": "verified",
  "state": "experiment",
  "project": "ephy",
  "title": "Halted Experiment Report: test-experiment",
  "reported_at": "2026-09-01T00:00:00Z"
}
```