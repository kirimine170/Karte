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
