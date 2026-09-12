package ephyrecordsv2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

var fixtureKey = bytes.Repeat([]byte{0x42}, 32)

const fixtureScope = "22222222-2222-4222-8222-222222222222"
const fixtureProducer = "33333333-3333-4333-8333-333333333333"
const fixtureConversation = "11111111-1111-4111-8111-111111111111"
const fixturePolicy = "66666666-6666-4666-8666-666666666666"

func newFixture(t *testing.T) (*Service, Grant) {
	t.Helper()
	base := t.TempDir()
	data := filepath.Join(base, "data")
	must(t, os.Mkdir(data, 0700))
	s, e := New(data, filepath.Join(base, "private-config"))
	must(t, e)
	s.Now = func() time.Time { return time.Date(2026, 9, 12, 0, 0, 0, 0, time.UTC) }
	g := Grant{SchemaVersion: Version, PolicyID: fixturePolicy, Revision: 1, Enabled: true, UserID: "synthetic-human", ActorID: "runtime-fixture", ProducerID: fixtureProducer, ScopeID: fixtureScope, StorageAreaID: "synthetic-only", Project: "synthetic-diary", Records: map[string]Classification{}, Operations: []string{"create_record", "append_events", "revise_derivation"}, DeniedTags: []string{}, ValidFrom: "2026-09-01T00:00:00Z", ConsentEpoch: 1, ScopeGeneration: 1, Storage: true, KeyID: "fixture-key"}
	for _, typ := range []string{"conversation", "summary", "ephy_diary"} {
		kind := "note"
		if typ == "ephy_diary" {
			kind = "journal"
		}
		g.Records[typ] = Classification{Kind: kind, Sensitivity: "internal", Tags: []string{"ephy:" + typ}, ProvenanceTypes: []string{"canonical"}}
	}
	must(t, s.Configure(g, fixtureKey))
	return s, g
}
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}
func proposal(seq int64) Proposal {
	return Proposal{SchemaVersion: Version, CandidateID: uuid.NewString(), Operation: "create_record", LogicalKey: "conversation:" + fixtureConversation + ":1", ScopeID: fixtureScope, ProducerID: fixtureProducer, Actor: contextcore.Actor{Type: "ephy", ID: "runtime-fixture"}, PolicyID: fixturePolicy, PolicyRevision: 1, ConsentEpoch: 1, ScopeGeneration: 1, Record: RecordSpec{Type: "conversation", Title: "合成会話", ConversationID: fixtureConversation, Segment: 1, Timezone: "Asia/Tokyo", LocalDate: "2026-09-12"}, Events: []Event{{ConversationID: fixtureConversation, ScopeID: fixtureScope, ProducerID: fixtureProducer, EventID: uuid.NewString(), Seq: seq, Revision: 1, TurnID: uuid.NewString(), Type: "user_final", InputKind: "asr_final", Text: "次回は冬の観測を相談したい．", OccurredAt: "2026-09-12T00:00:00Z", Timezone: "Asia/Tokyo", LocalDate: "2026-09-12", ASR: &ASR{Provider: "synthetic", ModelRevision: "fixture-1", FinalRevision: 3}, ConsentEpoch: 1}}, CreatedAt: "2026-09-12T00:00:00Z", Auth: Auth{KeyID: "fixture-key", MAC: strings.Repeat("0", 64)}}
}
func signed(t *testing.T, p Proposal) []byte {
	t.Helper()
	b, e := json.Marshal(p)
	must(t, e)
	p.Auth.MAC, e = Sign(b, fixtureKey)
	must(t, e)
	b, e = json.Marshal(p)
	must(t, e)
	return b
}
func signedRequest(t *testing.T, q Request) []byte {
	t.Helper()
	b, e := json.Marshal(q)
	must(t, e)
	q.Auth.MAC, e = Sign(b, fixtureKey)
	must(t, e)
	b, e = json.Marshal(q)
	must(t, e)
	return b
}
func apply(t *testing.T, s *Service, p Proposal) Receipt {
	t.Helper()
	r, e := s.Apply(signed(t, p))
	must(t, e)
	return r
}
func query(target Target) Request {
	return Request{ProtocolVersion: Version, RequestID: uuid.NewString(), Operation: "read", ScopeID: fixtureScope, ProducerID: fixtureProducer, Actor: contextcore.Actor{Type: "ephy", ID: "runtime-fixture"}, PolicyID: fixturePolicy, PolicyRevision: 1, ConsentEpoch: 1, ScopeGeneration: 1, CreatedAt: "2026-09-12T00:00:00Z", Auth: Auth{KeyID: "fixture-key", MAC: strings.Repeat("0", 64)}, Target: &target}
}
func state(t *testing.T, s *Service, id string) (DocState, []byte, Record) {
	t.Helper()
	var d DocState
	var raw []byte
	var r Record
	must(t, canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		l, e := loadLedger(w)
		if e != nil {
			return e
		}
		d = l.Docs[id]
		raw, e = w.Read(d.Path)
		if e != nil {
			return e
		}
		r, e = Parse(raw)
		return e
	}))
	return d, raw, r
}
func errIs(t *testing.T, e error, want string) {
	t.Helper()
	if e == nil || !strings.Contains(e.Error(), want) {
		t.Fatalf("error=%v，want %s", e, want)
	}
}

func TestEightEventsAndCandidateEventIdempotency(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(1)
	first := apply(t, s, p)
	r := first
	for i := int64(2); i <= 8; i++ {
		p = proposal(i)
		p.Operation = "append_events"
		base := r.Applied
		p.Target = &base
		if i%2 == 0 {
			p.Events[0].Type = "assistant_result"
			p.Events[0].ASR = nil
			p.Events[0].InputKind = ""
			p.Events[0].Assistant = &Assistant{Generation: "completed", Display: "confirmed_full", Playback: "unknown", SpeechUnits: []SpeechUnit{}}
		}
		r = apply(t, s, p)
		again := apply(t, s, p)
		if !reflect.DeepEqual(r, again) {
			t.Fatalf("same candidate changed result")
		}
	}
	d, b, record := state(t, s, r.Applied.DocID)
	if len(record.Events) != 8 || d.Target.Revision != 8 || canonical.Hash(b) != r.Applied.SHA256 {
		t.Fatal("lost or duplicated events")
	}
	p.CandidateID = uuid.NewString()
	p.Target = &first.Applied
	duplicate := apply(t, s, p)
	if duplicate.Status != "already_applied" || duplicate.Applied != r.Applied {
		t.Fatal("event redelivery did not return original effect")
	}
	_, after, _ := state(t, s, r.Applied.DocID)
	if !bytes.Equal(b, after) {
		t.Fatal("event re-delivery rewrote canonical")
	}
	q := query(r.Applied)
	response, e := s.Query(signedRequest(t, q))
	must(t, e)
	if len(response.Results) != 1 || len(response.Results[0].Events) != 8 {
		t.Fatalf("read failed: %+v", response)
	}
	q = query(first.Applied)
	response, e = s.Query(signedRequest(t, q))
	must(t, e)
	if len(response.Results) != 1 || len(response.Results[0].Events) != 1 {
		t.Fatal("historical revision unavailable")
	}
}
func TestIDReuseAfterReceiptPurge(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(1)
	r := apply(t, s, p)
	must(t, os.Remove(filepath.Join(s.DataRoot, receiptPath(p.CandidateID))))
	changed := p
	changed.Events = append([]Event(nil), p.Events...)
	changed.Events[0].Text = "夏"
	_, e := s.Apply(signed(t, changed))
	errIs(t, e, "id_reuse")
	changed.CandidateID = uuid.NewString()
	_, e = s.Apply(signed(t, changed))
	errIs(t, e, "id_reuse")
	again := apply(t, s, p)
	if again.Applied != r.Applied {
		t.Fatal("minimal ledger lost prior effect")
	}
}
func TestGrantAndPrivacyDenials(t *testing.T) {
	tests := map[string]func(*Proposal, *Grant){
		"actor": func(p *Proposal, g *Grant) { p.Actor.ID = "self-claimed" }, "producer": func(p *Proposal, g *Grant) { p.ProducerID = uuid.NewString(); p.Events[0].ProducerID = p.ProducerID }, "scope": func(p *Proposal, g *Grant) { p.ScopeID = uuid.NewString(); p.Events[0].ScopeID = p.ScopeID }, "policy revision": func(p *Proposal, g *Grant) { p.PolicyRevision++ }, "consent": func(p *Proposal, g *Grant) { p.ConsentEpoch++; p.Events[0].ConsentEpoch++ }, "generation": func(p *Proposal, g *Grant) { p.ScopeGeneration++ }, "record allowlist": func(p *Proposal, g *Grant) { delete(g.Records, "conversation") }, "disabled": func(p *Proposal, g *Grant) { g.Enabled = false }, "expired": func(p *Proposal, g *Grant) { g.ExpiresAt = "2026-09-11T00:00:00Z" }, "operation": func(p *Proposal, g *Grant) { g.Operations = []string{"append_events"} },
	}
	for name, edit := range tests {
		t.Run(name, func(t *testing.T) {
			s, g := newFixture(t)
			p := proposal(1)
			edit(&p, &g)
			// Fixture-only reset lets this case test the exact initial registration．
			must(t, os.Remove(filepath.Join(s.ConfigRoot, "registrations.json")))
			must(t, s.Configure(g, fixtureKey))
			_, e := s.Apply(signed(t, p))
			if e == nil {
				t.Fatal("unauthorized write accepted")
			}
			if _, e = os.Stat(filepath.Join(s.DataRoot, "content")); !os.IsNotExist(e) {
				t.Fatal("denial wrote content")
			}
		})
	}
	for _, reason := range []string{"project", "sensitivity", "tag", "provenance", "capability"} {
		t.Run("privacy "+reason, func(t *testing.T) {
			s, _ := newFixture(t)
			policy := contextcore.DefaultPolicy()
			a := policy.Actors["ephy"]
			switch reason {
			case "project":
				a.Projects = []string{"elsewhere"}
			case "sensitivity":
				a.SensitivityCeiling = "public"
			case "tag":
				a.DeniedTags = []string{"ephy:conversation"}
			case "provenance":
				a.ProvenanceTypes = []string{"manual"}
			case "capability":
				a.Capabilities = []contextcore.Capability{contextcore.CapabilityRead}
			}
			policy.Actors["ephy"] = a
			must(t, s.SetPrivacyPolicy(policy))
			_, e := s.Apply(signed(t, proposal(1)))
			errIs(t, e, "permission_denied")
		})
	}
	s, _ := newFixture(t)
	p := proposal(1)
	b := signed(t, p)
	p.Auth.MAC = strings.Repeat("0", 64)
	b, _ = json.Marshal(p)
	_, e := s.Apply(b)
	errIs(t, e, "permission_denied")
}
func TestRevokeBetweenReceptionAndCommit(t *testing.T) {
	for _, privacy := range []bool{false, true} {
		t.Run(fmt.Sprint(privacy), func(t *testing.T) {
			s, g := newFixture(t)
			s.AfterReceive = func() error {
				if privacy {
					p := contextcore.DefaultPolicy()
					a := p.Actors["ephy"]
					a.Projects = []string{"elsewhere"}
					p.Actors["ephy"] = a
					return s.SetPrivacyPolicy(p)
				}
				g.Revision++
				g.ConsentEpoch++
				g.Enabled = false
				return s.Configure(g, fixtureKey)
			}
			_, e := s.Apply(signed(t, proposal(1)))
			errIs(t, e, "permission_denied")
			if _, e = os.Stat(filepath.Join(s.DataRoot, "content")); !os.IsNotExist(e) {
				t.Fatal("revocation did not fence write")
			}
		})
	}
}
func TestCrashRecoveryAtEveryBoundary(t *testing.T) {
	for _, stage := range []string{"prepared", "canonical", "ledger", "saved", "receipt", "archive"} {
		t.Run(stage, func(t *testing.T) {
			s, _ := newFixture(t)
			p := proposal(1)
			raw := signed(t, p)
			pending := filepath.Join(s.DataRoot, outboxDir, "pending", p.CandidateID+".json")
			must(t, os.MkdirAll(filepath.Dir(pending), 0700))
			must(t, os.WriteFile(pending, raw, 0600))
			s.Fault = func(at string) error {
				if at == stage {
					return errors.New("synthetic_crash")
				}
				return nil
			}
			_, e := s.Apply(raw)
			errIs(t, e, "synthetic_crash")
			restarted, e := New(s.DataRoot, s.ConfigRoot)
			must(t, e)
			restarted.Now = s.Now
			_, e = restarted.Recover()
			must(t, e)
			receipt, e := restarted.Apply(raw)
			must(t, e)
			d, b, r := state(t, restarted, receipt.Applied.DocID)
			if d.Target.Revision != 1 || len(r.Events) != 1 || canonical.Hash(b) != receipt.Applied.SHA256 {
				t.Fatal("recovery corrupted canonical")
			}
			if _, e = os.Stat(pending); !os.IsNotExist(e) {
				t.Fatal("recovery did not archive pending")
			}
			if _, e = os.Stat(filepath.Join(s.DataRoot, txPath(p.CandidateID))); !os.IsNotExist(e) {
				t.Fatal("transaction not finalized")
			}
		})
	}
}
func TestSavedReceiptRecoveryPreservesLaterHumanBytes(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(1)
	s.Fault = func(at string) error {
		if at == "saved" {
			return errors.New("synthetic_crash")
		}
		return nil
	}
	_, e := s.Apply(signed(t, p))
	errIs(t, e, "synthetic_crash")
	s.Fault = nil
	d, _, _ := state(t, s, docID(p))
	edited := []byte("human changed this after the durable commit\n")
	must(t, os.WriteFile(filepath.Join(s.DataRoot, d.Path), edited, 0600))
	_, e = s.Recover()
	must(t, e)
	receipt := apply(t, s, p)
	if receipt.Applied != d.Target {
		t.Fatal("lost committed effect")
	}
	got, e := os.ReadFile(filepath.Join(s.DataRoot, d.Path))
	must(t, e)
	if !bytes.Equal(edited, got) {
		t.Fatal("recovery overwrote human content")
	}
	response, e := s.Query(signedRequest(t, query(receipt.Applied)))
	must(t, e)
	if len(response.Results) != 0 {
		t.Fatal("untracked human bytes leaked as canonical revision")
	}
}
func TestPreparedRecoveryReauthorizesAndPreservesConflicts(t *testing.T) {
	for _, mode := range []string{"revoked", "human"} {
		t.Run(mode, func(t *testing.T) {
			s, g := newFixture(t)
			p := proposal(1)
			s.Fault = func(at string) error {
				if at == "prepared" {
					return errors.New("synthetic_crash")
				}
				return nil
			}
			_, e := s.Apply(signed(t, p))
			errIs(t, e, "synthetic_crash")
			s.Fault = nil
			var tx transaction
			b, e := os.ReadFile(filepath.Join(s.DataRoot, txPath(p.CandidateID)))
			must(t, e)
			must(t, json.Unmarshal(b, &tx))
			if mode == "revoked" {
				g.Revision++
				g.ConsentEpoch++
				g.Enabled = false
				must(t, s.Configure(g, fixtureKey))
			} else {
				path := filepath.Join(s.DataRoot, tx.Doc.Path)
				must(t, os.MkdirAll(filepath.Dir(path), 0700))
				must(t, os.WriteFile(path, []byte("human collision"), 0600))
			}
			result, e := s.Recover()
			must(t, e)
			if len(result) != 1 || result[0].Status == "recovered" {
				t.Fatalf("unexpected recovery: %+v", result)
			}
			if mode == "human" {
				b, e = os.ReadFile(filepath.Join(s.DataRoot, tx.Doc.Path))
				must(t, e)
				if string(b) != "human collision" {
					t.Fatal("clobbered conflict")
				}
			}
		})
	}
}
func TestCollisionAndConcurrentAppends(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(1)
	collision := filepath.Join(s.DataRoot, "content", "projects", "synthetic-diary", "note", "2026-09", "ephy-v2-"+docID(p)+".md")
	must(t, os.MkdirAll(filepath.Dir(collision), 0700))
	must(t, os.WriteFile(collision, []byte("existing human document"), 0600))
	r := apply(t, s, p)
	d, _, _ := state(t, s, r.Applied.DocID)
	if !strings.HasSuffix(d.Path, "-1.md") {
		t.Fatal("collision suffix missing")
	}
	b, e := os.ReadFile(collision)
	must(t, e)
	if string(b) != "existing human document" {
		t.Fatal("collision overwritten")
	}
	p = proposal(2)
	p.Operation = "append_events"
	p.Target = &r.Applied
	raw := signed(t, p)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.Apply(raw); results <- e }()
	}
	wg.Wait()
	close(results)
	for e := range results {
		must(t, e)
	}
	_, _, record := state(t, s, r.Applied.DocID)
	if len(record.Events) != 2 {
		t.Fatal("concurrent duplicate append")
	}
	stale := proposal(3)
	stale.Operation = "append_events"
	stale.Target = &r.Applied
	_, e = s.Apply(signed(t, stale))
	errIs(t, e, "conflict")
}
func TestHumanRevisionBlocksAutomaticOverwrite(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(1)
	r := apply(t, s, p)
	d, b, _ := state(t, s, r.Applied.DocID)
	edited := bytes.Replace(b, []byte("次回は冬の観測を相談したい．"), []byte("人が編集した原文．"), 1)
	must(t, canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		handled, e := s.SaveHuman(w, d.Path, edited)
		if !handled {
			t.Fatal("human save did not own record")
		}
		return e
	}))
	current, now, record := state(t, s, r.Applied.DocID)
	if current.Target.Revision != 2 || !record.Meta.HumanEdited || !bytes.Contains(now, []byte("人が編集した原文．")) {
		t.Fatal("human revision missing")
	}
	append := proposal(2)
	append.Operation = "append_events"
	append.Target = &current.Target
	_, e := s.Apply(signed(t, append))
	errIs(t, e, "conflict")
	must(t, canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		_, e := s.SaveHuman(w, d.Path, edited)
		errIs(t, e, "conflict")
		return nil
	}))
}
