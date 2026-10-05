package ephyoutbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func producerFixture(t *testing.T) (string, string) {
	t.Helper()
	source := filepath.Join("..", "..", "testdata", "worker-experiment-v1")
	bundle := t.TempDir()
	if err := filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		target := filepath.Join(bundle, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0700)
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0600)
	}); err != nil {
		t.Fatal(err)
	}
	return bundle, filepath.Join(bundle, "metadata.json")
}

func producerSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	result := map[string]string{}
	if err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(dir, name)
		result[relative] = SHA256Bytes(raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func producerWriteJSON(t *testing.T, name string, value any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, producerJSON(value), 0600); err != nil {
		t.Fatal(err)
	}
}

func producerReadJSON(t *testing.T, name string, value any) {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, value); err != nil {
		t.Fatal(err)
	}
}

func newPreparedProducer(t *testing.T) (*ExperimentProducer, string, string, string, ExperimentProducerStatus) {
	t.Helper()
	bundle, metadata := producerFixture(t)
	data := t.TempDir()
	p, err := NewExperimentProducer(data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.Close() })
	status, err := p.Prepare(bundle, metadata)
	if err != nil {
		t.Fatal(err)
	}
	return p, data, bundle, metadata, status
}

// This harness writes only synthetic receipt/archive files. Real SaveFile
// acceptance is covered separately by the application integration test.
func producerAcceptHarness(t *testing.T, data, id string) {
	t.Helper()
	store, err := NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := store.ReadPending(id)
	if err != nil {
		t.Fatal(err)
	}
	docID, _ := DeriveCreateDocID(id)
	decision, err := ResolvePlacement(data, proposal, docID)
	if err != nil {
		t.Fatal(err)
	}
	digest := SHA256Bytes([]byte("synthetic canonical receipt payload"))
	receipt := Receipt{SchemaVersion: SchemaVersion, CandidateID: id, Result: "accepted", DocID: &docID, RelativePath: &decision.RelativePath, ResultingSHA256: &digest, ProcessedAt: "2026-10-05T00:01:00Z"}
	if err := store.WriteReceipt(receipt); err != nil {
		t.Fatal(err)
	}
	if err := store.MoveProposal(id, "accepted"); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentProducerSyntheticRoundTripAndRestart(t *testing.T) {
	p, data, bundle, metadata, prepared := newPreparedProducer(t)
	before := producerSnapshot(t, bundle)
	if prepared.Phase != "prepared" || prepared.Verification != "unverified" || prepared.State != "experiment" || prepared.Adopted {
		t.Fatalf("unsafe prepare status: %+v", prepared)
	}
	if _, err := os.Stat(filepath.Join(data, "content")); !os.IsNotExist(err) {
		t.Fatal("prepare wrote canonical content")
	}
	pending, err := p.Publish(prepared.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Phase != "pending" || pending.PayloadSHA256 != prepared.PayloadSHA256 {
		t.Fatal("unstable publication")
	}
	producerAcceptHarness(t, data, prepared.CandidateID)
	p.Close()
	reopened, err := NewExperimentProducer(data)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, operation := range []string{"status", "publish", "prepare"} {
		var status ExperimentProducerStatus
		switch operation {
		case "status":
			status, err = reopened.Status(prepared.CandidateID)
		case "publish":
			status, err = reopened.Publish(prepared.CandidateID)
		case "prepare":
			status, err = reopened.Prepare(bundle, metadata)
		}
		if err != nil {
			t.Fatal(err)
		}
		if status.Phase != "report_accepted" || status.Adopted || status.Verification != "unverified" || status.PayloadSHA256 != prepared.PayloadSHA256 {
			t.Fatalf("%s promoted or changed payload: %+v", operation, status)
		}
	}
	if _, err := os.Stat(filepath.Join(data, filepath.FromSlash(producerOutboxDir), "pending", prepared.CandidateID+".json")); !os.IsNotExist(err) {
		t.Fatal("receipt retry recreated pending proposal")
	}
	if !bytes.Equal(producerJSON(before), producerJSON(producerSnapshot(t, bundle))) {
		t.Fatal("producer changed original Worker evidence")
	}
}

func TestExperimentProducerRejectsMetadataReuseBeforeAndAfterReceipt(t *testing.T) {
	for _, phase := range []string{"prepared", "pending", "accepted"} {
		t.Run(phase, func(t *testing.T) {
			p, data, bundle, metadata, status := newPreparedProducer(t)
			if phase != "prepared" {
				if _, err := p.Publish(status.CandidateID); err != nil {
					t.Fatal(err)
				}
			}
			if phase == "accepted" {
				producerAcceptHarness(t, data, status.CandidateID)
			}
			before := producerSnapshot(t, data)
			var input WorkerExperimentMetadata
			producerReadJSON(t, metadata, &input)
			input.Title = "Different metadata with identical evidence"
			producerWriteJSON(t, metadata, input)
			if _, err := p.Prepare(bundle, metadata); err == nil {
				t.Fatal("same ID accepted changed metadata")
			}
			if !bytes.Equal(producerJSON(before), producerJSON(producerSnapshot(t, data))) {
				t.Fatal("conflicting prepare changed existing payload")
			}
		})
	}
}

func TestExperimentProducerInvalidInputsHaveNoSideEffects(t *testing.T) {
	cases := map[string]func(*WorkerExperimentMetadata, *workerManifest){
		"wrong-adapter-version": func(m *WorkerExperimentMetadata, _ *workerManifest) { m.AdapterVersion = "unknown" },
		"live-input":            func(m *WorkerExperimentMetadata, _ *workerManifest) { v := false; m.SyntheticOnly = &v },
		"missing-synthetic":     func(m *WorkerExperimentMetadata, _ *workerManifest) { m.SyntheticOnly = nil },
		"wrong-worker-version":  func(_ *WorkerExperimentMetadata, m *workerManifest) { m.SchemaVersion = "unknown" },
		"different-audit":       func(_ *WorkerExperimentMetadata, m *workerManifest) { m.AuditID = "other" },
		"different-job":         func(_ *WorkerExperimentMetadata, m *workerManifest) { m.JobID = "other" },
		"missing-artifact":      func(_ *WorkerExperimentMetadata, m *workerManifest) { m.Artifacts = m.Artifacts[:21] },
		"wrong-artifact-order": func(_ *WorkerExperimentMetadata, m *workerManifest) {
			m.Artifacts[0], m.Artifacts[1] = m.Artifacts[1], m.Artifacts[0]
		},
		"duplicate-path": func(_ *WorkerExperimentMetadata, m *workerManifest) { m.Artifacts[1].Path = m.Artifacts[0].Path },
		"case-path-alias": func(_ *WorkerExperimentMetadata, m *workerManifest) {
			m.Artifacts[1].Path = strings.ToUpper(m.Artifacts[0].Path)
		},
		"path-traversal": func(_ *WorkerExperimentMetadata, m *workerManifest) { m.Artifacts[0].Path = "../outside" },
		"windows-device": func(_ *WorkerExperimentMetadata, m *workerManifest) { m.Artifacts[0].Path = "artifacts/con.txt" },
		"missing-size":   func(_ *WorkerExperimentMetadata, m *workerManifest) { m.Artifacts[0].SizeBytes = nil },
		"negative-size":  func(_ *WorkerExperimentMetadata, m *workerManifest) { v := int64(-1); m.Artifacts[0].SizeBytes = &v },
		"wrong-size":     func(_ *WorkerExperimentMetadata, m *workerManifest) { v := int64(0); m.Artifacts[0].SizeBytes = &v },
		"oversized-artifact": func(_ *WorkerExperimentMetadata, m *workerManifest) {
			v := int64(producerMaxArtifact + 1)
			m.Artifacts[0].SizeBytes = &v
		},
		"uppercase-sha": func(_ *WorkerExperimentMetadata, m *workerManifest) {
			m.Artifacts[0].SHA256 = strings.ToUpper(m.Artifacts[0].SHA256)
		},
		"wrong-sha":             func(_ *WorkerExperimentMetadata, m *workerManifest) { m.Artifacts[0].SHA256 = strings.Repeat("0", 64) },
		"missing-reported-at":   func(m *WorkerExperimentMetadata, _ *workerManifest) { m.ReportedAt = "" },
		"invalid-id":            func(m *WorkerExperimentMetadata, _ *workerManifest) { m.CandidateID = "../candidate" },
		"unknown-worker-result": func(m *WorkerExperimentMetadata, _ *workerManifest) { m.WorkerResult = "verified" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			bundle, metadata := producerFixture(t)
			var input WorkerExperimentMetadata
			var manifest workerManifest
			producerReadJSON(t, metadata, &input)
			manifestPath := filepath.Join(bundle, "evidence-manifest.json")
			producerReadJSON(t, manifestPath, &manifest)
			mutate(&input, &manifest)
			producerWriteJSON(t, metadata, input)
			producerWriteJSON(t, manifestPath, manifest)
			data := t.TempDir()
			p, err := NewExperimentProducer(data)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if _, err := p.Prepare(bundle, metadata); err == nil {
				t.Fatal("invalid input accepted")
			}
			entries, err := os.ReadDir(data)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid input changed data directory")
			}
		})
	}
}

func TestExperimentProducerNeverPromotesWorkerResult(t *testing.T) {
	for _, result := range []string{"external_review_pending", "strict_pass", "halted"} {
		t.Run(result, func(t *testing.T) {
			bundle, metadata := producerFixture(t)
			var input WorkerExperimentMetadata
			producerReadJSON(t, metadata, &input)
			input.WorkerResult = result
			producerWriteJSON(t, metadata, input)
			binding, _, err := loadWorkerBundle(bundle, metadata)
			if err != nil {
				t.Fatal(err)
			}
			if binding.Record.Verification != "unverified" || binding.Record.State != "experiment" || binding.Proposal.Sensitivity != "internal" || binding.Proposal.Placement.Kind != "report" || binding.Proposal.Operation != "create" {
				t.Fatal("adapter widened authority")
			}
		})
	}
}

func TestExperimentProducerRejectsReceiptAndArchiveSubstitution(t *testing.T) {
	for _, mutate := range []string{"candidate", "doc-id", "placement", "filename", "archive", "missing-archive", "receipt-unknown", "receipt-duplicate"} {
		t.Run(mutate, func(t *testing.T) {
			p, data, _, _, status := newPreparedProducer(t)
			if _, err := p.Publish(status.CandidateID); err != nil {
				t.Fatal(err)
			}
			producerAcceptHarness(t, data, status.CandidateID)
			receiptPath := filepath.Join(data, filepath.FromSlash(producerOutboxDir), "receipts", status.CandidateID+".json")
			archivePath := filepath.Join(data, filepath.FromSlash(producerOutboxDir), "accepted", status.CandidateID+".json")
			var receipt Receipt
			producerReadJSON(t, receiptPath, &receipt)
			switch mutate {
			case "candidate":
				receipt.CandidateID = "other-candidate"
				producerWriteJSON(t, receiptPath, receipt)
			case "doc-id":
				v := strings.Repeat("0", 64)
				receipt.DocID = &v
				producerWriteJSON(t, receiptPath, receipt)
			case "placement":
				v := "content/projects/other/report/2026-10/other.md"
				receipt.RelativePath = &v
				producerWriteJSON(t, receiptPath, receipt)
			case "filename":
				v := "content/projects/ephy/report/2026-10/other-candidate.md"
				receipt.RelativePath = &v
				producerWriteJSON(t, receiptPath, receipt)
			case "archive":
				var proposal Proposal
				producerReadJSON(t, archivePath, &proposal)
				proposal.ProposedBody += "\nchanged"
				producerWriteJSON(t, archivePath, proposal)
			case "missing-archive":
				if err := os.Remove(archivePath); err != nil {
					t.Fatal(err)
				}
			case "receipt-unknown":
				raw := producerJSON(receipt)
				raw = bytes.Replace(raw, []byte("{"), []byte(`{"extra":true,`), 1)
				if err := os.WriteFile(receiptPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "receipt-duplicate":
				raw := producerJSON(receipt)
				raw = bytes.Replace(raw, []byte("{"), []byte(`{"candidate_id":"other",`), 1)
				if err := os.WriteFile(receiptPath, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := producerSnapshot(t, data)
			if _, err := p.Status(status.CandidateID); err == nil {
				t.Fatal("substituted receipt accepted")
			}
			if _, err := p.Publish(status.CandidateID); err == nil {
				t.Fatal("substituted receipt allowed retry")
			}
			if !bytes.Equal(producerJSON(before), producerJSON(producerSnapshot(t, data))) {
				t.Fatal("status/retry changed substituted artifacts")
			}
		})
	}
}

func TestExperimentProducerConcurrentPublishAndNoClobber(t *testing.T) {
	p, data, _, _, status := newPreparedProducer(t)
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := p.Publish(status.CandidateID); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(data, filepath.FromSlash(producerOutboxDir), "pending", status.CandidateID+".json")
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var proposal Proposal
	producerReadJSON(t, file, &proposal)
	proposal.ProposedBody = "Different existing payload"
	producerWriteJSON(t, file, proposal)
	changed, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Publish(status.CandidateID); err == nil {
		t.Fatal("conflicting pending overwritten")
	}
	after, err := os.ReadFile(file)
	if err != nil || !bytes.Equal(changed, after) || bytes.Equal(before, after) {
		t.Fatal("existing pending was replaced")
	}
	entries, err := os.ReadDir(filepath.Dir(file))
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary publication files leaked")
	}
}

func TestExperimentProducerAtomicLinkRefusesRacingDestination(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	a, b := []byte("payload-A"), []byte("payload-B")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, raw := range [][]byte{a, b} {
		wg.Add(1)
		go func(raw []byte) { defer wg.Done(); errs <- installProducerJSON(root, "candidate.json", raw) }(raw)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d concurrent payloads installed", success)
	}
	raw, err := root.ReadFile("candidate.json")
	if err != nil || (!bytes.Equal(raw, a) && !bytes.Equal(raw, b)) {
		t.Fatal("partial or invalid destination")
	}
}

func TestExperimentProducerTamperedPreparationOrEvidence(t *testing.T) {
	for _, kind := range []string{"record", "proposal", "evidence", "manifest"} {
		t.Run(kind, func(t *testing.T) {
			p, data, _, _, status := newPreparedProducer(t)
			name := filepath.Join(data, filepath.FromSlash(producerBindingDir), status.CandidateID+".json")
			switch kind {
			case "record", "proposal":
				var binding producerBinding
				producerReadJSON(t, name, &binding)
				if kind == "record" {
					binding.Record.Title = "Changed"
				} else {
					binding.Proposal.ProposedBody = "Changed"
				}
				producerWriteJSON(t, name, binding)
			case "evidence":
				name = filepath.Join(data, ".mdsys", "ephy", "experiments", status.CandidateID, "worker", "artifacts", "candidate_patch")
				if err := os.WriteFile(name, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				name = filepath.Join(data, ".mdsys", "ephy", "experiments", status.CandidateID, "manifest.json")
				if err := os.WriteFile(name, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := p.Publish(status.CandidateID); err == nil {
				t.Fatal("tampered prepared payload published")
			}
			if _, err := os.Stat(filepath.Join(data, filepath.FromSlash(producerOutboxDir))); !os.IsNotExist(err) {
				t.Fatal("invalid preparation created outbox")
			}
		})
	}
}

func TestExperimentProducerRejectsAmbiguousJSONAndChangedSource(t *testing.T) {
	for _, kind := range []string{"duplicate-key", "unknown-field", "trailing-json", "invalid-utf8", "source-changed", "source-missing"} {
		t.Run(kind, func(t *testing.T) {
			bundle, metadata := producerFixture(t)
			raw, err := os.ReadFile(metadata)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "duplicate-key":
				raw = bytes.Replace(raw, []byte("{"), []byte(`{"candidate_id":"other",`), 1)
			case "unknown-field":
				raw = bytes.Replace(raw, []byte("{"), []byte(`{"verification":"verified",`), 1)
			case "trailing-json":
				raw = append(raw, []byte("{}")...)
			case "invalid-utf8":
				raw = append(raw, 0xff)
			case "source-changed":
				if err := os.WriteFile(filepath.Join(bundle, "artifacts", "candidate_patch.txt"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "source-missing":
				if err := os.Remove(filepath.Join(bundle, "artifacts", "candidate_patch.txt")); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(metadata, raw, 0600); err != nil {
				t.Fatal(err)
			}
			data := t.TempDir()
			p, err := NewExperimentProducer(data)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if _, err := p.Prepare(bundle, metadata); err == nil {
				t.Fatal("ambiguous or changed source accepted")
			}
			entries, err := os.ReadDir(data)
			if err != nil || len(entries) != 0 {
				t.Fatal("invalid source created managed files")
			}
		})
	}
}

func TestExperimentProducerPreservesBindingDuringConcurrentPrepare(t *testing.T) {
	bundle, metadata := producerFixture(t)
	data := t.TempDir()
	p, err := NewExperimentProducer(data)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var input WorkerExperimentMetadata
	producerReadJSON(t, metadata, &input)
	input.Title = "Other racing metadata"
	other := filepath.Join(t.TempDir(), "metadata.json")
	producerWriteJSON(t, other, input)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, name := range []string{metadata, other} {
		wg.Add(1)
		go func(name string) { defer wg.Done(); _, err := p.Prepare(bundle, name); errs <- err }(name)
	}
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d different racing payloads bound", success)
	}
	if _, err := p.Publish(input.CandidateID); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentProducerPreservesReadOnlyWorkerRoot(t *testing.T) {
	bundle, metadata := producerFixture(t)
	for _, data := range []string{bundle, filepath.Join(bundle, "derived-data")} {
		t.Run("inside-source", func(t *testing.T) {
			if err := os.MkdirAll(data, 0700); err != nil {
				t.Fatal(err)
			}
			before := producerSnapshot(t, bundle)
			p, err := NewExperimentProducer(data)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if _, err := p.Prepare(bundle, metadata); err == nil {
				t.Fatal("derived writes allowed inside Worker bundle")
			}
			if !bytes.Equal(producerJSON(before), producerJSON(producerSnapshot(t, bundle))) {
				t.Fatal("source changed during refused prepare")
			}
		})
	}
}

func TestExperimentProducerRejectsLinksAndAliases(t *testing.T) {
	for _, kind := range []string{"source-file", "source-directory", "pending-file", "pending-directory", "binding-alias"} {
		t.Run(kind, func(t *testing.T) {
			p, data, bundle, metadata, status := newPreparedProducer(t)
			switch kind {
			case "source-file":
				name := filepath.Join(bundle, "artifacts", "candidate_patch.txt")
				raw, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "patch.txt")
				if err := os.WriteFile(outside, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, name); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				if _, err := p.Prepare(bundle, metadata); err == nil {
					t.Fatal("source link accepted")
				}
			case "source-directory":
				name := filepath.Join(bundle, "artifacts")
				moved := filepath.Join(bundle, "artifacts-original")
				if err := os.Rename(name, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, name); err != nil {
					t.Skipf("symlink unavailable: %v", err)
				}
				if _, err := p.Prepare(bundle, metadata); err == nil {
					t.Fatal("source directory link accepted")
				}
			case "pending-file", "pending-directory":
				parent := filepath.Join(data, filepath.FromSlash(producerOutboxDir), "pending")
				if err := os.MkdirAll(parent, 0700); err != nil {
					t.Fatal(err)
				}
				name := filepath.Join(parent, status.CandidateID+".json")
				if kind == "pending-file" {
					target := filepath.Join(t.TempDir(), "proposal.json")
					producerWriteJSON(t, target, map[string]any{})
					if err := os.Symlink(target, name); err != nil {
						t.Skipf("symlink unavailable: %v", err)
					}
				} else {
					if err := os.Mkdir(name, 0700); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := p.Publish(status.CandidateID); err == nil {
					t.Fatal("pending link/directory replaced")
				}
			case "binding-alias":
				name := filepath.Join(data, filepath.FromSlash(producerBindingDir), status.CandidateID+".json")
				alias := filepath.Join(filepath.Dir(name), strings.ToUpper(status.CandidateID)+".json")
				if err := os.Rename(name, alias); err != nil {
					t.Fatal(err)
				}
				if _, err := p.Status(status.CandidateID); err == nil {
					t.Fatal("binding case alias accepted")
				}
			}
		})
	}
}

func TestExperimentProducerPublishDoesNotRecreatePendingAfterAcceptance(t *testing.T) {
	p, data, _, _, prepared := newPreparedProducer(t)
	if _, err := p.Publish(prepared.CandidateID); err != nil {
		t.Fatal(err)
	}
	status, err := p.publish(prepared.CandidateID, func() { producerAcceptHarness(t, data, prepared.CandidateID) })
	if err != nil {
		t.Fatalf("acceptance during publication retry: %v", err)
	}
	if status.Phase != "report_accepted" || status.Adopted || status.Verification != "unverified" {
		t.Fatalf("incorrect post-acceptance status: %+v", status)
	}
	pending := filepath.Join(data, filepath.FromSlash(producerOutboxDir), "pending", prepared.CandidateID+".json")
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("publication retry recreated accepted pending proposal")
	}
	store, err := NewStore(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MoveProposal(prepared.CandidateID, "accepted"); err != nil {
		t.Fatalf("normal acceptance retry is no longer recoverable: %v", err)
	}
}

func TestExperimentProducerPublicationAcrossProcesses(t *testing.T) {
	const rootEnv = "KARTE_SYNTHETIC_PRODUCER_TEST_ROOT"
	if data := os.Getenv(rootEnv); data != "" {
		p, err := NewExperimentProducer(data)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		fmt.Fprintln(os.Stdout, "PRODUCER_CHILD_READY")
		status, err := p.Publish("synthetic-candidate-001")
		if err != nil {
			t.Fatal(err)
		}
		if status.Phase != "report_accepted" || status.Adopted || status.Verification != "unverified" {
			t.Fatalf("queued a second proposal instead of observing acceptance: %+v", status)
		}
		return
	}
	p, data, _, _, prepared := newPreparedProducer(t)
	root, err := p.operationRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	release, err := acquireProducerPublication(root, prepared.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	defer func() {
		if locked {
			release()
		}
	}()
	cmd := exec.Command(os.Args[0], "-test.run=^TestExperimentProducerPublicationAcrossProcesses$", "-test.v")
	cmd.Env = append(os.Environ(), rootEnv+"="+data)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "PRODUCER_CHILD_READY" {
				ready <- struct{}{}
			}
		}
		done <- cmd.Wait()
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // Only this test's owned child; safe after Wait.
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("child before readiness: %v %s", err, stderr.String())
	case <-time.After(15 * time.Second):
		t.Fatal("child did not reach publication")
	}
	select {
	case err := <-done:
		t.Fatalf("another process bypassed the publication lock: %v %s", err, stderr.String())
	case <-time.After(150 * time.Millisecond):
	}
	// Simulate the first producer while it owns publication, then normal human
	// acceptance before the waiting process gets its turn to inspect the outbox.
	binding, err := p.readBinding(root, prepared.CandidateID)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := producerDirectory(root, producerOutboxDir+"/pending", true)
	if err != nil {
		t.Fatal(err)
	}
	err = installProducerJSON(dir, prepared.CandidateID+".json", producerJSON(binding.Proposal))
	dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	producerAcceptHarness(t, data, prepared.CandidateID)
	release()
	locked = false
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("waiting producer failed: %v %s", err, stderr.String())
		}
	case <-time.After(15 * time.Second):
		t.Fatal("waiting producer did not finish after release")
	}
	if _, err := os.Stat(filepath.Join(data, filepath.FromSlash(producerOutboxDir), "pending", prepared.CandidateID+".json")); !os.IsNotExist(err) {
		t.Fatal("waiting producer recreated accepted pending")
	}
}

func TestExperimentProducerRejectsSourceRootAliases(t *testing.T) {
	for _, kind := range []string{"same-root", "descendant"} {
		t.Run(kind, func(t *testing.T) {
			bundle, metadata := producerFixture(t)
			alias := filepath.Join(t.TempDir(), "source-parent-alias")
			if err := os.Symlink(filepath.Dir(bundle), alias); err != nil {
				t.Skipf("directory symlink unavailable: %v", err)
			}
			data := filepath.Join(alias, filepath.Base(bundle))
			if kind == "descendant" {
				data = filepath.Join(data, "derived-root")
				if err := os.Mkdir(data, 0700); err != nil {
					t.Fatal(err)
				}
			}
			before := producerSnapshot(t, bundle)
			p, err := NewExperimentProducer(data)
			if err != nil {
				t.Fatal(err)
			}
			defer p.Close()
			if _, err := p.Prepare(bundle, metadata); err == nil {
				t.Fatal("source volume/path alias allowed derived writes inside original evidence")
			}
			if !bytes.Equal(producerJSON(before), producerJSON(producerSnapshot(t, bundle))) {
				t.Fatal("alias refusal changed original evidence")
			}
		})
	}
}
