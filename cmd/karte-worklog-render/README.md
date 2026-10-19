# Read-only worklog rendering bridge

This local helper renders one **existing derived Markdown view** with the
official pinned KarteRenderer. It is not a new Markdown parser or document
generator, a v2 record reader, a writer, a browser launcher, or the Karte GUI.

## Provenance

- The worklog service and generator come from the existing local Karte commit
  `9a46168ff7272185c3eca38d4fff645e836492b3` (`feat/work-records-daily-report`).
- That commit directly extends cached public main
  `68e245204fe2b2af5c21d271d5b8213691b8cbd7`. The specified main commit does **not**
  contain `internal/worklog` or `cmd/karte-worklog`. No upstream publication of
  this local extension is claimed.
- Both commits pin the official renderer dependency
  `github.com/kirimine170/KarteRenderer`, replaced by
  `github.com/kirimine170/Karte_renderer v0.0.0-20260812100243-30e5dfbab03f`
  (full commit `30e5dfbab03f92e2eb7c8a67e17936579075fe88`).

## Fixed protocol

Invoke `karte-worklog-render.exe --root <configured worklog root>` with a single
UTF-8 JSON object on stdin. The unchanged schema-1 request is
`{"task_id":"selected-task-id"}`. The explicit snapshot request is exactly
`{"schema_version":2,"operation":"snapshot","task_id":"selected-task-id"}`.
`schema_version` must be the JSON integer `2` (not `2.0`, an exponent or string).
The request limit is 1 KiB. Unknown keys, case aliases, duplicate decoded keys,
invalid UTF-8 and trailing input are rejected. No filename, executable, format,
URL, theme, or command is accepted.

A successful response contains `schema_version:1`, `ok:true`, `task_id`,
`doc_id`, `relative_path`, `source_sha256` (authoritative ledger bytes),
`markdown_sha256` (existing derived document bytes), `renderer_commit`, `html`,
and the renderer's `metadata` with `schemaVersion:1`. The total stdout limit is
2 MiB including its final newline; larger results become `output_limit`, never
truncated documents.

A successful snapshot response uses `schema_version:2` with all the same fields
and adds `markdown_base64`: standard padded Base64 of the exact authorized
`ReadView.Markdown` bytes that were passed to the existing renderer. Decoded bytes
match `markdown_sha256`; HTML and metadata are unchanged, including the renderer
metadata's own `schemaVersion:1`. The base64 bytes count toward the same total
2 MiB stdout limit. No Markdown is returned if rendering fails.

Domain failures return exit 0 and exactly `schema_version`, `ok:false`, and
`error`. Invalid arguments or any invalid/unaccepted request always return the
existing schema-1 `invalid_request` envelope. After a strictly valid snapshot
request is accepted, all later read/render failures and `output_limit` use schema
2. A schema-1 success is never a snapshot success. Clients must fail closed on a
version mismatch; they may report a schema-1 `invalid_request` received for a
malformed snapshot request, but must not retry it as a schema-1 record read. Codes are `invalid_request`, `permission_denied`, `invalid_record`,
`view_missing`, `view_conflict`, `v2_conflict`, `read_failed`, `render_failed`,
or `output_limit`. A stdout write failure exits nonzero. No raw diagnostic,
record body, root path, or private exception is emitted on failure.

## Boundaries

Every call rechecks the configured local worklog read policy under the existing
canonical lease. Its authoritative source is `.mdsys/worklog/v1/tasks/<id>.json`;
the Markdown at `content/projects/<project>/note/worklog-<id>.md` is a generated
view. The existing generator verifies the view and prior CAS view state. Missing,
old, edited or invalid documents are refused without repair or regeneration.
Snapshot protocol version 2 follows this same read policy and v1 worklog path;
it does not enable worklog-v2 access. It does not copy or write a native-app root.
The caller must revalidate current authorization and identity before making any
user-authorized private copy and must never use stored bytes as a denial fallback.

The v2 ledger membership guard reads only bounded ownership metadata and rejects
collisions or malformed state. It never reads a v2 document or grants v2 access.
Rendering uses the pinned library's result API with a memory filesystem exposing
only the verified Markdown. OS filesystem fallback, themes, imports, active
links/assets, Marp, external renderer processes, and network access are excluded.
The canonical lease may use its existing lock file; no record/config write API
is called by the helper.

The returned HTML is not trusted executable content. The caller must provide a
sandboxed, script-free, network-free view with request-time authorization and
expiration. The helper does not make HTML safe by collecting metadata alone.
It never starts a GUI or browser. Existing writer CLI configuration is unchanged.

## Local validation and build

Use the frozen offline Go environment, existing module cache, and external build
cache; do not update `go.mod`, `go.sum` or download dependencies.

```text
go test ./internal/worklog ./internal/ephyrecordsv2 ./cmd/karte-worklog-render
go vet ./internal/worklog ./internal/ephyrecordsv2 ./cmd/karte-worklog-render
go build -trimpath -o <new artifact path>/karte-worklog-render.exe ./cmd/karte-worklog-render
```

Package the helper only with a provenance receipt binding its SHA-256, final
local Karte patch hash, base commit, Go version, and the official renderer pin.
