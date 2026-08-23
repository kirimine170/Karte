package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCsvPageStrictRFC4180BOMCRLFAndPagination(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	content := "\ufeffname,notes\r\nAlice,\"line one\r\nline two\"\r\nBob,\"escaped \"\"quote\"\"\"\r\nCarol,last\r\n"
	writeCSVStoreFixture(t, root, "data/csv/people.csv", []byte(content))

	first, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/people.csv", Page: 1, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Header) != 2 || first.Header[0] != "name" || first.TotalRows != 3 || len(first.Rows) != 2 || !first.HasMore {
		t.Fatalf("first page = %#v", first)
	}
	if first.Rows[0][1] != "line one\r\nline two" || first.Rows[1][1] != `escaped "quote"` {
		t.Fatalf("quoted fields = %#v", first.Rows)
	}
	if !strings.HasPrefix(first.Revision, "sha256:") || len(first.Revision) != len("sha256:")+64 {
		t.Fatalf("revision = %q", first.Revision)
	}

	second, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/people.csv", Page: 2, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Rows) != 1 || second.Rows[0][0] != "Carol" || second.HasMore || second.Revision != first.Revision {
		t.Fatalf("second page = %#v", second)
	}
}

func TestCsvParserAllowsExactlyOneLeadingBOMBeforeQuotedHeader(t *testing.T) {
	parser := newStrictCSVParser(strings.NewReader("\ufeff\"name\",value\r\nAlice,1\r\n"))
	header, err := parser.ReadRecord()
	if err != nil || len(header) != 2 || header[0] != "name" {
		t.Fatalf("quoted header after BOM = %#v，error %v", header, err)
	}
	row, err := parser.ReadRecord()
	if err != nil || len(row) != 2 || row[0] != "Alice" {
		t.Fatalf("row after BOM = %#v，error %v", row, err)
	}

	double := newStrictCSVParser(strings.NewReader("\ufeff\ufeffname\r\n"))
	if _, err := double.ReadRecord(); err == nil || !strings.Contains(err.Error(), "BOM") {
		t.Fatalf("double leading BOM error = %v", err)
	}
}

func TestCsvStrictParserRejectsMalformedLimitsAndInvalidUTF8(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
		want    string
	}{
		{name: "bare quote", content: []byte("a,b\nx,y\"z\n"), want: "quote in an unquoted field"},
		{name: "unterminated", content: []byte("a,b\n\"x,y\n"), want: "unterminated"},
		{name: "after quote", content: []byte("a,b\n\"x\"z,y\n"), want: "after a closing quote"},
		{name: "field mismatch", content: []byte("a,b\nx\n"), want: "expected 2"},
		{name: "invalid utf8", content: []byte{'a', '\n', 0xff, '\n'}, want: "valid UTF-8"},
		{name: "internal bom", content: []byte("a,b\nx,\xef\xbb\xbfvalue\n"), want: "BOM"},
		{name: "empty header", content: []byte(",\nvalue,other\n"), want: "non-empty field"},
		{name: "oversized cell", content: []byte("h\n" + strings.Repeat("x", csvMaxCellBytes+1) + "\n"), want: "cell exceeds"},
		{name: "oversized record", content: []byte("h\n" + strings.Repeat(strings.Repeat("x", 220*1024)+",", 5) + "tail\n"), want: "record exceeds"},
		{name: "too many fields", content: []byte(strings.Repeat("h,", csvMaxFields) + "h\n"), want: "field limit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parser := newStrictCSVParser(strings.NewReader(string(test.content)))
			var err error
			for err == nil {
				_, err = parser.ReadRecord()
			}
			if errors.Is(err, io.EOF) || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v，want %q", err, test.want)
			}
		})
	}
}

func TestCsvParserFileByteLimitExactBoundary(t *testing.T) {
	exact := newStrictCSVParser(strings.NewReader("h\n"))
	exact.bytesRead = defaultMaxCSVBytes - 2
	if record, err := exact.ReadRecord(); err != nil || len(record) != 1 || record[0] != "h" {
		t.Fatalf("exact max record = %#v，error %v", record, err)
	}

	over := newStrictCSVParser(strings.NewReader("h\n"))
	over.bytesRead = defaultMaxCSVBytes - 1
	if _, err := over.ReadRecord(); err == nil || !strings.Contains(err.Error(), "file exceeds") {
		t.Fatalf("max+1 file error = %v", err)
	}
}

func TestCsvPageDecodedTransferLimit(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	cell := strings.Repeat("x", csvMaxCellBytes)
	var content strings.Builder
	content.WriteString("value\r\n")
	for index := 0; index < 17; index++ {
		content.WriteByte('"')
		content.WriteString(cell)
		content.WriteString("\"\r\n")
	}
	writeCSVStoreFixture(t, root, "data/csv/large-page.csv", []byte(content.String()))
	_, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/large-page.csv", Page: 1, Limit: 50})
	if err == nil || !strings.Contains(err.Error(), "decoded transfer limit") {
		t.Fatalf("decoded page error = %v", err)
	}
}

func TestCsvPageJSONTransferLimitCountsEscaping(t *testing.T) {
	t.Run("rows", func(t *testing.T) {
		app, root := newCSVStoreFixture(t)
		quotedCell := `"` + strings.Repeat(`""`, csvMaxCellBytes) + `"`
		var content strings.Builder
		content.WriteString("value\r\n")
		for index := 0; index < 8; index++ {
			content.WriteString(quotedCell)
			content.WriteString("\r\n")
		}
		writeCSVStoreFixture(t, root, "data/csv/json-large.csv", []byte(content.String()))
		_, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/json-large.csv", Page: 1, Limit: 50})
		if err == nil || !strings.Contains(err.Error(), "JSON transfer limit") {
			t.Fatalf("JSON transfer error = %v", err)
		}
	})

	t.Run("header only", func(t *testing.T) {
		app, root := newCSVStoreFixture(t)
		cell := strings.Repeat("<", 250*1024)
		header := strings.Join([]string{cell, cell, cell, cell}, ",") + "\r\n"
		writeCSVStoreFixture(t, root, "data/csv/json-header.csv", []byte(header))
		_, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/json-header.csv", Page: 1, Limit: 50})
		if err == nil || !strings.Contains(err.Error(), "JSON transfer limit") {
			t.Fatalf("header-only JSON transfer error = %v", err)
		}
	})
}

func TestCsvPageRejectsEmptyFileAndPagesOneThousandRows(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	writeCSVStoreFixture(t, root, "data/csv/empty.csv", nil)
	if _, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/empty.csv", Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("empty CSV error = %v", err)
	}

	var content strings.Builder
	content.WriteString("id,value\r\n")
	for index := 0; index < 1_000; index++ {
		fmt.Fprintf(&content, "%d,row-%04d\r\n", index, index)
	}
	writeCSVStoreFixture(t, root, "data/csv/thousand.csv", []byte(content.String()))
	page, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/thousand.csv", Page: 20, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if page.TotalRows != 1_000 || len(page.Rows) != 50 || page.HasMore || page.Rows[49][1] != "row-0999" {
		t.Fatalf("thousand-row page = %#v", page)
	}
	if _, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/thousand.csv", Page: 21, Limit: 50}); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("out-of-range page error = %v", err)
	}
}

func TestCsvPageDetectsSameInodeMutationBetweenDataAndRevisionPasses(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	path := "data/csv/read-race.csv"
	writeCSVStoreFixture(t, root, path, []byte("h\naaaa\n"))
	hooks := defaultCSVStoreHooks()
	var opens atomic.Int32
	hooks.afterFileLstat = func(csvRoot *os.Root, name string, _ os.FileInfo) error {
		if opens.Add(1) == 2 {
			return csvRoot.WriteFile(name, []byte("h\nbbbb\n"), 0o644)
		}
		return nil
	}
	app.csvStore.hooks = &hooks
	if _, err := app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50}); !errors.Is(err, errCSVRevisionConflict) {
		t.Fatalf("same-inode read race error = %v", err)
	}
}

func TestCsvStoreRejectsTraversalSymlinkNonRegularAndTOCTOU(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	writeCSVStoreFixture(t, root, "data/csv/safe.csv", []byte("h\nv\n"))
	for _, path := range []string{"../safe.csv", "/tmp/safe.csv", "data/csv/../safe.csv", `data\csv\safe.csv`, "data/CSV/safe.csv", "data/csv/safe.txt"} {
		if _, err := app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50}); err == nil {
			t.Fatalf("invalid path %q was accepted", path)
		}
	}

	if runtime.GOOS != "windows" {
		t.Run("ancestor symlink", func(t *testing.T) {
			dataRoot := t.TempDir()
			outside := t.TempDir()
			if err := os.MkdirAll(filepath.Join(outside, "csv"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(dataRoot, "data")); err != nil {
				t.Fatal(err)
			}
			if _, err := (&App{dataDir: dataRoot}).GetCsvPage(CsvPageRequest{Path: "data/csv/x.csv", Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("ancestor symlink error = %v", err)
			}
		})

		t.Run("final symlink", func(t *testing.T) {
			outside := filepath.Join(t.TempDir(), "outside.csv")
			if err := os.WriteFile(outside, []byte("h\noutside\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(root, "data", "csv", "linked.csv")); err != nil {
				t.Fatal(err)
			}
			if _, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/linked.csv", Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "non-symlink") {
				t.Fatalf("final symlink error = %v", err)
			}
		})

		t.Run("hard link", func(t *testing.T) {
			alias := filepath.Join(root, "data", "csv", "alias.csv")
			if err := os.Link(filepath.Join(root, "data", "csv", "safe.csv"), alias); err != nil {
				t.Fatal(err)
			}
			if _, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/safe.csv", Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "hard-link") {
				t.Fatalf("hard-link error = %v", err)
			}
			if _, err := os.Lstat(alias); err != nil {
				t.Fatalf("non-reserved hard-link alias was removed: %v", err)
			}
			if err := os.Remove(alias); err != nil {
				t.Fatal(err)
			}
		})
	}

	if err := os.Mkdir(filepath.Join(root, "data", "csv", "directory.csv"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/directory.csv", Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "regular") {
		t.Fatalf("nonregular error = %v", err)
	}
	writeCSVStoreFixture(t, root, "data/csv/Case.csv", []byte("h\nv\n"))
	if _, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/case.csv", Page: 1, Limit: 50}); err == nil {
		t.Fatalf("case alias error = %v", err)
	}

	hooks := defaultCSVStoreHooks()
	hooks.afterFileLstat = func(csvRoot *os.Root, name string, _ os.FileInfo) error {
		if err := csvRoot.Rename(name, "old.csv"); err != nil {
			return err
		}
		return csvRoot.WriteFile(name, []byte("h\nreplacement\n"), 0o644)
	}
	app.csvStore.hooks = &hooks
	if _, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/safe.csv", Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("TOCTOU error = %v", err)
	}
}

func TestCsvStoreRejectsDataRootSymlinkAndSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation may require elevated Windows privileges")
	}
	t.Run("root symlink", func(t *testing.T) {
		parent := t.TempDir()
		outside := t.TempDir()
		link := filepath.Join(parent, "linked-root")
		if err := os.Symlink(outside, link); err != nil {
			t.Fatal(err)
		}
		if _, err := (&App{dataDir: link}).GetCsvPage(CsvPageRequest{Path: "data/csv/x.csv", Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "non-symlink") {
			t.Fatalf("root symlink error = %v", err)
		}
	})

	t.Run("root swap", func(t *testing.T) {
		app, root := newCSVStoreFixture(t)
		writeCSVStoreFixture(t, root, "data/csv/x.csv", []byte("h\nv\n"))
		hooks := defaultCSVStoreHooks()
		hooks.afterDataRootOpen = func(_ *os.Root, _ os.FileInfo) error {
			moved := root + "-moved"
			if err := os.Rename(root, moved); err != nil {
				return err
			}
			if err := os.Mkdir(root, 0o755); err != nil {
				return err
			}
			return nil
		}
		app.csvStore.hooks = &hooks
		if _, err := app.GetCsvPage(CsvPageRequest{Path: "data/csv/x.csv", Page: 1, Limit: 50}); err == nil || !strings.Contains(err.Error(), "changed") {
			t.Fatalf("root swap error = %v", err)
		}
	})
}

func TestCsvSaveOptimisticRevisionAndPageSemantics(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	var content strings.Builder
	content.WriteString("id,value\r\n")
	for index := 0; index < 75; index++ {
		fmt.Fprintf(&content, "%d,row-%d\r\n", index, index)
	}
	path := "data/csv/edit.csv"
	writeCSVStoreFixture(t, root, path, []byte(content.String()))
	first, err := app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}

	invalid := []CsvSavePageRequest{
		{Path: path, Revision: first.Revision, Page: 3, Limit: 50, Header: first.Header, Rows: first.Rows},
		{Path: path, Revision: first.Revision, Page: 1, Limit: 50, Header: []string{"only"}, Rows: first.Rows},
		{Path: path, Revision: first.Revision, Page: 1, Limit: 50, Header: first.Header, Rows: first.Rows[:49]},
		{Path: path, Revision: first.Revision, Page: 1, Limit: 50, Header: first.Header, Rows: [][]string{{"one-field"}}},
	}
	for index, request := range invalid {
		if _, err := app.SaveCsvPage(request); err == nil {
			t.Fatalf("invalid edit %d was accepted", index)
		}
	}

	second, err := app.GetCsvPage(CsvPageRequest{Path: path, Page: 2, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	second.Rows = append(second.Rows, []string{"75", "new-final-row"})
	saved, err := app.SaveCsvPage(CsvSavePageRequest{
		Path: path, Revision: second.Revision, Page: 2, Limit: 50, Header: second.Header, Rows: second.Rows,
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.TotalRows != 76 || saved.Revision == second.Revision {
		t.Fatalf("save result = %#v", saved)
	}
	bytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "\n") && !strings.Contains(string(bytes), "\r\n") {
		t.Fatal("saved CSV did not use CRLF records")
	}
	if _, err := app.SaveCsvPage(CsvSavePageRequest{
		Path: path, Revision: second.Revision, Page: 2, Limit: 50, Header: second.Header, Rows: second.Rows,
	}); !errors.Is(err, errCSVRevisionConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
}

func TestCsvRevisionDetectsSameMtimeSameSizeAndPrePublishMutation(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	path := "data/csv/revision.csv"
	absolute := filepath.Join(root, filepath.FromSlash(path))
	original := []byte("h\naaaa\n")
	writeCSVStoreFixture(t, root, path, original)
	stamp := time.Unix(1_900_000_000, 0)
	if err := os.Chtimes(absolute, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	page, err := app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte("h\nbbbb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(absolute, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err := app.SaveCsvPage(CsvSavePageRequest{Path: path, Revision: page.Revision, Page: 1, Limit: 50, Header: page.Header, Rows: page.Rows}); !errors.Is(err, errCSVRevisionConflict) {
		t.Fatalf("same metadata conflict = %v", err)
	}

	page, err = app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	hooks := defaultCSVStoreHooks()
	hooks.beforePublishRevalidate = func(_ *os.Root, _ string) error {
		if err := os.WriteFile(absolute, []byte("h\ncccc\n"), 0o644); err != nil {
			return err
		}
		return os.Chtimes(absolute, stamp, stamp)
	}
	app.csvStore.hooks = &hooks
	if _, err := app.SaveCsvPage(CsvSavePageRequest{Path: path, Revision: page.Revision, Page: 1, Limit: 50, Header: page.Header, Rows: [][]string{{"edited"}}}); !errors.Is(err, errCSVRevisionConflict) {
		t.Fatalf("pre-publish mutation conflict = %v", err)
	}
	assertNoCSVTemporaryFiles(t, root)
}

func TestCsvAtomicSaveFaultsAndPartialWrites(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*csvStoreHooks)
		wantError string
	}{
		{
			name: "partial writes complete",
			configure: func(hooks *csvStoreHooks) {
				hooks.write = func(file *os.File, data []byte) (int, error) {
					if len(data) > 1 {
						data = data[:1]
					}
					return file.Write(data)
				}
			},
		},
		{
			name: "write fault",
			configure: func(hooks *csvStoreHooks) {
				hooks.write = func(_ *os.File, _ []byte) (int, error) { return 0, errors.New("ENOSPC") }
			},
			wantError: "ENOSPC",
		},
		{
			name: "sync fault",
			configure: func(hooks *csvStoreHooks) {
				hooks.sync = func(_ *os.File) error { return errors.New("sync fault") }
			},
			wantError: "sync fault",
		},
		{
			name: "close fault",
			configure: func(hooks *csvStoreHooks) {
				hooks.close = func(_ *os.File) error { return errors.New("close fault") }
			},
			wantError: "close fault",
		},
		{
			name: "replace fault",
			configure: func(hooks *csvStoreHooks) {
				hooks.replace = func(_ *os.Root, _, _ string) error { return errors.New("rename fault") }
			},
			wantError: "rename fault",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			app, root := newCSVStoreFixture(t)
			path := "data/csv/fault.csv"
			original := []byte("h\nold\n")
			writeCSVStoreFixture(t, root, path, original)
			page, err := app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50})
			if err != nil {
				t.Fatal(err)
			}
			hooks := defaultCSVStoreHooks()
			test.configure(&hooks)
			app.csvStore.hooks = &hooks
			_, err = app.SaveCsvPage(CsvSavePageRequest{
				Path: path, Revision: page.Revision, Page: 1, Limit: 50, Header: page.Header, Rows: [][]string{{"new"}},
			})
			if test.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
				updated, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
				if readErr != nil || !strings.Contains(string(updated), "new") {
					t.Fatalf("updated bytes = %q，error %v", updated, readErr)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v，want %q", err, test.wantError)
				}
				unchanged, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
				if readErr != nil || string(unchanged) != string(original) {
					t.Fatalf("fault changed destination to %q，error %v", unchanged, readErr)
				}
			}
			assertNoCSVTemporaryFiles(t, root)
		})
	}
}

func TestCsvPostCommitSyncFaultReturnsCommittedUncertainError(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	path := "data/csv/committed.csv"
	writeCSVStoreFixture(t, root, path, []byte("h\nold\n"))
	page, err := app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	hooks := defaultCSVStoreHooks()
	hooks.syncRoot = func(_ *os.Root) error { return errors.New("directory sync fault") }
	app.csvStore.hooks = &hooks
	result, err := app.SaveCsvPage(CsvSavePageRequest{
		Path: path, Revision: page.Revision, Page: 1, Limit: 50, Header: page.Header, Rows: [][]string{{"committed"}},
	})
	if !errors.Is(err, errCSVCommitUncertain) || result.Revision == "" || result.Revision == page.Revision {
		t.Fatalf("post-commit result = %#v，error %v", result, err)
	}
	updated, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil || !strings.Contains(string(updated), "committed") {
		t.Fatalf("committed bytes = %q，error %v", updated, err)
	}
	assertNoCSVTemporaryFiles(t, root)
}

func TestCsvNewFilePublicationFaultBoundariesAndMode(t *testing.T) {
	t.Run("normal mode", func(t *testing.T) {
		app, root := newCSVStoreFixture(t)
		path := "data/csv/new.csv"
		result, err := app.SaveCsvPage(CsvSavePageRequest{
			Path: path, Page: 1, Limit: 50, Header: []string{"h"}, Rows: [][]string{{"v"}},
		})
		if err != nil || result.Revision == "" {
			t.Fatalf("new result = %#v，error %v", result, err)
		}
		if runtime.GOOS != "windows" {
			info, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(path)))
			if statErr != nil {
				t.Fatal(statErr)
			}
			if info.Mode().Perm() != 0o644 {
				t.Fatalf("new mode = %v，want 0644", info.Mode().Perm())
			}
		}
		assertNoCSVTemporaryFiles(t, root)
	})

	t.Run("link fault is pre-commit", func(t *testing.T) {
		app, root := newCSVStoreFixture(t)
		hooks := defaultCSVStoreHooks()
		hooks.link = func(_ *os.Root, _, _ string) error { return errors.New("link fault") }
		app.csvStore.hooks = &hooks
		path := "data/csv/link-fault.csv"
		if _, err := app.SaveCsvPage(CsvSavePageRequest{
			Path: path, Page: 1, Limit: 50, Header: []string{"h"}, Rows: [][]string{{"v"}},
		}); err == nil || !strings.Contains(err.Error(), "link fault") {
			t.Fatalf("link fault error = %v", err)
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(path))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("link fault published destination: %v", err)
		}
		assertNoCSVTemporaryFiles(t, root)
	})

	t.Run("post-commit sync fault reports uncertainty", func(t *testing.T) {
		app, root := newCSVStoreFixture(t)
		hooks := defaultCSVStoreHooks()
		hooks.syncRoot = func(_ *os.Root) error { return errors.New("directory sync fault") }
		app.csvStore.hooks = &hooks
		path := "data/csv/sync-fault.csv"
		result, err := app.SaveCsvPage(CsvSavePageRequest{
			Path: path, Page: 1, Limit: 50, Header: []string{"h"}, Rows: [][]string{{"committed"}},
		})
		if !errors.Is(err, errCSVCommitUncertain) || result.Revision == "" || !strings.Contains(err.Error(), "directory Sync") {
			t.Fatalf("post-commit create result = %#v，error %v", result, err)
		}
		assertNoCSVTemporaryFiles(t, root)
	})

	t.Run("post-commit cleanup fault reports uncertainty", func(t *testing.T) {
		app, root := newCSVStoreFixture(t)
		hooks := defaultCSVStoreHooks()
		hooks.remove = func(_ *os.Root, _ string) error { return errors.New("cleanup fault") }
		app.csvStore.hooks = &hooks
		path := "data/csv/cleanup-fault.csv"
		result, err := app.SaveCsvPage(CsvSavePageRequest{
			Path: path, Page: 1, Limit: 50, Header: []string{"h"}, Rows: [][]string{{"committed"}},
		})
		if !errors.Is(err, errCSVCommitUncertain) || result.Revision == "" || !strings.Contains(err.Error(), "cleanup") {
			t.Fatalf("cleanup fault result = %#v，error %v", result, err)
		}
		matches, globErr := filepath.Glob(filepath.Join(root, "data", "csv", csvStoreTemporaryNamePrefix+"*.tmp"))
		if globErr != nil || len(matches) != 1 {
			t.Fatalf("committed cleanup fault temps = %v，error %v", matches, globErr)
		}
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(path))); statErr != nil {
			t.Fatalf("committed destination error = %v", statErr)
		}

		app.csvStore.hooks = nil
		page, reloadErr := app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50})
		if reloadErr != nil || page.Revision != result.Revision || len(page.Rows) != 1 || page.Rows[0][0] != "committed" {
			t.Fatalf("recovered committed page = %#v，error %v", page, reloadErr)
		}
		assertNoCSVTemporaryFiles(t, root)
	})
}

func TestCsvStoreConcurrentRevisionAllowsOnlyOneSave(t *testing.T) {
	app, root := newCSVStoreFixture(t)
	path := "data/csv/concurrent.csv"
	writeCSVStoreFixture(t, root, path, []byte("h\nold\n"))
	page, err := app.GetCsvPage(CsvPageRequest{Path: path, Page: 1, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var successes atomic.Int32
	var conflicts atomic.Int32
	done := make(chan struct{}, 2)
	for _, value := range []string{"first", "second"} {
		go func(value string) {
			_, saveErr := app.SaveCsvPage(CsvSavePageRequest{
				Path: path, Revision: page.Revision, Page: 1, Limit: 50, Header: page.Header, Rows: [][]string{{value}},
			})
			if saveErr == nil {
				successes.Add(1)
			} else if errors.Is(saveErr, errCSVRevisionConflict) {
				conflicts.Add(1)
			}
			done <- struct{}{}
		}(value)
	}
	<-done
	<-done
	if successes.Load() != 1 || conflicts.Load() != 1 {
		t.Fatalf("successes=%d，conflicts=%d", successes.Load(), conflicts.Load())
	}
}

func newCSVStoreFixture(t *testing.T) (*App, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "data", "csv"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &App{dataDir: root}, root
}

func writeCSVStoreFixture(t *testing.T, root, relativePath string, data []byte) {
	t.Helper()
	absolute := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertNoCSVTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, "data", "csv", csvStoreTemporaryNamePrefix+"*.tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("csv temporary files remain: %v", matches)
	}
}
