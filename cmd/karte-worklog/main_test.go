package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLIEndToEnd(t *testing.T) {
	root := t.TempDir()
	var out, diagnostic bytes.Buffer
	call := func(args []string, input string) int {
		out.Reset()
		diagnostic.Reset()
		return run(append([]string{"--root", root}, args...), strings.NewReader(input), &out, &diagnostic)
	}
	if call([]string{"init"}, "") != 0 {
		t.Fatal(diagnostic.String())
	}
	if call([]string{"--observer", "start"}, `{"task_id":"pi-test","event_id":"s","title":"CLI","objective":"observe"}`) != 0 {
		t.Fatal(out.String(), diagnostic.String())
	}
	if call([]string{"--observer", "append"}, `{"task_id":"pi-test","event_id":"f","kind":"failure","text":"tool isError=true","next_action":"inspect"}`) != 0 {
		t.Fatal(out.String(), diagnostic.String())
	}
	if call([]string{"read"}, `{"task_id":"pi-test"}`) != 0 || !strings.Contains(out.String(), "host_observation") {
		t.Fatal(out.String(), diagnostic.String())
	}
	if call([]string{"daily"}, "") != 0 || !strings.Contains(out.String(), "日報") {
		t.Fatal(out.String(), diagnostic.String())
	}
	if call([]string{"--observer", "serve"}, "") == 0 {
		t.Fatal("observer serve allowed")
	}
}
