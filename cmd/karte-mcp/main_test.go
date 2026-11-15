package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestSharedContextRequiresExplicitAgentCredential(t *testing.T) {
	t.Setenv("KARTE_DATA_DIR", t.TempDir())
	t.Setenv("KARTE_DECISION_AGENT_TOKEN", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--shared-context"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "requires") {
		t.Fatalf("shared context without token must fail closed: %d %q", code, stderr.String())
	}
}

func TestSharedContextRefusesNonLoopbackHTTP(t *testing.T) {
	t.Setenv("KARTE_DATA_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"--shared-context", "--agent-http", "0.0.0.0:8877"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "loopback") {
		t.Fatalf("shared context must not listen publicly: %d %q", code, stderr.String())
	}
}

func TestSharedContextDoesNotIssueTokensInServingMode(t *testing.T) {
	t.Setenv("KARTE_DATA_DIR", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := run([]string{"--shared-context", "--issue-decision-token", "agent:chatgpt"}, strings.NewReader(""), &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "separately") {
		t.Fatalf("token issuance must be separate: %d %q", code, stderr.String())
	}
}
