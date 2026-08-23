package main

import (
	"context"
	"crypto/sha256"
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

	"karte/internal/compliance"
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
		if isDarwinTarget(t.Name) {
			if err := materializeDarwinInfoPlist(projectRoot); err != nil {
				log.Fatalf("failed to materialize macOS Info.plist for %s: %v", t.Name, err)
			}
		}

		if err := runWailsBuild(ctx, t); err != nil {
			log.Fatalf("wails build failed for %s: %v", t.Name, err)
		}

		if err := moveArtifacts(t.ArtifactDir); err != nil {
			log.Fatalf("failed to move artifacts for %s: %v", t.Name, err)
		}
		if isWindowsTarget(t) {
			if err := packageTemplateIntoArtifact(projectRoot, t.ArtifactDir); err != nil {
				log.Fatalf("failed to package karte_data_template for %s: %v", t.Name, err)
			}
			if err := packageWindowsRuntimeDLLs(ctx, t.ArtifactDir); err != nil {
				log.Fatalf("failed to package Windows runtime DLLs for %s: %v", t.Name, err)
			}
			if err := packageWindowsRuntimeLicenses(ctx, t.ArtifactDir); err != nil {
				log.Fatalf("failed to package Windows runtime licenses for %s: %v", t.Name, err)
			}
			if err := packageWindowsFFmpeg(ctx, t.ArtifactDir); err != nil {
				log.Fatalf("failed to package Windows FFmpeg for %s: %v", t.Name, err)
			}
		}

		// macOS 向けビルドでは、templateをコピーする
		if isDarwinTarget(t.Name) {
			if err := packageTemplateIntoAppBundle(projectRoot, t.ArtifactDir); err != nil {
				log.Fatalf("failed to package karte_data_template for %s: %v", t.Name, err)
			}
		}
		if err := packageComplianceDocuments(projectRoot, t); err != nil {
			log.Fatalf("failed to package compliance documents for %s: %v", t.Name, err)
		}
		artifactRoot, artifactPlatform := complianceArtifactRoot(t)
		if err := compliance.PackageArtifactAssets(projectRoot, artifactRoot, artifactPlatform); err != nil {
			log.Fatalf("failed to package artifact asset inventory for %s: %v", t.Name, err)
		}

		fmt.Printf("✅ %s artifacts stored in %s\n", t.Name, t.ArtifactDir)
	}

	fmt.Println("All requested targets built successfully.")
}

func complianceArtifactRoot(buildTarget target) (string, string) {
	if isDarwinTarget(buildTarget.Name) {
		return filepath.Join(buildTarget.ArtifactDir, "Karte.app"), "darwin"
	}
	if isWindowsTarget(buildTarget) {
		return buildTarget.ArtifactDir, "windows"
	}
	return buildTarget.ArtifactDir, "linux"
}

func materializeDarwinInfoPlist(projectRoot string) error {
	source := filepath.Join(projectRoot, "templates", "macos", "Info.plist")
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("inspect macOS Info.plist template: %w", err)
	}
	if !sourceInfo.Mode().IsRegular() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("macOS Info.plist template is not a regular file: %s", source)
	}
	contents, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read macOS Info.plist template: %w", err)
	}

	destinationDirectory := filepath.Join(projectRoot, "build", "darwin")
	if err := os.MkdirAll(destinationDirectory, 0o755); err != nil {
		return fmt.Errorf("create Wails macOS build assets directory: %w", err)
	}
	temporary, err := os.CreateTemp(destinationDirectory, ".Info.plist-*")
	if err != nil {
		return fmt.Errorf("create temporary Wails Info.plist: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(sourceInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("set temporary Wails Info.plist permissions: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		return fmt.Errorf("write temporary Wails Info.plist: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync temporary Wails Info.plist: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary Wails Info.plist: %w", err)
	}

	destination := filepath.Join(destinationDirectory, "Info.plist")
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace existing Wails Info.plist: %w", err)
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("install Wails Info.plist: %w", err)
	}
	return nil
}

func packageComplianceDocuments(projectRoot string, buildTarget target) error {
	documentRoot := buildTarget.ArtifactDir
	if isDarwinTarget(buildTarget.Name) {
		documentRoot = filepath.Join(buildTarget.ArtifactDir, "Karte.app", "Contents", "Resources")
		info, err := os.Stat(documentRoot)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("macOS Resources directory is unavailable: %s", documentRoot)
		}
	}
	files := map[string]string{
		"LICENSE":                "LICENSE",
		"THIRD_PARTY_NOTICES.md": "THIRD_PARTY_NOTICES.md",
		"bom.cdx.json":           "bom.cdx.json",
		filepath.Join("compliance", "assets.json"):     filepath.Join("compliance", "assets.json"),
		filepath.Join("compliance", "components.json"): filepath.Join("compliance", "components.json"),
	}
	for destination, source := range files {
		sourcePath := filepath.Join(projectRoot, source)
		info, err := os.Lstat(sourcePath)
		if err != nil {
			return fmt.Errorf("required compliance source %s: %w", source, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("required compliance source %s is not a regular file", source)
		}
		if err := copyFile(sourcePath, filepath.Join(documentRoot, destination)); err != nil {
			return fmt.Errorf("copy compliance source %s: %w", source, err)
		}
	}
	return nil
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
	wailsBinary := strings.TrimSpace(os.Getenv("KARTE_WAILS_BIN"))
	if wailsBinary == "" {
		wailsBinary = "wails"
	}
	env := t.Env
	if isWindowsTarget(t) {
		var err error
		env, err = withWindowsBuildPath(ctx, env)
		if err != nil {
			fmt.Printf("WARN: failed to configure Windows build PATH: %v\n", err)
		}
	}
	return runCommand(ctx, ".", env, wailsBinary, args...)
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

func packageWindowsRuntimeLicenses(ctx context.Context, artifactDir string) error {
	licenseDir := filepath.Join(artifactDir, "licenses")
	if err := os.MkdirAll(licenseDir, 0o755); err != nil {
		return fmt.Errorf("create Windows license directory: %w", err)
	}

	sherpaDir, err := goListModuleDir(ctx, "github.com/k2-fsa/sherpa-onnx-go-windows")
	if err != nil {
		return fmt.Errorf("locate sherpa-onnx-go-windows license: %w", err)
	}
	portAudioGoDir, err := goListModuleDir(ctx, "github.com/gordonklaus/portaudio")
	if err != nil {
		return fmt.Errorf("locate portaudio Go wrapper license: %w", err)
	}
	required := []struct {
		source string
		name   string
	}{
		{filepath.Join(sherpaDir, "LICENSE"), "sherpa-onnx-Apache-2.0.txt"},
		{filepath.Join(portAudioGoDir, "LICENSE"), "gordonklaus-portaudio-MIT.txt"},
	}
	for _, item := range required {
		if err := copyFile(item.source, filepath.Join(licenseDir, item.name)); err != nil {
			return fmt.Errorf("copy %s: %w", item.name, err)
		}
	}

	optional := []struct {
		envName string
		name    string
	}{
		{"KARTE_ONNXRUNTIME_LICENSE_FILE", "ONNXRuntime-MIT.txt"},
		{"KARTE_PORTAUDIO_LICENSE_FILE", "PortAudio-MIT.txt"},
	}
	for _, item := range optional {
		source := strings.TrimSpace(os.Getenv(item.envName))
		if source == "" {
			if os.Getenv("KARTE_REQUIRE_RUNTIME_LICENSES") == "1" {
				return fmt.Errorf("%s is required for release packaging", item.envName)
			}
			continue
		}
		if err := copyFile(source, filepath.Join(licenseDir, item.name)); err != nil {
			return fmt.Errorf("copy %s: %w", item.name, err)
		}
	}

	gccLicenseDir := strings.TrimSpace(os.Getenv("KARTE_GCC_RUNTIME_LICENSE_DIR"))
	if gccLicenseDir == "" {
		if os.Getenv("KARTE_REQUIRE_RUNTIME_LICENSES") == "1" {
			return errors.New("KARTE_GCC_RUNTIME_LICENSE_DIR is required for release packaging")
		}
		return nil
	}
	entries, err := os.ReadDir(gccLicenseDir)
	if err != nil {
		return fmt.Errorf("read GCC runtime license directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		target := filepath.Join(licenseDir, "GCC-runtime-"+entry.Name()+".txt")
		if err := copyFile(filepath.Join(gccLicenseDir, entry.Name()), target); err != nil {
			return fmt.Errorf("copy GCC runtime license %s: %w", entry.Name(), err)
		}
	}
	return nil
}

type windowsRuntimeManifest struct {
	SchemaVersion int                    `json:"schemaVersion"`
	Components    []windowsRuntimeBinary `json:"components"`
	Files         []windowsRuntimeFile   `json:"files"`
}

type windowsRuntimeBinary struct {
	Name               string `json:"name"`
	Version            string `json:"version"`
	SourceURL          string `json:"sourceUrl"`
	SourceCommit       string `json:"sourceCommit"`
	License            string `json:"license"`
	SHA256             string `json:"sha256"`
	BuildConfiguration string `json:"buildConfiguration"`
}

type windowsRuntimeFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

func packageWindowsFFmpeg(ctx context.Context, artifactDir string) error {
	binary := strings.TrimSpace(os.Getenv("KARTE_FFMPEG_BINARY"))
	if binary == "" {
		binary = strings.TrimSpace(os.Getenv("FFMPEG_PATH"))
	}
	if binary == "" {
		if os.Getenv("KARTE_REQUIRE_FFMPEG") == "1" {
			return errors.New("KARTE_FFMPEG_BINARY is required for release packaging")
		}
		fmt.Println("WARN: KARTE_FFMPEG_BINARY is not set; development artifact will not contain ffmpeg.exe")
		return nil
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() {
		return fmt.Errorf("FFmpeg binary is not a regular file: %s", binary)
	}

	destination := filepath.Join(artifactDir, "ffmpeg.exe")
	if err := copyFile(binary, destination); err != nil {
		return fmt.Errorf("copy FFmpeg binary: %w", err)
	}
	packagedFiles := []string{destination}
	if runtimeDir := strings.TrimSpace(os.Getenv("KARTE_FFMPEG_RUNTIME_DIR")); runtimeDir != "" {
		entries, err := os.ReadDir(runtimeDir)
		if err != nil {
			return fmt.Errorf("read FFmpeg runtime directory: %w", err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".dll") {
				continue
			}
			source := filepath.Join(runtimeDir, entry.Name())
			target := filepath.Join(artifactDir, entry.Name())
			if err := copyFile(source, target); err != nil {
				return fmt.Errorf("copy FFmpeg runtime %s: %w", entry.Name(), err)
			}
			packagedFiles = append(packagedFiles, target)
		}
	}
	licenseFile := strings.TrimSpace(os.Getenv("KARTE_FFMPEG_LICENSE_FILE"))
	if licenseFile == "" {
		return errors.New("KARTE_FFMPEG_LICENSE_FILE is required when bundling FFmpeg")
	}
	licenseDir := filepath.Join(artifactDir, "licenses")
	if err := os.MkdirAll(licenseDir, 0o755); err != nil {
		return fmt.Errorf("create license directory: %w", err)
	}
	if err := copyFile(licenseFile, filepath.Join(licenseDir, "FFmpeg-LGPL-2.1.txt")); err != nil {
		return fmt.Errorf("copy FFmpeg license: %w", err)
	}

	versionOutput, err := exec.CommandContext(ctx, destination, "-version").Output()
	if err != nil {
		return fmt.Errorf("read FFmpeg version: %w", err)
	}
	version := strings.TrimSpace(strings.SplitN(string(versionOutput), "\n", 2)[0])
	buildOutput, err := exec.CommandContext(ctx, destination, "-buildconf").CombinedOutput()
	if err != nil {
		return fmt.Errorf("read FFmpeg build configuration: %w: %s", err, strings.TrimSpace(string(buildOutput)))
	}
	configuration := strings.TrimSpace(string(buildOutput))
	content, err := os.ReadFile(destination)
	if err != nil {
		return fmt.Errorf("hash FFmpeg binary: %w", err)
	}
	digest := sha256.Sum256(content)
	manifest := windowsRuntimeManifest{
		SchemaVersion: 1,
		Components: []windowsRuntimeBinary{{
			Name:               "ffmpeg",
			Version:            version,
			SourceURL:          strings.TrimSpace(os.Getenv("KARTE_FFMPEG_SOURCE_URL")),
			SourceCommit:       strings.TrimSpace(os.Getenv("KARTE_FFMPEG_SOURCE_COMMIT")),
			License:            "LGPL-2.1-or-later",
			SHA256:             fmt.Sprintf("%x", digest),
			BuildConfiguration: configuration,
		}},
	}
	for _, packagedFile := range packagedFiles {
		packagedContent, err := os.ReadFile(packagedFile)
		if err != nil {
			return fmt.Errorf("hash packaged FFmpeg file %s: %w", packagedFile, err)
		}
		packagedDigest := sha256.Sum256(packagedContent)
		manifest.Files = append(manifest.Files, windowsRuntimeFile{
			Name:   filepath.Base(packagedFile),
			SHA256: fmt.Sprintf("%x", packagedDigest),
		})
	}
	if manifest.Components[0].SourceURL == "" || manifest.Components[0].SourceCommit == "" {
		return errors.New("KARTE_FFMPEG_SOURCE_URL and KARTE_FFMPEG_SOURCE_COMMIT are required when bundling FFmpeg")
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Windows runtime manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')
	if err := os.WriteFile(filepath.Join(artifactDir, "runtime-manifest.json"), manifestJSON, 0o644); err != nil {
		return fmt.Errorf("write Windows runtime manifest: %w", err)
	}
	return nil
}

func copyExistingDLL(src, artifactDir string) error {
	if _, err := os.Stat(src); err != nil {
		return fmt.Errorf("required DLL not found %s: %w", src, err)
	}
	dst := filepath.Join(artifactDir, filepath.Base(src))
	if _, err := os.Stat(dst); err == nil {
		_ = os.Chmod(dst, 0o666)
		if err := os.Remove(dst); err != nil {
			return fmt.Errorf("remove existing DLL %s: %w", dst, err)
		}
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	return os.Chmod(dst, 0o644)
}

func packageTemplateIntoArtifact(projectRoot, artifactDir string) error {
	templateSource := filepath.Join(projectRoot, "templates", "karte_data_template")
	if fi, err := os.Stat(templateSource); err != nil || !fi.IsDir() {
		return nil
	}
	templateTarget := filepath.Join(artifactDir, "karte_data_template")
	if err := os.MkdirAll(templateTarget, 0o755); err != nil {
		return fmt.Errorf("create template target %s: %w", templateTarget, err)
	}
	if err := copyDir(templateSource, templateTarget); err != nil {
		return fmt.Errorf("copy template from %s to %s: %w", templateSource, templateTarget, err)
	}
	return nil
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
