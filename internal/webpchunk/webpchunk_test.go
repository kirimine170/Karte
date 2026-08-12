package webpchunk

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestTagsRoundTripInUnicodeWindowsPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "OneDrive - 研究室", "画像😀")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "タグ付き.webp")
	header := make([]byte, 12)
	copy(header[0:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], 4)
	copy(header[8:12], "WEBP")
	if err := os.WriteFile(path, header, 0o644); err != nil {
		t.Fatal(err)
	}

	want := []string{"Windows", "日本語", "資料😀"}
	if err := WriteTagsToWebP(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadTagsFromWebP(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) < 12 || string(content[:4]) != "RIFF" || string(content[8:12]) != "WEBP" {
		t.Fatalf("invalid RIFF/WebP header: %q", content)
	}
}
