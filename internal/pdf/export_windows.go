//go:build windows

package pdf

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "embed"
)

const (
	wkhtmltopdfVersion = "0.12.6-1"
	wkhtmltopdfURL     = "https://github.com/wkhtmltopdf/packaging/releases/download/0.12.6-1/wkhtmltox-0.12.6-1.msvc2015-win64.exe"
)

//go:embed fonts/NotoSansJP-Regular.ttf
var font []byte

var wkhtmltopdfInstallMu sync.Mutex

// EnsureWKHTMLToPDFAvailable prepares wkhtmltopdf on Windows so PDF export can run without manual installation.
func EnsureWKHTMLToPDFAvailable(logPath string) error {
	_, err := resolveWKHTMLToPDFPath(logPath)
	return err
}

// ExportHTMLToPDF generates a PDF at outPath using wkhtmltopdf.
// プレビューHTMLをそのままのレイアウトで出力することはできませんが、内容はテキストとして安全に出力します。
func ExportHTMLToPDF(htmlStr string, outPath string, logPath string, pageSize string, _ float64, _ float64) error {
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

	wkhtmlPath, err := resolveWKHTMLToPDFPath(logPath)
	if err != nil {
		if log_err := logPDFExport(logPath, fmt.Sprintf("failed to prepare wkhtmltopdf: %v", err)); log_err != nil {
			return fmt.Errorf("failed to prepare wkhtmltopdf: %v\n%v", err, log_err)
		}
		return fmt.Errorf("failed to prepare wkhtmltopdf: %v", err)
	}

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
	if size := strings.TrimSpace(strings.ToUpper(pageSize)); size != "" && !strings.EqualFold(size, "infinite") {
		args = append([]string{"--page-size", size}, args...)
	}

	cmd := exec.Command(wkhtmlPath, args...)

	out, err := cmd.CombinedOutput()
	if err != nil {
		// 典型的な失敗要因:
		// - 自動配置した wkhtmltopdf.exe を起動できない
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

func resolveWKHTMLToPDFPath(logPath string) (string, error) {
	if p := strings.TrimSpace(os.Getenv("KARTE_WKHTMLTOPDF_PATH")); p != "" {
		if fileExists(p) {
			return p, nil
		}
		return "", fmt.Errorf("KARTE_WKHTMLTOPDF_PATH not found: %s", p)
	}

	for _, p := range wkhtmltopdfCandidates() {
		if fileExists(p) {
			return p, nil
		}
	}

	if p, err := exec.LookPath("wkhtmltopdf.exe"); err == nil && fileExists(p) {
		return p, nil
	}

	return installWKHTMLToPDF(logPath)
}

func wkhtmltopdfCandidates() []string {
	candidates := []string{}
	if cacheExe, err := cachedWKHTMLToPDFPath(); err == nil {
		candidates = append(candidates, cacheExe)
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		if base := strings.TrimSpace(os.Getenv(env)); base != "" {
			candidates = append(candidates, filepath.Join(base, "wkhtmltopdf", "bin", "wkhtmltopdf.exe"))
			candidates = append(candidates, filepath.Join(base, "wkhtmltox", "bin", "wkhtmltopdf.exe"))
		}
	}
	return candidates
}

func installWKHTMLToPDF(logPath string) (string, error) {
	wkhtmltopdfInstallMu.Lock()
	defer wkhtmltopdfInstallMu.Unlock()

	exePath, err := cachedWKHTMLToPDFPath()
	if err != nil {
		return "", err
	}
	installDir := filepath.Dir(filepath.Dir(exePath))
	if fileExists(exePath) {
		return exePath, nil
	}
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create wkhtmltopdf install dir: %w", err)
	}

	installerPath := filepath.Join(installDir, "wkhtmltox-installer.exe")
	if err := downloadWKHTMLToPDFInstaller(installerPath, logPath); err != nil {
		return "", err
	}
	defer os.Remove(installerPath)

	if err := runWKHTMLToPDFInstaller(installerPath, installDir, logPath); err != nil {
		return "", err
	}
	if !fileExists(exePath) {
		return "", fmt.Errorf("wkhtmltopdf installer completed but %s was not created", exePath)
	}
	return exePath, nil
}

func cachedWKHTMLToPDFPath() (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to resolve user cache dir: %w", err)
	}
	return filepath.Join(cacheDir, "Karte", "wkhtmltopdf", wkhtmltopdfVersion, "bin", "wkhtmltopdf.exe"), nil
}

func downloadWKHTMLToPDFInstaller(installerPath string, logPath string) error {
	downloadURL := strings.TrimSpace(os.Getenv("KARTE_WKHTMLTOPDF_URL"))
	if downloadURL == "" {
		downloadURL = wkhtmltopdfURL
	}
	_ = logPDFExport(logPath, fmt.Sprintf("downloading wkhtmltopdf installer from %s", downloadURL))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create wkhtmltopdf download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download wkhtmltopdf installer: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("failed to download wkhtmltopdf installer: HTTP %s", resp.Status)
	}

	tmpPath := installerPath + ".download"
	out, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create wkhtmltopdf installer file: %w", err)
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to save wkhtmltopdf installer: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close wkhtmltopdf installer file: %w", closeErr)
	}
	if err := os.Rename(tmpPath, installerPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to move wkhtmltopdf installer into place: %w", err)
	}
	return nil
}

func runWKHTMLToPDFInstaller(installerPath string, installDir string, logPath string) error {
	_ = logPDFExport(logPath, fmt.Sprintf("installing wkhtmltopdf into %s", installDir))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, installerPath, "/S", "/D="+installDir)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("wkhtmltopdf installer timed out")
	}
	if err != nil {
		return fmt.Errorf("wkhtmltopdf installer failed: %w, output: %s", err, string(out))
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
