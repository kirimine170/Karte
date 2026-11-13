package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"karte/internal/decisionlog"
)

func TestGraphExcludesDecisionLinks(t *testing.T) {
	app, root := newEphyTestApp(t)
	id := "decision-" + strings.Repeat("a", 64)
	decision := decisionlog.Path(id)
	filename := id + ".md"
	privateTitle := "private-decision-title"
	paths := map[string]string{
		decision:            "# " + privateTitle + "\n",
		"content/normal.md": "# Normal\n",
		"content/nested/source.md": "[normal](../normal.md)\n" +
			"[private](../.decision-log/" + filename + ")\n" +
			"[encoded](../%2edecision-log/" + filename + ")\n" +
			"[absolute](/content/.decision-log/" + filename + ")\n" +
			"[file](file:///" + strings.TrimLeft(filepath.ToSlash(filepath.Join(root, decision)), "/") + ")\n" +
			"[[../.DECISION-LOG/" + filename + "]]\n" +
			"![private](content/.decision-log/" + filename + ")\n",
		".mdsys/links.json": `[{"id":"old-private-edge","source":"doc:/nested/source.md","target":"doc:/.decision-log/` + filename + `"}]`,
	}
	for relative, body := range paths {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}

	graph, err := app.GetGraphData()
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Edges) != 1 || graph.Edges[0].Target != "doc:/normal.md" {
		t.Fatalf("unexpected graph edges: %+v", graph.Edges)
	}
	graphJSON, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(root, ".mdsys", "links.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{graphJSON, stored} {
		if strings.Contains(string(data), id) || strings.Contains(string(data), privateTitle) {
			t.Fatal("graph exposed a private decision")
		}
	}
	original, err := os.ReadFile(filepath.Join(root, decision))
	if err != nil || string(original) != paths[decision] {
		t.Fatal("graph modified the private decision file")
	}
}
