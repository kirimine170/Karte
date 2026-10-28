package accounting

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func draftBatch(t *testing.T) (*Service, Book) {
	t.Helper()
	s := NewService(t.TempDir())
	b := EmptyBook()
	for _, description := range []string{"CSV参加費", "CSV会場費", "対象外の下書き"} {
		entry := testEntry()
		entry.Description = description
		var err error
		b, err = s.SaveEntry(entry, b.Revision)
		if err != nil {
			t.Fatal(err)
		}
	}
	return s, b
}

func TestPostDraftsOneAtomicRevision(t *testing.T) {
	s, b := draftBatch(t)
	b.Entries[0].SourceKey = "csv-original"
	b.Entries[0].SourceID = "123"
	if _, err := s.save(b); err != nil {
		t.Fatal(err)
	}
	b, _ = s.Load()
	revision := b.Revision
	ids := []string{b.Entries[0].ID, b.Entries[1].ID}
	posted, err := s.PostDrafts(ids, revision)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Revision != revision+1 || posted.Entries[0].Status != "posted" || posted.Entries[1].Status != "posted" || posted.Entries[2].Status != "draft" {
		t.Fatal(posted)
	}
	if posted.Entries[0].SourceKey != "csv-original" || posted.Entries[0].SourceID != "123" {
		t.Fatal("source identity changed")
	}
	loaded, err := NewService(filepath.Dir(filepath.Dir(s.dir))).Load()
	if err != nil || loaded.Revision != posted.Revision || loaded.Entries[1].Status != "posted" {
		t.Fatal("posting did not persist", err)
	}
	if _, err = s.PostDrafts(ids, posted.Revision); err == nil {
		t.Fatal("already posted accepted")
	}
}

func TestPostDraftsRejectsWholeBatch(t *testing.T) {
	for _, name := range []string{"empty", "duplicate", "missing", "stale", "posted", "invalid"} {
		t.Run(name, func(t *testing.T) {
			s, b := draftBatch(t)
			ids := []string{b.Entries[0].ID, b.Entries[1].ID}
			switch name {
			case "empty":
				ids = nil
			case "duplicate":
				ids[1] = ids[0]
			case "missing":
				ids[1] = "missing"
			case "posted":
				b.Entries[1].Status = "posted"
			case "invalid":
				b.Entries[1].Credit[0].Amount = 1
			}
			// Simulate legacy/corrupt rows to exercise revalidation at approval time．
			data, _ := json.Marshal(b)
			path := filepath.Join(s.dir, "book.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			revision := b.Revision
			if name == "stale" {
				revision--
			}
			before, _ := os.ReadFile(path)
			if _, err := s.PostDrafts(ids, revision); err == nil {
				t.Fatal("invalid batch accepted")
			}
			after, _ := os.ReadFile(path)
			if string(before) != string(after) {
				t.Fatal("failed batch changed persisted book")
			}
		})
	}
}

func TestDeleteDraftPreservesOtherEntriesAndOriginal(t *testing.T) {
	s, b := draftBatch(t)
	if _, err := s.DeleteDraft(b.Entries[0].ID, b.Revision-1); err == nil {
		t.Fatal("stale deletion accepted")
	}
	next, err := s.DeleteDraft(b.Entries[1].ID, b.Revision)
	if err != nil || len(next.Entries) != 2 || next.Entries[0].ID != b.Entries[0].ID || next.Entries[1].ID != b.Entries[2].ID {
		t.Fatal(next, err)
	}
	if _, err := s.DeleteDraft(b.Entries[1].ID, next.Revision); err == nil {
		t.Fatal("missing draft deletion accepted")
	}
}

func TestPostImportedSampleCSV(t *testing.T) {
	path := os.Getenv("KARTE_ACCOUNTING_SAMPLE_CSV")
	if path == "" {
		t.Skip("optional private sample CSV")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(t.TempDir())
	imported, err := s.ImportJournalCSV(string(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported.Entries) == 0 {
		t.Fatal("no imported entries")
	}
	ids := make([]string, len(imported.Entries))
	var total int64
	for i, e := range imported.Entries {
		ids[i] = e.ID
		for _, l := range e.Debit {
			total += l.Amount
		}
	}
	posted, err := s.PostDrafts(ids, imported.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if posted.Revision != imported.Revision+1 {
		t.Fatal("multiple revision increments")
	}
	for i, e := range posted.Entries {
		before := imported.Entries[i]
		before.Status = "posted"
		before.UpdatedAt = e.UpdatedAt
		a, _ := json.Marshal(before)
		b, _ := json.Marshal(e)
		if string(a) != string(b) {
			t.Fatal("posting modified transaction fields")
		}
	}
	if _, err = s.ImportJournalCSV(string(data), posted.Revision); err == nil {
		t.Fatal("posted CSV reimport duplicated entries")
	}
	t.Logf("sample: %d entries，%d yen，one revision，deduplication preserved", len(ids), total)
}
