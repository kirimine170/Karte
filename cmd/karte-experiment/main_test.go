package main

import (
	"bytes"
	"encoding/json"
	"karte/internal/ephyoutbox"
	"path/filepath"
	"testing"
)

func TestCLIOnlyPreparesPublishesAndReadsSyntheticStatus(t *testing.T) {
	data := t.TempDir()
	bundle := filepath.Join("..", "..", "testdata", "worker-experiment-v1")
	var output bytes.Buffer
	if err := run([]string{"prepare", "-data-root", data, "-worker-bundle", bundle, "-metadata", filepath.Join(bundle, "metadata.json")}, &output); err != nil {
		t.Fatal(err)
	}
	var prepared ephyoutbox.ExperimentProducerStatus
	if err := json.Unmarshal(output.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Phase != "prepared" || prepared.Adopted {
		t.Fatal("unsafe prepared result")
	}
	for _, operation := range []string{"publish", "status"} {
		output.Reset()
		if err := run([]string{operation, "-data-root", data, "-candidate-id", prepared.CandidateID}, &output); err != nil {
			t.Fatal(err)
		}
		var status ephyoutbox.ExperimentProducerStatus
		if err := json.Unmarshal(output.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		if status.Phase != "pending" || status.PayloadSHA256 != prepared.PayloadSHA256 || status.Verification != "unverified" || status.Adopted {
			t.Fatal("unsafe CLI result")
		}
	}
}

func TestCLIRejectsAuthorityAndAmbiguousArguments(t *testing.T) {
	for _, args := range [][]string{
		{}, {"accept"}, {"grant"}, {"configure"}, {"adopt"}, {"canonical-write"},
		{"prepare"}, {"publish", "-data-root", t.TempDir()}, {"status", "-data-root", t.TempDir(), "-candidate-id", "id", "extra"},
		{"publish", "-data-root", t.TempDir(), "-candidate-id", "id", "-metadata", "metadata.json"},
		{"prepare", "-data-root", t.TempDir(), "-worker-bundle", "bundle", "-metadata", "metadata.json", "-candidate-id", "override"},
	} {
		t.Run("reject", func(t *testing.T) {
			var output bytes.Buffer
			if err := run(args, &output); err == nil || output.Len() != 0 {
				t.Fatalf("accepted arguments: %v", args)
			}
		})
	}
}
