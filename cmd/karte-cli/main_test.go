package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRunListReturnsNotFoundBeforeInit(t *testing.T) {
	tmp := t.TempDir()
	var out bytes.Buffer
	var errOut bytes.Buffer
	code := run([]string{"list", "--root", tmp, "--json"}, &out, &errOut)
	if code != exitNotFound {
		t.Fatalf("expected %d, got %d", exitNotFound, code)
	}
	var resp map[string]any
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse json: %v", err)
	}
	if ok, _ := resp["ok"].(bool); ok {
		t.Fatalf("expected ok=false, got %v", ok)
	}
}

func TestRunWriteAndReadJSON(t *testing.T) {
	tmp := t.TempDir()
	contentFile := filepath.Join(tmp, "payload.md")
	if err := os.WriteFile(contentFile, []byte("# hello\n"), 0o644); err != nil {
		t.Fatalf("failed to write payload: %v", err)
	}

	var out bytes.Buffer
	var errOut bytes.Buffer
	if code := run([]string{"init", "--root", tmp, "--json"}, &out, &errOut); code != exitOK {
		t.Fatalf("init code=%d stderr=%s", code, errOut.String())
	}

	out.Reset()
	errOut.Reset()
	writeArgs := []string{"write", "--root", tmp, "--path", "test.md", "--content-file", contentFile, "--create", "--json"}
	if code := run(writeArgs, &out, &errOut); code != exitOK {
		t.Fatalf("write code=%d stderr=%s out=%s", code, errOut.String(), out.String())
	}

	out.Reset()
	errOut.Reset()
	readArgs := []string{"read", "--root", tmp, "--path", "test.md", "--json"}
	if code := run(readArgs, &out, &errOut); code != exitOK {
		t.Fatalf("read code=%d stderr=%s", code, errOut.String())
	}

	var resp struct {
		OK     bool `json:"ok"`
		Result struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse output: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected ok=true: %s", out.String())
	}
	if resp.Result.Path != "content/test.md" {
		t.Fatalf("unexpected path: %s", resp.Result.Path)
	}
	if resp.Result.Content == "" {
		t.Fatal("expected content")
	}
}

func TestNormalizeCLIPathAddsContentPrefix(t *testing.T) {
	if got := normalizeCLIPath("note.md"); got != "content/note.md" {
		t.Fatalf("unexpected normalized path: %s", got)
	}
	if got := normalizeCLIPath("content/note.md"); got != "content/note.md" {
		t.Fatalf("unexpected prefixed path: %s", got)
	}
}
