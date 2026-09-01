# Stable doc_id becomes the UI identity while filenames remain stable

## Status

Proposed．Separate backlog work，not part of Karte–Ephy V1.1 acceptance．

## Context

Karte already assigns `doc_id` lazily and maintains a `doc_id` to path map，but not every canonical file is guaranteed to have a valid unique identifier before use．Some UI and file operations still expose the physical filename as document identity，and title edits may be confused with rename expectations．Renaming physical files creates Git churn and can break path-based consumers even when the logical document is unchanged．

## Proposed decision

Every canonical Markdown document has one immutable，unique `doc_id`．The UI uses frontmatter `title` as the visible name and `doc_id` as the logical key．Editing `title` never renames or moves the file．The physical filename is assigned once at creation and remains stable unless the user invokes a future explicit move operation．Links，selection，history，index identity，and conflict detection resolve through `doc_id` first and treat the path as current storage metadata．

## Migration questions

- How missing，duplicate，or malformed `doc_id` values are repaired without silent identity changes．
- Whether existing path-based links are rewritten，resolved through a compatibility index，or both．
- How non-Markdown resources receive stable identities．
- How an explicit move remains Git-aware，reviewable，and recoverable．
- When filename visibility should remain available in Developer Mode and diagnostics．

## Acceptance boundary

The future implementation must inventory all canonical files，produce a dry-run migration report，reject duplicate identity，keep title-only edits path-stable，and prove that search，graph，board，preview，Git history，and Ephy append continue to resolve the same logical document．

## Date

2026-09-01
