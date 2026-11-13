package googlesync

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"karte/internal/canonical"
	"karte/internal/decisionlog"
)

type memoryDrive struct {
	files   map[string]driveRevision
	data    map[string]revision
	offline bool
}

func (m *memoryDrive) list(context.Context) (map[string]driveRevision, error) {
	if m.offline {
		return nil, errors.New("offline")
	}
	out := make(map[string]driveRevision, len(m.files))
	for id, file := range m.files {
		out[id] = file
	}
	return out, nil
}

func (m *memoryDrive) read(_ context.Context, file driveRevision) (revision, error) {
	return m.data[file.ID], nil
}

func (m *memoryDrive) create(_ context.Context, id string, value revision, raw []byte) error {
	if id != canonical.Hash(raw) {
		return errors.New("revision hash mismatch")
	}
	m.files[id] = driveRevision{ID: id, Parent: value.Parent, FileID: id}
	m.data[id] = value
	return nil
}

func TestDecisionDriveOfflineAndConflicts(t *testing.T) {
	ctx := context.Background()
	first, second := t.TempDir(), t.TempDir()
	drive := &memoryDrive{files: map[string]driveRevision{}, data: map[string]revision{}}
	link := workspaceLink{
		SyncStatus: SyncStatus{Enabled: true, Name: "fixture"},
		Account:    strings.Repeat("a", 64),
	}
	for _, root := range []string{first, second} {
		if err := canonical.WithWriter(root, func(w *canonical.Writer) error { return saveLink(w, link) }); err != nil {
			t.Fatal(err)
		}
	}
	run := func(root string) (SyncStatus, error) {
		t.Helper()
		var current workspaceLink
		if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
			var err error
			current, err = readLink(w)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return syncWithStore(ctx, root, current, drive)
	}
	snapshot := func(root string) decisionlog.Snapshot {
		t.Helper()
		var value decisionlog.Snapshot
		if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
			var err error
			value, err = decisionlog.ReadSnapshot(w)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return value
	}

	var id string
	err := canonical.WithWriter(first, func(w *canonical.Writer) error {
		if err := decisionlog.InitializeSettings(w); err != nil {
			return err
		}
		records, err := (decisionlog.Settings{}).ExtractSummary("2026-11-12_fixture.txt",
			[]byte("【Karte】【決定】同期の検証。"), "https://example.invalid/source", time.Now())
		if err != nil || len(records) != 1 {
			t.Fatalf("extract decision: %v", err)
		}
		id = records[0].ID
		_, err = decisionlog.Accept(w, records[0])
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := run(first); err != nil {
		t.Fatal(err)
	}
	if _, err := run(second); err != nil {
		t.Fatal(err)
	}
	base := snapshot(second)
	if base.Records[id].ID != id || base.Settings == nil || len(base.Settings.PrivateTogether) != 1 {
		t.Fatal("sync did not restore the record and publication settings")
	}

	edit := func(root, content string) {
		t.Helper()
		if err := canonical.WithWriter(root, func(w *canonical.Writer) error {
			before, err := decisionlog.ReadSnapshot(w)
			if err != nil {
				return err
			}
			after := decisionlog.Snapshot{Settings: before.Settings, Records: map[string]decisionlog.Record{}}
			record := before.Records[id]
			record.Content = content
			after.Records[id] = record
			return decisionlog.ApplySnapshot(w, before, after)
		}); err != nil {
			t.Fatal(err)
		}
	}
	drive.offline = true
	edit(second, "second offline edit")
	if _, err := run(second); err == nil || snapshot(second).Records[id].Content != "second offline edit" {
		t.Fatal("offline sync changed the local record")
	}
	drive.offline = false
	if _, err := run(second); err != nil {
		t.Fatal(err)
	}
	if _, err := run(first); err != nil || snapshot(first).Records[id].Content != "second offline edit" {
		t.Fatalf("reverse sync failed: %v", err)
	}

	edit(first, "first concurrent edit")
	edit(second, "second concurrent edit")
	if _, err := run(first); err != nil {
		t.Fatal(err)
	}
	if status, err := run(second); err != nil || status.Conflicts != 1 {
		t.Fatalf("sync missed the conflict: %+v %v", status, err)
	}
	if status, err := run(first); err != nil || status.Conflicts != 1 {
		t.Fatalf("origin did not receive the conflict: %+v %v", status, err)
	}
	for _, pair := range [][3]string{{first, "first concurrent edit", "second concurrent edit"}, {second, "second concurrent edit", "first concurrent edit"}} {
		if snapshot(pair[0]).Records[id].Content != pair[1] {
			t.Fatal("sync overwrote a concurrent edit")
		}
		path := filepath.Join(pair[0], syncDir, "conflicts")
		files, err := os.ReadDir(path)
		if err != nil || len(files) != 1 {
			t.Fatalf("expected one conflict copy: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(path, files[0].Name()))
		var copy revision
		if err != nil || json.Unmarshal(raw, &copy) != nil || copy.Snapshot.Records[id].Content != pair[2] {
			t.Fatal("other concurrent version was not retained")
		}
	}
}
