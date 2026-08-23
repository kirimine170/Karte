package main

import (
	"archive/zip"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	maximumArchiveEntries       = 250_000
	maximumArchiveExpandedBytes = uint64(8 << 30)
	maximumSymlinkTargetBytes   = uint64(4 << 10)
)

type artifactArchiveEntry struct {
	file          *zip.File
	name          string
	mode          os.FileMode
	directory     bool
	symlink       bool
	symlinkTarget string
}

type artifactArchive struct {
	reader        *zip.ReadCloser
	entries       []artifactArchiveEntry
	expandedBytes uint64
}

type artifactExtractionRootIdentity struct {
	info         os.FileInfo
	resolvedPath string
}

func main() {
	archivePath := flag.String("archive", "", "absolute path to the artifact ZIP")
	destination := flag.String("destination", "", "absolute path to a new extraction directory")
	flag.Parse()
	if flag.NArg() != 0 || *archivePath == "" || *destination == "" {
		flag.Usage()
		os.Exit(2)
	}
	entries, bytes, err := extractArtifactZip(*archivePath, *destination)
	if err != nil {
		fmt.Fprintf(os.Stderr, "artifact extraction failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("artifact extraction passed: entries=%d expanded_bytes=%d destination=%s\n", entries, bytes, *destination)
}

func extractArtifactZip(archivePath, destination string) (int, uint64, error) {
	return extractArtifactZipWithHook(archivePath, destination, nil)
}

func extractArtifactZipWithHook(archivePath, destination string, afterPreflight func(string) error) (int, uint64, error) {
	if !filepath.IsAbs(archivePath) || filepath.Clean(archivePath) != archivePath {
		return 0, 0, fmt.Errorf("archive path must be absolute and clean: %q", archivePath)
	}
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination {
		return 0, 0, fmt.Errorf("destination path must be absolute and clean: %q", destination)
	}
	if _, err := os.Lstat(destination); err == nil {
		return 0, 0, fmt.Errorf("destination already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return 0, 0, fmt.Errorf("inspect destination: %w", err)
	}
	parent := filepath.Dir(destination)
	if info, err := os.Lstat(parent); err != nil {
		return 0, 0, fmt.Errorf("inspect destination parent: %w", err)
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, 0, fmt.Errorf("destination parent is not a real directory: %s", parent)
	}

	archive, err := preflightArtifactZip(archivePath)
	if err != nil {
		return 0, 0, err
	}
	defer archive.reader.Close()
	if err := os.Mkdir(destination, 0o700); err != nil {
		return 0, 0, fmt.Errorf("create extraction root: %w", err)
	}
	rootIdentity, err := os.Lstat(destination)
	if err != nil {
		return 0, 0, fmt.Errorf("inspect extraction root: %w", err)
	}
	rootResolvedPath, err := filepath.EvalSymlinks(destination)
	if err != nil {
		return 0, 0, fmt.Errorf("resolve extraction root: %w", err)
	}
	identity := artifactExtractionRootIdentity{info: rootIdentity, resolvedPath: filepath.Clean(rootResolvedPath)}
	if afterPreflight != nil {
		if err := afterPreflight(destination); err != nil {
			return 0, 0, fmt.Errorf("after preflight: %w", err)
		}
	}
	if err := extractPreflightedArtifact(archive, destination, identity); err != nil {
		return 0, 0, err
	}
	return len(archive.entries), archive.expandedBytes, nil
}

func preflightArtifactZip(archivePath string) (*artifactArchive, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open artifact ZIP: %w", err)
	}
	fail := func(err error) (*artifactArchive, error) {
		_ = reader.Close()
		return nil, err
	}
	if len(reader.File) == 0 {
		return fail(errors.New("artifact ZIP is empty"))
	}
	if len(reader.File) > maximumArchiveEntries {
		return fail(fmt.Errorf("artifact ZIP has too many entries: %d", len(reader.File)))
	}

	archive := &artifactArchive{reader: reader}
	byName := make(map[string]artifactArchiveEntry, len(reader.File))
	portableNames := make(map[string]string, len(reader.File))
	for _, file := range reader.File {
		name, err := validateArtifactArchiveName(file.Name)
		if err != nil {
			return fail(err)
		}
		portableName := strings.ToLower(name)
		if previous, exists := portableNames[portableName]; exists {
			return fail(fmt.Errorf("duplicate or case-colliding archive entries: %q and %q", previous, file.Name))
		}
		portableNames[portableName] = file.Name

		mode := file.Mode()
		entry := artifactArchiveEntry{file: file, name: name, mode: mode}
		switch {
		case strings.HasSuffix(file.Name, "/") || mode.IsDir():
			entry.directory = true
		case mode&os.ModeSymlink != 0:
			entry.symlink = true
			target, err := readArtifactSymlinkTarget(file)
			if err != nil {
				return fail(fmt.Errorf("read symlink %q: %w", file.Name, err))
			}
			entry.symlinkTarget = target
		case mode.IsRegular():
			if file.UncompressedSize64 > maximumArchiveExpandedBytes || archive.expandedBytes > maximumArchiveExpandedBytes-file.UncompressedSize64 {
				return fail(fmt.Errorf("artifact ZIP expands beyond %d bytes", maximumArchiveExpandedBytes))
			}
			archive.expandedBytes += file.UncompressedSize64
		default:
			return fail(fmt.Errorf("unsupported archive entry type %s for %q", mode, file.Name))
		}
		archive.entries = append(archive.entries, entry)
		byName[name] = entry
	}

	for _, entry := range archive.entries {
		for ancestor := path.Dir(entry.name); ancestor != "."; ancestor = path.Dir(ancestor) {
			if candidate, exists := byName[ancestor]; exists && !candidate.directory {
				return fail(fmt.Errorf("archive entry %q descends through non-directory %q", entry.name, ancestor))
			}
		}
		if !entry.symlink {
			continue
		}
		if _, err := resolveArtifactSymlinkChain(entry.name, entry.symlinkTarget, byName); err != nil {
			return fail(err)
		}
	}
	return archive, nil
}

func validateArtifactArchiveName(name string) (string, error) {
	if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid archive entry name %q", name)
	}
	if strings.Contains(name, "\\") {
		return "", fmt.Errorf("archive entry uses a non-portable backslash: %q", name)
	}
	trimmed := strings.TrimSuffix(name, "/")
	if trimmed == "" || strings.HasPrefix(trimmed, "/") || hasWindowsVolumePrefix(trimmed) {
		return "", fmt.Errorf("archive entry is absolute: %q", name)
	}
	segments := strings.Split(trimmed, "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return "", fmt.Errorf("archive entry has unsafe path component: %q", name)
		}
		if err := validatePortableArtifactSegment(segment); err != nil {
			return "", fmt.Errorf("archive entry has non-portable path component %q: %w", segment, err)
		}
	}
	normalized := path.Clean(trimmed)
	if normalized == "." || normalized == ".." || strings.HasPrefix(normalized, "../") {
		return "", fmt.Errorf("archive entry escapes extraction root: %q", name)
	}
	return normalized, nil
}

func validatePortableArtifactSegment(segment string) error {
	if strings.ContainsRune(segment, ':') {
		return errors.New("colon is not allowed")
	}
	if strings.HasSuffix(segment, ".") || strings.HasSuffix(segment, " ") {
		return errors.New("trailing dot or space is not allowed")
	}
	for _, character := range segment {
		if character < 0x20 {
			return errors.New("control character is not allowed")
		}
	}
	base := strings.ToUpper(strings.SplitN(segment, ".", 2)[0])
	switch base {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return errors.New("reserved Windows device name is not allowed")
	}
	return nil
}

func hasWindowsVolumePrefix(name string) bool {
	return len(name) >= 2 && ((name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= 'a' && name[0] <= 'z')) && name[1] == ':'
}

func readArtifactSymlinkTarget(file *zip.File) (string, error) {
	if file.UncompressedSize64 == 0 || file.UncompressedSize64 > maximumSymlinkTargetBytes {
		return "", fmt.Errorf("invalid target size %d", file.UncompressedSize64)
	}
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, int64(maximumSymlinkTargetBytes)+1))
	closeErr := reader.Close()
	if readErr != nil {
		return "", readErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if uint64(len(data)) != file.UncompressedSize64 || !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
		return "", errors.New("invalid symlink target")
	}
	return string(data), nil
}

func resolveArtifactSymlinkName(name, target string) (string, error) {
	if target == "" || strings.Contains(target, "\\") || strings.HasPrefix(target, "/") || hasWindowsVolumePrefix(target) {
		return "", fmt.Errorf("archive symlink %q has unsafe target %q", name, target)
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	if resolved == "." || resolved == ".." || strings.HasPrefix(resolved, "../") || strings.HasPrefix(resolved, "/") {
		return "", fmt.Errorf("archive symlink %q escapes extraction root via %q", name, target)
	}
	return resolved, nil
}

func resolveArtifactSymlinkChain(name, target string, entries map[string]artifactArchiveEntry) (string, error) {
	candidate, err := resolveArtifactSymlinkName(name, target)
	if err != nil {
		return "", err
	}
	seen := make(map[string]struct{}, len(entries))
	for resolutions := 0; resolutions <= len(entries); resolutions++ {
		if _, exists := seen[candidate]; exists {
			return "", fmt.Errorf("archive symlink %q forms a cycle via %q", name, candidate)
		}
		seen[candidate] = struct{}{}

		parts := strings.Split(candidate, "/")
		replaced := false
		for index := 1; index <= len(parts); index++ {
			prefix := strings.Join(parts[:index], "/")
			entry, exists := entries[prefix]
			if !exists || !entry.symlink {
				continue
			}
			replacement, err := resolveArtifactSymlinkName(prefix, entry.symlinkTarget)
			if err != nil {
				return "", err
			}
			if index < len(parts) {
				replacement = path.Clean(path.Join(replacement, strings.Join(parts[index:], "/")))
			}
			if replacement == ".." || strings.HasPrefix(replacement, "../") || strings.HasPrefix(replacement, "/") {
				return "", fmt.Errorf("archive symlink %q escapes extraction root through %q", name, prefix)
			}
			candidate = replacement
			replaced = true
			break
		}
		if replaced {
			continue
		}
		if targetEntry, exists := entries[candidate]; exists {
			if targetEntry.symlink {
				return "", fmt.Errorf("archive symlink %q could not resolve target %q", name, target)
			}
			return candidate, nil
		}
		if archiveContainsDirectory(entries, candidate) {
			return candidate, nil
		}
		return "", fmt.Errorf("archive symlink %q has missing target %q", name, target)
	}
	return "", fmt.Errorf("archive symlink %q exceeds resolution limit", name)
}

func archiveContainsDirectory(entries map[string]artifactArchiveEntry, directory string) bool {
	prefix := directory + "/"
	for name := range entries {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func extractPreflightedArtifact(archive *artifactArchive, destination string, rootIdentity artifactExtractionRootIdentity) error {
	entries := append([]artifactArchiveEntry(nil), archive.entries...)
	sort.SliceStable(entries, func(i, j int) bool {
		return artifactEntryOrder(entries[i]) < artifactEntryOrder(entries[j])
	})
	for _, entry := range entries {
		if err := revalidateArtifactRoot(destination, rootIdentity); err != nil {
			return fmt.Errorf("revalidate extraction root before %q: %w", entry.name, err)
		}
		destinationPath, err := artifactDestinationPath(destination, entry.name)
		if err != nil {
			return err
		}
		if err := revalidateArtifactAncestors(destination, destinationPath); err != nil {
			return fmt.Errorf("revalidate %q: %w", entry.name, err)
		}
		switch {
		case entry.directory:
			if err := createArtifactDirectory(destination, rootIdentity, destinationPath, entry.mode); err != nil {
				return fmt.Errorf("extract directory %q: %w", entry.name, err)
			}
		case entry.symlink:
			if err := ensureArtifactParentDirectories(destination, rootIdentity, filepath.Dir(destinationPath)); err != nil {
				return fmt.Errorf("prepare symlink %q: %w", entry.name, err)
			}
			if err := revalidateArtifactAncestors(destination, destinationPath); err != nil {
				return fmt.Errorf("revalidate symlink %q: %w", entry.name, err)
			}
			if err := revalidateArtifactRoot(destination, rootIdentity); err != nil {
				return fmt.Errorf("revalidate extraction root for symlink %q: %w", entry.name, err)
			}
			if err := os.Symlink(entry.symlinkTarget, destinationPath); err != nil {
				return fmt.Errorf("extract symlink %q: %w", entry.name, err)
			}
		default:
			if err := ensureArtifactParentDirectories(destination, rootIdentity, filepath.Dir(destinationPath)); err != nil {
				return fmt.Errorf("prepare file %q: %w", entry.name, err)
			}
			if err := revalidateArtifactAncestors(destination, destinationPath); err != nil {
				return fmt.Errorf("revalidate file %q: %w", entry.name, err)
			}
			if err := revalidateArtifactRoot(destination, rootIdentity); err != nil {
				return fmt.Errorf("revalidate extraction root for file %q: %w", entry.name, err)
			}
			if err := extractArtifactRegularFile(entry, destinationPath); err != nil {
				return fmt.Errorf("extract file %q: %w", entry.name, err)
			}
		}
	}
	return nil
}

func artifactEntryOrder(entry artifactArchiveEntry) int {
	if entry.directory {
		return 0
	}
	if entry.symlink {
		return 2
	}
	return 1
}

func artifactDestinationPath(root, name string) (string, error) {
	destination := filepath.Join(root, filepath.FromSlash(name))
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("archive entry escaped extraction root: %q", name)
	}
	return destination, nil
}

func revalidateArtifactAncestors(root, destination string) error {
	relative, err := filepath.Rel(root, destination)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("destination escaped extraction root")
	}
	current := root
	parts := strings.Split(relative, string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("ancestor is not a real directory: %s", current)
		}
	}
	return nil
}

func revalidateArtifactRoot(root string, expected artifactExtractionRootIdentity) error {
	current, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !current.IsDir() || current.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("extraction root is not a real directory: %s", root)
	}
	if expected.info == nil || !os.SameFile(expected.info, current) {
		return fmt.Errorf("extraction root identity changed: %s", root)
	}
	resolvedPath, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve extraction root: %w", err)
	}
	if filepath.Clean(resolvedPath) != expected.resolvedPath {
		return fmt.Errorf("extraction root resolved path changed: %s -> %s", expected.resolvedPath, resolvedPath)
	}
	return nil
}

func ensureArtifactParentDirectories(root string, rootIdentity artifactExtractionRootIdentity, parent string) error {
	if err := revalidateArtifactRoot(root, rootIdentity); err != nil {
		return err
	}
	relative, err := filepath.Rel(root, parent)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return errors.New("parent escaped extraction root")
	}
	if relative == "." {
		return nil
	}
	current := root
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if err := revalidateArtifactRoot(root, rootIdentity); err != nil {
			return err
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("parent is not a real directory: %s", current)
		}
	}
	return nil
}

func createArtifactDirectory(root string, rootIdentity artifactExtractionRootIdentity, destination string, mode os.FileMode) error {
	if err := ensureArtifactParentDirectories(root, rootIdentity, filepath.Dir(destination)); err != nil {
		return err
	}
	if err := revalidateArtifactRoot(root, rootIdentity); err != nil {
		return err
	}
	permissions := mode.Perm()
	if permissions == 0 {
		permissions = 0o755
	}
	if err := os.Mkdir(destination, permissions); err != nil && !os.IsExist(err) {
		return err
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("directory path was replaced: %s", destination)
	}
	return nil
}

func extractArtifactRegularFile(entry artifactArchiveEntry, destination string) (result error) {
	permissions := entry.mode.Perm()
	if permissions == 0 {
		permissions = 0o644
	}
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, permissions)
	if err != nil {
		return err
	}
	installed := true
	defer func() {
		_ = output.Close()
		if result != nil && installed {
			_ = os.Remove(destination)
		}
	}()
	input, err := entry.file.Open()
	if err != nil {
		return err
	}
	// The ZIP reader validates CRC and the declared size only when it reaches
	// EOF.  Cap the copy at one byte beyond the preflighted declaration so a
	// forged header cannot expand without bound before the mismatch is caught.
	written, copyErr := io.Copy(output, io.LimitReader(input, int64(entry.file.UncompressedSize64)+1))
	inputCloseErr := input.Close()
	if copyErr != nil {
		return fmt.Errorf("copy expanded data after %d bytes: %w", written, copyErr)
	}
	if inputCloseErr != nil {
		return inputCloseErr
	}
	if uint64(written) != entry.file.UncompressedSize64 {
		return fmt.Errorf("expanded size %d does not match declared size %d", written, entry.file.UncompressedSize64)
	}
	if err := output.Sync(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	installed = false
	return nil
}
