# Artifact Manifest v0.1

This document describes the structure of the artifact manifest for experiment evidence.

## Overview

The artifact manifest is a JSON file that describes all the evidence files associated with a specific experiment. It is stored in the managed area under `.mdsys/ephy/experiments/{candidateID}/manifest.json`.

## Schema

```json
{
  "schema_version": "0.1",
  "candidate_id": "string",
  "entries": [
    {
      "logical_ref": "string",
      "sha256": "string",
      "size_bytes": "integer"
    }
  ],
  "written_at": "string"
}
```

## Fields

### Required Fields

| Field | Type | Description |
|-------|------|-------------|
| schema_version | string | Must be "0.1" |
| candidate_id | string | Unique identifier for the candidate |
| entries | array of objects | List of evidence entries |
| written_at | string | RFC3339 timestamp when manifest was written |

### Entries Field

Each entry in the manifest contains:

| Field | Type | Description |
|-------|------|-------------|
| logical_ref | string | Logical reference to the evidence |
| sha256 | string | SHA-256 hash of the evidence |
| size_bytes | integer | Size of the evidence in bytes |

## Example

```json
{
  "schema_version": "0.1",
  "candidate_id": "experiment-abc-123",
  "entries": [
    {
      "logical_ref": "halt/stderr",
      "sha256": "8276fdcd6ceeb245c6246c521c886cea8592bab3cdc9fd69ba41414d08f53b46",
      "size_bytes": 32
    }
  ],
  "written_at": "2026-09-01T00:00:00Z"
}
```

## Storage Location

The manifest file is stored in the managed area under:
`.mdsys/ephy/experiments/{candidateID}/manifest.json`

## Validation Rules

- `schema_version` must be "0.1"
- `candidate_id` must match the candidate ID of the experiment
- `entries` must not be empty
- Each entry's `logical_ref` must be a valid logical reference
- Each entry's `sha256` must be a valid 64-character hexadecimal string
- `written_at` must be a valid RFC3339 timestamp