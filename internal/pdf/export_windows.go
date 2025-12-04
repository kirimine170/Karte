//go:build windows

package pdf

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	_ "embed"
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

	tmpDir, err := os.MkdirTemp("", "karte-pdf-tmp")
	if err != nil {
		panic("PANIC! - Coudln't make tmp directory")
	}
	defer os.RemoveAll(tmpDir)

	tmpHTML := filepath.Join(tmpDir, "input.html")
	if err := os.WriteFile(tmpHTML, []byte(htmlStr), 0o644); err != nil {
		panic("PANIC! - Failed to write tmp html")
	}

	wkhtmlPath := "C:\\Program Files\\wkhtmltopdf\\bin\\wkhtmltopdf.exe"

	args := []string{
		"--encoding", "utf-8",
		"--print-media-type",
		"--enable-local-file-access",
		// 必要であればここにマージンや DPI などのオプションを追加:
		// "--margin-top", "10mm",
		// "--margin-bottom", "15mm",
		// "--dpi", "300",
		tmpHTML,
		outPath,
	}

	cmd := exec.Command(wkhtmlPath, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		// 典型的な失敗要因:
		// - wkhtmltopdf.exe がインストールされていない / PATH にない
		// - HTML が巨大すぎる / 一部 CSS が原因でクラッシュ など
		return fmt.Errorf("wkhtmltopdf failed: %v, output: %s", err, string(out))
	}

	// --- 7. PDF ファイルの存在チェック（念のため） -------------------------
	if st, err := os.Stat(outPath); err != nil {
		return fmt.Errorf("pdf not created: %v", err)
	} else if st.Size() == 0 {
		return fmt.Errorf("pdf created but empty: %s", outPath)
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
