# Synthetic Worker experiment producer v1

`cmd/karte-experiment` bridges a complete Worker `ephy.evidence-manifest.v1`
bundle into Karte `ExperimentRecord` v0.1 and an outbox proposal v1.1.
This first adapter accepts explicitly synthetic inputs only. Worker PR18 has no
Karte adapter; this contract is a Karte-side fixture contract, not a claim that
the Worker emits these metadata fields or that its evaluations are verified.
No model, live Worker execution, user data, adoption, or improvement is involved.

The Worker schema is pinned from
[`ephy-worker@9d38e2c`](https://github.com/kirimine170/ephy-worker/blob/9d38e2cb7f6808407ab376f2150284a46a19fee9/.agents/skills/ephy-worker-self-improvement/references/evidence-manifest.schema.json)
in `schemas/experiment/producer/v1/worker-evidence-manifest.schema.json`.
`testdata/worker-experiment-v1` contains the complete versioned synthetic fixture.

## Commands

Build with the existing Go toolchain:

```text
go build -o karte-experiment ./cmd/karte-experiment
```

Create a fresh, empty synthetic Karte data directory before these commands.
The executable requires an existing directory outside the Worker bundle and
refuses a symlink root.

```text
karte-experiment prepare -data-root SYNTHETIC_DATA_DIR -worker-bundle testdata/worker-experiment-v1 -metadata testdata/worker-experiment-v1/metadata.json
karte-experiment publish -data-root SYNTHETIC_DATA_DIR -candidate-id synthetic-candidate-001
karte-experiment status -data-root SYNTHETIC_DATA_DIR -candidate-id synthetic-candidate-001
```

The executable exposes only `prepare`, `publish`, and `status`. It has no
accept/grant/configure/adopt/canonical-write command. Its writes are confined to
producer bindings, derived evidence copies, and pending proposals below
`.mdsys/ephy`. Original Worker bundles and metadata are read-only. Existing
authentication, registration, privacy policy, permissions, and canonical files
are not changed. The root handle is retained for the producer's lifetime.
Prepare checks the actual source/data root identities. On Windows it resolves
the already-open handles to GUID volume paths (normalized DOS/UNC paths for
remote shares), preserving identity through volume/path aliases before deciding
whether roots on separate volumes are outside one another.
Linux uses the opened directory's `/proc/self/fd` path and macOS uses F_GETPATH.
No platform reopens `Root.Name()` to decide source containment; this display
name can be relative or stale after a rename. A data object moved into the
Worker bundle is refused even if its old pathname now names another directory.

## Input contract

Metadata uses `adapter_version: karte.worker-experiment.v1` and requires
`synthetic_only: true`. It supplies candidate/experiment/run/attempt IDs,
target_commit, environment, model, checker, worker_result, observed facts,
interpretation, halt_reason, project, title, and a fixed RFC3339 `reported_at`.
The fixture lists every field. Unknown or duplicate JSON keys, trailing JSON,
invalid UTF-8, and missing required values are rejected.

`experiment_id` must equal manifest `audit_id`; `run_id` must equal `job_id`.
IDs must also fit the narrower Karte v0.1 contract. No ID truncation or inferred
identity is performed. `reported_at` is supplied once by metadata and is never
replaced with the current clock; it determines the proposal date and month.

The manifest must contain all 22 artifact IDs in the Worker v1 schema's order.
Each file's exact bytes, SHA256, and size are checked before any managed write.
The candidate_patch artifact supplies `patch_sha256`. Source paths must be
portable relative ASCII paths: no links, traversal, case aliases, duplicate
paths, file/directory collisions, device names, or trailing dot/space segments.
This adapter deliberately supports a stricter path subset than the Worker
schema. Unreferenced source files are left untouched and are not imported.
Metadata and manifest each have a 256 KiB limit, each artifact an 8 MiB limit,
and the artifacts together a 32 MiB limit. Sizes are checked before allocation.

The adapter always sets `state=experiment`, `verification=unverified`,
`kind=report`, `sensitivity=internal`, and `operation=create`.
`external_review_pending`, `strict_pass`, and `halted` remain unverified Worker
result labels in observed facts. No Worker result promotes verification.

## Payload binding and recovery

`prepare` validates the full source snapshot, deterministically builds the
record/proposal, and atomically binds the candidate ID to that complete payload
in `.mdsys/ephy/experiment-producer/CANDIDATE.json`. Binding entries cover raw
metadata, raw manifest, and every artifact's hash and size. The binding includes
the complete generated record and proposal. `ExperimentPublisher` then stores
derived immutable evidence under `.mdsys/ephy/experiments/CANDIDATE`.
`prepare` does not queue a proposal or write canonical content.

An identical retry is idempotent. Any different metadata, fixed timestamp,
manifest bytes, artifact bytes, generated record, or proposal under the same ID
is rejected, including after a receipt. The binding remains after acceptance.
An outbox/evidence candidate without this producer's binding is refused.
If prepare stops after binding but before evidence completion, an identical
prepare retry can complete it; publish/status refuse incomplete preparation.
No abandoned artifacts from another operation are removed.

`publish` reconstructs and verifies the complete prepared payload and evidence
inventory, checks pending/processed proposals and receipts, then installs the
serialized proposal under `.mdsys/ephy/outbox/pending/CANDIDATE.json`.
A flushed same-directory temporary file is linked to the destination in one
exclusive filesystem operation, and its temporary name is removed. No rename
that can replace a destination is used. An identical concurrent publication is
idempotent; different existing content, a case alias, link, directory, or an
unsupported hard-link filesystem is refused without replacing the destination.
Candidate publication is serialized across producer instances and processes by
an OS file lock below `experiment-producer/.locks`. Named lock files remain in
place; the OS releases a held lock when its process exits. Once pending is
observed, retry only reads status and never reinstalls its pathname. This also
prevents a waiting first-time publisher from queuing a second copy after human
acceptance archives the first. Karte's acceptance authority and transaction are
unchanged; the producer does not acquire an acceptance or canonical-write lock.
Unix directory entries are fsynced; Windows flushes file handles and does not
claim POSIX directory-fsync support.

`status` validates the binding, original imported input bytes, evidence manifest,
and every available outbox proposal. V1.1 receipts do not contain a proposal
digest, so identity alone is insufficient: an accepted/rejected receipt must
have the exact matching archived proposal, matching candidate ID, derived
create doc ID when present, and matching project/report/month placement.
An accepted receipt returns `phase=report_accepted`, `state=experiment`,
`verification=unverified`, and `adopted=false`. Acceptance of a report records
evidence; it does not adopt a skill or improvement. Human edits during normal
Karte acceptance are permitted, and status does not require the current
canonical file to retain its historical receipt hash after later human edits.
The original archived proposal remains the payload association.

A receipt written just before its proposal is archived is reported as incomplete
and can be retried after normal Karte acceptance/recovery finishes. Publish
never accepts or repairs that human-review transaction. Status is read-only.

## Synthetic verification

```text
go test -race -count=1 ./internal/ephyoutbox ./cmd/karte-experiment
go test -count=1 . -run '^TestExperimentProducerSyntheticSaveFileRoundTrip$'
python scripts/verify_worker_experiment_fixture.py
```

The application test alone invokes existing `AcceptEphyProposal` and `SaveFile`
inside a temporary synthetic root. It proves prepare/publish do not write
canonical content, acceptance saves once, and producer receipt retry does not
save again. Additional regressions cover metadata changes after acceptance,
receipt/archive substitution, immutable evidence, bad manifest/hash/size/ID,
concurrent publishers, and the command authority boundary.
The accepted/pending race regression fixes the interleaving deterministically;
another native test uses a separately owned process waiting on publication while
synthetic acceptance finishes. Windows CI also verifies source and data roots
on different volumes; same-volume data inside the source bundle remains refused.
Backend CI validates the fixture against the pinned Worker JSON Schema using
its existing jsonschema dependency; no new installation or permission is added.
