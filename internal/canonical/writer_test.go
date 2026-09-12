package canonical

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
)

func TestWriteCASPreservesOnFaultAndExternalEdit(t *testing.T) {
	for _, external := range []bool{false, true} {
		t.Run(strconv.FormatBool(external), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "record.md")
			original := []byte("original human content\n")
			if e := os.WriteFile(path, original, 0640); e != nil {
				t.Fatal(e)
			}
			e := WithWriter(root, func(w *Writer) error {
				h := Hash(original)
				w.BeforeReplace = func(string) error {
					if external {
						return os.WriteFile(path, []byte("later human edit\n"), 0640)
					}
					return errors.New("disk fault")
				}
				return w.WriteCAS("record.md", &h, []byte("proposed overwrite"), 0640)
			})
			if e == nil {
				t.Fatal("failure was not reported")
			}
			want := original
			if external {
				want = []byte("later human edit\n")
				if !errors.Is(e, ErrConflict) {
					t.Fatal(e)
				}
			}
			got, e := os.ReadFile(path)
			if e != nil || !bytes.Equal(got, want) {
				t.Fatalf("content not preserved: %q %v", got, e)
			}
			st, e := os.Stat(path)
			if e != nil || st.Mode().Perm() != 0640 {
				t.Fatal("mode changed on failure")
			}
			temps, _ := filepath.Glob(filepath.Join(root, ".karte-write-*"))
			if len(temps) != 0 {
				t.Fatal("orphan temporary file")
			}
		})
	}
}
func TestWriterRejectsTraversalAndSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "protected.md")
	if e := os.WriteFile(target, []byte("outside"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Symlink(outside, filepath.Join(root, "escape")); e != nil {
		t.Skip(e)
	}
	for _, name := range []string{"../protected.md", "escape/protected.md"} {
		if e := WithWriter(root, func(w *Writer) error { return w.Write(name, []byte("clobber"), 0600) }); e == nil {
			t.Fatal("unsafe write succeeded")
		}
	}
	got, e := os.ReadFile(target)
	if e != nil || string(got) != "outside" {
		t.Fatal("escaped root")
	}
}
func TestCrossProcessWriterSerializesUpdates(t *testing.T) {
	if root := os.Getenv("KARTE_WRITER_FIXTURE_ROOT"); root != "" {
		for i := 0; i < 10; i++ {
			if e := WithWriter(root, func(w *Writer) error {
				b, e := w.Read("counter")
				if e != nil {
					return e
				}
				n, e := strconv.Atoi(string(b))
				if e != nil {
					return e
				}
				return w.Write("counter", []byte(strconv.Itoa(n+1)), 0600)
			}); e != nil {
				entries, statErr := os.ReadDir(filepath.Join(root, ".mdsys"))
				t.Fatalf("iteration=%d root=%s error=%v entries=%v stat=%v", i, root, e, entries, statErr)
			}
		}
		return
	}
	root := t.TempDir()
	if e := os.WriteFile(filepath.Join(root, "counter"), []byte("0"), 0600); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	fail := make(chan error, 3)
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestCrossProcessWriterSerializesUpdates$")
			cmd.Env = append(os.Environ(), "KARTE_WRITER_FIXTURE_ROOT="+root)
			output, e := cmd.CombinedOutput()
			if e != nil {
				e = fmt.Errorf("%w: %s", e, output)
			}
			fail <- e
		}()
	}
	wg.Wait()
	close(fail)
	for e := range fail {
		if e != nil {
			t.Fatal(e)
		}
	}
	b, e := os.ReadFile(filepath.Join(root, "counter"))
	if e != nil || string(b) != "30" {
		t.Fatalf("lost updates: %s %v", b, e)
	}
}

func TestPortableSlashPaths(t *testing.T) {
	root := t.TempDir()
	if e := WithWriter(root, func(w *Writer) error {
		if e := w.MkdirAll("nested/portable/directory", 0700); e != nil {
			return e
		}
		if e := w.WriteCAS("nested/portable/directory/record.md", nil, []byte("portable"), 0600); e != nil {
			return e
		}
		b, e := w.Read("nested/portable/directory/record.md")
		if e != nil {
			return e
		}
		if string(b) != "portable" {
			t.Fatal("slash path read differs")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
