package ephyrecordsv2

import (
	"encoding/json"
	"github.com/google/uuid"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"os"
	"path/filepath"
	"testing"
)

func publish(t *testing.T, s *Service, dir, id string, raw []byte) {
	t.Helper()
	must(t, canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error { return w.Write(filepath.Join(dir, id+".json"), raw, 0600) }))
}
func TestMailboxProcessingAndCurrentPolicyResponseInvalidation(t *testing.T) {
	s, g := newFixture(t)
	p := proposal(1)
	raw := signed(t, p)
	publish(t, s, filepath.Join(outboxDir, "pending"), p.CandidateID, raw)
	summary, e := s.ProcessPending(20)
	must(t, e)
	if summary.Processed != 1 || summary.Failed != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	receiptBytes, e := os.ReadFile(filepath.Join(s.DataRoot, receiptPath(p.CandidateID)))
	must(t, e)
	var receipt Receipt
	must(t, json.Unmarshal(receiptBytes, &receipt))
	// Receipt re-delivery also archives the retry，without another transaction．
	publish(t, s, filepath.Join(outboxDir, "pending"), p.CandidateID, raw)
	summary, e = s.ProcessPending(20)
	must(t, e)
	if summary.Processed != 1 {
		t.Fatal("redelivery not processed")
	}
	if _, e = os.Stat(filepath.Join(s.DataRoot, outboxDir, "pending", p.CandidateID+".json")); !os.IsNotExist(e) {
		t.Fatal("successful retry remained pending")
	}
	q := query(receipt.Applied)
	qraw := signedRequest(t, q)
	publish(t, s, filepath.Join(contextDir, "requests"), q.RequestID, qraw)
	_, e = s.ProcessPending(20)
	must(t, e)
	responsePath := filepath.Join(s.DataRoot, contextDir, "responses", q.RequestID+".json")
	responseBytes, e := os.ReadFile(responsePath)
	must(t, e)
	var response Response
	must(t, json.Unmarshal(responseBytes, &response))
	if response.Status != "ok" || len(response.Results) != 1 {
		t.Fatal("mailbox read failed")
	}
	g.Revision++
	g.ConsentEpoch++
	g.Enabled = false
	must(t, s.Configure(g, fixtureKey))
	if _, e = os.Stat(responsePath); !os.IsNotExist(e) {
		t.Fatal("revocation retained response body")
	}
	publish(t, s, filepath.Join(contextDir, "requests"), q.RequestID, qraw)
	_, e = s.ProcessPending(20)
	must(t, e)
	responseBytes, e = os.ReadFile(responsePath)
	must(t, e)
	must(t, json.Unmarshal(responseBytes, &response))
	if response.Status != "not_available" || len(response.Results) != 0 {
		t.Fatal("retry replayed cached authorized body after revocation")
	}
}
func TestPrivacyPolicyAndCanonicalMutationPurgeResponseBodies(t *testing.T) {
	for _, mode := range []string{"privacy", "append"} {
		t.Run(mode, func(t *testing.T) {
			s, _ := newFixture(t)
			first := apply(t, s, proposal(1))
			q := query(first.Applied)
			publish(t, s, filepath.Join(contextDir, "requests"), q.RequestID, signedRequest(t, q))
			_, e := s.ProcessPending(20)
			must(t, e)
			if mode == "privacy" {
				policy := contextcore.DefaultPolicy()
				a := policy.Actors["ephy"]
				a.SensitivityCeiling = "public"
				policy.Actors["ephy"] = a
				must(t, s.SetPrivacyPolicy(policy))
			} else {
				p := proposal(2)
				p.Operation = "append_events"
				p.Target = &first.Applied
				apply(t, s, p)
			}
			if _, e = os.Stat(filepath.Join(s.DataRoot, contextDir, "responses", q.RequestID+".json")); !os.IsNotExist(e) {
				t.Fatal("obsolete response remained after current-state mutation")
			}
		})
	}
}
func TestMailboxIDReusePreservesOriginalReceiptAndRejectsUnknownVersion(t *testing.T) {
	s, _ := newFixture(t)
	p := proposal(1)
	accepted := apply(t, s, p)
	p.Events[0].Text = "another payload"
	publish(t, s, filepath.Join(outboxDir, "pending"), p.CandidateID, signed(t, p))
	summary, e := s.ProcessPending(20)
	must(t, e)
	if summary.Failed != 1 {
		t.Fatal("rejected proposal count missing")
	}
	b, e := os.ReadFile(filepath.Join(s.DataRoot, receiptPath(p.CandidateID)))
	must(t, e)
	var receipt Receipt
	must(t, json.Unmarshal(b, &receipt))
	if receipt.Applied != accepted.Applied {
		t.Fatal("ID-reuse rejection replaced committed receipt")
	}
	p.CandidateID = uuid.NewString()
	p.SchemaVersion = "9.0"
	publish(t, s, filepath.Join(outboxDir, "pending"), p.CandidateID, signed(t, p))
	summary, e = s.ProcessPending(20)
	must(t, e)
	if summary.Failed != 1 {
		t.Fatal("unknown protocol was accepted")
	}
	var rejection Rejection
	b, e = os.ReadFile(filepath.Join(s.DataRoot, outboxDir, "rejected", p.CandidateID+".result.json"))
	must(t, e)
	must(t, json.Unmarshal(b, &rejection))
	if rejection.Code != "unsupported_protocol" {
		t.Fatal("missing explicit unsupported result")
	}
}
func TestCredentialDirectoryCannotAliasDataOrGit(t *testing.T) {
	s, g := newFixture(t)
	inside := filepath.Join(s.DataRoot, "private")
	must(t, os.Mkdir(inside, 0700))
	alias := filepath.Join(t.TempDir(), "alias")
	must(t, os.Symlink(inside, alias))
	_, e := New(s.DataRoot, alias)
	errIs(t, e, "outside_data_root")
	repo := t.TempDir()
	must(t, os.Mkdir(filepath.Join(repo, ".git"), 0700))
	_, e = New(s.DataRoot, filepath.Join(repo, "private"))
	errIs(t, e, "credentials_inside_git")
	g.Training = true
	errIs(t, s.Configure(g, fixtureKey), "invalid_grant")
	g.Training = false
	g.ExternalTransfer = true
	errIs(t, s.Configure(g, fixtureKey), "invalid_grant")
}
func TestUnknownFieldsAndMalformedUnicodeAreRejectedBeforeMAC(t *testing.T) {
	for _, raw := range []string{`{"text":"\ud800"}`, `{"text":"\udc00"}`, `{"text":"\ud800\u0041"}`} {
		if _, e := CanonicalJSON([]byte(raw)); e == nil {
			t.Fatalf("invalid surrogate accepted: %s", raw)
		}
	}
	if _, e := CanonicalJSON([]byte(`{"text":"\ud83d\ude03\\ud800"}`)); e != nil {
		t.Fatal(e)
	}
	s, _ := newFixture(t)
	p := proposal(1)
	raw := signed(t, p)
	var m map[string]any
	must(t, json.Unmarshal(raw, &m))
	m["events"].([]any)[0].(map[string]any)["pretend_final"] = true
	raw, e := json.Marshal(m)
	must(t, e)
	_, e = s.Apply(raw)
	if e == nil {
		t.Fatal("unknown event field accepted")
	}
}
