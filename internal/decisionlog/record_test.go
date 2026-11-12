package decisionlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"karte/internal/canonical"
)

func TestRecordFixtures(t *testing.T) {
	dir := filepath.Join("..", "..", "schemas", "decision-log", "v0.1", "fixtures")
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		t.Run(file.Name(), func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(dir, file.Name()))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Decode(data); (err != nil) != strings.HasPrefix(file.Name(), "invalid-") {
				t.Fatalf("fixture validation returned %v", err)
			}
		})
	}
}

func TestPublicationRestriction(t *testing.T) {
	settings := Settings{Businesses: map[string]string{"team-a": "Team A"}, PrivateTogether: [][]string{{"Team A", "Team B"}}}
	records, err := settings.ExtractSummary("2026-11-12_fixture.txt", []byte("【Team A】【決定】Ｔｅａｍ Ｂと共同の検証．"), "https://example.invalid/source", time.Now())
	if err != nil || len(records) != 1 {
		t.Fatalf("extracting private record: %v", err)
	}
	record := records[0]
	if record.PublicationRestriction != WorkspacePrivate {
		t.Fatal("workspace rule was not attached to the candidate")
	}
	record.Visibility = Public
	if record.Validate() == nil {
		t.Fatal("public visibility bypassed the business separation rule")
	}
}

func TestReplacementChainValidation(t *testing.T) {
	now := time.Now()
	root := t.TempDir()
	records, err := (Settings{}).ExtractSummary("2026-11-12_fixture.txt", []byte("【Karte】【決定】一．\n【Karte】【決定】二．\n【Karte】【決定】三．"), "https://example.invalid/source", now)
	if err != nil || len(records) != 3 {
		t.Fatalf("extracting chain fixture: %v", err)
	}
	a, b, c := records[0], records[1], records[2]
	b.Supersedes, c.Supersedes = a.ID, b.ID
	err = canonical.WithWriter(root, func(w *canonical.Writer) error {
		for _, record := range []Record{a, b, c} {
			if _, err := Accept(w, record); err != nil {
				return err
			}
		}
		entries, err := load(w)
		if err != nil {
			return err
		}
		if entries[a.ID].ReplacedBy != b.ID || entries[b.ID].ReplacedBy != c.ID ||
			entries[a.ID].Status != Superseded || entries[b.ID].Status != Superseded || entries[c.ID].Status != Decided {
			t.Fatal("replacement chain was not projected correctly")
		}
		// Install a malformed on-disk cycle to exercise read-side validation.
		a.Supersedes = c.ID
		encoded, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if err := w.Write(Path(a.ID), []byte("---\n"+string(encoded)+"\n---\n"), 0600); err != nil {
			return err
		}
		if _, err := List(w); err == nil || !strings.Contains(err.Error(), "cyclic") {
			t.Fatalf("expected cyclic replacement error, got %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
