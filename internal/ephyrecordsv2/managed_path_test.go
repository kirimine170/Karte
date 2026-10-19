package ephyrecordsv2

import (
	"encoding/json"
	"karte/internal/canonical"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func ownershipLedger(path string) []byte {
	id := "11111111-1111-4111-8111-111111111111"
	state := ledger{Docs: map[string]DocState{id: {Target: Target{DocID: id, Revision: 1, SHA256: strings.Repeat("a", 64)}, Path: path, ScopeID: "synthetic", ProducerID: "synthetic", LogicalKey: "synthetic"}}, Candidates: map[string]Receipt{}, Events: map[string]eventEffect{}}
	raw, _ := json.Marshal(state)
	return raw
}

func checkManagedFixture(t *testing.T, raw []byte, path string) (bool, error) {
	t.Helper()
	root := t.TempDir()
	if raw != nil {
		file := filepath.Join(root, stateDir, "ledger.json")
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var found bool
	err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		var e error
		found, e = IsManagedPath(w, path)
		return e
	})
	// No record body has been created. Matching ownership must not read one.
	if _, err := os.Stat(filepath.Join(root, "content")); !os.IsNotExist(err) {
		t.Fatalf("unexpected document access/write: %v", err)
	}
	return found, err
}

func TestIsManagedPathWithoutReadingDocument(t *testing.T) {
	path := "content/projects/synthetic/note/worklog-one.md"
	for _, tc := range []struct {
		name string
		raw  []byte
		path string
		want bool
	}{
		{"missing", nil, path, false},
		{"empty", []byte(`{"docs":{},"candidates":{},"events":{}}`), path, false},
		{"owned", ownershipLedger(path), path, true},
		{"case-alias", ownershipLedger(strings.ToUpper(path)), path, true},
		{"noncollision", ownershipLedger(path), "content/projects/synthetic/note/worklog-two.md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			found, err := checkManagedFixture(t, tc.raw, tc.path)
			if err != nil || found != tc.want {
				t.Fatalf("got %v,%v", found, err)
			}
		})
	}
}

func TestIsManagedPathRejectsMalformedState(t *testing.T) {
	path := "content/projects/synthetic/note/worklog-one.md"
	for name, raw := range map[string][]byte{
		"syntax": []byte(`{`), "missing": []byte(`{}`), "null": []byte(`{"docs":null,"candidates":{},"events":{}}`),
		"duplicate":  []byte(`{"docs":{},"docs":{},"candidates":{},"events":{}}`),
		"unknown":    []byte(`{"docs":{},"candidates":{},"events":{},"other":true}`),
		"trailing":   []byte(`{"docs":{},"candidates":{},"events":{}} {}`),
		"oversize":   []byte(strings.Repeat(" ", (2<<20)+1)),
		"empty-path": ownershipLedger(""), "traversal": ownershipLedger("content/../secret.md"),
		"ads": ownershipLedger(path + ":private"), "trailing-dot": ownershipLedger(path + "."),
		"identity": []byte(`{"docs":{"invalid":{"path":"content/a.md"}},"candidates":{},"events":{}}`),
	} {
		t.Run(name, func(t *testing.T) {
			if found, err := checkManagedFixture(t, raw, path); err == nil || found {
				t.Fatalf("accepted %v %v", found, err)
			}
		})
	}
	for _, path := range []string{"../secret.md", ".mdsys/private", "content/../secret.md", "content/a.md:secret", "content/a.md ", "C:/secret.md"} {
		t.Run(path, func(t *testing.T) {
			if _, err := checkManagedFixture(t, nil, path); err == nil {
				t.Fatal("accepted invalid path")
			}
		})
	}
}
