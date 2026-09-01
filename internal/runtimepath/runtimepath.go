package runtimepath

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	DataDirConfigName = ".karte-data-dir"
	runtimePIDPath    = ".mdsys/runtime/karte.pid"
)

// ConfiguredDataDir resolves an optional application-adjacent data directory
// pointer. This keeps a packaged Karte on the same workspace when it is opened
// again without the environment inherited from its launcher.
func ConfiguredDataDir(appPlacedDir string) (string, bool, error) {
	configPath := filepath.Join(appPlacedDir, DataDirConfigName)
	raw, err := os.ReadFile(configPath)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", true, fmt.Errorf("read data directory config: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", true, fmt.Errorf("data directory config is empty: %s", configPath)
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", true, fmt.Errorf("data directory config must contain one path: %s", configPath)
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(appPlacedDir, value)
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return "", true, fmt.Errorf("resolve configured data directory: %w", err)
	}
	return filepath.Clean(abs), true, nil
}

// WriteRuntimePID records which Karte process owns a data directory. Launchers
// use the data-root-local marker to avoid reusing a Karte opened on another
// workspace merely because its executable path is identical.
func WriteRuntimePID(dataDir string, pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("invalid runtime pid: %d", pid)
	}
	path := filepath.Join(dataDir, filepath.FromSlash(runtimePIDPath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create runtime marker directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".karte.pid.*")
	if err != nil {
		return "", fmt.Errorf("create runtime marker: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if _, err := fmt.Fprintf(tmp, "%d\n", pid); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write runtime marker: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close runtime marker: %w", err)
	}
	if err := replaceRuntimePID(tmpPath, path); err != nil {
		return "", fmt.Errorf("publish runtime marker: %w", err)
	}
	return path, nil
}

// ReadRuntimePID reads the data-root-local Karte process marker.
func ReadRuntimePID(dataDir string) (int, error) {
	path := filepath.Join(dataDir, filepath.FromSlash(runtimePIDPath))
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("invalid runtime marker %s", path)
	}
	return pid, nil
}

// RemoveRuntimePID removes the marker only when it still names this process.
// This prevents an older instance from clearing a newer instance's marker.
func RemoveRuntimePID(dataDir string, pid int) error {
	current, err := ReadRuntimePID(dataDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if current != pid {
		return nil
	}
	return os.Remove(filepath.Join(dataDir, filepath.FromSlash(runtimePIDPath)))
}

// DefaultDataDir returns the default root and workspace paths for a packaged
// application. Windows keeps writable state under LocalAppData; other
// platforms retain the existing application-adjacent workspace behaviour.
func DefaultDataDir(goos, appPlacedDir, localAppData string) (root, dataDir string, err error) {
	if goos != "windows" {
		return appPlacedDir, filepath.Join(appPlacedDir, "karte_data"), nil
	}
	localAppData = strings.TrimSpace(localAppData)
	if localAppData == "" {
		return "", "", fmt.Errorf("LOCALAPPDATA is not set")
	}
	root = filepath.Join(localAppData, "Karte")
	return root, filepath.Join(root, "karte_data"), nil
}

// MigrateLegacyDataDir copies an application-adjacent workspace into the new
// writable location. The source is never removed. A staging directory keeps a
// failed migration from being mistaken for a completed one on the next launch.
func MigrateLegacyDataDir(legacyDir, dataDir string) (bool, error) {
	legacyAbs, err := filepath.Abs(legacyDir)
	if err != nil {
		return false, fmt.Errorf("resolve legacy data directory: %w", err)
	}
	dataAbs, err := filepath.Abs(dataDir)
	if err != nil {
		return false, fmt.Errorf("resolve destination data directory: %w", err)
	}
	if strings.EqualFold(filepath.Clean(legacyAbs), filepath.Clean(dataAbs)) {
		return false, nil
	}
	if info, err := os.Stat(dataAbs); err == nil {
		if !info.IsDir() {
			return false, fmt.Errorf("destination data path is not a directory: %s", dataAbs)
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect destination data directory: %w", err)
	}
	legacyInfo, err := os.Stat(legacyAbs)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect legacy data directory: %w", err)
	}
	if !legacyInfo.IsDir() {
		return false, fmt.Errorf("legacy data path is not a directory: %s", legacyAbs)
	}

	staging := dataAbs + ".migrating"
	if err := os.RemoveAll(staging); err != nil {
		return false, fmt.Errorf("clear stale migration directory: %w", err)
	}
	if err := copyDir(legacyAbs, staging); err != nil {
		_ = os.RemoveAll(staging)
		return false, fmt.Errorf("copy legacy data directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dataAbs), 0o755); err != nil {
		_ = os.RemoveAll(staging)
		return false, fmt.Errorf("create data directory parent: %w", err)
	}
	if err := os.Rename(staging, dataAbs); err != nil {
		_ = os.RemoveAll(staging)
		return false, fmt.Errorf("finish data directory migration: %w", err)
	}
	return true, nil
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
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
		entryInfo, err := entry.Info()
		if err != nil {
			return err
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link migration is not supported: %s", srcPath)
		}
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath, entryInfo.Mode()); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
