//go:build windows

package pdf

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	_ "embed"

	fpdf "github.com/jung-kurt/gofpdf"
)

//go:embed fonts/NotoSansJP-Regular.ttf
var font []byte

// ExportHTMLToPDF generates a PDF at outPath using gofpdf with UTF-8 font embedding.
// プレビューHTMLをそのままのレイアウトで出力することはできませんが、内容はテキストとして安全に出力します。
func ExportHTMLToPDF(htmlStr string, outPath string) error {
	if strings.TrimSpace(htmlStr) == "" {
		return fmt.Errorf("empty html")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %v", err)
	}

	// fontPath, err := resolveJPFontPath()
	// if err != nil {
	// 	return err
	// }

	pdf := fpdf.New("P", "mm", "A4", "")
	// pdf.AddUTF8Font("Noto", "", fontPath)
	pdf.AddUTF8FontFromBytes("Noto", "", font)
	pdf.SetFont("Noto", "", 12)
	pdf.SetAutoPageBreak(true, 15)
	pdf.SetMargins(15, 15, 15)
	pdf.AddPage()

	text := simplifyHTMLToText(htmlStr)
	if strings.TrimSpace(text) == "" {
		text = "(no content)"
	}
	pdf.MultiCell(0, 6, text, "", "", false)

	if err := pdf.OutputFileAndClose(outPath); err != nil {
		return fmt.Errorf("failed to write pdf: %v", err)
	}
	return nil
}

// resolveJPFontPath tries to find a Japanese TTF/OTF font for embedding.
// Priority:
// 1) env KARTE_PDF_FONT
// 2) themes/default/NotoSansJP-Regular.ttf (relative to executable dir)
// 3) Windows default Japanese fonts under C:\\Windows\\Fonts (common candidates)
func resolveJPFontPath() (string, error) {
	if p := strings.TrimSpace(os.Getenv("KARTE_PDF_FONT")); p != "" {
		if fileExists(p) {
			ext := strings.ToLower(filepath.Ext(p))
			if ext == ".ttc" {
				return "", fmt.Errorf("font at KARTE_PDF_FONT is TTC and unsupported by gofpdf: %s", p)
			}
			return p, nil
		}
		return "", fmt.Errorf("KARTE_PDF_FONT not found: %s", p)
	}
	exe, _ := os.Executable()
	base := filepath.Dir(exe)
	cand := filepath.Join(base, "themes", "default", "NotoSansJP-Regular.ttf")
	if fileExists(cand) {
		return cand, nil
	}
	winFontDir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	winCandidates := []string{
		filepath.Join(winFontDir, "NotoSansCJKjp-Regular.otf"),
		filepath.Join(winFontDir, "NotoSansJP-Regular.ttf"),
	}
	for _, p := range winCandidates {
		if fileExists(p) {
			return p, nil
		}
	}
	return "", fmt.Errorf("Japanese font not found. Set KARTE_PDF_FONT to a TTF/OTF path or place NotoSansJP-Regular.ttf under themes/default.")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// simplifyHTMLToText removes scripts/styles/tags and converts basic blocks to newlines
func simplifyHTMLToText(in string) string {
	s := in
	reScript := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	s = reScript.ReplaceAllString(s, "")
	reStyle := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	s = reStyle.ReplaceAllString(s, "")
	reHead := regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	s = reHead.ReplaceAllString(s, "")

	blockTags := []string{"p", "div", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6", "li", "ul", "ol", "br"}
	for _, tag := range blockTags {
		reOpen := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>`)
		reClose := regexp.MustCompile(`(?is)</` + tag + `>`)
		s = reOpen.ReplaceAllString(s, "\n")
		s = reClose.ReplaceAllString(s, "\n")
	}

	reTags := regexp.MustCompile(`(?is)<[^>]+>`)
	s = reTags.ReplaceAllString(s, "")

	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			if len(out) == 0 || out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n")
}
