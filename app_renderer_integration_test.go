package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	karterenderer "github.com/kirimine170/KarteRenderer"
)

func TestExportHTMLToPDFWithRendererUsesTemporaryHTMLInput(t *testing.T) {
	originalExport := exportRendererHTMLPDF
	t.Cleanup(func() { exportRendererHTMLPDF = originalExport })

	var rendererInputPath string
	exportRendererHTMLPDF = func(_ context.Context, htmlPath, outputPath string, opts karterenderer.PDFOptions) error {
		rendererInputPath = htmlPath
		content, err := os.ReadFile(htmlPath)
		if err != nil {
			return err
		}
		if !strings.Contains(string(content), "renderer input") {
			t.Fatalf("unexpected renderer input: %s", content)
		}
		if opts.Engine != "auto" || !opts.AllowLocalFiles {
			t.Fatalf("unexpected renderer options: %+v", opts)
		}
		return os.WriteFile(outputPath, []byte("%PDF-1.4"), 0o644)
	}

	outputPath := filepath.Join(t.TempDir(), "output.pdf")
	if err := exportHTMLToPDFWithRenderer(context.Background(), "<p>renderer input</p>", outputPath); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		t.Fatalf("expected renderer output: %v", err)
	}
	if _, err := os.Stat(rendererInputPath); !os.IsNotExist(err) {
		t.Fatalf("temporary renderer input was not removed: %v", err)
	}
}

func TestKarteRendererDependencyContractFixtures(t *testing.T) {
	root := filepath.Join("testdata", "renderer-contract")

	t.Run("document imports", func(t *testing.T) {
		html, frontMatter, err := karterenderer.RenderMarkdown(root, "document.md")
		if err != nil {
			t.Fatal(err)
		}
		if frontMatter.Title != "Karte Renderer Contract" {
			t.Fatalf("unexpected title: %q", frontMatter.Title)
		}
		for _, want := range []string{
			"<h1>Karte Renderer Contract</h1>",
			"<h2>Imported summary</h2>",
			"<h3>Nested contract detail</h3>",
			`class="katex-display"`,
			`data-katex="score = 42"`,
		} {
			if !strings.Contains(html, want) {
				t.Fatalf("renderer contract output is missing %q:\n%s", want, html)
			}
		}
		if strings.Contains(html, "@import(") {
			t.Fatalf("renderer left an unresolved import:\n%s", html)
		}
		for _, excluded := range []string{"<th>Ignored</th>", "<td>not-selected</td>"} {
			if strings.Contains(html, excluded) {
				t.Fatalf("renderer ignored the CSV column selection and emitted %q:\n%s", excluded, html)
			}
		}
		lastCSVIndex := -1
		for _, want := range []string{
			"<th>Metric</th>",
			"<th>Value</th>",
			"<td>status</td>",
			"<td>rendered</td>",
		} {
			index := strings.Index(html, want)
			if index < 0 {
				t.Fatalf("renderer contract output is missing selected CSV content %q:\n%s", want, html)
			}
			if index <= lastCSVIndex {
				t.Fatalf("renderer emitted selected CSV content out of order at %q:\n%s", want, html)
			}
			lastCSVIndex = index
		}
	})

	t.Run("document imports with CRLF", func(t *testing.T) {
		source, err := os.ReadFile(filepath.Join(root, "document.md"))
		if err != nil {
			t.Fatal(err)
		}
		markdown := strings.ReplaceAll(string(source), "\r\n", "\n")
		markdown = strings.ReplaceAll(markdown, "\n", "\r\n")
		html, _, err := karterenderer.RenderString(root, markdown)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"<h2>Imported summary</h2>", "<h3>Nested contract detail</h3>"} {
			if !strings.Contains(html, want) {
				t.Fatalf("renderer CRLF contract output is missing %q:\n%s", want, html)
			}
		}
		if strings.Contains(html, "@import(") {
			t.Fatalf("renderer left an unresolved CRLF import:\n%s", html)
		}
	})

	t.Run("marp slides", func(t *testing.T) {
		html, frontMatter, err := karterenderer.RenderMarkdown(root, "slides.md")
		if err != nil {
			t.Fatal(err)
		}
		if !frontMatter.Marp {
			t.Fatal("renderer did not preserve the Marp front matter contract")
		}
		const slideMarker = `<section class="marp-slide">`
		remaining := html
		var slides []string
		for {
			start := strings.Index(remaining, slideMarker)
			if start < 0 {
				break
			}
			end := strings.Index(remaining[start:], "</section>")
			if end < 0 {
				t.Fatalf("renderer returned an unterminated Marp slide:\n%s", html)
			}
			end += start + len("</section>")
			slides = append(slides, remaining[start:end])
			remaining = remaining[end:]
		}
		if len(slides) != 2 {
			t.Fatalf("renderer returned %d Marp slides, want 2:\n%s", len(slides), html)
		}
		for index, want := range []string{"<h1>First slide</h1>", "<h1>Second slide</h1>"} {
			if !strings.Contains(slides[index], want) {
				t.Fatalf("renderer contract slide %d is missing %q:\n%s", index+1, want, slides[index])
			}
		}
	})
}
