# Renderer migration audit

This document records the rendering call paths that remain in Karte after the
`Karte_renderer` dependency integration. It is the removal checklist for
T-002/T-003 and the scope definition for the cross-repository contract tests in
T-004.

## Active production paths

| Karte entry point | Renderer API | Karte-owned work before/after rendering | Status |
| --- | --- | --- | --- |
| `App.PreviewMarkdown` / `App.PreviewMarkdownForPath` | `RenderString` | Legacy Marp detection, graph/version warnings, image URL rewriting, printout preview metadata | Migrated |
| `App.build` | `RenderMarkdown` | Walk content, write public HTML, build the index, atomically replace output | Migrated |
| `App.ExportPDF` / `exportHTMLToPDFWithRenderer` | `ExportHTMLPDF` | Resolve images, wait for printout pagination, create temporary HTML, emit progress | Migrated |
| `scripts/update-karte-renderer.sh` | Go module dependency | Pin a tested pseudo-version, test Renderer, then test Karte | Migrated |

Karte still parses front matter in `internal/frontmatter` before previewing.
That parser drives App-specific decisions and graph metadata, so it is not a
replacement renderer and must remain until the App/Renderer metadata contract
is expanded.

## Legacy implementations

| Path | Current callers | Decision | Removal gate |
| --- | --- | --- | --- |
| `internal/site` | No production caller | Remove; it duplicates Markdown, front matter, layout, CSV import, and KaTeX rendering | T-004 contract fixtures pass on all supported CI jobs |
| `internal/marp` | No production caller | Remove; it is a partial Marp parser/renderer superseded by Renderer | Marp fixture passes and no package import is reintroduced |
| `internal/pdf` | No production caller | Remove; the Windows wkhtmltopdf path is superseded by `ExportHTMLPDF` | PDF adapter test passes on supported OS builds |

The packages remain temporarily so removal can be reviewed separately from the
dependency migration. New production code must not import them.

## Contract coverage

`app_renderer_integration_test.go` and `testdata/renderer-contract` exercise the
dependency through the API Karte imports:

- Markdown file rendering and front matter
- nested Markdown import
- selected CSV columns
- display TeX import
- Marp selection and slide boundaries
- PDF adapter input, options, output, and temporary-file cleanup

Renderer owns deeper parser, path-confinement, browser-command, and golden
fixture coverage. Karte owns only the boundary assertions required by its App
entry points.

## T-003 removal sequence

1. Run the Renderer dependency tests and Karte's contract fixtures on the
   macOS, Windows, and Linux CI matrix.
2. Delete `internal/site`, `internal/marp`, and `internal/pdf`.
3. Remove dependencies that become unused after `go mod tidy`.
4. Run preview, public build, Marp, and PDF regression tests.
5. Confirm no production import of the deleted packages exists.
