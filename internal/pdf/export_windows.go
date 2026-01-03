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
	"time"

	_ "embed"
)

//go:embed fonts/NotoSansJP-Regular.ttf
var font []byte

// ExportHTMLToPDF generates a PDF at outPath using gofpdf with UTF-8 font embedding.
// プレビューHTMLをそのままのレイアウトで出力することはできませんが、内容はテキストとして安全に出力します。
func ExportHTMLToPDF(htmlStr string, outPath string, logPath string) error {
	//TODO 第三引数としてlogPath: stringを受け取り、すべてのログはそこに向かって吐き出す

	if strings.TrimSpace(htmlStr) == "" {
		if log_err := logPDFExport(logPath, "empty html"); log_err != nil {
			return fmt.Errorf("empty html\n%v", log_err)
		}
		return fmt.Errorf("empty html")
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		if log_err := logPDFExport(logPath, fmt.Sprintf("failed to create ouput dir: %v", err)); log_err != nil {
			return fmt.Errorf("failed to create output dir: %v\n%v", err, log_err)
		}
		return fmt.Errorf("failed to create output dir: %v", err)
	}

	tmpDir, err := os.MkdirTemp("", "karte-pdf-tmp")
	if err != nil {
		if log_err := logPDFExport(logPath, "PANIC! - Couldn't make tmp directory"); log_err != nil {
			panic(fmt.Sprintf("%v\nPANIC! - Couldn't make tmp directory", log_err))
		}
		panic("PANIC! - Couldn't make tmp directory")
	}
	defer os.RemoveAll(tmpDir)

	tmpHTML := filepath.Join(tmpDir, "input.html")
	if err := os.WriteFile(tmpHTML, []byte(htmlStr), 0o644); err != nil {
		if log_err := logPDFExport(logPath, "PANIC! - Failed to write tmp html"); log_err != nil {
			panic(fmt.Sprintf("%v\nPANIC! - Failed to write tmp html", log_err))
		}
		panic("PANIC! - Failed to write tmp html")
	}

	wkhtmlPath := "C:\\Program Files\\wkhtmltopdf\\bin\\wkhtmltopdf.exe" //HACK 仮実装

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
		if log_err := logPDFExport(logPath, fmt.Sprintf("wkhtmltopdf failed: %v, output: %s", err, string(out))); log_err != nil {
			return fmt.Errorf("wkhtmltopdf failed: %v, output: %s\n%v", err, string(out), log_err)
		}
		return fmt.Errorf("wkhtmltopdf failed: %v, output: %s", err, string(out))
	}

	// --- 7. PDF ファイルの存在チェック（念のため） -------------------------
	if st, err := os.Stat(outPath); err != nil {
		if log_err := logPDFExport(logPath, fmt.Sprintf("pdf not created: %v", err)); log_err != nil {
			return fmt.Errorf("pdf not created: %v\n%v", err, log_err)
		}
		return fmt.Errorf("pdf not created: %v", err)
	} else if st.Size() == 0 {
		if log_err := logPDFExport(logPath, fmt.Sprintf("pdf created but empty: %s", outPath)); log_err != nil {
			return fmt.Errorf("pdf created but empty: %s\n%v", outPath, log_err)
		}
		return fmt.Errorf("pdf created but empty: %s", outPath)
	}

	return nil
}

func logPDFExport(logPath string, msg string) error {
	file, f_err := os.OpenFile(logPath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)
	if f_err != nil {
		fmt.Println("File", logPath, "couldn't open,")
		return fmt.Errorf("File couldn't open")
	}
	defer file.Close() // 関数のreturnまで遅延し実行

	now := time.Now()
	timestamp := now.Format("2006-01-02T15:04:05-0700")

	_, err := fmt.Fprintf(file, "%s [DEBUG] %s", timestamp, msg) // TODO
	if err != nil {
		fmt.Println("Couldn't write log to file")
		return fmt.Errorf("Couldn't write log to file")
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
