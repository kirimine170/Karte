package pdf

// finiteFallbackPDFConfigDimensions exposes the current finite-mode fallback
// createPDF configuration so it can be regression-tested from Go.
func finiteFallbackPDFConfigDimensions(pageWidthPt, pageHeightPt float64) (float64, float64) {
	// Use explicit paper size in finite mode so WebKit paginates to the target
	// printout dimensions instead of capturing a single long page.
	if pageWidthPt > 0 && pageHeightPt > 0 {
		return pageWidthPt, pageHeightPt
	}
	return 0, 0
}
