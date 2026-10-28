package accounting

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "golang.org/x/image/webp"
	"karte/internal/accounting/receipt/domain"
	"karte/internal/accounting/receipt/extraction"
	"karte/internal/accounting/receipt/ocr"
)

const MaxReceiptBytes = 30 * 1024 * 1024

func (s *Service) ImportReceipt(name, encoded string) (Book, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return b, err
	}
	if len(encoded) > base64.StdEncoding.EncodedLen(MaxReceiptBytes) {
		return b, fmt.Errorf("証憑の上限は30MiBです")
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(data) == 0 {
		return b, fmt.Errorf("証憑データが不正です")
	}
	ext := strings.ToLower(filepath.Ext(name))
	mime := http.DetectContentType(data)
	valid := map[string]string{".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".webp": "image/webp", ".pdf": "application/pdf"}
	if valid[ext] == "" || valid[ext] != mime {
		return b, fmt.Errorf("JPEG・PNG・WebP・PDFの原本を選択してください（内容と拡張子の一致が必要です）")
	}
	if strings.HasPrefix(mime, "image/") {
		cfg, _, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil || int64(cfg.Width)*int64(cfg.Height) > 80_000_000 {
			return b, fmt.Errorf("画像を読み取れないか，8000万画素を超えています")
		}
	}
	digest := hash(data)
	for _, r := range b.Receipts {
		if r.SHA256 == digest {
			return b, fmt.Errorf("同じ証憑は取り込み済みです: %s", r.Name)
		}
	}
	r := Receipt{ID: domain.NewID("receipt"), Name: filepath.Base(name), SHA256: digest, MediaType: mime, Size: len(data), ImportedAt: time.Now().UTC()}
	if err = atomicWrite(filepath.Join(s.dir, "originals", r.SHA256+ext), data); err != nil {
		return b, err
	}
	b.Receipts = append(b.Receipts, r)
	return s.save(b)
}

func (s *Service) receiptPath(r Receipt) string {
	return filepath.Join(s.dir, "originals", r.SHA256+strings.ToLower(filepath.Ext(r.Name)))
}

func (s *Service) ReceiptData(id string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.load()
	if err != nil {
		return "", err
	}
	for _, r := range b.Receipts {
		if r.ID == id {
			data, err := os.ReadFile(s.receiptPath(r))
			if err != nil {
				return "", err
			}
			if hash(data) != r.SHA256 {
				return "", fmt.Errorf("原本のハッシュが一致しません")
			}
			return "data:" + r.MediaType + ";base64," + base64.StdEncoding.EncodeToString(data), nil
		}
	}
	return "", fmt.Errorf("証憑が見つかりません")
}

// Extract materializes the vendored worker or uses an explicitly selected Receipt Lab runtime．
// OCR runs outside the book lock，so bookkeeping can continue while the model works．
func (s *Service) Extract(ctx context.Context, id string) (Book, error) {
	s.mu.Lock()
	b, err := s.load()
	var r Receipt
	if err == nil {
		for _, candidate := range b.Receipts {
			if candidate.ID == id {
				r = candidate
				break
			}
		}
	}
	s.mu.Unlock()
	if err != nil {
		return b, err
	}
	if r.ID == "" {
		return b, fmt.Errorf("証憑が見つかりません")
	}
	data, err := os.ReadFile(s.receiptPath(r))
	if err != nil {
		return b, err
	}
	if hash(data) != r.SHA256 {
		return b, fmt.Errorf("原本のハッシュが一致しません")
	}
	runtimeDir := os.Getenv("KARTE_ACCOUNTING_OCR_RUNTIME")
	if runtimeDir == "" {
		runtimeDir = filepath.Join(s.dir, "runtime")
		files, _ := resources.ReadDir("receipt/worker")
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".py") {
				continue
			}
			data, e := resources.ReadFile("receipt/worker/" + f.Name())
			if e != nil {
				return b, e
			}
			if e = atomicWrite(filepath.Join(runtimeDir, "worker", f.Name()), data); e != nil {
				return b, e
			}
		}
	}
	worker := ocr.NewWorkerManager(runtimeDir, s.dir, 120*time.Second, nil)
	defer worker.Close()
	doc, ocrErr := worker.Recognize(ctx, domain.Evidence{ID: r.ID, StoredPath: s.receiptPath(r), MediaType: r.MediaType, ImportedAt: r.ImportedAt})
	var obs *domain.ReceiptObservation
	if ocrErr == nil {
		value, e := (extraction.Extractor{}).Extract(ctx, domain.Evidence{ID: r.ID, ImportedAt: r.ImportedAt}, *doc)
		ocrErr = e
		obs = &value
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err = s.load()
	if err != nil {
		return b, err
	}
	for i := range b.Receipts {
		if b.Receipts[i].ID == id {
			if ocrErr != nil {
				b.Receipts[i].Error = ocrErr.Error()
			} else {
				b.Receipts[i].OCR = doc
				b.Receipts[i].Observation = obs
				b.Receipts[i].Error = ""
			}
			return s.save(b)
		}
	}
	return b, fmt.Errorf("証憑が見つかりません")
}
