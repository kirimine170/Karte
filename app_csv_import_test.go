package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCsvImportNativeAndChunkedUseStrictStagedPublication(t *testing.T) {
	app, config, _, recorder := newMediaImportTestApp(t)
	config.CSVMaxBytes = 1 * 1024 * 1024
	payload := []byte("name,notes\r\nAlice,\"line one\r\nline two\"\r\n")
	source := filepath.Join(t.TempDir(), "people.csv")
	if err := os.WriteFile(source, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	nativePath, err := app.ImportCsvFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if nativePath != "data/csv/people.csv" {
		t.Fatalf("native path = %q", nativePath)
	}
	stored, err := os.ReadFile(filepath.Join(app.dataDir, filepath.FromSlash(nativePath)))
	if err != nil || !bytes.Equal(stored, payload) {
		t.Fatalf("native bytes = %q，error %v", stored, err)
	}

	session, err := app.BeginMediaImport(mediaImportKindCSV, "people.csv", int64(len(payload)))
	if err != nil {
		t.Fatal(err)
	}
	offset := int64(0)
	for start := 0; start < len(payload); start += 7 {
		end := start + 7
		if end > len(payload) {
			end = len(payload)
		}
		offset, err = app.AppendMediaImportChunk(session.ID, offset, base64.StdEncoding.EncodeToString(payload[start:end]))
		if err != nil {
			t.Fatal(err)
		}
	}
	chunkedPath, err := app.FinishMediaImport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if chunkedPath != "data/csv/people_01.csv" {
		t.Fatalf("chunked collision path = %q", chunkedPath)
	}
	if recorder.eventCount() != 2 {
		t.Fatalf("csv import events = %d，want 2", recorder.eventCount())
	}
}

func TestCsvImportRejectsMalformedSymlinkAndOverLimitWithoutPublish(t *testing.T) {
	app, config, _, recorder := newMediaImportTestApp(t)
	config.CSVMaxBytes = 32
	malformed := filepath.Join(t.TempDir(), "malformed.csv")
	if err := os.WriteFile(malformed, []byte("a,b\n\"unterminated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ImportCsvFile(malformed); err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Fatalf("malformed import error = %v", err)
	}
	if recorder.eventCount() != 0 {
		t.Fatalf("malformed import emitted %d events", recorder.eventCount())
	}
	assertNoCSVTemporaryFiles(t, app.dataDir)

	tooLarge := filepath.Join(t.TempDir(), "large.csv")
	if err := os.WriteFile(tooLarge, []byte("header\n"+strings.Repeat("x", 32)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ImportCsvFile(tooLarge); !errors.Is(err, errMediaImportTooLarge) {
		t.Fatalf("large import error = %v", err)
	}

	if runtime.GOOS != "windows" {
		target := filepath.Join(t.TempDir(), "target.csv")
		if err := os.WriteFile(target, []byte("h\nv\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "linked.csv")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := app.ImportCsvFile(link); err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("symlink import error = %v", err)
		}
	}
}

func TestCsvLegacyBase64IsStrictAndBounded(t *testing.T) {
	app, config, _, recorder := newMediaImportTestApp(t)
	config.CSVMaxBytes = 128
	config.LegacyMaxBytes = 64
	valid := base64.StdEncoding.EncodeToString([]byte("h\nvalue\n"))
	path, err := app.ImportCsvBase64("legacy.csv", valid)
	if err != nil || path != "data/csv/legacy.csv" {
		t.Fatalf("legacy import path = %q，error %v", path, err)
	}
	malformedCSV := base64.StdEncoding.EncodeToString([]byte("h\n\"unterminated\n"))
	if _, err := app.ImportCsvBase64("bad.csv", malformedCSV); err == nil || !strings.Contains(err.Error(), "unterminated") {
		t.Fatalf("malformed legacy CSV error = %v", err)
	}
	overLimit := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{'x'}, 65))
	if _, err := app.ImportCsvBase64("large.csv", overLimit); !errors.Is(err, errMediaImportTooLarge) {
		t.Fatalf("legacy limit error = %v", err)
	}
	if recorder.eventCount() != 1 {
		t.Fatalf("legacy CSV events = %d，want 1", recorder.eventCount())
	}
}
