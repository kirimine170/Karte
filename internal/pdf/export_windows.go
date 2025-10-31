//go:build windows

package pdf

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	fpdf "github.com/jung-kurt/gofpdf"
)

// ExportHTMLToPDF generates a PDF at outPath using gofpdf with UTF-8 font embedding.
// Note: HTMLはgofpdfの簡易HTMLサポート範囲（基本タグのみ）で描画されます。
func ExportHTMLToPDF(html string, outPath string) error {
	if strings.TrimSpace(html) == "" {
		return fmt.Errorf("empty html")
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return fmt.Errorf("failed to create output dir: %v", err)
	}

	fontPath, err := resolveJPFontPath()
	if err != nil {
		return err
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	// 日本語対応フォントを埋め込み
	// gofpdf は AddUTF8Font の戻り値を返さないため、エラーは後段の OutputFileAndClose で検出される
	pdf.AddUTF8Font("Noto", "", fontPath)
	pdf.SetFont("Noto", "", 12)
	pdf.SetAutoPageBreak(true, 15)
	pdf.SetMargins(15, 15, 15)
	pdf.AddPage()

	// 安全性重視: HTMLを簡易テキストに変換して描画（gofpdfのHTMLパーサは限定的でパニックすることがある）
	text := simplifyHTMLToText(html)
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
			// Reject TTC: gofpdf does not support TrueType Collection
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
	// Try common Windows built-in Japanese fonts
	winFontDir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	winCandidates := []string{
		// Yu Gothic / Yu Mincho (TrueType Collection on most systems)
		filepath.Join(winFontDir, "YuGothR.ttc"),
		filepath.Join(winFontDir, "YuGothM.ttc"),
		filepath.Join(winFontDir, "YuMincho.ttc"),
		// Meiryo
		filepath.Join(winFontDir, "meiryo.ttc"),
		filepath.Join(winFontDir, "meiryob.ttc"),
		// MS Gothic / MS Mincho
		filepath.Join(winFontDir, "msgothic.ttc"),
		filepath.Join(winFontDir, "msmincho.ttc"),
		// Noto CJK (if installed via optional features or manually)
		filepath.Join(winFontDir, "NotoSansCJKjp-Regular.otf"),
		filepath.Join(winFontDir, "NotoSansJP-Regular.ttf"),
	}
	foundTTC := false
	for _, p := range winCandidates {
		if fileExists(p) {
			ext := strings.ToLower(filepath.Ext(p))
			if ext == ".ttc" {
				foundTTC = true
				continue
			}
			// Accept only TTF/OTF
			if ext == ".ttf" || ext == ".otf" {
				return p, nil
			}
		}
	}
	// フォント未配置の明確なエラー
	if foundTTC {
		return "", fmt.Errorf("Found only Windows TTC fonts (unsupported by gofpdf). Please set KARTE_PDF_FONT to a Japanese TTF/OTF (e.g., NotoSansJP-Regular.ttf/.otf) or place it under themes/default.")
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
	// Remove script/style blocks
	reScript := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	s = reScript.ReplaceAllString(s, "")
	reStyle := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	s = reStyle.ReplaceAllString(s, "")
	reHead := regexp.MustCompile(`(?is)<head[^>]*>.*?</head>`)
	s = reHead.ReplaceAllString(s, "")

	// Replace common block tags with newlines
	blockTags := []string{"p", "div", "section", "article", "h1", "h2", "h3", "h4", "h5", "h6", "li", "ul", "ol", "br"}
	for _, tag := range blockTags {
		reOpen := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>`) // opening tag
		reClose := regexp.MustCompile(`(?is)</` + tag + `>`)      // closing tag
		s = reOpen.ReplaceAllString(s, "\n")
		s = reClose.ReplaceAllString(s, "\n")
	}

	// Strip remaining tags
	reTags := regexp.MustCompile(`(?is)<[^>]+>`) // any tag
	s = reTags.ReplaceAllString(s, "")

	// Unescape HTML entities
	s = html.UnescapeString(s)

	// Normalize newlines and trim excessive blank lines
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" {
			// collapse multiple empty lines
			if len(out) == 0 || out[len(out)-1] != "" {
				out = append(out, "")
			}
			continue
		}
		out = append(out, t)
	}
	return strings.Join(out, "\n")
}
