package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"
)

// Override with -ldflags "-X main.karteUtilVersion=<version>" when packaging releases.
var karteUtilVersion = "dev"

type KarteUtilStatus struct {
	Path      string `json:"path"`
	Available bool   `json:"available"`
	Version   string `json:"version"`
}

type karteUtilManifest struct {
	UtilVersion string `json:"utilVersion"`
	BinaryName  string `json:"binaryName"`
	BinaryHash  string `json:"binaryHash"`
	InstalledAt string `json:"installedAt"`
}

func (a *App) GetKarteUtilCLIPath() KarteUtilStatus {
	return KarteUtilStatus{
		Path:      a.karteUtilPath,
		Available: a.karteUtilReady,
		Version:   karteUtilVersion,
	}
}

func (a *App) initializeKarteUtil() error {
	a.logInfo("Karte util bootstrap: start")

	utilDir := filepath.Join(a.dataDir, "karte_util")
	if err := os.MkdirAll(utilDir, 0o755); err != nil {
		a.karteUtilReady = false
		return fmt.Errorf("create karte_util dir: %w", err)
	}
	a.karteUtilDir = utilDir

	binaryName := karteUtilBinaryName()
	targetPath := filepath.Join(utilDir, binaryName)
	manifestPath := filepath.Join(utilDir, "manifest.json")

	bundledPath := a.findBundledKarteUtilBinaryPath(binaryName)
	if bundledPath == "" {
		if ok := ensureUtilExecutable(targetPath); ok {
			a.karteUtilPath = targetPath
			a.karteUtilReady = true
			a.logInfo(fmt.Sprintf("Karte util bootstrap: using existing local binary (%s)", targetPath))
			return nil
		}
		a.karteUtilPath = ""
		a.karteUtilReady = false
		a.logError("Karte util bootstrap: bundled binary not found and no local fallback")
		return nil
	}

	bundledHash, err := fileSHA256Hex(bundledPath)
	if err != nil {
		if ok := ensureUtilExecutable(targetPath); ok {
			a.karteUtilPath = targetPath
			a.karteUtilReady = true
			return fmt.Errorf("hash bundled util (using existing fallback): %w", err)
		}
		a.karteUtilPath = ""
		a.karteUtilReady = false
		return fmt.Errorf("hash bundled util: %w", err)
	}

	needsInstall := true
	if manifest, err := readKarteUtilManifest(manifestPath); err == nil {
		if manifest.UtilVersion == karteUtilVersion && manifest.BinaryName == binaryName && manifest.BinaryHash == bundledHash {
			if existingHash, hashErr := fileSHA256Hex(targetPath); hashErr == nil && existingHash == bundledHash {
				needsInstall = false
			}
		}
	} else if !os.IsNotExist(err) {
		a.logError(fmt.Sprintf("Karte util bootstrap: invalid manifest detected, reinstalling (%v)", err))
	}

	if needsInstall {
		a.logInfo("Karte util bootstrap: installing/updating bundled CLI")
		if err := installBundledUtil(bundledPath, targetPath); err != nil {
			if ok := ensureUtilExecutable(targetPath); ok {
				a.karteUtilPath = targetPath
				a.karteUtilReady = true
				return fmt.Errorf("install bundled util (using existing fallback): %w", err)
			}
			a.karteUtilPath = ""
			a.karteUtilReady = false
			return fmt.Errorf("install bundled util: %w", err)
		}

		manifest := karteUtilManifest{
			UtilVersion: karteUtilVersion,
			BinaryName:  binaryName,
			BinaryHash:  bundledHash,
			InstalledAt: time.Now().Format(time.RFC3339),
		}
		if err := writeKarteUtilManifest(manifestPath, manifest); err != nil {
			if ok := ensureUtilExecutable(targetPath); ok {
				a.karteUtilPath = targetPath
				a.karteUtilReady = true
				return fmt.Errorf("write util manifest (installed binary kept): %w", err)
			}
			a.karteUtilPath = ""
			a.karteUtilReady = false
			return fmt.Errorf("write util manifest: %w", err)
		}
	}

	if ok := ensureUtilExecutable(targetPath); !ok {
		a.karteUtilPath = ""
		a.karteUtilReady = false
		return fmt.Errorf("installed util is not executable: %s", targetPath)
	}

	a.karteUtilPath = targetPath
	a.karteUtilReady = true
	a.logInfo(fmt.Sprintf("Karte util bootstrap: ready (path=%s, version=%s)", targetPath, karteUtilVersion))
	return nil
}

func (a *App) findBundledKarteUtilBinaryPath(binaryName string) string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	exeDir := filepath.Dir(exePath)
	platformKeys := karteUtilPlatformKeys()

	var candidateBases []string
	if strings.HasSuffix(filepath.ToSlash(exeDir), "/Contents/MacOS") {
		candidateBases = append(candidateBases, filepath.Join(filepath.Dir(exeDir), "Resources"))
	}
	candidateBases = append(candidateBases, exeDir, a.root)

	for _, base := range candidateBases {
		for _, key := range platformKeys {
			candidate := filepath.Join(base, "karte_util_bundles", key, binaryName)
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	return ""
}

func karteUtilBinaryName() string {
	if goruntime.GOOS == "windows" {
		return "karte-cli.exe"
	}
	return "karte-cli"
}

func karteUtilPlatformKeys() []string {
	keys := []string{fmt.Sprintf("%s-%s", goruntime.GOOS, goruntime.GOARCH)}
	if goruntime.GOOS == "darwin" {
		keys = append(keys, "darwin-universal")
		if goruntime.GOARCH == "arm64" {
			keys = append(keys, "darwin-amd64")
		} else {
			keys = append(keys, "darwin-arm64")
		}
	}
	return keys
}

func readKarteUtilManifest(path string) (*karteUtilManifest, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var manifest karteUtilManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func writeKarteUtilManifest(path string, manifest karteUtilManifest) error {
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := fmt.Sprintf("%s.tmp.%d", path, time.Now().UnixNano())
	if err := os.WriteFile(tmpPath, body, 0o644); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil && goruntime.GOOS == "windows" {
		if err := os.Remove(path); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func installBundledUtil(srcPath, dstPath string) error {
	tmpPath := fmt.Sprintf("%s.tmp.%d", dstPath, time.Now().UnixNano())
	if err := copyFileWithMode(srcPath, tmpPath); err != nil {
		return err
	}
	if goruntime.GOOS != "windows" {
		if err := os.Chmod(tmpPath, 0o755); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if _, err := os.Stat(dstPath); err == nil && goruntime.GOOS == "windows" {
		if err := os.Remove(dstPath); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if goruntime.GOOS != "windows" {
		if err := os.Chmod(dstPath, 0o755); err != nil {
			return err
		}
	}
	return nil
}

func copyFileWithMode(srcPath, dstPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	info, err := src.Stat()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}
	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
	if err != nil {
		return err
	}
	defer dst.Close()
	if _, err := io.Copy(dst, src); err != nil {
		return err
	}
	return nil
}

func fileSHA256Hex(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func ensureUtilExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if goruntime.GOOS == "windows" {
		return true
	}
	if info.Mode().Perm()&0o111 == 0 {
		if err := os.Chmod(path, 0o755); err != nil {
			return false
		}
	}
	return true
}
