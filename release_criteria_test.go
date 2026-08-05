package main

import (
	"os"
	"strings"
	"testing"
)

func TestV1ReleaseCriteriaDefinesAllRequiredGates(t *testing.T) {
	b, err := os.ReadFile("RELEASE_CRITERIA_V1.md")
	if err != nil {
		t.Fatal(err)
	}
	document := string(b)
	requiredSections := []string{
		"## 判定規則",
		"## 対応platformと配布物",
		"## Gate checklist",
		"## リリース手順",
		"## Rollback判定",
		"## Evidence記録template",
	}
	for _, section := range requiredSections {
		if !strings.Contains(document, section) {
			t.Errorf("release criteria is missing section %q", section)
		}
	}

	requiredGates := []string{
		"F-01", "F-02", "F-03",
		"S-01", "S-02", "S-03",
		"C-01", "C-02", "C-03",
		"D-01", "D-02", "D-03", "D-04",
		"R-01", "R-02", "R-03",
	}
	for _, gate := range requiredGates {
		rowPrefix := "| " + gate + " |"
		if count := strings.Count(document, rowPrefix); count != 1 {
			t.Errorf("release gate %s occurs %d times, want exactly once", gate, count)
		}
	}

	for _, state := range []string{"`PASS`", "`FAIL`", "`BLOCKED`"} {
		if !strings.Contains(document, state) {
			t.Errorf("release criteria does not define state %s", state)
		}
	}
	if !strings.Contains(document, "1件でも`FAIL`／`BLOCKED`なら") {
		t.Error("release criteria must block publishing when any required gate is not PASS")
	}
}

func TestV1ReleaseCriteriaWorkflowsCanRunForCandidateSHA(t *testing.T) {
	workflows := []string{
		".github/workflows/ci.yml",
		".github/workflows/test.yml",
		".github/workflows/asr-audio-ci.yml",
		".github/workflows/frontend-ci.yml",
		".github/workflows/frontend-e2e.yml",
	}
	for _, workflow := range workflows {
		b, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "  workflow_dispatch:") {
			t.Errorf("%s cannot be manually dispatched for a release candidate", workflow)
		}
	}

	backend, err := os.ReadFile(".github/workflows/test.yml")
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(backend), "      - 'RELEASE_CRITERIA_V1.md'"); count != 2 {
		t.Errorf("Backend CI release criteria path occurs %d times, want push and pull_request", count)
	}
}
