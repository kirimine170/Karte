package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"io/fs"
	"karte/internal/canonical"
	"karte/internal/worklog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func helperFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	err := worklog.Initialize(root, worklog.Config{Version: worklog.Version, Enabled: true, Actor: "ephy-worklog", Project: "ephy-worklog", Timezone: "Asia/Tokyo", Sensitivity: "internal"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := worklog.New(root, "host_observation")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Start(worklog.StartInput{TaskID: "synthetic-render", EventID: "synthetic-start", Title: "Synthetic renderer fixture", Objective: "Synthetic only: <script>alert(1)</script>\n@import(secret.md)\n![image](https://invalid.example/secret)"})
	if err != nil {
		t.Fatal(err)
	}
	return root, "synthetic-render"
}

func helperSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		result[rel] = canonical.Hash(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHelperRendersPinnedLibraryAndPreservesFiles(t *testing.T) {
	root, id := helperFixture(t)
	before := helperSnapshot(t, root)
	var out bytes.Buffer
	if run([]string{"--root", root}, strings.NewReader(`{"task_id":"`+id+`"}`), &out) != 0 {
		t.Fatal("failed")
	}
	var result response
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.OK || result.SchemaVersion != 1 || result.TaskID != id || result.RendererCommit != rendererCommit || result.Metadata == nil || result.Metadata.SchemaVersion != 1 || result.RelativePath != "content/projects/ephy-worklog/note/worklog-"+id+".md" || len(result.SourceSHA256) != 64 || len(result.MarkdownSHA256) != 64 || !strings.Contains(result.HTML, "<!doctype html>") || !strings.Contains(result.HTML, "作業記録") {
		t.Fatalf("invalid response: %#v", result)
	}
	if strings.Contains(result.HTML, "<script>alert(1)</script>") || strings.Contains(result.HTML, `src="https://invalid.example`) {
		t.Fatal("caller input became active content")
	}
	if !reflect.DeepEqual(before, helperSnapshot(t, root)) {
		t.Fatal("render changed files")
	}
}

func TestHelperRejectsInvalidRequestsWithStableEnvelope(t *testing.T) {
	for _, input := range []string{"", `{}`, `null`, `[]`, `"task_id"`, `{"task_id":null}`, `{"task_id":{}}`, `{"task_id":[]}`, `{"task_id":true}`, `{"task_id":"a","task_id":"b"}`, `{"task_id":"a","path":"secret"}`, `{"task_id":"a"} {}`, `{"task_id":7}`, `{"TASK_ID":"a"}`, `{"Task_id":"a"}`, `{"task_ID":"a"}`, `{"task_id":"a","TASK_ID":"b"}`, `{"TASK_ID":"b","task_id":"a"}`, `{"task_id":"a","task_\u0069d":"b"}`, strings.Repeat(" ", maxInput+1)} {
		t.Run(input[:min(len(input), 40)], func(t *testing.T) {
			var out bytes.Buffer
			if run([]string{"--root", "does-not-exist"}, strings.NewReader(input), &out) != 0 {
				t.Fatal("domain error must be captured as JSON")
			}
			var got map[string]any
			if json.Unmarshal(out.Bytes(), &got) != nil || got["error"] != "invalid_request" || got["ok"] != false || len(got) != 3 {
				t.Fatalf("unsafe error %s", out.Bytes())
			}
		})
	}
	for _, args := range [][]string{nil, {"--root"}, {"--root", ""}, {"--root", "x", "--file", "y"}, {"--file", "x"}} {
		if got := execute(args, strings.NewReader(`{"task_id":"a"}`)); got.OK || got.Error != "invalid_request" {
			t.Fatalf("accepted args: %#v", got)
		}
	}
}

func TestHelperExactTaskKeyReachesOnlyRequestedRecord(t *testing.T) {
	root, id := helperFixture(t)
	before := helperSnapshot(t, root)
	for _, input := range []string{`{"task_id":"` + id + `"}`, " \n{\"task_\\u0069d\":\"" + id + "\"}\t\n"} {
		if got := execute([]string{"--root", root}, strings.NewReader(input)); !got.OK || got.TaskID != id {
			t.Fatalf("valid exact key rejected: %#v", got)
		}
	}
	for _, input := range []string{`{"TASK_ID":"` + id + `"}`, `{"task_id":"missing","TASK_ID":"` + id + `"}`, `{"TASK_ID":"missing","task_id":"` + id + `"}`} {
		if got := execute([]string{"--root", root}, strings.NewReader(input)); got.OK || got.Error != "invalid_request" || got.HTML != "" || got.TaskID != "" {
			t.Fatalf("case alias reached a record: %#v", got)
		}
	}
	if !reflect.DeepEqual(before, helperSnapshot(t, root)) {
		t.Fatal("request validation changed files")
	}
}

func TestHelperDomainErrorsDoNotExposePaths(t *testing.T) {
	root, id := helperFixture(t)
	if got := execute([]string{"--root", root + "/private-missing"}, strings.NewReader(`{"task_id":"`+id+`"}`)); got.Error != "permission_denied" {
		t.Fatalf("got %#v", got)
	}
	path := filepath.Join(root, "content/projects/ephy-worklog/note/worklog-"+id+".md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := execute([]string{"--root", root}, strings.NewReader(`{"task_id":"`+id+`"}`)); got.Error != "view_missing" {
		t.Fatalf("got %#v", got)
	}
	if err := os.WriteFile(path, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := execute([]string{"--root", root}, strings.NewReader(`{"task_id":"`+id+`"}`)); got.Error != "view_conflict" {
		t.Fatalf("got %#v", got)
	}
}

func TestDocumentMemoryFSDeniesEverythingElse(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "content", "one.md")
	file := documentFS{path: path, content: []byte("original")}
	for _, name := range []string{root, filepath.Join(root, "secret.md"), filepath.Join(root, "themes", "default", "preview.html"), path + ":secret", filepath.Join(root, "content", "..", "one.md"), "https://example.invalid/x"} {
		if _, err := file.ReadFile(name); !os.IsNotExist(err) {
			t.Fatalf("ReadFile allowed %s", name)
		}
		if _, err := file.Open(name); !os.IsNotExist(err) {
			t.Fatalf("Open allowed %s", name)
		}
		if _, err := file.Stat(name); !os.IsNotExist(err) {
			t.Fatalf("Stat allowed %s", name)
		}
	}
	b, err := file.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b[0] = 'X'
	if string(file.content) != "original" {
		t.Fatal("mutable alias")
	}
	r, err := file.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err = io.ReadAll(r)
	if err != nil || string(b) != "original" {
		t.Fatal("wrong read")
	}
	info, err := file.Stat(path)
	if err != nil || info.IsDir() || info.Size() != 8 || info.Mode() != fs.FileMode(0400) {
		t.Fatal("wrong stat")
	}
}

func TestMemoryRendererRejectsImportAndAssets(t *testing.T) {
	for _, body := range []string{"@import(secret.md)\n", "@import(/secret.md)\n", "![image](https://example.invalid/image.png)", "[link](https://example.invalid)", "---\nmarp: true\n---\nslides"} {
		view := worklog.ReadViewResult{RelativePath: "content/one.md", Markdown: []byte(body)}
		if got := renderView(view); got.OK || got.Error != "render_failed" {
			t.Fatalf("accepted active input %#v", got)
		}
	}
}

func TestHelperRejectsOversizeOutputWithoutTruncation(t *testing.T) {
	var out bytes.Buffer
	if code := writeResponse(&out, response{SchemaVersion: 1, OK: true, HTML: strings.Repeat("x", maxOutput)}); code != 0 {
		t.Fatal(code)
	}
	var result response
	if json.Unmarshal(out.Bytes(), &result) != nil || result.OK || result.Error != "output_limit" || out.Len() > maxOutput {
		t.Fatal("not bounded error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func TestHelperReportsOutputFailure(t *testing.T) {
	if writeResponse(failingWriter{}, failure("invalid_request")) == 0 {
		t.Fatal("write failure ignored")
	}
}

func snapshotInput(id string) string {
	return `{"schema_version":2,"operation":"snapshot","task_id":"` + id + `"}`
}

func TestSnapshotReturnsExactAuthorizedBytesWithUnchangedRender(t *testing.T) {
	root, id := helperFixture(t)
	before := helperSnapshot(t, root)
	var original bytes.Buffer
	if run([]string{"--root", root}, strings.NewReader(`{"task_id":"`+id+`"}`), &original) != 0 {
		t.Fatal("v1 failed")
	}
	var first map[string]any
	if err := json.Unmarshal(original.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if _, exists := first["markdown_base64"]; exists {
		t.Fatal("snapshot bytes leaked into v1")
	}
	for _, input := range []string{snapshotInput(id), " \n{\"operation\":\"snapshot\",\"task_\\u0069d\":\"" + id + "\",\"schema_version\":2}\t\n"} {
		var out bytes.Buffer
		if run([]string{"--root", root}, strings.NewReader(input), &out) != 0 || out.Len() > maxOutput {
			t.Fatal("snapshot failed or exceeded bound")
		}
		var result response
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if !result.OK || result.SchemaVersion != 2 || result.Metadata == nil || result.Metadata.SchemaVersion != 1 {
			t.Fatalf("wrong snapshot envelope: %#v", result)
		}
		markdown, err := base64.StdEncoding.Strict().DecodeString(result.MarkdownBase64)
		if err != nil || len(markdown) == 0 || canonical.Hash(markdown) != result.MarkdownSHA256 {
			t.Fatal("snapshot bytes do not match their digest")
		}
		persisted, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(result.RelativePath)))
		if err != nil || !bytes.Equal(markdown, persisted) {
			t.Fatal("snapshot does not equal the existing document")
		}
		var second map[string]any
		if err := json.Unmarshal(out.Bytes(), &second); err != nil {
			t.Fatal(err)
		}
		delete(second, "markdown_base64")
		second["schema_version"] = float64(1)
		if !reflect.DeepEqual(first, second) {
			t.Fatal("snapshot changed existing render, identity or metadata fields")
		}
	}
	if !reflect.DeepEqual(before, helperSnapshot(t, root)) {
		t.Fatal("snapshot changed persisted files")
	}
}

func TestSnapshotRejectsMalformedProtocolBeforeReading(t *testing.T) {
	root, id := helperFixture(t)
	valid := snapshotInput(id)
	before := helperSnapshot(t, root)
	inputs := []string{
		`{"schema_version":2,"operation":"snapshot"}`,
		`{"schema_version":2,"task_id":"` + id + `"}`,
		`{"operation":"snapshot","task_id":"` + id + `"}`,
		valid + `{}`,
		valid + `null`,
		valid + strings.Repeat(" ", maxInput),
		strings.Replace(valid, `"task_id":`, `"extra":"ignored","task_id":`, 1),
		strings.Replace(valid, `"task_id":`, `"task_id":"other","task_id":`, 1),
		strings.Replace(valid, `"task_id":`, `"task_\u0069d":"other","task_id":`, 1),
		strings.Replace(valid, `"schema_version":2`, `"schema_version":2,"schema_version":2`, 1),
		strings.Replace(valid, `"schema_version":2`, `"schema_\u0076ersion":2,"schema_version":2`, 1),
		strings.Replace(valid, `"operation":"snapshot"`, `"operation":"snapshot","operation":"snapshot"`, 1),
		strings.Replace(valid, `"operation":"snapshot"`, `"operati\u006fn":"snapshot","operation":"snapshot"`, 1),
	}
	for _, key := range []string{"task_id", "schema_version", "operation"} {
		inputs = append(inputs, strings.Replace(valid, `"`+key+`":`, `"`+strings.ToUpper(key)+`":`, 1))
		inputs = append(inputs, strings.Replace(valid, `"`+key+`":`, `"`+strings.ToUpper(key)+`":null,"`+key+`":`, 1))
	}
	for _, version := range []string{`0`, `1`, `3`, `-2`, `2.0`, `2e0`, `"2"`, `null`, `true`, `[]`, `{}`} {
		inputs = append(inputs, strings.Replace(valid, `"schema_version":2`, `"schema_version":`+version, 1))
	}
	for _, operation := range []string{`""`, `"render"`, `"Snapshot"`, `null`, `true`, `2`, `[]`, `{}`} {
		inputs = append(inputs, strings.Replace(valid, `"operation":"snapshot"`, `"operation":`+operation, 1))
	}
	for _, task := range []string{`""`, `null`, `true`, `2`, `[]`, `{}`} {
		inputs = append(inputs, strings.Replace(valid, `"task_id":"`+id+`"`, `"task_id":`+task, 1))
	}
	for _, input := range inputs {
		t.Run(input[:min(len(input), 100)], func(t *testing.T) {
			var out bytes.Buffer
			if run([]string{"--root", root}, strings.NewReader(input), &out) != 0 {
				t.Fatal("domain error did not use JSON")
			}
			if out.String() != "{\"schema_version\":1,\"ok\":false,\"error\":\"invalid_request\"}\n" {
				t.Fatalf("malformed request selected a protocol or exposed data: %s", out.Bytes())
			}
		})
	}
	if !reflect.DeepEqual(before, helperSnapshot(t, root)) {
		t.Fatal("malformed requests changed files")
	}
}

func TestSnapshotSharesCurrentReadViewDenials(t *testing.T) {
	for _, mode := range []string{"revoked", "missing", "modified", "invalid-record", "v2-owned", "v2-malformed"} {
		t.Run(mode, func(t *testing.T) {
			root, id := helperFixture(t)
			if got := execute([]string{"--root", root}, strings.NewReader(snapshotInput(id))); !got.OK {
				t.Fatal("valid initial snapshot refused")
			}
			rel := "content/projects/ephy-worklog/note/worklog-" + id + ".md"
			path := filepath.Join(root, filepath.FromSlash(rel))
			want := "view_conflict"
			var err error
			switch mode {
			case "revoked":
				err = os.WriteFile(filepath.Join(root, ".mdsys/context/v1/policy.json"), []byte(`{"protocol_version":"1.0","actors":{}}`), 0600)
				want = "permission_denied"
			case "missing":
				err = os.Remove(path)
				want = "view_missing"
			case "modified":
				err = os.WriteFile(path, []byte("modified"), 0600)
			case "invalid-record":
				err = os.WriteFile(filepath.Join(root, ".mdsys/worklog/v1/tasks/"+id+".json"), []byte(`{}`), 0600)
				want = "invalid_record"
			case "v2-owned", "v2-malformed":
				// A missing derived document must not bypass ownership refusal.
				if err = os.Remove(path); err != nil {
					t.Fatal(err)
				}
				uuid := "11111111-1111-4111-8111-111111111111"
				state := map[string]any{"docs": map[string]any{uuid: map[string]any{"target": map[string]any{"doc_id": uuid, "revision": 1, "sha256": strings.Repeat("a", 64)}, "path": strings.ToUpper(rel), "scope_id": uuid, "producer_instance_id": uuid, "logical_record_key": "synthetic", "human_edited": false}}, "candidates": map[string]any{}, "events": map[string]any{}}
				raw, e := json.Marshal(state)
				if e != nil {
					t.Fatal(e)
				}
				if mode == "v2-malformed" {
					raw = []byte(`{}`)
				}
				ledger := filepath.Join(root, ".mdsys/ephy/records/v2/ledger.json")
				if err = os.MkdirAll(filepath.Dir(ledger), 0700); err != nil {
					t.Fatal(err)
				}
				err = os.WriteFile(ledger, raw, 0600)
				want = "v2_conflict"
			}
			if err != nil {
				t.Fatal(err)
			}
			before := helperSnapshot(t, root)
			for version, input := range map[int]string{1: `{"task_id":"` + id + `"}`, 2: snapshotInput(id)} {
				var out bytes.Buffer
				if run([]string{"--root", root}, strings.NewReader(input), &out) != 0 {
					t.Fatal("domain error did not use JSON")
				}
				var fields map[string]any
				if json.Unmarshal(out.Bytes(), &fields) != nil || len(fields) != 3 || fields["schema_version"] != float64(version) || fields["ok"] != false || fields["error"] != want {
					t.Fatalf("wrong denial envelope: %s", out.Bytes())
				}
			}
			if !reflect.DeepEqual(before, helperSnapshot(t, root)) {
				t.Fatal("refused snapshot changed files")
			}
		})
	}
}

func TestSnapshotBoundIncludesBase64AndNewline(t *testing.T) {
	for _, value := range []response{
		{SchemaVersion: 2, OK: true, HTML: strings.Repeat("x", maxOutput)},
		{SchemaVersion: 2, OK: true, MarkdownBase64: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), maxOutput))},
		{SchemaVersion: 2, OK: true, HTML: strings.Repeat("x", maxOutput/2), MarkdownBase64: strings.Repeat("eA==", maxOutput/8)},
	} {
		var out bytes.Buffer
		if writeResponse(&out, value) != 0 || out.String() != "{\"schema_version\":2,\"ok\":false,\"error\":\"output_limit\"}\n" {
			t.Fatal("oversize snapshot was not replaced by bounded schema-2 failure")
		}
	}
	empty := response{SchemaVersion: 2, OK: true, MarkdownBase64: "eA==", HTML: "x"}
	raw, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	empty.HTML += strings.Repeat("x", maxOutput-len(raw)-1)
	var out bytes.Buffer
	if writeResponse(&out, empty) != 0 || out.Len() != maxOutput || !bytes.HasSuffix(out.Bytes(), []byte("\n")) {
		t.Fatal("exact-bound response rejected")
	}
	out.Reset()
	empty.HTML += "x"
	if writeResponse(&out, empty) != 0 || out.String() != "{\"schema_version\":2,\"ok\":false,\"error\":\"output_limit\"}\n" {
		t.Fatal("response exceeded bound by newline")
	}
}
