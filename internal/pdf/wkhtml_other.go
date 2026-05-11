//go:build !windows

package pdf

// EnsureWKHTMLToPDFAvailable is a no-op on platforms that do not use wkhtmltopdf.
func EnsureWKHTMLToPDFAvailable(_ string) error {
	return nil
}
