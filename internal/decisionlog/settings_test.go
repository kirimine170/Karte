package decisionlog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"karte/internal/canonical"
)

func TestDefaultSettingsAndReviewer(t *testing.T) {
	root := t.TempDir()
	records, err := (Settings{}).ExtractSummary("2026-11-12_fixture.txt",
		[]byte("【HSAP】【決定】Ａ社の検証を進める。"), "https://example.invalid/summary", time.Now())
	if err != nil || len(records) != 1 {
		t.Fatalf("extract proposal: %v", err)
	}
	err = canonical.WithWriter(root, func(w *canonical.Writer) error {
		if _, err := Accept(w, records[0]); err == nil {
			t.Fatal("accepted without reviewer settings")
		}
		if err := InitializeSettings(w); err != nil {
			return err
		}
		settings, err := LoadSettings(w)
		if err != nil {
			return err
		}
		want := map[string]string{"overall": "全体", "karte": "Karte", "hacksick": "HackSick", "essen": "ESSEN", "mirai-labo": "MIRAIラボ", "hsap": "HSAP", "ephy": "ephy", "far": "FAR"}
		if settings.Decider != Reviewer || !reflect.DeepEqual(settings.businessLabels(), want) ||
			!reflect.DeepEqual(settings.PrivateTogether, [][]string{{"hsap", "essen"}}) {
			t.Fatalf("unexpected settings: %+v", settings)
		}
		wrong := records[0]
		wrong.Decider = "local-human"
		if _, err := Accept(w, wrong); err == nil {
			t.Fatal("accepted a record with a different decider")
		}
		if err := os.WriteFile(filepath.Join(root, SettingsPath), []byte(`{"decider":"local-human"}`), 0600); err != nil {
			return err
		}
		if _, err := Accept(w, records[0]); err == nil {
			t.Fatal("accepted with a different configured reviewer")
		}
		if err := os.WriteFile(filepath.Join(root, SettingsPath), []byte(`{"decider":"雫さん"}`), 0600); err != nil {
			return err
		}
		_, err = Accept(w, records[0])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrivateTogetherAcrossRecords(t *testing.T) {
	root := t.TempDir()
	records, err := (Settings{}).ExtractSummary("2026-11-12_fixture.txt",
		[]byte("【HSAP】【決定】資料甲。\n【ESSEN】【決定】資料乙。\n【Karte】【決定】資料丙。"),
		"https://example.invalid/summary", time.Now())
	if err != nil || len(records) != 3 {
		t.Fatalf("extract proposals: %v", err)
	}
	err = canonical.WithWriter(root, func(w *canonical.Writer) error {
		if err := w.Write(SettingsPath, []byte(`{"decider":"雫さん","private_together":[]}`), 0600); err != nil {
			return err
		}
		for _, record := range records {
			record.VisibilityTags = []string{Public}
			if _, err := Accept(w, record); err != nil {
				return err
			}
		}
		entries, err := List(w)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.VisibleTo([]string{Public}) != (entry.Business == "karte") {
				t.Errorf("unexpected visibility for %s: %s", entry.Business, entry.Visibility)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
