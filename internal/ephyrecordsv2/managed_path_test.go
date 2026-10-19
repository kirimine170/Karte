package ephyrecordsv2

import (
	"bytes"
	"encoding/json"
	"karte/internal/canonical"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func ownershipLedger(path string) []byte {
	id := "11111111-1111-4111-8111-111111111111"
	state := ledger{Docs: map[string]DocState{id: {Target: Target{DocID: id, Revision: 1, SHA256: strings.Repeat("a", 64)}, Path: path, ScopeID: fixtureScope, ProducerID: fixtureProducer, LogicalKey: "conversation:" + fixtureConversation + ":1"}}, Candidates: map[string]Receipt{}, Events: map[string]eventEffect{}}
	raw, _ := json.Marshal(state)
	return raw
}

// Build real writer state, then copy only its ledger into the guard fixture.
// No document body exists in the fixture on which IsManagedPath is called.
func populatedOwnershipLedger(t *testing.T) ([]byte, string) {
	t.Helper()
	s, _ := newFixture(t)
	p := stableProposal(1)
	first := apply(t, s, p)
	next := stableProposal(2)
	next.Operation = "append_events"
	next.Target = &first.Applied
	latest := apply(t, s, next)
	duplicate := p
	duplicate.CandidateID = stableFixtureID("ownership-duplicate")
	if got := apply(t, s, duplicate); got.Status != "already_applied" || got.Applied != first.Applied {
		t.Fatal("historical redelivery did not preserve original receipt")
	}
	summary := derived(latest, p.Events[0], "summary")
	summary.CandidateID = stableFixtureID("ownership-summary")
	apply(t, s, summary)
	doc, raw, _ := state(t, s, latest.Applied.DocID)
	edited := bytes.Replace(raw, []byte(p.Events[0].Text), []byte("Synthetic human edit."), 1)
	if bytes.Equal(edited, raw) {
		t.Fatal("human edit fixture did not change text")
	}
	must(t, canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		handled, err := s.SaveHuman(w, doc.Path, edited)
		if !handled {
			t.Fatal("human edit not handled")
		}
		return err
	}))
	raw, err := os.ReadFile(filepath.Join(s.DataRoot, stateDir, "ledger.json"))
	must(t, err)
	return raw, doc.Path
}

func TestIsManagedPathAcceptsExistingWriterHistory(t *testing.T) {
	raw, path := populatedOwnershipLedger(t)
	t.Logf("synthetic_writer_ledger: %s", raw)
	var value ledger
	must(t, json.Unmarshal(raw, &value))
	statuses := map[string]bool{}
	historical := false
	emptyEvents := false
	for _, receipt := range value.Candidates {
		statuses[receipt.Status] = true
		doc := value.Docs[receipt.Applied.DocID]
		historical = historical || receipt.Applied.Revision < doc.Target.Revision && receipt.Applied.SHA256 != doc.Target.SHA256
		emptyEvents = emptyEvents || receipt.Status == "accepted" && len(receipt.EventIDs) == 0
	}
	if !reflect.DeepEqual(statuses, map[string]bool{"accepted": true, "already_applied": true, "human_edit": true}) || !historical || !emptyEvents {
		t.Fatal("writer fixture omitted a receipt state")
	}
	for _, lookup := range []string{path, "content/unmanaged.md"} {
		if found, err := checkManagedFixture(t, raw, lookup); err != nil || found != (lookup == path) {
			t.Fatalf("valid writer history rejected: %v %v", found, err)
		}
	}
}

func ownershipJSON(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var value map[string]any
	must(t, json.Unmarshal(raw, &value))
	return value
}

func ownershipObject(value map[string]any, path []string) map[string]any {
	for _, key := range path {
		value = value[key].(map[string]any)
	}
	return value
}

func TestIsManagedPathRejectsMalformedObjectsAtEveryDepth(t *testing.T) {
	raw, path := populatedOwnershipLedger(t)
	value := ownershipJSON(t, raw)
	// Dynamic ID maps have arbitrary UUID keys; their entries and all nested
	// protocol objects have fixed, required, case-sensitive field names.
	paths := [][]string{nil}
	for id := range value["docs"].(map[string]any) {
		paths = append(paths, []string{"docs", id}, []string{"docs", id, "target"})
	}
	for id := range value["candidates"].(map[string]any) {
		paths = append(paths, []string{"candidates", id}, []string{"candidates", id, "applied"}, []string{"candidates", id, "adoption"})
	}
	for id := range value["events"].(map[string]any) {
		paths = append(paths, []string{"events", id}, []string{"events", id, "receipt"}, []string{"events", id, "receipt", "applied"}, []string{"events", id, "receipt", "adoption"})
	}
	for _, objectPath := range paths {
		object := ownershipObject(value, objectPath)
		for key := range object {
			for _, mutation := range []string{"missing", "null", "case", "alias", "wrong-type"} {
				t.Run(strings.Join(append(append([]string{}, objectPath...), key, mutation), "/"), func(t *testing.T) {
					changed := ownershipJSON(t, raw)
					object := ownershipObject(changed, objectPath)
					switch mutation {
					case "missing":
						delete(object, key)
					case "null":
						object[key] = nil
					case "case":
						object[strings.ToUpper(key)] = object[key]
						delete(object, key)
					case "alias":
						object[strings.ToUpper(key)] = object[key]
					case "wrong-type":
						if _, ok := object[key].(string); ok {
							object[key] = false
						} else {
							object[key] = "wrong-type"
						}
					}
					encoded, err := json.Marshal(changed)
					must(t, err)
					if found, err := checkManagedFixture(t, encoded, path); found || err == nil {
						t.Fatalf("accepted malformed object: %v %v", found, err)
					}
				})
			}
		}
		t.Run(strings.Join(objectPath, "/")+"/unknown", func(t *testing.T) {
			changed := ownershipJSON(t, raw)
			ownershipObject(changed, objectPath)["unknown"] = false
			encoded, err := json.Marshal(changed)
			must(t, err)
			if found, err := checkManagedFixture(t, encoded, path); found || err == nil {
				t.Fatalf("accepted unknown property: %v %v", found, err)
			}
		})
	}
}

func TestIsManagedPathRejectsNullAndZeroMapEntries(t *testing.T) {
	raw, path := populatedOwnershipLedger(t)
	for _, field := range []string{"docs", "candidates", "events"} {
		for _, replacement := range []any{nil, map[string]any{}, false, "", []any{}} {
			changed := ownershipJSON(t, raw)
			entries := changed[field].(map[string]any)
			for key := range entries {
				entries[key] = replacement
				break
			}
			encoded, err := json.Marshal(changed)
			must(t, err)
			if found, err := checkManagedFixture(t, encoded, path); found || err == nil {
				t.Fatalf("accepted malformed %s entry: %v %v", field, found, err)
			}
		}
	}
}

func TestIsManagedPathRejectsInvalidLedgerSemantics(t *testing.T) {
	raw, path := populatedOwnershipLedger(t)
	for name, mutate := range map[string]func(*ledger){
		"doc-scope": func(s *ledger) {
			for id, d := range s.Docs {
				d.ScopeID = "invalid"
				s.Docs[id] = d
				break
			}
		},
		"doc-producer": func(s *ledger) {
			for id, d := range s.Docs {
				d.ProducerID = "invalid"
				s.Docs[id] = d
				break
			}
		},
		"receipt-version": func(s *ledger) {
			for id, r := range s.Candidates {
				r.SchemaVersion = "1.0"
				s.Candidates[id] = r
				break
			}
		},
		"receipt-id": func(s *ledger) {
			for id, r := range s.Candidates {
				r.CandidateID = stableFixtureID("wrong")
				s.Candidates[id] = r
				break
			}
		},
		"receipt-hash": func(s *ledger) {
			for id, r := range s.Candidates {
				r.ProposalHash = "invalid"
				s.Candidates[id] = r
				break
			}
		},
		"receipt-status": func(s *ledger) {
			for id, r := range s.Candidates {
				r.Status = "rejected"
				s.Candidates[id] = r
				break
			}
		},
		"receipt-doc": func(s *ledger) {
			for id, r := range s.Candidates {
				r.Applied.DocID = stableFixtureID("missing")
				s.Candidates[id] = r
				break
			}
		},
		"receipt-future": func(s *ledger) {
			for id, r := range s.Candidates {
				r.Applied.Revision = s.Docs[r.Applied.DocID].Target.Revision + 1
				s.Candidates[id] = r
				break
			}
		},
		"receipt-current-hash": func(s *ledger) {
			for id, r := range s.Candidates {
				r.Applied.Revision = s.Docs[r.Applied.DocID].Target.Revision
				r.Applied.SHA256 = strings.Repeat("f", 64)
				s.Candidates[id] = r
				break
			}
		},
		"receipt-event-null": func(s *ledger) {
			for id, r := range s.Candidates {
				r.EventIDs = nil
				s.Candidates[id] = r
				break
			}
		},
		"receipt-event-id": func(s *ledger) {
			for id, r := range s.Candidates {
				r.EventIDs = []string{"invalid"}
				s.Candidates[id] = r
				break
			}
		},
		"receipt-event-missing": func(s *ledger) {
			for id, r := range s.Candidates {
				r.EventIDs = []string{stableFixtureID("missing")}
				s.Candidates[id] = r
				break
			}
		},
		"receipt-adoption": func(s *ledger) {
			for id, r := range s.Candidates {
				r.Adoption = Adoption{}
				s.Candidates[id] = r
				break
			}
		},
		"event-key": func(s *ledger) {
			for key, e := range s.Events {
				delete(s.Events, key)
				s.Events["invalid"] = e
				break
			}
		},
		"event-hash": func(s *ledger) {
			for key, e := range s.Events {
				e.Hash = "invalid"
				s.Events[key] = e
				break
			}
		},
		"event-doc": func(s *ledger) {
			for key, e := range s.Events {
				e.DocID = stableFixtureID("missing")
				s.Events[key] = e
				break
			}
		},
		"event-receipt": func(s *ledger) {
			for key, e := range s.Events {
				e.Receipt.ProposalHash = strings.Repeat("f", 64)
				s.Events[key] = e
				break
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			var value ledger
			must(t, json.Unmarshal(raw, &value))
			mutate(&value)
			encoded, err := json.Marshal(value)
			must(t, err)
			if found, err := checkManagedFixture(t, encoded, path); found || err == nil {
				t.Fatalf("accepted invalid semantics: %v %v", found, err)
			}
		})
	}
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
