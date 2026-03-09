//go:build darwin

package pdf

/*
extern void getFiniteFallbackPDFConfigDimensions(double pageWidthPt, double pageHeightPt, double* outWidthPt, double* outHeightPt);
*/
import "C"

//export getFiniteFallbackPDFConfigDimensions
func getFiniteFallbackPDFConfigDimensions(pageWidthPt C.double, pageHeightPt C.double, outWidthPt *C.double, outHeightPt *C.double) {
	widthPt, heightPt := finiteFallbackPDFConfigDimensions(float64(pageWidthPt), float64(pageHeightPt))
	if outWidthPt != nil {
		*outWidthPt = C.double(widthPt)
	}
	if outHeightPt != nil {
		*outHeightPt = C.double(heightPt)
	}
}
