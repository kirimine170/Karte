package ephyoutbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestExperimentConcurrentGoroutineRetries(t *testing.T) {
	root := t.TempDir()
	record, evidence := syntheticExperiment()
	start := make(chan struct{})
	results := make(chan error, 12)
	var workers sync.WaitGroup
	for i := 0; i < 12; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			publisher, err := NewExperimentPublisher(root)
			if err == nil {
				_, err = publisher.Publish(record, evidence)
			}
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Errorf("identical concurrent retry failed: %v", err)
		}
	}
	store, err := NewExperimentEvidenceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(record.CandidateID, record.Evidence); err != nil {
		t.Fatal(err)
	}
	before := experimentSnapshot(t, root)
	publisher, err := NewExperimentPublisher(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(record, evidence); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Fatal("retry changed concurrent winner's bytes")
	}
	entries, err := os.ReadDir(store.root)
	if err != nil || len(entries) != 1 || entries[0].Name() != record.CandidateID {
		t.Fatalf("staging debris remains: entries=%v err=%v", entries, err)
	}
}

func TestExperimentConcurrentProcessRetries(t *testing.T) {
	if root := os.Getenv("KARTE_SYNTHETIC_PUBLISH_ROOT"); root != "" {
		record, evidence := syntheticExperiment()
		if os.Getenv("KARTE_SYNTHETIC_PUBLISH_VARIANT") == "b" {
			evidence["halt/stderr.txt"] = []byte("other synthetic evidence\n")
			record.Evidence[0].SHA256 = SHA256Bytes(evidence["halt/stderr.txt"])
		}
		publisher, err := NewExperimentPublisher(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := publisher.Publish(record, evidence); err != nil {
			fmt.Println("RESULT rejected")
		} else {
			fmt.Println("RESULT published")
		}
		return
	}
	for _, scenario := range []string{"same", "conflicting"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			type result struct {
				variant   string
				published bool
				err       error
				output    string
			}
			results := make(chan result, 8)
			start := make(chan struct{})
			var workers sync.WaitGroup
			for i := 0; i < 8; i++ {
				variant := "a"
				if scenario == "conflicting" && i%2 != 0 {
					variant = "b"
				}
				workers.Add(1)
				go func(variant string) {
					defer workers.Done()
					<-start
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()
					cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExperimentConcurrentProcessRetries$")
					cmd.Env = append(os.Environ(), "KARTE_SYNTHETIC_PUBLISH_ROOT="+root, "KARTE_SYNTHETIC_PUBLISH_VARIANT="+variant)
					output, err := cmd.CombinedOutput()
					results <- result{variant, strings.Contains(string(output), "RESULT published"), err, string(output)}
				}(variant)
			}
			close(start)
			workers.Wait()
			close(results)
			counts := map[string]int{}
			for result := range results {
				if result.err != nil {
					t.Fatalf("synthetic child failed: %v\n%s", result.err, result.output)
				}
				if result.published {
					counts[result.variant]++
				}
			}
			if len(counts) != 1 {
				t.Fatalf("multiple different contents succeeded: %v", counts)
			}
			winner, total := "", 8
			if scenario == "conflicting" {
				total = 4
			}
			for variant, count := range counts {
				winner = variant
				if count != total {
					t.Fatalf("identical winner retries failed: %v", counts)
				}
			}
			record, evidence := syntheticExperiment()
			if winner == "b" {
				evidence["halt/stderr.txt"] = []byte("other synthetic evidence\n")
				record.Evidence[0].SHA256 = SHA256Bytes(evidence["halt/stderr.txt"])
			}
			store, err := NewExperimentEvidenceStore(root)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Verify(record.CandidateID, record.Evidence); err != nil {
				t.Fatal(err)
			}
			before := experimentSnapshot(t, root)
			publisher, err := NewExperimentPublisher(root)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := publisher.Publish(record, evidence); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
				t.Fatal("restart retry changed winning evidence bytes")
			}
			entries, err := os.ReadDir(store.root)
			if err != nil || len(entries) != 1 || entries[0].Name() != record.CandidateID {
				t.Fatalf("staging debris remains: %v err=%v", entries, err)
			}
		})
	}
}

func TestExperimentInstallationNeverClobbers(t *testing.T) {
	for _, kind := range []string{"empty-directory", "directory", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dataDir := t.TempDir()
			root, err := os.OpenRoot(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := root.Mkdir(".stage", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := root.WriteFile(".stage/new.txt", []byte("new synthetic content"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "empty-directory", "directory":
				if err := root.Mkdir("candidate", 0o700); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					if err := root.WriteFile("candidate/old.txt", []byte("old synthetic content"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
			case "file":
				if err := root.WriteFile("candidate", []byte("old synthetic file"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := root.Mkdir("target", 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dataDir, "target"), filepath.Join(dataDir, "candidate")); err != nil {
					t.Fatal(err)
				}
			}
			before := experimentSnapshot(t, dataDir)
			if err := installEvidenceDirectory(root, ".stage", "candidate"); err == nil {
				t.Fatal("existing destination was replaced")
			}
			if !reflect.DeepEqual(before, experimentSnapshot(t, dataDir)) {
				t.Fatal("failed install changed prior files")
			}
			if _, err := root.Lstat("candidate"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExperimentWindowsAliasControls(t *testing.T) {
	for _, kind := range []string{"candidate", "candidate-inverse", "manifest", "file", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dataDir := t.TempDir()
			store, err := NewExperimentEvidenceStore(dataDir)
			if err != nil {
				t.Fatal(err)
			}
			record, evidence := syntheticExperiment()
			if err := store.WriteEvidence(record.CandidateID, evidence); err != nil {
				t.Fatal(err)
			}
			candidate := store.CandidateDir(record.CandidateID)
			requestedID := record.CandidateID
			probe := filepath.Join(store.root, strings.ToUpper(record.CandidateID))
			if _, err := os.Stat(probe); os.IsNotExist(err) {
				t.Skip("case-sensitive filesystem: no actual alias to exercise")
			} else if err != nil {
				t.Fatal(err)
			}
			t.Log("case-insensitive filesystem alias confirmed")
			source, destination := "", ""
			switch kind {
			case "candidate":
				requestedID = strings.ToUpper(record.CandidateID)
			case "candidate-inverse":
				source, destination = candidate, probe
			case "manifest":
				source, destination = filepath.Join(candidate, "manifest.json"), filepath.Join(candidate, "MANIFEST.JSON")
			case "file":
				source, destination = filepath.Join(candidate, "halt", "stderr.txt"), filepath.Join(candidate, "halt", "STDERR.TXT")
			case "directory":
				source, destination = filepath.Join(candidate, "halt"), filepath.Join(candidate, "HALT")
			}
			if source != "" {
				if err := os.Rename(source, source+"-temporary"); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(source+"-temporary", destination); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "candidate-inverse" {
				path := filepath.Join(destination, "manifest.json")
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var manifest EvidenceManifest
				if err := json.Unmarshal(data, &manifest); err != nil {
					t.Fatal(err)
				}
				manifest.CandidateID = strings.ToUpper(record.CandidateID)
				data, err = json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before := experimentSnapshot(t, dataDir)
			for attempt := 0; attempt < 2; attempt++ {
				if err := store.Verify(requestedID, record.Evidence); err == nil {
					t.Error("verification trusted a case alias")
				}
				if err := store.WriteEvidence(requestedID, evidence); err == nil {
					t.Error("write trusted a case alias")
				}
				if !reflect.DeepEqual(before, experimentSnapshot(t, dataDir)) {
					t.Error("case alias operation changed bytes")
				}
				store, err = NewExperimentEvidenceStore(dataDir)
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestExperimentInvalidPayloadPreflight(t *testing.T) {
	for _, kind := range []string{"trailing-dot", "device", "empty-segment", "dot-segment", "reserved-manifest", "prefix-collision"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			store, err := NewExperimentEvidenceStore(root)
			if err != nil {
				t.Fatal(err)
			}
			ref := map[string]string{"trailing-dot": "halt/log.", "device": "halt/con.txt", "empty-segment": "halt//log", "dot-segment": "halt/./log", "reserved-manifest": "manifest.json/log", "prefix-collision": "halt"}[kind]
			entries := map[string][]byte{ref: []byte("synthetic")}
			if kind == "prefix-collision" {
				entries["halt/log"] = []byte("synthetic")
			}
			before := experimentSnapshot(t, root)
			if err := store.WriteEvidence("candidate-preflight", entries); err == nil {
				t.Error("invalid payload was accepted")
			}
			if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
				t.Error("preflight error mutated managed store")
			}
		})
	}
}

func TestExperimentMaximumEvidenceCreatesValidProposal(t *testing.T) {
	record, _ := syntheticExperiment()
	record.Evidence = make([]ExperimentEvidence, 64)
	for i := range record.Evidence {
		record.Evidence[i] = ExperimentEvidence{LogicalRef: fmt.Sprintf("halt/log-%02d", i), SHA256: SHA256Bytes([]byte("synthetic"))}
	}
	proposal, err := BuildExperimentProposal(record, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.SourceRefs) != 64 {
		t.Fatalf("source_refs exceeds V1.1: %d", len(proposal.SourceRefs))
	}
	if err := proposal.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestExperimentAbandonedStagingSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	store, err := NewExperimentEvidenceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	abandoned := filepath.Join(store.root, ".evidence-abandoned")
	if err := os.MkdirAll(abandoned, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(abandoned, "partial.txt")
	content := []byte("synthetic uncommitted evidence")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	record, evidence := syntheticExperiment()
	publisher, err := NewExperimentPublisher(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Publish(record, evidence); err != nil {
		t.Fatal(err)
	}
	store, err = NewExperimentEvidenceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Verify(record.CandidateID, record.Evidence); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(content) {
		t.Fatalf("restart altered unrelated abandoned staging: err=%v", err)
	}
	if err := store.Verify(".evidence-abandoned", record.Evidence); err == nil {
		t.Fatal("uncommitted staging was treated as a candidate")
	}
}

func TestExperimentProposalValidationPrecedesEvidenceWrite(t *testing.T) {
	root := t.TempDir()
	publisher, err := NewExperimentPublisher(root)
	if err != nil {
		t.Fatal(err)
	}
	record, evidence := syntheticExperiment()
	record.Environment = strings.Repeat("synthetic", 150000)
	before := experimentSnapshot(t, root)
	if _, err := publisher.Publish(record, evidence); err == nil {
		t.Fatal("oversized generated V1.1 proposal was accepted")
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Fatal("proposal validation failure wrote evidence")
	}
	if _, err := os.Stat(publisher.store.root); !os.IsNotExist(err) {
		t.Fatalf("proposal validation created managed paths: err=%v", err)
	}
}

func TestExperimentRecordFixtureValidation(t *testing.T) {
	for _, name := range []string{"valid", "invalid-evidence", "invalid-halt", "invalid-ref", "invalid-sha", "invalid-state", "invalid-version"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("..", "..", "schemas", "experiment", "v0.1", "fixtures", "experiment-record-"+name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			record, err := DecodeExperimentRecord(data)
			if err == nil {
				err = record.Validate()
			}
			if (err == nil) != (name == "valid") {
				t.Fatalf("fixture validation result is wrong: err=%v", err)
			}
		})
	}
}

func TestExperimentMultipleEvidenceRetryKeepsManifestOrder(t *testing.T) {
	root := t.TempDir()
	store, err := NewExperimentEvidenceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{"z/last": []byte("synthetic z"), "a/first": []byte("synthetic a"), "middle": []byte("synthetic middle")}
	if err := store.WriteEvidence("candidate-order", entries); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(store.CandidateDir("candidate-order"), "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest EvidenceManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Entries) != 3 || manifest.Entries[0].LogicalRef != "a/first" || manifest.Entries[1].LogicalRef != "middle" || manifest.Entries[2].LogicalRef != "z/last" {
		t.Fatalf("manifest is not sorted: %#v", manifest.Entries)
	}
	before := experimentSnapshot(t, root)
	store, err = NewExperimentEvidenceStore(root)
	if err != nil {
		t.Fatal(err)
	}
	reordered := map[string][]byte{"middle": entries["middle"], "a/first": entries["a/first"], "z/last": entries["z/last"]}
	if err := store.WriteEvidence("candidate-order", reordered); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, experimentSnapshot(t, root)) {
		t.Fatal("same reference set reordered or refreshed manifest")
	}
}
