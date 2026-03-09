//go:build darwin

package pdf

import "testing"

func TestFiniteFallbackPDFConfigDimensionsPreserveRequestedPaperSize(t *testing.T) {
	widthPt, heightPt := finiteFallbackPDFConfigDimensions(595.0, 842.0)
	if widthPt != 595.0 || heightPt != 842.0 {
		t.Fatalf("finite fallback should preserve requested paper size, got %.1fpt x %.1fpt", widthPt, heightPt)
	}
}

func TestFiniteFallbackPDFConfigDimensionsDefaultWhenNoPaperSize(t *testing.T) {
	widthPt, heightPt := finiteFallbackPDFConfigDimensions(0, 0)
	if widthPt != 0 || heightPt != 0 {
		t.Fatalf("expected zero dimensions to keep default WebKit behavior, got %.1fpt x %.1fpt", widthPt, heightPt)
	}
}
