package ocr

import (
	"context"
	"image"
	"image/png"
	"karte/internal/accounting/receipt/domain"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestMockWorkerProtocol(t *testing.T) {
	t.Setenv("RECEIPT_OCR_MOCK", "1")
	_, file, _, _ := runtime.Caller(0)
	project := filepath.Clean(filepath.Join(filepath.Dir(file), ".."))
	dir := t.TempDir()
	input := filepath.Join(dir, "receipt.png")
	f, err := os.Create(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 600, 1000))); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	m := NewWorkerManager(project, dir, 10*time.Second, nil)
	defer m.Close()
	doc, err := m.Recognize(context.Background(), domain.Evidence{ID: "ev_test", StoredPath: input, MediaType: "image/png"})
	if err != nil {
		t.Fatal(err)
	}
	if doc.Engine != "mock" || len(doc.Pages) != 1 || len(doc.Pages[0].Blocks) < 5 {
		t.Fatalf("unexpected doc: %+v", doc)
	}
}

func TestHealthDoesNotWaitForWorkerOperationLock(t *testing.T) {
	m := NewWorkerManager(t.TempDir(), t.TempDir(), time.Second, nil)
	m.mu.Lock()
	defer m.mu.Unlock()
	done := make(chan domain.OCRHealth, 1)
	go func() { done <- m.Health(context.Background()) }()
	select {
	case health := <-done:
		if health.Status != "unknown" {
			t.Fatalf("status=%s", health.Status)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Health blocked behind the worker operation lock")
	}
}
