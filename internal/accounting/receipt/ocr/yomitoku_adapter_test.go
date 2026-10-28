package ocr

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdaptYomiTokuNormalizesBoxes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	raw := []byte(`{"pages":[{"width":1000,"height":2000,"blocks":[{"text":"合計","confidence":0.9,"bbox":[100,200,600,300]}]}]}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	pages, err := AdaptYomiToku(path)
	if err != nil {
		t.Fatal(err)
	}
	b := pages[0].Blocks[0]
	if b.Box.X != .1 || b.Box.Y != .1 || b.Box.Width != .5 || b.Box.Height != .05 {
		t.Fatalf("box=%+v", b.Box)
	}
}
