package pdf

// finiteFallbackPDFConfigDimensions exposes the current finite-mode fallback
// createPDF configuration so it can be regression-tested from Go.
func finiteFallbackPDFConfigDimensions(pageWidthPt, pageHeightPt float64) (float64, float64) {
	_ = pageWidthPt
	_ = pageHeightPt
	return 0, 0
}
