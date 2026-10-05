package main

import (
	"bytes"
	"encoding/json"
	"karte/internal/ephyoutbox"
	fm "karte/internal/frontmatter"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Acceptance is confined to this synthetic harness and uses Karte's existing
// human-review SaveFile route. The producer exposes no acceptance operation.
func TestExperimentProducerSyntheticSaveFileRoundTrip(t *testing.T) {
	app, data := newEphyTestApp(t)
	bundle := filepath.Join("testdata", "worker-experiment-v1")
	p, err := ephyoutbox.NewExperimentProducer(data)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	prepared, err := p.Prepare(bundle, filepath.Join(bundle, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(prepared.CandidateID); err != nil {
		t.Fatal(err)
	}
	inbox, err := app.ListEphyProposals()
	if err != nil || len(inbox.Proposals) != 1 {
		t.Fatalf("synthetic inbox: %+v %v", inbox, err)
	}
	review := inbox.Proposals[0]
	if review.Proposal.Placement.Kind != "report" || review.Proposal.Sensitivity != "internal" {
		t.Fatal("unsafe placement")
	}
	canonicalPath := filepath.Join(data, filepath.FromSlash(review.ResolvedRelativePath))
	if _, err := os.Stat(canonicalPath); !os.IsNotExist(err) {
		t.Fatal("producer wrote canonical content")
	}
	saves := 0
	app.ephySaveFile = func(name, content string) error { saves++; return app.SaveFile(name, content) }
	receipt, err := app.AcceptEphyProposal(prepared.CandidateID, review.Proposal.ProposedFrontmatter, review.Proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Result != "accepted" || saves != 1 {
		t.Fatalf("synthetic acceptance failed: %+v saves=%d", receipt, saves)
	}
	canonical, err := os.ReadFile(canonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	frontmatter, body := fm.ParseFrontMatter(string(canonical))
	if frontmatter == nil || frontmatter.Raw["kind"] != "report" || frontmatter.Raw["sensitivity"] != "internal" || frontmatter.Raw["project"] != review.Proposal.Placement.Project {
		t.Fatalf("saved report frontmatter changed authority: %+v", frontmatter)
	}
	if body != review.Proposal.ProposedBody || !strings.Contains(body, "- State: experiment\n") || !strings.Contains(body, "- Verification: unverified\n") || !strings.Contains(body, "not adopted") {
		t.Fatal("saved report body changed the bound experiment or promoted authority")
	}
	if receipt.DocID == nil || *receipt.DocID != frontmatter.DocID || receipt.ResultingSHA256 == nil || *receipt.ResultingSHA256 != ephyoutbox.SHA256Bytes(canonical) {
		t.Fatal("receipt does not match the saved synthetic report")
	}
	status, err := p.Status(prepared.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "report_accepted" || status.State != "experiment" || status.Verification != "unverified" || status.Adopted || status.PayloadSHA256 != prepared.PayloadSHA256 {
		t.Fatalf("receipt promoted or changed payload: %+v", status)
	}
	if _, err := p.Publish(prepared.CandidateID); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(canonicalPath)
	if err != nil || !bytes.Equal(canonical, after) || saves != 1 {
		t.Fatal("producer retry changed canonical content")
	}
	// Same evidence, changed metadata, after genuine synthetic acceptance.
	raw, err := os.ReadFile(filepath.Join(bundle, "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	input["title"] = "Changed after acceptance"
	raw, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	metadata := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(metadata, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Prepare(bundle, metadata); err == nil {
		t.Fatal("accepted ID allowed a changed payload")
	}
}
