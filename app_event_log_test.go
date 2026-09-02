package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSaveEventLogsPersistsMetadataOnly(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	app.ctx = nil
	payload := `[
  {
    "component": "Sidebar",
    "action": "search-input",
    "state": {
      "query": "private search query",
      "path": "content/projects/private/secret.md",
      "candidateId": "candidate-private-id",
      "error": "private error text",
      "count": 2
    },
    "timestamp": 1788310000000
  }
]`
	ok, err := app.SaveEventLogs(payload)
	if err != nil || !ok {
		t.Fatalf("event metadata was not saved: ok=%v err=%v", ok, err)
	}
	files, err := filepath.Glob(filepath.Join(dataRoot, ".mdsys", "event-logs_*.json"))
	if err != nil || len(files) != 1 {
		t.Fatalf("event log file missing: files=%#v err=%v", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"state", "private search query", "content/projects/private", "candidate-private-id", "private error text", "\"count\""} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("event log disclosed %q: %s", secret, data)
		}
	}
	for _, metadata := range []string{"Sidebar", "search-input", "1788310000000"} {
		if !strings.Contains(string(data), metadata) {
			t.Fatalf("event log lost %q: %s", metadata, data)
		}
	}
	info, err := os.Stat(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("event log permissions are too broad: %o", info.Mode().Perm())
	}
}

func TestSaveEventLogsRejectsTrailingJSON(t *testing.T) {
	app, _ := newEphyTestApp(t)
	if _, err := app.SaveEventLogs(`[{"component":"Topbar","action":"init","timestamp":1788310000000}] {}`); err == nil {
		t.Fatal("trailing event log JSON was accepted")
	}
}

func TestOperationalLogsDiscardMessagesAndUsePrivatePermissions(t *testing.T) {
	app, dataRoot := newEphyTestApp(t)
	app.logFilePath = filepath.Join(dataRoot, "app.log")
	app.logInfo("private title，content/projects/private/secret.md，doc:private-id")
	app.LogJS("error", "private query and body")

	data, err := os.ReadFile(app.logFilePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private title", "content/projects/private", "doc:private-id", "private query", "private body"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("operational log disclosed %q: %s", secret, data)
		}
	}
	if !strings.Contains(string(data), "operation=") || !strings.Contains(string(data), "operation=javascript") {
		t.Fatalf("operational metadata is missing: %s", data)
	}
	info, err := os.Stat(app.logFilePath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("operational log permissions are too broad: %o", info.Mode().Perm())
	}
}

func TestProposalPolicyRejectsMalformedTagValues(t *testing.T) {
	if _, _, err := policyTagsFromFrontmatter(map[string]any{"tags": []any{"person:alice", 42}}); err == nil {
		t.Fatal("malformed proposal tags were accepted")
	}
}
