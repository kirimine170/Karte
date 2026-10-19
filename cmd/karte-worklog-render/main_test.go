package main

import (
	"bytes"
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
