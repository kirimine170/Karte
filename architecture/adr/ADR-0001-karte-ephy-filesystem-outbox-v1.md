# Karte–Ephy V1.1 uses a reviewed filesystem outbox and Karte-owned placement

## Status

Accepted

## Context

Karte owns canonical Markdown under `KARTE_DATA_DIR/content`．Ephy needs search access and a way to submit memory candidates without bypassing human review or Karte's existing persistence behavior．Karte T-021 rejects localhost HTTP，and Wails `file-changed` is an internal application notification rather than a repository boundary．

## Decision

The V1.1 formal boundary is a read-only filesystem adapter plus a reviewed outbox．Ephy reads only `content/**/*.md` without copying Karte content，and atomically publishes a versioned document candidate plus semantic placement hints to `.mdsys/ephy/outbox/pending`．Karte owns the final path，uses a project-first `<project>/<kind>/<YYYY-MM>` policy，shows its routing reason and alternatives，and requires an explicit accept，edit-and-accept，or reject action．Only acceptance calls the existing `SaveFile` path．Karte atomically publishes a receipt for Ephy．

API，localhost server，and Wails IPC are not V1.1 boundaries．Create carries a complete candidate document，while append carries only a frontmatter patch and Markdown fragment for document end．Move，rename，delete，forgetting，and arbitrary patch operations remain disabled．The machine-readable contract and cross-repository fixtures are under `schemas/karte-ephy/v1`．

## Consequences

Ephy never writes canonical content or chooses a final create path．Karte detects stale `base_sha256` values，`doc_id` mismatches，and project／kind mismatches before saving and does not use last-write-wins．Unresolved classification is returned to Ephy for user consultation before publication．Proposal and receipt publication use a same-filesystem temporary file，flush，and rename．A later boundary change requires an ADR and synchronized schema／fixture changes in both repositories．

## Related work

- Karte T-021，T-087，T-088，T-089
- Ephy T-097，T-106，T-107，T-108

## Date

2026-09-01
