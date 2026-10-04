package ephyoutbox

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// NewExperimentEvidenceStore anchors operations in an existing data directory.
// Store operations reject links in the managed area and never resolve evidence
// references through absolute paths.
// The caller owns the store and must call Close to release its root handle.
func NewExperimentEvidenceStore(dataDir string) (*ExperimentEvidenceStore, error) {
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("data directory must be a directory, not a symlink")
	}
	handle, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	openedInfo, err := handle.Stat(".")
	if err != nil || !os.SameFile(info, openedInfo) {
		handle.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("data directory changed while opening its root")
	}
	return &ExperimentEvidenceStore{dataRoot: abs, root: filepath.Join(abs, ".mdsys", "ephy", "experiments"), dataHandle: handle}, nil
}

// Close releases the store's retained data-directory handle. Operations that
// already acquired an independent root may finish; subsequent operations fail.
// Close is safe to repeat, including concurrently.
func (s *ExperimentEvidenceStore) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.dataHandle.Close() })
	return s.closeErr
}

// CandidateDir is a diagnostic path; filesystem operations use rooted handles.
func (s *ExperimentEvidenceStore) CandidateDir(candidateID string) string {
	return filepath.Join(s.root, candidateID)
}

func portableEvidenceSegment(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return false
	}
	base := strings.ToLower(strings.SplitN(name, ".", 2)[0])
	if base == "con" || base == "prn" || base == "aux" || base == "nul" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

func validEvidenceCandidate(candidateID string) bool {
	return candidateIDPattern.MatchString(candidateID) && portableEvidenceSegment(candidateID)
}

func isValidLogicalRef(ref string) bool {
	if len(ref) == 0 || len(ref) > 2048 || !logicalRefPattern.MatchString(ref) || path.Clean(ref) != ref {
		return false
	}
	parts := strings.Split(ref, "/")
	if parts[0] == "manifest.json" {
		return false
	}
	for _, part := range parts {
		if !portableEvidenceSegment(part) {
			return false
		}
	}
	return true
}

func evidenceRefs(expected []ExperimentEvidence) (map[string]string, error) {
	if len(expected) < 1 || len(expected) > 64 {
		return nil, fmt.Errorf("evidence must contain 1-64 entries")
	}
	refs := make(map[string]string, len(expected))
	for _, entry := range expected {
		if !isValidLogicalRef(entry.LogicalRef) || !isSHA256(entry.SHA256) {
			return nil, fmt.Errorf("invalid evidence reference or digest")
		}
		if _, exists := refs[entry.LogicalRef]; exists {
			return nil, fmt.Errorf("duplicate evidence reference: %s", entry.LogicalRef)
		}
		refs[entry.LogicalRef] = entry.SHA256
	}
	for ref := range refs {
		for parent := path.Dir(ref); parent != "."; parent = path.Dir(parent) {
			if _, exists := refs[parent]; exists {
				return nil, fmt.Errorf("evidence file/directory collision: %s", parent)
			}
		}
	}
	return refs, nil
}

// Snapshot and validate the complete payload before creating any store paths.
func prepareEvidence(entries map[string][]byte) (map[string][]byte, []ExperimentEvidence, error) {
	copyEntries := make(map[string][]byte, len(entries))
	expected := make([]ExperimentEvidence, 0, len(entries))
	for ref, content := range entries {
		content = bytes.Clone(content)
		copyEntries[ref] = content
		expected = append(expected, ExperimentEvidence{LogicalRef: ref, SHA256: SHA256Bytes(content)})
	}
	if _, err := evidenceRefs(expected); err != nil {
		return nil, nil, err
	}
	sort.Slice(expected, func(i, j int) bool { return expected[i].LogicalRef < expected[j].LogicalRef })
	return copyEntries, expected, nil
}

func exactEvidenceEntry(root *os.Root, name string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return nil
		}
	}
	return fmt.Errorf("evidence identity does not match actual filename: %s", name)
}

func openEvidenceDirectory(root *os.Root, name string) (*os.Root, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("evidence directory must not be a symlink: %s", name)
	}
	if err := exactEvidenceEntry(root, name); err != nil {
		return nil, err
	}
	return root.OpenRoot(name)
}

func (s *ExperimentEvidenceStore) openManaged(create bool) (*os.Root, error) {
	// Duplicate the retained root for this operation; never re-resolve dataDir.
	root, err := s.dataHandle.OpenRoot(".")
	if err != nil {
		return nil, err
	}
	for _, name := range []string{".mdsys", "ephy", "experiments"} {
		if create {
			if err := root.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
				root.Close()
				return nil, err
			}
		}
		next, err := openEvidenceDirectory(root, name)
		if err == nil && create {
			err = syncEvidenceDirectory(root)
		}
		root.Close()
		if err != nil {
			if next != nil {
				next.Close()
			}
			return nil, err
		}
		root = next
	}
	return root, nil
}

func verifyEvidenceDirectory(root *os.Root, candidateID string, expected []ExperimentEvidence) error {
	refs, err := evidenceRefs(expected)
	if err != nil {
		return err
	}
	info, err := root.Lstat("manifest.json")
	if err != nil {
		return fmt.Errorf("inspect evidence manifest: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("evidence manifest must be a regular file")
	}
	if info.Size() > 256*1024 {
		return fmt.Errorf("evidence manifest exceeds size limit")
	}
	if err := exactEvidenceEntry(root, "manifest.json"); err != nil {
		return err
	}
	manifestFile, err := root.Open("manifest.json")
	if err != nil {
		return err
	}
	defer manifestFile.Close()
	// Pointer size fields distinguish the required integer zero from missing/null.
	var manifest struct {
		SchemaVersion string `json:"schema_version"`
		CandidateID   string `json:"candidate_id"`
		WrittenAt     string `json:"written_at"`
		Entries       []struct {
			LogicalRef string `json:"logical_ref"`
			SHA256     string `json:"sha256"`
			SizeBytes  *int64 `json:"size_bytes"`
		} `json:"entries"`
	}
	decoder := json.NewDecoder(manifestFile)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode evidence manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("evidence manifest has trailing JSON")
	}
	if manifest.SchemaVersion != ExperimentSchemaVersion || manifest.CandidateID != candidateID {
		return fmt.Errorf("evidence manifest identity mismatch")
	}
	if _, err := time.Parse(time.RFC3339Nano, manifest.WrittenAt); err != nil {
		return fmt.Errorf("invalid manifest written_at: %w", err)
	}
	if len(manifest.Entries) != len(refs) {
		return fmt.Errorf("evidence reference sets differ")
	}
	manifestRefs := make(map[string]EvidenceEntry, len(manifest.Entries))
	for _, entry := range manifest.Entries {
		if !isValidLogicalRef(entry.LogicalRef) || !isSHA256(entry.SHA256) || entry.SizeBytes == nil || *entry.SizeBytes < 0 {
			return fmt.Errorf("invalid manifest entry")
		}
		if _, exists := manifestRefs[entry.LogicalRef]; exists {
			return fmt.Errorf("duplicate manifest reference")
		}
		if refs[entry.LogicalRef] != entry.SHA256 {
			return fmt.Errorf("manifest/expected evidence digest mismatch")
		}
		manifestRefs[entry.LogicalRef] = EvidenceEntry{LogicalRef: entry.LogicalRef, SHA256: entry.SHA256, SizeBytes: *entry.SizeBytes}
	}
	// Walk actual directory entries to detect case aliases, links, and files that
	// were omitted from the manifest. No symlink is followed while walking.
	seen := make(map[string]bool, len(refs))
	return walkVerifiedEvidence(root, manifestRefs, seen)
}

func walkVerifiedEvidence(root *os.Root, entries map[string]EvidenceEntry, seen map[string]bool) error {
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink in evidence directory: %s", name)
		}
		if entry.IsDir() {
			if name != "." {
				needed := false
				for ref := range entries {
					if strings.HasPrefix(ref, name+"/") {
						needed = true
						break
					}
				}
				if !needed {
					return fmt.Errorf("unreferenced evidence directory: %s", name)
				}
			}
			return nil
		}
		if name == "manifest.json" {
			return nil
		}
		expected, exists := entries[name]
		if !exists {
			return fmt.Errorf("unreferenced evidence file: %s", name)
		}
		// Go 1.25 Unix DirEntry.Info resolves the display name against cwd.
		// OpenRoot names may be relative (or stale after a directory move), so
		// inspect the entry through the already-open root handle instead.
		info, err := root.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != expected.SizeBytes {
			return fmt.Errorf("evidence type/size mismatch: %s", name)
		}
		file, err := root.Open(name)
		if err != nil {
			return err
		}
		hash := sha256.New()
		size, readErr := io.Copy(hash, file)
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if size != expected.SizeBytes || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("evidence digest/size mismatch: %s", name)
		}
		seen[name] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(seen) != len(entries) {
		return fmt.Errorf("expected evidence file missing")
	}
	return nil
}

func verifyStoredEvidence(root *os.Root, candidateID string, expected []ExperimentEvidence) error {
	candidate, err := openEvidenceDirectory(root, candidateID)
	if err != nil {
		return err
	}
	defer candidate.Close()
	return verifyEvidenceDirectory(candidate, candidateID, expected)
}

func (s *ExperimentEvidenceStore) Verify(candidateID string, expected []ExperimentEvidence) error {
	if !validEvidenceCandidate(candidateID) {
		return fmt.Errorf("invalid evidence candidate_id")
	}
	if _, err := evidenceRefs(expected); err != nil {
		return err
	}
	root, err := s.openManaged(false)
	if err != nil {
		return err
	}
	defer root.Close()
	return verifyStoredEvidence(root, candidateID, expected)
}

func writeEvidenceFile(root *os.Root, name string, content []byte) error {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(content)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func (s *ExperimentEvidenceStore) WriteEvidence(candidateID string, entries map[string][]byte) error {
	if !validEvidenceCandidate(candidateID) {
		return fmt.Errorf("invalid evidence candidate_id")
	}
	contents, expected, err := prepareEvidence(entries)
	if err != nil {
		return err
	}
	root, err := s.openManaged(true)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(candidateID); err == nil {
		if err := verifyStoredEvidence(root, candidateID, expected); err != nil {
			return fmt.Errorf("existing evidence preserved: %w", err)
		}
		return syncEvidenceDirectory(root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	stageName := ".evidence-" + hex.EncodeToString(nonce[:])
	if err := root.Mkdir(stageName, 0o700); err != nil {
		return err
	}
	defer root.RemoveAll(stageName)
	stage, err := root.OpenRoot(stageName)
	if err != nil {
		return err
	}
	defer stage.Close()
	manifest := EvidenceManifest{SchemaVersion: ExperimentSchemaVersion, CandidateID: candidateID,
		WrittenAt: time.Now().UTC().Format(time.RFC3339Nano), Entries: make([]EvidenceEntry, 0, len(expected))}
	for _, entry := range expected {
		if err := stage.MkdirAll(path.Dir(entry.LogicalRef), 0o700); err != nil {
			return err
		}
		if err := writeEvidenceFile(stage, entry.LogicalRef, contents[entry.LogicalRef]); err != nil {
			return err
		}
		manifest.Entries = append(manifest.Entries, EvidenceEntry{LogicalRef: entry.LogicalRef, SHA256: entry.SHA256, SizeBytes: int64(len(contents[entry.LogicalRef]))})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err := writeEvidenceFile(stage, "manifest.json", append(data, '\n')); err != nil {
		return err
	}
	if err := verifyEvidenceDirectory(stage, candidateID, expected); err != nil {
		return err
	}
	if err := fs.WalkDir(stage.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || !entry.IsDir() {
			return err
		}
		dir, err := stage.OpenRoot(name)
		if err != nil {
			return err
		}
		defer dir.Close()
		return syncEvidenceDirectory(dir)
	}); err != nil {
		return err
	}
	if err := stage.Close(); err != nil {
		return err
	}
	if err := installEvidenceDirectory(root, stageName, candidateID); err != nil {
		// A concurrent writer may have installed the same immutable content.
		// Verification also rejects an existing file, empty directory, or link.
		if verifyErr := verifyStoredEvidence(root, candidateID, expected); verifyErr != nil {
			return fmt.Errorf("install evidence without replacement: %w; existing evidence: %v", err, verifyErr)
		}
	}
	return syncEvidenceDirectory(root)
}
