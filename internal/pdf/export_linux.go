//go:build linux

package pdf

import "fmt"

// ExportHTMLToPDF renders HTML to a PDF at outPath using WebKitGTK (to be implemented)
func ExportHTMLToPDF(html string, outPath string, _ string, _ string, _ float64, _ float64) error {
	return fmt.Errorf("pdf export (linux) not implemented yet")
}
