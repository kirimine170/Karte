package portablepackage

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"karte/internal/contextcore"
)

const projection = "---\ndoc_id: DOC-000001\ntitle: \"会議 \\U0001F4DD\"\nproject: legacy\nkind: note\nsensitivity: internal\n---\n# 議事録\n\n- 採択した内容\n"

func TestSnapshotRoundTrip(t *testing.T) {
	source := []byte(projection)
	snapshot, err := New(source)
	if err != nil {
		t.Fatal(err)
	}
	source[len(source)-1] = '!'
	path := filepath.Join(t.TempDir(), "会議 📝.karte")
	if err := Write(path, snapshot); err != nil {
		t.Fatal(err)
	}
	actual, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Manifest != snapshot.Manifest || string(actual.Markdown) != projection || actual.Title() != "会議 📝" {
		t.Fatalf("unexpected snapshot: %+v", actual.Manifest)
	}
	replacement, err := New([]byte(strings.ReplaceAll(projection, "議事録", "改訂")))
	if err != nil || replacement.Manifest.RootResourceID != actual.Manifest.RootResourceID ||
		replacement.Manifest.RootRevisionID == actual.Manifest.RootRevisionID {
		t.Fatalf("identity changed or revision did not advance: %+v, %v", replacement.Manifest, err)
	}
	second := filepath.Join(t.TempDir(), "copy.kartepack")
	if err := Write(second, actual); err != nil {
		t.Fatal(err)
	}
	firstZip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := Read(second)
	if err != nil || legacy.Manifest != actual.Manifest || !bytes.Equal(legacy.Markdown, actual.Markdown) {
		t.Fatalf("legacy extension must open the same snapshot: %v", err)
	}
	secondZip, err := os.ReadFile(second)
	if err != nil || !bytes.Equal(firstZip, secondZip) {
		t.Fatalf("identical snapshot must produce identical ZIP: %v", err)
	}
	replacement.Markdown[0] = '!'
	if err := Write(path, replacement); err == nil {
		t.Fatal("corrupted snapshot was written")
	}
	saved, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(saved, firstZip) {
		t.Fatal("failed write changed the destination")
	}
}

func TestProjectionUsesClassificationAndOmitsUnrecognizedMetadata(t *testing.T) {
	source := []byte("---\ndoc_id: DOC-000001\ntitle: 会議 📝\nprivate_path: /synthetic/private.md\n---\n# 議事録\n\n- 採択した内容\n")
	resource := contextcore.Resource{Project: "legacy", Kind: "note", Sensitivity: "internal", SHA256: hash(source)}
	snapshot, err := PersonalProjection(source, "fallback", resource)
	if err != nil || string(snapshot.Markdown) != projection {
		t.Fatalf("projection did not match allowlisted metadata: %v\n%s", err, snapshot.Markdown)
	}
	source[len(source)-1] = '!'
	if _, err := PersonalProjection(source, "fallback", resource); err == nil {
		t.Fatal("projected bytes other than the authorized source")
	}
}

func TestSnapshotRejectsActiveAndExternalContent(t *testing.T) {
	for _, body := range []string{
		"![caption](/data/image/private.webp)", "[[Private note]]", "[doc](../private.md)",
		"[doc][id]\n\n[id]: ../private.md", "<script>window.alert(1)</script>",
		"https://example.test/remote", "  @import(type=\"csv\" path=\"data/csv/private.csv\")",
		"@import table.csv", "@chart(path=\"data/csv/private.csv\")",
		"| Heading |\n| --- |\n| [link](private.md) |",
		"Term\n:   <img src=\"private.webp\">",
		"Footnote[^1]\n\n[^1]: [link](private.md)",
	} {
		t.Run(body, func(t *testing.T) {
			if _, err := New([]byte(projection + "\n" + body + "\n")); !errors.Is(err, ErrNotStandalone) {
				t.Fatalf("expected standalone error, got %v", err)
			}
		})
	}
}

func TestArchiveRejectsMalformedEntriesAndManifests(t *testing.T) {
	snapshot, err := New([]byte(projection))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(snapshot.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	blobName := blobPrefix + snapshot.Manifest.Blob.SHA256
	tests := []struct {
		name      string
		entry     string
		method    uint16
		mode      fs.FileMode
		manifest  []byte
		blob      []byte
		tamperCRC bool
		want      error
	}{
		{name: "missing", entry: "unlisted", want: ErrInvalid},
		{name: "duplicate", entry: manifestName, want: ErrInvalid},
		{name: "case alias", entry: strings.ToUpper(blobName), want: ErrInvalid},
		{name: "traversal", entry: "../note.md", want: ErrInvalid},
		{name: "absolute Windows path", entry: "C:/temp/note.md", want: ErrInvalid},
		{name: "symlink", mode: fs.ModeSymlink, want: ErrInvalid},
		{name: "compressed", method: zip.Deflate, want: ErrInvalid},
		{name: "oversize", blob: bytes.Repeat([]byte("a"), MaxMarkdownBytes+1), want: ErrInvalid},
		{name: "digest mismatch", blob: []byte(strings.ReplaceAll(projection, "議事録", "改訂版")), want: ErrInvalid},
		{name: "CRC mismatch", tamperCRC: true, want: ErrInvalid},
		{name: "duplicate JSON key", manifest: append([]byte(`{"format_version":"0.1",`), manifest[1:]...), want: ErrInvalid},
		{name: "unknown version", manifest: bytes.Replace(manifest, []byte(`"0.1"`), []byte(`"0.2"`), 1), want: ErrUnsupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var raw bytes.Buffer
			archive := zip.NewWriter(&raw)
			entry := tc.entry
			if entry == "" {
				entry = blobName
			}
			metadata := tc.manifest
			if metadata == nil {
				metadata = manifest
			}
			data := tc.blob
			if data == nil {
				data = snapshot.Markdown
			}
			for _, spec := range []struct {
				header zip.FileHeader
				data   []byte
			}{{zip.FileHeader{Name: manifestName}, metadata}, {zip.FileHeader{Name: entry, Method: tc.method}, data}} {
				if spec.header.Name != manifestName && tc.mode != 0 {
					spec.header.SetMode(tc.mode | 0600)
				}
				file, err := archive.CreateHeader(&spec.header)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.Write(spec.data); err != nil {
					t.Fatal(err)
				}
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			if tc.tamperCRC {
				raw.Bytes()[bytes.Index(raw.Bytes(), data)] = '!'
			}
			if _, err := readArchive(raw.Bytes()); !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}
