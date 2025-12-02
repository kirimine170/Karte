package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
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
	return runCommand(ctx, ".", t.Env, "wails", args...)
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
