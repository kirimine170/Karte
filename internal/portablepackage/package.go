// Package portablepackage transports an immutable, personal Markdown snapshot.
package portablepackage

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

const (
	Version          = "0.1"
	Extension        = ".kartepack"
	MaxMarkdownBytes = 2 << 20
	maxManifestBytes = 4096
	maxArchiveBytes  = MaxMarkdownBytes + maxManifestBytes + 4096
	manifestName     = "manifest.json"
	blobPrefix       = "blobs/sha256/"
)

var (
	ErrInvalid     = errors.New("invalid Karte package")
	ErrUnsupported = errors.New("unsupported Karte package version")
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// The root revision identifies the bytes of the exported projection. The
// resource ID is a legacy Markdown doc_id and remains stable across revisions.
type Manifest struct {
	FormatVersion  string `json:"format_version"`
	RootResourceID string `json:"root_resource_id"`
	RootRevisionID string `json:"root_revision_id"`
	Blob           Blob   `json:"blob"`
}

type Blob struct {
	SHA256    string `json:"sha256"`
	Size      int    `json:"size"`
	MediaType string `json:"media_type"`
}

type Snapshot struct {
	Manifest Manifest
	Markdown []byte
}

func New(markdown []byte) (Snapshot, error) {
	doc, err := parseDocument(markdown)
	if err != nil {
		return Snapshot{}, err
	}
	digest := hash(markdown)
	return Snapshot{
		Manifest: Manifest{
			FormatVersion: Version, RootResourceID: doc.DocID, RootRevisionID: digest,
			Blob: Blob{SHA256: digest, Size: len(markdown), MediaType: "text/markdown"},
		},
		Markdown: bytes.Clone(markdown),
	}, nil
}

// Write publishes a complete verified ZIP alongside the selected destination.
func Write(path string, snapshot Snapshot) error {
	if err := snapshot.validate(); err != nil {
		return err
	}
	manifest, err := json.Marshal(snapshot.Manifest)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".kartepack-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	archive := zip.NewWriter(file)
	for _, entry := range []struct {
		name string
		data []byte
	}{
		{manifestName, manifest},
		{blobPrefix + snapshot.Manifest.Blob.SHA256, snapshot.Markdown},
	} {
		// Store the bounded payload to avoid expansion and preserve identical output.
		writer, err := archive.CreateHeader(&zip.FileHeader{Name: entry.name, Method: zip.Store})
		if err != nil {
			return err
		}
		if _, err := writer.Write(entry.data); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if _, err := Read(file.Name()); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func Read(path string) (Snapshot, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Snapshot{}, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxArchiveBytes {
		return Snapshot{}, ErrInvalid
	}
	file, err := os.Open(path)
	if err != nil {
		return Snapshot{}, err
	}
	defer file.Close()
	actual, err := file.Stat()
	if err != nil || !os.SameFile(info, actual) {
		return Snapshot{}, ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxArchiveBytes+1))
	if err != nil || len(raw) > maxArchiveBytes {
		return Snapshot{}, ErrInvalid
	}
	return readArchive(raw)
}

func readArchive(raw []byte) (Snapshot, error) {
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil || len(archive.File) != 2 || archive.Comment != "" {
		return Snapshot{}, ErrInvalid
	}
	entries := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		if file.Method != zip.Store || !file.Mode().IsRegular() || entries[file.Name] != nil ||
			file.CompressedSize64 != file.UncompressedSize64 {
			return Snapshot{}, ErrInvalid
		}
		entries[file.Name] = file
	}
	manifestBytes, err := readEntry(entries[manifestName], maxManifestBytes)
	if err != nil {
		return Snapshot{}, err
	}
	manifest, err := parseManifest(manifestBytes)
	if err != nil {
		return Snapshot{}, err
	}
	markdown, err := readEntry(entries[blobPrefix+manifest.Blob.SHA256], MaxMarkdownBytes)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := Snapshot{Manifest: manifest, Markdown: markdown}
	return snapshot, snapshot.validate()
}

func readEntry(file *zip.File, limit int) ([]byte, error) {
	if file == nil || file.UncompressedSize64 > uint64(limit) {
		return nil, ErrInvalid
	}
	reader, err := file.Open()
	if err != nil {
		return nil, ErrInvalid
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil || len(data) > limit || uint64(len(data)) != file.UncompressedSize64 {
		return nil, ErrInvalid
	}
	return data, nil
}

func parseManifest(data []byte) (Manifest, error) {
	// Re-encoding enforces the v0.1 wire form, including exact field names,
	// required properties and rejection of duplicate JSON keys.
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, ErrInvalid
	}
	canonical, err := json.Marshal(manifest)
	var compact bytes.Buffer
	if err != nil || json.Compact(&compact, data) != nil || !bytes.Equal(compact.Bytes(), canonical) {
		return Manifest{}, ErrInvalid
	}
	if manifest.FormatVersion != Version {
		return Manifest{}, ErrUnsupported
	}
	if !digestPattern.MatchString(manifest.Blob.SHA256) {
		return Manifest{}, ErrInvalid
	}
	return manifest, nil
}

func (snapshot Snapshot) validate() error {
	if snapshot.Manifest.FormatVersion != Version {
		return ErrUnsupported
	}
	expected, err := New(snapshot.Markdown)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if snapshot.Manifest != expected.Manifest {
		return ErrInvalid
	}
	return nil
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
