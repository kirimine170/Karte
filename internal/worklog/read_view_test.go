package worklog

import (
	"bytes"
	"encoding/json"
	"errors"
	"karte/internal/canonical"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readViewSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		files[rel] = canonical.Hash(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestReadViewReusesExistingGeneratorWithoutMutation(t *testing.T) {
	s := fixture(t, "host_observation")
	begin(t, s, "render-one")
	for count := 0; count < 3; count++ {
		if count > 0 {
			_, err := s.Append("render-one", EventInput{EventID: "note-" + strings.Repeat("x", count), Kind: "note", Text: "<script>private</script> @import(secret.md)"})
			if err != nil {
				t.Fatal(err)
			}
		}
		before := readViewSnapshot(t, s.Root)
		view, err := s.ReadView("render-one")
		if err != nil {
			t.Fatal(err)
		}
		task, err := s.Read("render-one")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(s.Root, taskPath(task.TaskID)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(view.Markdown, renderTask(task)) || view.SourceSHA256 != canonical.Hash(raw) || view.MarkdownSHA256 != canonical.Hash(view.Markdown) || view.RelativePath != taskView(task) || view.DocID != task.DocID {
			t.Fatalf("wrong identity: %#v", view)
		}
		if !reflect.DeepEqual(before, readViewSnapshot(t, s.Root)) {
			t.Fatal("reader changed persisted files")
		}
	}
}

func TestReadViewChecksCurrentAuthorization(t *testing.T) {
	for _, mode := range []string{"disabled", "malformed-config", "revoked"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t, "host_observation")
			begin(t, s, "one")
			if _, err := s.ReadView("one"); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Root, configPath)
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "disabled":
				raw = bytes.Replace(raw, []byte(`"enabled":true`), []byte(`"enabled":false`), 1)
			case "malformed-config":
				raw = []byte(`{`)
			case "revoked":
				path = filepath.Join(s.Root, ".mdsys/context/v1/policy.json")
				raw = []byte(`{"protocol_version":"1.0","actors":{}}`)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			before := readViewSnapshot(t, s.Root)
			if _, err := s.ReadView("one"); !errors.Is(err, ErrDenied) {
				t.Fatalf("accepted revoked access: %v", err)
			}
			if !reflect.DeepEqual(before, readViewSnapshot(t, s.Root)) {
				t.Fatal("denial changed files")
			}
		})
	}
}

func TestReadViewRejectsMissingStaleModifiedAndInvalidLedger(t *testing.T) {
	for _, mode := range []string{"missing", "modified", "stale", "base-hash", "source-corrupt", "oversize"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t, "host_observation")
			begin(t, s, "one")
			task, err := s.Read("one")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(s.Root, taskView(task))
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want := ErrViewConflict
			switch mode {
			case "missing":
				err = os.Remove(path)
				want = ErrViewMissing
			case "modified":
				err = os.WriteFile(path, append(original, []byte("modified")...), 0600)
			case "stale":
				_, err = s.Append("one", EventInput{EventID: "next", Kind: "note", Text: "new"})
				if err == nil {
					err = os.WriteFile(path, original, 0600)
				}
			case "base-hash":
				bad := strings.Repeat("a", 64)
				task.ViewBaseHash = &bad
				var raw []byte
				raw, err = json.Marshal(task)
				if err == nil {
					err = os.WriteFile(filepath.Join(s.Root, taskPath("one")), raw, 0600)
				}
				want = ErrViewRecord
			case "source-corrupt":
				err = os.WriteFile(filepath.Join(s.Root, taskPath("one")), []byte(`{"task_id":"other"}`), 0600)
				want = ErrViewRecord
			case "oversize":
				err = os.WriteFile(path, bytes.Repeat([]byte("x"), maxRecordBytes+1), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ReadView("one"); !errors.Is(err, want) {
				t.Fatalf("got %v want %v", err, want)
			}
		})
	}
}

func TestReadViewRejectsV2CollisionBeforeReadingView(t *testing.T) {
	for _, mode := range []string{"owned", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			s := fixture(t, "host_observation")
			begin(t, s, "one")
			task, err := s.Read("one")
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Remove(filepath.Join(s.Root, taskView(task))); err != nil {
				t.Fatal(err)
			}
			id := "11111111-1111-4111-8111-111111111111"
			state := map[string]any{"docs": map[string]any{id: map[string]any{"target": map[string]any{"doc_id": id, "revision": 1, "sha256": strings.Repeat("a", 64)}, "path": strings.ToUpper(taskView(task)), "scope_id": "synthetic", "producer_instance_id": "synthetic", "logical_record_key": "synthetic", "human_edited": false}}, "candidates": map[string]any{}, "events": map[string]any{}}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "malformed" {
				raw = []byte(`{}`)
			}
			file := filepath.Join(s.Root, ".mdsys/ephy/records/v2/ledger.json")
			if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(file, raw, 0600); err != nil {
				t.Fatal(err)
			}
			before := readViewSnapshot(t, s.Root)
			if _, err = s.ReadView("one"); !errors.Is(err, ErrViewV2) {
				t.Fatalf("got %v; view missing must not be reached", err)
			}
			if !reflect.DeepEqual(before, readViewSnapshot(t, s.Root)) {
				t.Fatal("v2 refusal changed files")
			}
		})
	}
}
