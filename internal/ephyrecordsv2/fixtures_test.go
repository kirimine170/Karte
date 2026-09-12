package ephyrecordsv2

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"karte/internal/canonical"
	"os"
	"path/filepath"
	"testing"
)

func stableFixtureID(name string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("karte-v2-synthetic-fixture:"+name)).String()
}
func stableProposal(seq int64) Proposal {
	p := proposal(seq)
	p.CandidateID = stableFixtureID("candidate-" + string(rune('0'+seq)))
	p.Events[0].EventID = stableFixtureID("event-" + string(rune('0'+seq)))
	p.Events[0].TurnID = stableFixtureID("turn-" + string(rune('0'+(seq+1)/2)))
	if seq == 1 {
		p.Events[0].EventID = "44444444-4444-4444-8444-444444444444"
		p.Events[0].TurnID = "55555555-5555-4555-8555-555555555555"
	}
	return p
}
func signedProposal(t *testing.T, p Proposal) Proposal {
	t.Helper()
	var result Proposal
	must(t, json.Unmarshal(signed(t, p), &result))
	return result
}
func fixture(t *testing.T, protocol, name string, v any) {
	t.Helper()
	b, e := json.MarshalIndent(v, "", "  ")
	must(t, e)
	b = append(b, '\n')
	fixtureBytes(t, protocol, name, b)
}
func fixtureBytes(t *testing.T, protocol, name string, b []byte) {
	t.Helper()
	path := filepath.Join("..", "..", "schemas", protocol, "v2", "fixtures", name)
	if os.Getenv("KARTE_UPDATE_V2_FIXTURES") == "1" {
		must(t, os.MkdirAll(filepath.Dir(path), 0755))
		must(t, os.WriteFile(path, b, 0644))
		return
	}
	want, e := os.ReadFile(path)
	must(t, e)
	if !bytes.Equal(b, want) {
		t.Fatalf("fixture drift: %s；regenerate only after reviewing the protocol change", path)
	}
}
func TestSharedV2Fixtures(t *testing.T) {
	s, g := newFixture(t)
	p := stableProposal(1)
	p = signedProposal(t, p)
	first := apply(t, s, p)
	fixture(t, "karte-ephy", "grant.json", g)
	fixture(t, "karte-ephy", "conversation-user-final.proposal.json", p)
	fixture(t, "karte-ephy", "conversation-user-final.receipt.json", first)
	_, raw, record := state(t, s, first.Applied.DocID)
	fixture(t, "karte-ephy", "conversation-user-final.record.json", record)
	fixture(t, "karte-ephy", "conversation-user-final.canonical.json", map[string]string{"markdown": string(raw), "sha256": canonical.Hash(raw)})
	eventBytes, e := encodeCanonical(p.Events[0])
	must(t, e)
	fixtureBytes(t, "karte-ephy", "conversation-user-final.event.canonical.json", eventBytes)
	if canonical.Hash(eventBytes) != "01065eadd62df6a0af52210def3c81f633017119375175799a5a29e56f491f46" {
		t.Fatal("Step 0 event hash changed")
	}

	proposals := []Proposal{p}
	receipts := []Receipt{first}
	last := first
	for seq := int64(2); seq <= 8; seq++ {
		next := stableProposal(seq)
		next.Operation = "append_events"
		base := last.Applied
		next.Target = &base
		if seq%2 == 0 {
			next.Events[0].Type = "assistant_result"
			next.Events[0].InputKind = ""
			next.Events[0].ASR = nil
			next.Events[0].Text = "合成の回答本文．"
			next.Events[0].Assistant = &Assistant{Generation: "completed", Display: "confirmed_full", Playback: "unknown", SpeechUnits: []SpeechUnit{}}
		}
		next = signedProposal(t, next)
		last = apply(t, s, next)
		proposals = append(proposals, next)
		receipts = append(receipts, last)
	}
	duplicate := proposals[7]
	duplicate.CandidateID = stableFixtureID("duplicate-event")
	duplicate = signedProposal(t, duplicate)
	dupReceipt := apply(t, s, duplicate)
	changed := duplicate
	changed.Events = append([]Event(nil), duplicate.Events...)
	changed.Events[0].Text = "ID を再利用した異なる本文．"
	changed = signedProposal(t, changed)
	_, reused := s.Apply(signed(t, changed))
	errIs(t, reused, "id_reuse")
	_, _, record = state(t, s, last.Applied.DocID)
	fixture(t, "karte-ephy", "conversation-8-messages.scenario.json", map[string]any{"proposals": proposals, "receipts": receipts, "duplicate_event": duplicate, "duplicate_receipt": dupReceipt, "reused_id": changed, "reused_result": "id_reuse", "expected_event_count": 8, "expected_revision": 8, "record": record})

	independent, _ := newFixture(t)
	source := apply(t, independent, p)
	summary := derived(source, p.Events[0], "summary")
	summary.CandidateID = stableFixtureID("summary")
	summary.Derivation.JobID = stableFixtureID("summary-job")
	summary = signedProposal(t, summary)
	apply(t, independent, summary)
	diary := derived(source, p.Events[0], "ephy_diary")
	diary.CandidateID = stableFixtureID("diary")
	diary.Derivation.JobID = stableFixtureID("diary-job")
	diary = signedProposal(t, diary)
	diaryReceipt := apply(t, independent, diary)
	fixture(t, "karte-ephy", "derived-summary.proposal.json", summary)
	fixture(t, "karte-ephy", "derived-diary.proposal.json", diary)
	_, _, diaryRecord := state(t, independent, diaryReceipt.Applied.DocID)
	fixture(t, "karte-ephy", "derived-diary.record.json", diaryRecord)

	interrupted, _ := newFixture(t)
	base := apply(t, interrupted, p)
	answer := stableProposal(2)
	answer.Target = &base.Applied
	answer.Operation = "append_events"
	answer.Events[0].TurnID = p.Events[0].TurnID
	answer.Events[0].Type = "assistant_result"
	answer.Events[0].InputKind = ""
	answer.Events[0].ASR = nil
	answer.Events[0].Text = "確定した回答の前半．後半の生成は中断した．"
	answer.Events[0].Assistant = &Assistant{Generation: "canceled", Display: "confirmed_prefix", Playback: "interrupted", SpeechUnits: []SpeechUnit{{UnitID: "unit-1", State: "completed"}, {UnitID: "unit-2", State: "started"}}}
	answer = signedProposal(t, answer)
	interruptedReceipt := apply(t, interrupted, answer)
	fixture(t, "karte-ephy", "interrupted-answer.proposal.json", answer)
	_, _, interruptedRecord := state(t, interrupted, interruptedReceipt.Applied.DocID)
	fixture(t, "karte-ephy", "interrupted-answer.record.json", interruptedRecord)

	escaped, _ := newFixture(t)
	escape := stableProposal(1)
	escape.Events[0].Text = "---\ndoc_id: fake\n<!-- karte-v2:item {} -->\n> 偽 anchor\n```\n<>& 雪☃️\n```\n  空白を保持  \n"
	escape = signedProposal(t, escape)
	escapedReceipt := apply(t, escaped, escape)
	fixture(t, "karte-ephy", "raw-text.proposal.json", escape)
	_, _, escapedRecord := state(t, escaped, escapedReceipt.Applied.DocID)
	fixture(t, "karte-ephy", "raw-text.record.json", escapedRecord)

	q := query(first.Applied)
	q.RequestID = stableFixtureID("read")
	signedQ := signedRequest(t, q)
	must(t, json.Unmarshal(signedQ, &q))
	read, e := independent.Query(signedQ)
	must(t, e)
	fixture(t, "karte-context", "read-request.json", q)
	fixture(t, "karte-context", "read-response.json", read)
	q.RequestID = stableFixtureID("search")
	q.Operation = "search"
	q.Target = nil
	q.Query = &Query{Text: "", RecordTypes: []string{"conversation", "summary", "ephy_diary"}, Timezone: "Asia/Tokyo", DateFrom: "2026-09-12", DateTo: "2026-09-12", Limit: 10}
	signedQ = signedRequest(t, q)
	must(t, json.Unmarshal(signedQ, &q))
	search, e := independent.Query(signedQ)
	must(t, e)
	fixture(t, "karte-context", "search-request.json", q)
	fixture(t, "karte-context", "search-response.json", search)
	capabilities, e := independent.Capabilities()
	must(t, e)
	fixture(t, "karte-context", "capabilities.json", capabilities)
}

func TestSharedStrictJSONCases(t *testing.T) {
	var cases []struct {
		Name      string `json:"name"`
		Raw       string `json:"raw"`
		Valid     bool   `json:"valid"`
		Canonical string `json:"canonical"`
	}
	b, e := os.ReadFile(filepath.Join("..", "..", "schemas", "karte-ephy", "v2", "fixtures", "strict-json.cases.json"))
	must(t, e)
	must(t, json.Unmarshal(b, &cases))
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			actual, e := CanonicalJSON([]byte(c.Raw))
			if (e == nil) != c.Valid {
				t.Fatalf("valid=%v，error=%v", c.Valid, e)
			}
			if e == nil && string(actual) != c.Canonical {
				t.Fatalf("canonical bytes differ")
			}
		})
	}
}
