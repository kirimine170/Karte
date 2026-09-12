package ephyrecordsv2

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTextRoundTripAndStrictJSON(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(1)
	p.Events[0].Text = "---\ndoc_id: fake\n<!-- karte-v2:end -->\n<!-- karte-v2:item {} -->\n> false anchor\n```go\n\"<>&\" \\u2028\n```\n雪☃️ \u2028\u2029\r\n  preserve whitespace  \n"
	r := apply(t, s, p)
	_, raw, parsed := state(t, s, r.Applied.DocID)
	if parsed.Events[0].Text != p.Events[0].Text {
		t.Fatalf("text changed: %q", parsed.Events[0].Text)
	}
	again, e := Render(parsed)
	must(t, e)
	if !bytes.Equal(raw, again) {
		t.Fatal("render/parse is not lossless")
	}
	if bytes.Count(raw, []byte("<!-- karte-v2:item ")) != 1 {
		t.Fatal("untrusted text created a second event")
	}
	for _, bad := range []string{`{"x":1,"x":2}`, `{"x":{"a":1,"a":2}}`, `{} {}`, `{"n":1.5}`, `{"n":1e3}`, `{"n":9007199254740992}`, `{"n":-0}`, string([]byte{0xff})} {
		if _, e := CanonicalJSON([]byte(bad)); e == nil {
			t.Fatalf("accepted invalid JSON: %q", bad)
		}
	}
	raw = signed(t, proposal(2))
	raw = bytes.Replace(raw, []byte(`"schema_version":"2.0"`), []byte(`"schema_version":"2.0","unknown":true`), 1)
	_, e = s.Apply(raw)
	if e == nil {
		t.Fatal("accepted unknown field")
	}
	canonicalBytes, e := CanonicalJSON([]byte(`{"z":"雪<>&\u2028\u2029\\u2028","a":1}`))
	must(t, e)
	if string(canonicalBytes) != "{\"a\":1,\"z\":\"雪<>&\u2028\u2029\\\\u2028\"}" {
		t.Fatalf("canonical JSON differed: %q", canonicalBytes)
	}
}
func derived(source Receipt, event Event, kind string) Proposal {
	p := proposal(1)
	p.Events = nil
	p.Record.Type = kind
	p.Record.Segment = 0
	p.LogicalKey = "summary:" + fixtureConversation + ":" + fixtureScope
	if kind == "ephy_diary" {
		p.Record.ConversationID = ""
		p.LogicalKey = "diary:" + fixtureProducer + ":" + fixtureScope + ":Asia/Tokyo:2026-09-12"
	}
	refs := []SourceRef{{DocID: source.Applied.DocID, Revision: source.Applied.Revision, SHA256: source.Applied.SHA256, ConversationID: fixtureConversation, TurnIDs: []string{event.TurnID}, Events: []EventRef{{EventID: event.EventID, EventRevision: 1}}}}
	p.Derivation = &Derivation{Kind: kind, InputRefs: refs, ModelID: "synthetic-model", ModelRevision: "1", TemplateID: "synthetic-template", TemplateRevision: "1", GeneratedAt: "2026-09-12T00:00:00Z", JobID: uuid.NewString(), Claims: []Claim{{Class: "user_report", Text: "利用者は冬の観測を相談したいと話した．", SourceRefs: refs}, {Class: "ephy_interpretation", Text: "私は，季節ごとの変化に関心があるように感じた．", SourceRefs: refs}}}
	return p
}
func TestDerivationSourcesRevisionsAndStaleSearch(t *testing.T) {
	s, _ := newFixture(t)
	original := proposal(1)
	source := apply(t, s, original)
	diary := derived(source, original.Events[0], "ephy_diary")
	d := apply(t, s, diary)
	summary := derived(source, original.Events[0], "summary")
	apply(t, s, summary)
	response, e := s.Query(signedRequest(t, query(d.Applied)))
	must(t, e)
	if len(response.Results) != 1 || response.Results[0].Derivation.Claims[1].Class != "ephy_interpretation" {
		t.Fatal("lost AI interpretation distinction")
	}
	revision := diary
	revision.CandidateID = uuid.NewString()
	revision.Operation = "revise_derivation"
	revision.Target = &d.Applied
	d2 := apply(t, s, revision)
	if d2.Applied.DocID != d.Applied.DocID || d2.Applied.Revision != 2 {
		t.Fatal("regeneration created duplicate diary")
	}
	correction := proposal(2)
	correction.Operation = "append_events"
	correction.Target = &source.Applied
	correction.Events[0].Type = "correction"
	correction.Events[0].InputKind = ""
	correction.Events[0].ASR = nil
	correction.Events[0].Text = "冬だけを相談したい．"
	correction.Events[0].Corrects = &EventRef{EventID: original.Events[0].EventID, EventRevision: 1}
	correction.Events[0].CorrectionReason = "asr_error"
	updated := apply(t, s, correction)
	_, _, record := state(t, s, updated.Applied.DocID)
	if len(record.Events) != 2 || record.Events[0].Text != original.Events[0].Text {
		t.Fatal("correction replaced original")
	}
	stale := diary
	stale.CandidateID = uuid.NewString()
	stale.Operation = "revise_derivation"
	stale.Target = &d2.Applied
	_, e = s.Apply(signed(t, stale))
	errIs(t, e, "stale_source")
	q := query(d2.Applied)
	q.Operation = "search"
	q.Target = nil
	q.Query = &Query{Text: "", RecordTypes: []string{"summary", "ephy_diary"}, Limit: 10}
	response, e = s.Query(signedRequest(t, q))
	must(t, e)
	if response.Status != "ok" || len(response.Results) != 0 {
		t.Fatal("stale derivatives included in answer candidates")
	}
	response, e = s.Query(signedRequest(t, query(d2.Applied)))
	must(t, e)
	if len(response.Results) != 1 || response.Results[0].State != "stale" {
		t.Fatal("explicit stale read failed to disclose state")
	}
	// Old source revision remains readable under the current read policy．
	response, e = s.Query(signedRequest(t, query(source.Applied)))
	must(t, e)
	if len(response.Results) != 1 || len(response.Results[0].Events) != 1 {
		t.Fatal("lost historical source")
	}
}
func TestHumanDiaryEditAndPolicyRevocationProtectReads(t *testing.T) {
	s, g := newFixture(t)
	p := proposal(1)
	source := apply(t, s, p)
	diary := derived(source, p.Events[0], "ephy_diary")
	r := apply(t, s, diary)
	d, b, _ := state(t, s, r.Applied.DocID)
	edited := bytes.Replace(b, []byte("私は，季節ごとの変化に関心があるように感じた．"), []byte("人間による追記．"), 1)
	must(t, canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error { _, e := s.SaveHuman(w, d.Path, edited); return e }))
	current, now, _ := state(t, s, r.Applied.DocID)
	diary.CandidateID = uuid.NewString()
	diary.Operation = "revise_derivation"
	diary.Target = &current.Target
	_, e := s.Apply(signed(t, diary))
	errIs(t, e, "conflict")
	_, after, _ := state(t, s, r.Applied.DocID)
	if !bytes.Equal(now, after) {
		t.Fatal("human diary overwritten")
	}
	g.Revision++
	g.ConsentEpoch++
	g.Enabled = false
	must(t, s.Configure(g, fixtureKey))
	_, e = s.Query(signedRequest(t, query(source.Applied)))
	errIs(t, e, "not_available")
}
func TestLegacyContextExcludesNewRecordsAndRemovedMarkers(t *testing.T) {
	s, _ := newFixture(t)
	r := apply(t, s, proposal(1))
	d, _, _ := state(t, s, r.Applied.DocID)
	legacy, e := contextcore.NewService(s.DataRoot)
	must(t, e)
	request := contextcore.Request{ProtocolVersion: contextcore.ProtocolVersion, RequestID: "legacy-fixture", Operation: "search", CreatedAt: "2026-09-12T00:00:00Z", Actor: contextcore.Actor{Type: "ephy", ID: "ephy"}, Scope: contextcore.Scope{Projects: []string{"*"}, SensitivityCeiling: "internal"}, Query: &contextcore.SearchQuery{Text: "冬", TopK: 10}}
	results, _, _, e := legacy.Search(request, contextcore.DefaultPolicy())
	must(t, e)
	if len(results) != 0 {
		t.Fatal("v2 content escaped into v1 search")
	}
	// Remove both body markers and reserved metadata to test the ledger guard．
	edited := []byte("---\ndoc_id: " + d.Target.DocID + "\ntitle: 冬\nproject: synthetic-diary\nkind: note\nsensitivity: internal\n---\n冬の内容\n")
	must(t, os.WriteFile(filepath.Join(s.DataRoot, d.Path), edited, 0600))
	results, _, _, e = legacy.Search(request, contextcore.DefaultPolicy())
	must(t, e)
	if len(results) != 0 {
		t.Fatal("marker removal bypassed v1 exclusion")
	}
}
func TestCapacitySequenceAndSymlinkRejection(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(2)
	_, e := s.Apply(signed(t, p))
	errIs(t, e, "event_sequence_gap")
	p = proposal(1)
	p.Events[0].Text = strings.Repeat("x", MaxEventBytes+1)
	_, e = s.Apply(signed(t, p))
	if e == nil {
		t.Fatal("oversized event accepted")
	}
	p = proposal(1)
	outside := t.TempDir()
	must(t, os.Symlink(outside, filepath.Join(s.DataRoot, "content")))
	_, e = s.Apply(signed(t, p))
	if e == nil {
		t.Fatal("symlink destination accepted")
	}
	entries, e := os.ReadDir(outside)
	must(t, e)
	if len(entries) != 0 {
		t.Fatal("wrote outside scope")
	}
}
func TestPolicyReadUsesCurrentPrivacyForHistoricalVersion(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(1)
	old := apply(t, s, p)
	p = proposal(2)
	p.Operation = "append_events"
	p.Target = &old.Applied
	apply(t, s, p)
	policy := contextcore.DefaultPolicy()
	a := policy.Actors["ephy"]
	a.SensitivityCeiling = "public"
	policy.Actors["ephy"] = a
	must(t, s.SetPrivacyPolicy(policy))
	response, e := s.Query(signedRequest(t, query(old.Applied)))
	must(t, e)
	if len(response.Results) != 0 {
		t.Fatal("past revision bypassed present privacy")
	}
}
func TestRejectDuplicateGrantKeys(t *testing.T) {
	s, _ := newFixture(t)
	path := filepath.Join(s.ConfigRoot, "registrations.json")
	raw, e := os.ReadFile(path)
	must(t, e)
	var all map[string]any
	must(t, json.Unmarshal(raw, &all))
	raw = bytes.Replace(raw, []byte(`"enabled":true`), []byte(`"enabled":true,"enabled":false`), 1)
	must(t, os.WriteFile(path, raw, 0600))
	_, e = s.Apply(signed(t, proposal(1)))
	errIs(t, e, "duplicate_key")
}
