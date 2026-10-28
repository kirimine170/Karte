package accounting

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testEntry() Entry {
	return Entry{Date: "2026-10-26", Description: "イベント会場費", Status: "draft", Debit: []Line{{Account: "会議費", Amount: 6400}}, Credit: []Line{{Account: "現金", Amount: 6400}}}
}

func TestJournalLifecycleAndRevision(t *testing.T) {
	s := NewService(t.TempDir())
	e := testEntry()
	b, err := s.SaveEntry(e, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 1 || b.Revision != 1 || b.Entries[0].ID == "" {
		t.Fatal("entry not persisted")
	}
	e = b.Entries[0]
	e.Status = "posted"
	if _, err = s.SaveEntry(e, 0); err == nil {
		t.Fatal("stale update accepted")
	}
	b, err = s.SaveEntry(e, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveEntry(e, 2); err == nil {
		t.Fatal("posted mutation accepted")
	}
	if _, err = s.DeleteDraft(e.ID, 2); err == nil {
		t.Fatal("posted deletion accepted")
	}
	loaded, err := NewService(filepath.Dir(filepath.Dir(s.dir))).Load()
	if err != nil || len(loaded.Entries) != 1 || loaded.Entries[0].Status != "posted" {
		t.Fatalf("reload: %v", err)
	}
	if _, err = os.Stat(filepath.Join(s.dir, "book.json.bak")); err != nil {
		t.Fatal(err)
	}
	if b.Revision != 2 {
		t.Fatal(b.Revision)
	}
}

func TestEntryValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Entry)
	}{
		{"unbalanced", func(e *Entry) { e.Credit[0].Amount = 1 }},
		{"negative", func(e *Entry) { e.Debit[0].Amount = -1; e.Credit[0].Amount = -1 }},
		{"overflow", func(e *Entry) { e.Debit[0].Amount = MaxAmount + 1; e.Credit[0].Amount = MaxAmount + 1 }},
		{"invalid-date", func(e *Entry) { e.Date = "2026-02-30" }},
		{"unknown-account", func(e *Entry) { e.Debit[0].Account = "unregistered" }},
		{"unknown-event", func(e *Entry) { e.EventID = "missing" }},
		{"missing-description", func(e *Entry) { e.Description = " " }},
		{"unknown-receipt", func(e *Entry) { e.ReceiptID = "missing" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := testEntry()
			tc.mutate(&e)
			s := NewService(t.TempDir())
			if _, err := s.SaveEntry(e, 0); err == nil {
				t.Fatal("invalid entry accepted")
			}
			b, _ := s.Load()
			if b.Revision != 0 || len(b.Entries) != 0 {
				t.Fatal("failed mutation changed book")
			}
		})
	}
	compound := testEntry()
	compound.Debit = []Line{{Account: "会議費", Amount: 6000}, {Account: "支払手数料", Amount: 400}}
	if err := ValidateEntry(EmptyBook(), compound); err != nil {
		t.Fatal(err)
	}
}

func testPNG(t *testing.T) string {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 60, 100))); err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b.Bytes())
}

func TestReceiptOriginalDedupeAndJournalLink(t *testing.T) {
	s := NewService(t.TempDir())
	encoded := testPNG(t)
	b, err := s.ImportReceipt("receipt.png", encoded)
	if err != nil {
		t.Fatal(err)
	}
	r := b.Receipts[0]
	if _, err = s.ImportReceipt("renamed.png", encoded); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err = s.ImportReceipt("mismatch.pdf", encoded); err == nil {
		t.Fatal("mismatched content accepted")
	}
	url, err := s.ReceiptData(r.ID)
	if err != nil || url != "data:image/png;base64,"+encoded {
		t.Fatal("original changed")
	}
	e := testEntry()
	e.ReceiptID = r.ID
	b, err = s.SaveEntry(e, b.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveEntry(e, b.Revision); err == nil {
		t.Fatal("receipt double booked")
	}
	if err = os.WriteFile(s.receiptPath(r), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReceiptData(r.ID); err == nil {
		t.Fatal("tampering not detected")
	}
}

const journalHeader = ",仕訳ID,取引発生日,勘定科目,補助科目,数量,単価,金額,勘定科目,補助科目,数量,単価,金額,担当者ID（HackSick_ID）,摘要,イベントID\r\n"
const journalRow = ",0,2026-02-28,現金,,8,1000,8000,売上高,参加費収入,8,1000,8000,2,イベント参加費徴収,2\r\n"

func TestSheetCSVAtomicImportAndDedupe(t *testing.T) {
	s := NewService(t.TempDir())
	bad := strings.Replace(journalRow, ",8000,2,", ",7000,2,", 1)
	if _, err := s.ImportJournalCSV(journalHeader+journalRow+bad, 0); err == nil {
		t.Fatal("invalid CSV accepted")
	}
	b, _ := s.Load()
	if len(b.Entries) != 0 || len(b.Events) != 0 {
		t.Fatal("partial import committed")
	}
	b, err := s.ImportJournalCSV("\ufeff\r\n"+journalHeader+journalRow+journalRow, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Entries) != 1 || b.Entries[0].Status != "draft" || b.Events[0].ID != "2" {
		t.Fatal("incorrect import")
	}
	if _, err = s.ImportJournalCSV(journalHeader+journalRow, b.Revision); err == nil {
		t.Fatal("CSV duplicate accepted")
	}
	changed := strings.ReplaceAll(journalRow, "8000", "9000")
	if _, err = s.ImportJournalCSV(journalHeader+changed, b.Revision); err == nil || !strings.Contains(err.Error(), "内容が変更") {
		t.Fatalf("changed source ID was not rejected: %v", err)
	}
	b, err = s.ImportEventsCSV(",イベントID,イベント名,開催形式,開催日時\n,2,検証用LT会,オンライン,2026-02-28\n", b.Revision)
	if err != nil || b.Events[0].Name != "検証用LT会" {
		t.Fatal(err)
	}
}

func TestCorruptBookIsNotOverwritten(t *testing.T) {
	s := NewService(t.TempDir())
	if err := atomicWrite(filepath.Join(s.dir, "book.json"), []byte("broken")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveEntry(testEntry(), 0); err == nil {
		t.Fatal("corrupt book overwritten")
	}
	data, _ := os.ReadFile(filepath.Join(s.dir, "book.json"))
	if string(data) != "broken" {
		t.Fatal("corrupt book changed")
	}
}

func TestReceiptOCRAndExtraction(t *testing.T) {
	t.Setenv("RECEIPT_OCR_MOCK", "1")
	t.Setenv("KARTE_ACCOUNTING_OCR_RUNTIME", "")
	s := NewService(t.TempDir())
	b, err := s.ImportReceipt("receipt.png", testPNG(t))
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.Extract(context.Background(), b.Receipts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r := b.Receipts[0]
	if r.Error != "" || r.OCR == nil || r.Observation == nil || r.Observation.TotalAmount.Value == nil {
		t.Fatalf("OCR pipeline failed: %s", r.Error)
	}
	if len(b.Entries) != 0 {
		t.Fatal("OCR automatically booked")
	}
}

func TestConfiguredRuntimeOCR(t *testing.T) {
	path := os.Getenv("KARTE_ACCOUNTING_TEST_RECEIPT")
	if path == "" {
		t.Skip("set KARTE_ACCOUNTING_TEST_RECEIPT to run the configured YomiToku runtime")
	}
	t.Setenv("RECEIPT_OCR_MOCK", "0")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(t.TempDir())
	b, err := s.ImportReceipt(filepath.Base(path), base64.StdEncoding.EncodeToString(data))
	if err != nil {
		t.Fatal(err)
	}
	b, err = s.Extract(context.Background(), b.Receipts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	r := b.Receipts[0]
	if r.Error != "" {
		t.Fatal(r.Error)
	}
	if r.OCR == nil || r.OCR.Engine == "mock" || r.Observation == nil || r.Observation.TotalAmount.Value == nil {
		t.Fatal("real OCR did not return an observed total")
	}
	if *r.Observation.TotalAmount.Value != 1100 {
		if os.Getenv("KARTE_ACCOUNTING_TEST_DEBUG") == "1" {
			for _, p := range r.OCR.Pages {
				for _, block := range p.Blocks {
					t.Logf("synthetic OCR: %s", block.Text)
				}
			}
		}
		t.Fatalf("expected synthetic receipt total 1100，got %d", *r.Observation.TotalAmount.Value)
	}
	t.Logf("real OCR engine=%s，total=%d，no automatic posting", r.OCR.Engine, *r.Observation.TotalAmount.Value)
}
