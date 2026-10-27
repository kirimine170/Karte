package clip

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMoveFileExclusiveClosesSourceBeforeRemoval(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "image.img")
	destination := filepath.Join(root, "image.jpg")
	if err := os.WriteFile(source, []byte("image bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := moveFileExclusive(source, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source was not removed: %v", err)
	}
	if data, err := os.ReadFile(destination); err != nil || string(data) != "image bytes" {
		t.Fatalf("destination was not preserved: %q, %v", data, err)
	}
}
