package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type dataDirectoryKind string

const (
	dataDirectoryOverride dataDirectoryKind = "override"
	dataDirectoryDev      dataDirectoryKind = "development"
	dataDirectoryUser     dataDirectoryKind = "user"
	dataDirectoryLegacy   dataDirectoryKind = "legacy"
)

type dataDirectoryResolveOptions struct {
	GOOS                           string
	ExecutablePath                 string
	WorkingDirectory               string
	UserConfigDirectory            string
	Override                       string
	DevelopmentDataDirectoryExists bool
}

type dataDirectoryResolution struct {
	RootDirectory       string
	DataDirectory       string
	LegacyDataDirectory string
	Kind                dataDirectoryKind
}

// resolveDataDirectory is a pure path resolver. Filesystem and environment
// observations are supplied through options so every platform branch can be
// exercised on non-Darwin CI hosts.
func resolveDataDirectory(options dataDirectoryResolveOptions) (dataDirectoryResolution, error) {
	executablePath, err := absolutePathFrom(options.WorkingDirectory, options.ExecutablePath)
	if err != nil {
		return dataDirectoryResolution{}, fmt.Errorf("resolve executable path: %w", err)
	}
	workingDirectory := strings.TrimSpace(options.WorkingDirectory)
	if workingDirectory != "" {
		workingDirectory = filepath.Clean(workingDirectory)
	}
	executableDirectory := filepath.Dir(executablePath)
	appPlacedDirectory := executableDirectory
	_, bundleParent, bundled := macAppBundleLocation(executablePath)
	if bundled {
		appPlacedDirectory = bundleParent
	}

	if override := strings.TrimSpace(options.Override); override != "" {
		overridePath, err := absolutePathFrom(workingDirectory, override)
		if err != nil {
			return dataDirectoryResolution{}, fmt.Errorf("resolve KARTE_DATA_DIR: %w", err)
		}
		return dataDirectoryResolution{
			RootDirectory: filepath.Dir(overridePath),
			DataDirectory: overridePath,
			Kind:          dataDirectoryOverride,
		}, nil
	}

	if options.DevelopmentDataDirectoryExists && isWailsDevelopmentDirectory(appPlacedDirectory) {
		if workingDirectory == "." || workingDirectory == "" {
			return dataDirectoryResolution{}, errors.New("working directory is required for development data")
		}
		return dataDirectoryResolution{
			RootDirectory: workingDirectory,
			DataDirectory: filepath.Join(workingDirectory, "karte_data"),
			Kind:          dataDirectoryDev,
		}, nil
	}

	legacyDataDirectory := filepath.Join(appPlacedDirectory, "karte_data")
	if options.GOOS == "darwin" && bundled {
		userConfigDirectory := strings.TrimSpace(options.UserConfigDirectory)
		if userConfigDirectory == "" {
			return dataDirectoryResolution{}, errors.New("user config directory is required for a macOS app bundle")
		}
		userConfigDirectory, err = absolutePathFrom(workingDirectory, userConfigDirectory)
		if err != nil {
			return dataDirectoryResolution{}, fmt.Errorf("resolve user config directory: %w", err)
		}
		dataDirectory := filepath.Join(userConfigDirectory, "Karte")
		return dataDirectoryResolution{
			RootDirectory:       dataDirectory,
			DataDirectory:       dataDirectory,
			LegacyDataDirectory: legacyDataDirectory,
			Kind:                dataDirectoryUser,
		}, nil
	}

	return dataDirectoryResolution{
		RootDirectory: appPlacedDirectory,
		DataDirectory: legacyDataDirectory,
		Kind:          dataDirectoryLegacy,
	}, nil
}

func absolutePathFrom(workingDirectory, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("path is empty")
	}
	if filepath.IsAbs(value) {
		return filepath.Clean(value), nil
	}
	workingDirectory = strings.TrimSpace(workingDirectory)
	if workingDirectory == "" {
		return "", fmt.Errorf("relative path %q requires a working directory", value)
	}
	return filepath.Clean(filepath.Join(workingDirectory, value)), nil
}

func macAppBundleLocation(executablePath string) (bundleDirectory, bundleParent string, ok bool) {
	executableDirectory := filepath.Dir(filepath.Clean(executablePath))
	if filepath.Base(executableDirectory) != "MacOS" {
		return "", "", false
	}
	contentsDirectory := filepath.Dir(executableDirectory)
	if filepath.Base(contentsDirectory) != "Contents" {
		return "", "", false
	}
	bundleDirectory = filepath.Dir(contentsDirectory)
	if !strings.EqualFold(filepath.Ext(bundleDirectory), ".app") {
		return "", "", false
	}
	return bundleDirectory, filepath.Dir(bundleDirectory), true
}

func isWailsDevelopmentDirectory(directory string) bool {
	normalized := filepath.ToSlash(filepath.Clean(directory))
	return normalized == "build/bin" || strings.HasSuffix(normalized, "/build/bin")
}

func resolveRuntimeDataDirectory(executablePath string) (dataDirectoryResolution, error) {
	workingDirectory, _ := os.Getwd()
	developmentDataDirectoryExists := false
	if workingDirectory != "" {
		if info, err := os.Stat(filepath.Join(workingDirectory, "karte_data")); err == nil && info.IsDir() {
			developmentDataDirectoryExists = true
		}
	}
	userConfigDirectory, _ := os.UserConfigDir()
	return resolveDataDirectory(dataDirectoryResolveOptions{
		GOOS:                           runtime.GOOS,
		ExecutablePath:                 executablePath,
		WorkingDirectory:               workingDirectory,
		UserConfigDirectory:            userConfigDirectory,
		Override:                       os.Getenv("KARTE_DATA_DIR"),
		DevelopmentDataDirectoryExists: developmentDataDirectoryExists,
	})
}

type dataDirectoryMigrationReport struct {
	Copied    int
	Preserved int
}

type migrationFileCopy func(sourcePath, destinationPath string, info fs.FileInfo) error

var errMigrationDestinationExists = errors.New("migration destination exists")

// migrateLegacyDataDirectory copies legacy data into the user data directory
// without modifying either existing destination entries or the legacy source.
func migrateLegacyDataDirectory(sourceDirectory, destinationDirectory string) (dataDirectoryMigrationReport, error) {
	return migrateLegacyDataDirectoryWithCopy(sourceDirectory, destinationDirectory, copyMigrationFileNoReplace)
}

func migrateLegacyDataDirectoryWithCopy(sourceDirectory, destinationDirectory string, copyFile migrationFileCopy) (dataDirectoryMigrationReport, error) {
	report := dataDirectoryMigrationReport{}
	sourceDirectory = filepath.Clean(sourceDirectory)
	destinationDirectory = filepath.Clean(destinationDirectory)
	if sourceDirectory == destinationDirectory {
		return report, nil
	}

	sourceInfo, err := os.Lstat(sourceDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			return report, nil
		}
		return report, fmt.Errorf("inspect legacy data directory: %w", err)
	}
	if !sourceInfo.IsDir() || sourceInfo.Mode()&os.ModeSymlink != 0 {
		return report, fmt.Errorf("legacy data path is not a directory: %s", sourceDirectory)
	}
	if pathContains(sourceDirectory, destinationDirectory) {
		return report, fmt.Errorf("migration destination cannot be inside legacy data directory: %s", destinationDirectory)
	}

	if destinationInfo, err := os.Lstat(destinationDirectory); err == nil {
		if !destinationInfo.IsDir() || destinationInfo.Mode()&os.ModeSymlink != 0 {
			return report, fmt.Errorf("data destination is not a directory: %s", destinationDirectory)
		}
	} else if os.IsNotExist(err) {
		if err := os.MkdirAll(destinationDirectory, sourceInfo.Mode().Perm()); err != nil {
			return report, fmt.Errorf("create data destination: %w", err)
		}
	} else {
		return report, fmt.Errorf("inspect data destination: %w", err)
	}

	err = filepath.WalkDir(sourceDirectory, func(sourcePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relativePath, err := filepath.Rel(sourceDirectory, sourcePath)
		if err != nil {
			return err
		}
		if relativePath == "." {
			return nil
		}

		destinationPath := filepath.Join(destinationDirectory, relativePath)
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if destinationInfo, err := os.Lstat(destinationPath); err == nil {
			if entry.IsDir() && destinationInfo.IsDir() && destinationInfo.Mode()&os.ModeSymlink == 0 {
				if filepath.ToSlash(relativePath) == ".git" {
					report.Preserved++
					return filepath.SkipDir
				}
				return nil
			}
			report.Preserved++
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}

		switch {
		case entry.IsDir():
			if err := os.Mkdir(destinationPath, info.Mode().Perm()); err != nil {
				if os.IsExist(err) {
					report.Preserved++
					return nil
				}
				return err
			}
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(sourcePath)
			if err != nil {
				return err
			}
			if err := os.Symlink(target, destinationPath); err != nil {
				if os.IsExist(err) {
					report.Preserved++
					return nil
				}
				return err
			}
			report.Copied++
		case info.Mode().IsRegular():
			if err := copyFile(sourcePath, destinationPath, info); err != nil {
				if errors.Is(err, errMigrationDestinationExists) {
					report.Preserved++
					return nil
				}
				return err
			}
			report.Copied++
		default:
			return fmt.Errorf("unsupported legacy data entry: %s", sourcePath)
		}
		return nil
	})
	if err != nil {
		return report, fmt.Errorf("migrate legacy data: %w", err)
	}
	return report, nil
}

func copyMigrationFileNoReplace(sourcePath, destinationPath string, info fs.FileInfo) (err error) {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	temporary, err := os.CreateTemp(filepath.Dir(destinationPath), ".karte-migrate-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	if _, err := io.Copy(temporary, source); err != nil {
		return err
	}
	if err := temporary.Chmod(info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(temporaryPath, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Link(temporaryPath, destinationPath); err != nil {
		if os.IsExist(err) {
			return errMigrationDestinationExists
		}
		return err
	}
	return nil
}

func pathContains(parentPath, childPath string) bool {
	relativePath, err := filepath.Rel(parentPath, childPath)
	if err != nil || relativePath == "." {
		return relativePath == "."
	}
	return relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) && !filepath.IsAbs(relativePath)
}
