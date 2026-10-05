package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"karte/internal/ephyoutbox"
	fm "karte/internal/frontmatter"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestExperimentProducerPublishCompetesWithRealSyntheticAcceptance(t *testing.T) {
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
	store, err := ephyoutbox.NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.ReadPending(prepared.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ephyoutbox.NewExperimentProducer(data)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	const publishers = 16
	start := make(chan struct{})
	entered := make(chan struct{}, publishers)
	results := make(chan error, publishers)
	var wg sync.WaitGroup
	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			entered <- struct{}{}
			_, err := other.Publish(prepared.CandidateID)
			results <- err
		}()
	}
	saves := 0
	app.ephySaveFile = func(name, content string) error {
		saves++
		if err := app.SaveFile(name, content); err != nil {
			return err
		}
		// Release producer retries while actual acceptance is between SaveFile
		// and its durable receipt/archive, using the unchanged App transaction.
		close(start)
		for i := 0; i < publishers; i++ {
			<-entered
		}
		return nil
	}
	receipt, err := app.AcceptEphyProposal(prepared.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody)
	if err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(results)
	transient := 0
	for err := range results {
		if err == nil {
			continue
		}
		if !errors.Is(err, os.ErrNotExist) && !strings.Contains(err.Error(), "acceptance may still be finishing") && !strings.Contains(err.Error(), "identity does not match actual filename") {
			t.Fatalf("unexpected concurrent publication error: %v", err)
		}
		transient++
	}
	t.Logf("%d publication retries observed the unfinished acceptance and failed closed", transient)
	status, err := p.Status(prepared.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	if status.Phase != "report_accepted" || status.State != "experiment" || status.Verification != "unverified" || status.Adopted || saves != 1 {
		t.Fatalf("real acceptance race changed authority or saved twice: %+v saves=%d", status, saves)
	}
	if _, err := os.Lstat(filepath.Join(data, ".mdsys", "ephy", "outbox", "pending", prepared.CandidateID+".json")); !os.IsNotExist(err) {
		t.Fatalf("accepted pending directory entry was recreated: %v", err)
	}
	if _, err := store.ReadPending(prepared.CandidateID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("accepted pending proposal was recreated: %v", err)
	}
	before, err := os.ReadFile(filepath.Join(data, filepath.FromSlash(*receipt.RelativePath)))
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ResultingSHA256 == nil || *receipt.ResultingSHA256 != ephyoutbox.SHA256Bytes(before) {
		t.Fatal("receipt mismatched actual SaveFile content")
	}
	if _, err := app.AcceptEphyProposal(prepared.CandidateID, proposal.ProposedFrontmatter, proposal.ProposedBody); err != nil {
		t.Fatalf("normal acceptance retry was blocked: %v", err)
	}
	after, err := os.ReadFile(filepath.Join(data, filepath.FromSlash(*receipt.RelativePath)))
	if err != nil || !bytes.Equal(before, after) || saves != 1 {
		t.Fatal("normal acceptance retry rewrote the synthetic report")
	}
}
