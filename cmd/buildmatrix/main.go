package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	targetFile = "build/targets.json"
	binDir     = "build/bin"
)

type target struct {
	Name        string            `json:"name"`
	Platform    string            `json:"platform"`
	ArtifactDir string            `json:"artifactDir"`
	Env         map[string]string `json:"env"`
	Flags       []string          `json:"flags"`
}

func main() {
	var (
		targetsFlag = flag.String("targets", "", "comma separated target names, e.g. windows,linux")
		allFlag     = flag.Bool("all", false, "build all targets defined in build/targets.json")
		prepFlag    = flag.Bool("prep", false, "run go mod tidy and npm install before building")
		cleanFlag   = flag.Bool("clean", false, "remove existing artifacts before building")
	)
	flag.Parse()

	if !*allFlag && strings.TrimSpace(*targetsFlag) == "" {
		log.Fatal("target is required: use --all or --targets")
	}

	ctx := context.Background()

	targets, err := loadTargets(targetFile)
	if err != nil {
		log.Fatalf("failed to read targets: %v", err)
	}

	selected, err := selectTargets(targets, *allFlag, *targetsFlag)
	if err != nil {
		log.Fatalf("failed to select targets: %v", err)
	}

	if *prepFlag {
		if err := runPrep(ctx); err != nil {
			log.Fatalf("prep failed: %v", err)
		}
	}

	// プロジェクトルート（現在の作業ディレクトリを前提とする）
	projectRoot, err := os.Getwd()
	if err != nil {
		log.Fatalf("failed to get working directory: %v", err)
	}

	for _, t := range selected {
		fmt.Printf("==> Building %s (%s)\n", t.Name, t.Platform)

		if *cleanFlag {
			if err := os.RemoveAll(t.ArtifactDir); err != nil {
				log.Fatalf("failed to clean artifact dir %s: %v", t.ArtifactDir, err)
			}
		}
		if err := os.MkdirAll(t.ArtifactDir, 0o755); err != nil {
			log.Fatalf("failed to create artifact dir %s: %v", t.ArtifactDir, err)
		}
		if err := os.RemoveAll(binDir); err != nil {
			log.Fatalf("failed to clean %s: %v", binDir, err)
		}

		if err := runWailsBuild(ctx, t); err != nil {
			log.Fatalf("wails build failed for %s: %v", t.Name, err)
		}

		if err := moveArtifacts(t.ArtifactDir); err != nil {
			log.Fatalf("failed to move artifacts for %s: %v", t.Name, err)
		}
		if isWindowsTarget(t) {
			if err := packageWindowsRuntimeDLLs(ctx, t.ArtifactDir); err != nil {
				log.Fatalf("failed to package Windows runtime DLLs for %s: %v", t.Name, err)
			}
		}

		// macOS 向けビルドでは、templateをコピーする
		if isDarwinTarget(t.Name) {
			if err := packageTemplateIntoAppBundle(projectRoot, t.ArtifactDir); err != nil {
				log.Fatalf("failed to package karte_data_template for %s: %v", t.Name, err)
			}
		}

		fmt.Printf("✅ %s artifacts stored in %s\n", t.Name, t.ArtifactDir)
	}

	fmt.Println("All requested targets built successfully.")
}

func loadTargets(path string) ([]target, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var targets []target
	if err := json.Unmarshal(data, &targets); err != nil {
		return nil, err
	}
	if len(targets) == 0 {
		return nil, errors.New("no targets defined")
	}
	return targets, nil
}

func selectTargets(all []target, wantAll bool, names string) ([]target, error) {
	if wantAll {
		return all, nil
	}
	requested := parseTargetNames(names)
	if len(requested) == 0 {
		return nil, errors.New("no target names provided")
	}
	index := make(map[string]target, len(all))
	for _, t := range all {
		index[t.Name] = t
	}
	var selected []target
	for _, name := range requested {
		t, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("unknown target %q", name)
		}
		selected = append(selected, t)
	}
	return selected, nil
}

func parseTargetNames(raw string) []string {
	chunks := strings.Split(raw, ",")
	var names []string
	for _, chunk := range chunks {
		name := strings.TrimSpace(chunk)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func runPrep(ctx context.Context) error {
	fmt.Println("==> Running go mod tidy")
	if err := runCommand(ctx, ".", nil, "go", "mod", "tidy"); err != nil {
		return err
	}

	// macOS ホストで PortAudio が未インストールの場合は、できるだけ自動で入れておく
	if runtime.GOOS == "darwin" {
		fmt.Println("==> Checking PortAudio installation (macOS host)")
		if err := ensurePortAudioOnDarwin(ctx); err != nil {
			// ここで失敗してもビルド自体は続行する（環境によっては brew が無いなどがあるため）
			fmt.Printf("WARN: failed to ensure PortAudio via brew: %v\n", err)
		}
	}

	// package-lock.jsonが存在する場合はnpm ciを使用（厳密にpackage-lock.jsonを守る）
	// 存在しない場合はnpm installを使用
	packageLockPath := filepath.Join("frontend", "package-lock.json")
	if _, err := os.Stat(packageLockPath); err == nil {
		fmt.Println("==> Running npm ci (frontend) - using package-lock.json")
		if err := runCommand(ctx, "frontend", nil, "npm", "ci"); err != nil {
			// npm ciが失敗した場合（例: package-lock.jsonが古い）、npm installにフォールバック
			fmt.Println("==> npm ci failed, falling back to npm install")
			return runCommand(ctx, "frontend", nil, "npm", "install")
		}
		return nil
	}

	fmt.Println("==> Running npm install (frontend)")
	return runCommand(ctx, "frontend", nil, "npm", "install")
}

// ensurePortAudioOnDarwin は、macOS ホストで PortAudio が入っていなさそうな場合に
// `brew install portaudio` を試みる。brew が無い / 失敗した場合は警告のみ。
func ensurePortAudioOnDarwin(ctx context.Context) error {
	// brew が存在するか確認
	if err := exec.Command("brew", "--version").Run(); err != nil {
		// brew が無ければ何もしない
		fmt.Println("brew not found; skip automatic PortAudio installation")
		return nil
	}

	fmt.Println("==> Ensuring PortAudio is installed via Homebrew (brew install portaudio)")
	// 失敗してもエラーを返すだけで、呼び出し側でワーニング扱いにする
	return runCommand(ctx, ".", nil, "brew", "install", "portaudio")
}

func runWailsBuild(ctx context.Context, t target) error {
	args := []string{"build", "-platform", t.Platform}
	args = append(args, t.Flags...)
	env := t.Env
	if isWindowsTarget(t) {
		var err error
		env, err = withWindowsBuildPath(ctx, env)
		if err != nil {
			fmt.Printf("WARN: failed to configure Windows build PATH: %v\n", err)
		}
	}
	return runCommand(ctx, ".", env, "wails", args...)
}

func isWindowsTarget(t target) bool {
	return strings.HasPrefix(t.Platform, "windows/") || t.Env["GOOS"] == "windows"
}

func withWindowsBuildPath(ctx context.Context, base map[string]string) (map[string]string, error) {
	env := copyEnvMap(base)
	var pathEntries []string

	if mingwBin := `C:\msys64\mingw64\bin`; dirExists(mingwBin) {
		pathEntries = append(pathEntries, mingwBin)
	}

	sherpaDir, err := goListModuleDir(ctx, "github.com/k2-fsa/sherpa-onnx-go-windows")
	if err == nil {
		dllDir := filepath.Join(sherpaDir, "lib", "x86_64-pc-windows-gnu")
		if dirExists(dllDir) {
			pathEntries = append(pathEntries, dllDir)
		}
	}

	if len(pathEntries) == 0 {
		return env, err
	}
	env["PATH"] = strings.Join(append(pathEntries, os.Getenv("PATH")), string(os.PathListSeparator))
	return env, nil
}

func copyEnvMap(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src)+1)
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func goListModuleDir(ctx context.Context, module string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", module)
	cmd.Dir = "."
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func packageWindowsRuntimeDLLs(ctx context.Context, artifactDir string) error {
	sherpaDir, err := goListModuleDir(ctx, "github.com/k2-fsa/sherpa-onnx-go-windows")
	if err != nil {
		return fmt.Errorf("locate sherpa-onnx-go-windows module: %w", err)
	}
	sherpaDLLDir := filepath.Join(sherpaDir, "lib", "x86_64-pc-windows-gnu")
	for _, name := range []string{
		"onnxruntime.dll",
		"sherpa-onnx-c-api.dll",
		"sherpa-onnx-cxx-api.dll",
	} {
		if err := copyExistingDLL(filepath.Join(sherpaDLLDir, name), artifactDir); err != nil {
			return err
		}
	}

	mingwBin := `C:\msys64\mingw64\bin`
	for _, name := range []string{
		"libportaudio.dll",
		"libgcc_s_seh-1.dll",
		"libstdc++-6.dll",
		"libwinpthread-1.dll",
	} {
		if err := copyExistingDLL(filepath.Join(mingwBin, name), artifactDir); err != nil {
			return err
		}
	}

	return nil
}

func copyExistingDLL(src, artifactDir string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("required DLL not found %s: %w", src, err)
	}
	dst := filepath.Join(artifactDir, filepath.Base(src))
	return copyFile(src, dst)
}

func moveArtifacts(destDir string) error {
	entries, err := os.ReadDir(binDir)
	if err != nil {
		return fmt.Errorf("read %s: %w", binDir, err)
	}
	if len(entries) == 0 {
		return fmt.Errorf("%s is empty", binDir)
	}
	for _, entry := range entries {
		src := filepath.Join(binDir, entry.Name())
		dst := filepath.Join(destDir, entry.Name())
		if err := os.RemoveAll(dst); err != nil {
			return fmt.Errorf("remove existing %s: %w", dst, err)
		}
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("move %s -> %s: %w", src, dst, err)
		}
	}
	return os.RemoveAll(binDir)
}

// isDarwinTarget reports whether the target name represents a macOS build.
// 現状の build/targets.json では "darwin", "darwin-arm64", "darwin-amd64" が対象。
func isDarwinTarget(name string) bool {
	return strings.HasPrefix(name, "darwin")
}

// packageTemplateIntoAppBundle replicates the packaging logic from the old package.sh:
// - Copy templates/karte_data_template into the build artifact directory
// - Overlay ASR models from karte_data/data/asr onto that template
// - Copy the resulting karte_data_template into .app/Contents/Resources
func packageTemplateIntoAppBundle(projectRoot, artifactDir string) error {
	appBundle := filepath.Join(artifactDir, "Karte.app")
	if fi, err := os.Stat(appBundle); err != nil || !fi.IsDir() {
		// macOS 以外、もしくは .app が存在しない場合は何もしない
		return nil
	}

	templateSource := filepath.Join(projectRoot, "templates", "karte_data_template")
	templateDir := filepath.Join(artifactDir, "karte_data_template")

	// テンプレートディレクトリをクリーンにしてコピー
	if err := os.RemoveAll(templateDir); err != nil {
		return fmt.Errorf("remove existing template dir %s: %w", templateDir, err)
	}
	if fi, err := os.Stat(templateSource); err == nil && fi.IsDir() {
		if err := copyDir(templateSource, templateDir); err != nil {
			return fmt.Errorf("copy template from %s to %s: %w", templateSource, templateDir, err)
		}
	} else {
		// テンプレートが無い場合は最低限のディレクトリだけ作る
		if err := os.MkdirAll(filepath.Join(templateDir, "data"), 0o755); err != nil {
			return fmt.Errorf("create empty template dir %s: %w", templateDir, err)
		}
	}

	// ASR モデルを karte_data からテンプレートへコピー（存在する場合のみ）
	asrSource := filepath.Join(projectRoot, "karte_data", "data", "asr")
	asrTarget := filepath.Join(templateDir, "data", "asr")
	if fi, err := os.Stat(asrSource); err == nil && fi.IsDir() {
		if err := os.MkdirAll(asrTarget, 0o755); err != nil {
			return fmt.Errorf("create asr target dir %s: %w", asrTarget, err)
		}
		if err := copyDir(asrSource, asrTarget); err != nil {
			return fmt.Errorf("copy ASR models from %s to %s: %w", asrSource, asrTarget, err)
		}
	}

	// .app バンドルの Resources 以下に karte_data_template を配置
	resourcesDir := filepath.Join(appBundle, "Contents", "Resources")
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		return fmt.Errorf("create Resources dir %s: %w", resourcesDir, err)
	}
	destTemplate := filepath.Join(resourcesDir, "karte_data_template")
	if err := os.RemoveAll(destTemplate); err != nil {
		return fmt.Errorf("remove existing %s: %w", destTemplate, err)
	}
	if err := copyDir(templateDir, destTemplate); err != nil {
		return fmt.Errorf("copy template into app bundle %s: %w", destTemplate, err)
	}

	// 一時的な templateDir を削除（Karte.app と同じ階層に残さない）
	if err := os.RemoveAll(templateDir); err != nil {
		return fmt.Errorf("remove temporary template dir %s: %w", templateDir, err)
	}

	return nil
}

// copyDir recursively copies a directory tree.
func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("source %s is not a directory", src)
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}

		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

// copyFile copies a single file, preserving its mode.
func copyFile(src, dst string) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	info, err := sf.Stat()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer df.Close()

	if _, err := io.Copy(df, sf); err != nil {
		return err
	}
	return nil
}

func runCommand(ctx context.Context, dir string, extraEnv map[string]string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = mergeEnv(os.Environ(), extraEnv)
	return cmd.Run()
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	envMap := make(map[string]string, len(base)+len(extra))
	for _, kv := range base {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}
	for k, v := range extra {
		envMap[k] = v
	}
	merged := make([]string, 0, len(envMap))
	for k, v := range envMap {
		merged = append(merged, fmt.Sprintf("%s=%s", k, v))
	}
	return merged
}
