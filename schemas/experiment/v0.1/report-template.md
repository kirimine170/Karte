# Experiment Report Template v0.1

This document describes the template for generating human-readable reports from experiment records.

## Template Structure

The report is generated in Markdown format with the following sections:

1. Title
2. Status
3. Identity
4. Target
5. Environment
6. Observed facts
7. Interpretation
8. Halt reason
9. Evidence
10. Verification

## Example Report

```markdown
# Halted Experiment Report: test-experiment

## Status

- Record: experiment v0.1 (halted, not re-run, not adopted)
- State: experiment
- Verification: verified

## Identity

- Experiment: test-experiment
- Run: run-456
- Attempt: attempt-789

## Target

- Commit: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2
- Patch SHA-256: a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2

## Environment

- Environment: test-env
- Model: gpt-4
- Checker: test-checker

## Observed facts

- The experiment encountered an error during processing.

## Interpretation

The experiment failed due to a runtime error that needs investigation.

## Halt reason

Runtime error: failed to process input

## Evidence

- `halt/stderr` — sha256:8276fdcd6ceeb245c6246c521c886cea8592bab3cdc9fd69ba41414d08f53b46
  (stored under .mdsys/ephy/experiments/experiment-abc-123/halt/stderr; not embedded in this document)

## Verification

verified
```

## Report Generation Process

1. The `RenderExperimentReport` function takes an `ExperimentRecord`
2. The function validates the record
3. The function builds a Markdown document with the sections above
4. The function returns the complete Markdown document

## Field Details

### Title

The title is set to the value of the `title` field from the experiment record.

### Status

- Shows the schema version and that this is a halted, non-re-run, non-adopted experiment
- Shows the state from the record
- Shows the verification status

### Identity

Includes experiment ID, run ID, and attempt ID.

### Target

Includes the target commit and patch SHA-256.

### Environment

Includes environment, model, and checker information.

### Observed facts

Lists all observations from the record, each on a new line.

### Interpretation

Includes the interpretation from the record.

### Halt reason

Includes the halt reason from the record.

### Evidence

Lists all evidence with logical references and SHA-256 hashes. Each entry includes a note about where the file is stored in the managed area.

### Verification

Includes the verification status from the record.