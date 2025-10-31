//go:build windows

package pdf

import "fmt"

// ExportHTMLToPDF renders HTML to a PDF at outPath using WebView2 (to be implemented)
func ExportHTMLToPDF(html string, outPath string) error {
	return fmt.Errorf("pdf export (windows) not implemented yet")
}
