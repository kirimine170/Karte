package runtimepath

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
