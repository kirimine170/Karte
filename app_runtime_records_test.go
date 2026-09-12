package main

import (
	"bytes"
	"encoding/json"
	"github.com/go-git/go-git/v5/plumbing/object"
	"karte/internal/contextcore"
	"karte/internal/ephyrecordsv2"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newRuntimeRecordTestApp(t *testing.T) (*App, *ephyrecordsv2.Service, ephyrecordsv2.Proposal, []byte) {
	t.Helper()
	app, root := newEphyTestApp(t)
	app.ephyV2ConfigRoot = filepath.Join(t.TempDir(), "private-config")
	s, e := ephyrecordsv2.New(root, app.ephyV2ConfigRoot)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("schemas/karte-ephy/v2/fixtures/grant.json")
	if e != nil {
		t.Fatal(e)
	}
	g, e := ephyrecordsv2.DecodeGrant(raw)
	if e != nil {
		t.Fatal(e)
	}
	g.ValidFrom = "2000-01-01T00:00:00Z"
	if e = s.Configure(g, bytes.Repeat([]byte{0x42}, 32)); e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile("schemas/karte-ephy/v2/fixtures/conversation-user-final.proposal.json")
	if e != nil {
		t.Fatal(e)
	}
	var p ephyrecordsv2.Proposal
	if e = json.Unmarshal(raw, &p); e != nil {
		t.Fatal(e)
	}
	return app, s, p, raw
}
func TestRuntimeRecordBackgroundProcessingAndHumanSave(t *testing.T) {
	app, s, p, raw := newRuntimeRecordTestApp(t)
	pending := filepath.Join(app.dataDir, ".mdsys/ephy/outbox/v2/pending", p.CandidateID+".json")
	if e := os.MkdirAll(filepath.Dir(pending), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(pending, raw, 0600); e != nil {
		t.Fatal(e)
	}
	result, e := app.ProcessContextRequests()
	if e != nil {
		t.Fatal(e)
	}
	if result.Processed != 1 {
		t.Fatalf("background processor did not adopt record: %+v", result)
	}
	receipt, e := s.Apply(raw)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.ToSlash(filepath.Join("content/projects/synthetic-diary/note/2026-09", "ephy-v2-"+receipt.Applied.DocID+".md"))
	content, e := app.LoadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	edited := strings.Replace(content, "次回は冬の観測を相談したい．", "人が編集した合成本文．", 1)
	if e = app.SaveFile(path, edited); e != nil {
		t.Fatal(e)
	}
	after, e := app.LoadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	r, e := ephyrecordsv2.Parse([]byte(after))
	if e != nil {
		t.Fatal(e)
	}
	if r.Meta.Revision != 2 || !r.Meta.HumanEdited || r.Events[0].Text != "人が編集した合成本文．" {
		t.Fatal("UI save did not record a human revision")
	}
	if e = app.SaveFile(path, edited); e == nil {
		t.Fatal("stale editor overwrote the current revision")
	}
}
func TestSiteBuildExcludesRuntimeRecords(t *testing.T) {
	app, s, _, raw := newRuntimeRecordTestApp(t)
	receipt, e := s.Apply(raw)
	if e != nil {
		t.Fatal(e)
	}
	ordinary := "content/ordinary.md"
	if e = app.SaveFile(ordinary, "---\ndoc_id: ordinary\ntitle: Ordinary\n---\nordinary public document\n"); e != nil {
		t.Fatal(e)
	}
	if e = app.BuildSite(); e != nil {
		t.Fatal(e)
	}
	index, e := os.ReadFile(filepath.Join(app.root, ".mdsys/index.json"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(index, []byte(receipt.Applied.DocID)) || bytes.Contains(index, []byte("ephy-v2")) {
		t.Fatal("v2 record was included in the site index")
	}
	html, e := os.ReadFile(filepath.Join(app.root, "public/ordinary.html"))
	if e != nil || !bytes.Contains(html, []byte("ordinary public document")) {
		t.Fatalf("ordinary site content regressed: %v", e)
	}
	e = filepath.WalkDir(filepath.Join(app.root, "public"), func(path string, entry os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.IsDir() {
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Contains(data, []byte("次回は冬の観測を相談したい")) {
			t.Fatal("storage-only text entered site output")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}

func TestRuntimeRecordUIListAndBoardReadHonorCurrentHumanPolicy(t *testing.T) {
	app, s, _, raw := newRuntimeRecordTestApp(t)
	receipt, e := s.Apply(raw)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.ToSlash(filepath.Join("content/projects/synthetic-diary/note/2026-09", "ephy-v2-"+receipt.Applied.DocID+".md"))
	if len(app.GetFileList()) != 1 {
		t.Fatal("authorized record missing from UI")
	}
	policy := contextcore.DefaultPolicy()
	human := policy.Actors["human"]
	human.SensitivityCeiling = "public"
	policy.Actors["human"] = human
	if e = s.SetPrivacyPolicy(policy); e != nil {
		t.Fatal(e)
	}
	if len(app.GetFileList()) != 0 {
		t.Fatal("UI list exposed a denied record")
	}
	if _, e = app.LoadFile(path); e == nil {
		t.Fatal("ordinary file read bypassed human policy")
	}
	if _, e = app.LoadBoard(path); e == nil {
		t.Fatal("board route bypassed human policy")
	}
}
func TestInitialGitCommitExcludesRuntimeStorageOnlyData(t *testing.T) {
	app, s, _, raw := newRuntimeRecordTestApp(t)
	receipt, e := s.Apply(raw)
	if e != nil {
		t.Fatal(e)
	}
	if e = app.initializeGitRepository(); e != nil {
		t.Fatal(e)
	}
	head, e := app.vcs.Repository().Head()
	if e != nil {
		t.Fatal(e)
	}
	commit, e := app.vcs.Repository().CommitObject(head.Hash())
	if e != nil {
		t.Fatal(e)
	}
	tree, e := commit.Tree()
	if e != nil {
		t.Fatal(e)
	}
	e = tree.Files().ForEach(func(f *object.File) error {
		if strings.Contains(f.Name, receipt.Applied.DocID) || strings.HasPrefix(f.Name, ".mdsys/") {
			t.Fatalf("initial commit captured storage-only data: %s", f.Name)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
