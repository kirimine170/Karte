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
			"<th>Metric</th>",
			"<td>rendered</td>",
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
	})

	t.Run("marp slides", func(t *testing.T) {
		html, frontMatter, err := karterenderer.RenderMarkdown(root, "slides.md")
		if err != nil {
			t.Fatal(err)
		}
		if !frontMatter.Marp {
			t.Fatal("renderer did not preserve the Marp front matter contract")
		}
		if got := strings.Count(html, `class="marp-slide"`); got != 2 {
			t.Fatalf("renderer returned %d Marp slides, want 2:\n%s", got, html)
		}
		for _, want := range []string{"<h1>First slide</h1>", "<h1>Second slide</h1>"} {
			if !strings.Contains(html, want) {
				t.Fatalf("renderer contract output is missing %q:\n%s", want, html)
			}
		}
	})
}
