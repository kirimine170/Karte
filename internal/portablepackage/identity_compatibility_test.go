package portablepackage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"karte/internal/contextcore"
)

func TestLegacyDocIDCompatibility(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "hybrid-sharing", "id-compatibility.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name     string `json:"name"`
			DocID    string `json:"doc_id"`
			Portable bool   `json:"portable"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil || len(fixture.Cases) == 0 {
		t.Fatalf("read identity fixture: %v", err)
	}
	for _, tc := range fixture.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			request := contextcore.Request{
				ProtocolVersion: contextcore.ProtocolVersion, RequestID: "identity-check",
				Operation: "read", DocID: &tc.DocID, CreatedAt: "2026-01-01T00:00:00Z",
				Actor: contextcore.Actor{Type: "human", ID: "synthetic-human"},
				Scope: contextcore.Scope{SensitivityCeiling: "internal"},
			}
			if err := request.Validate(); err != nil {
				t.Fatalf("Context request rejected the legacy ID: %v", err)
			}
			source := []byte(fmt.Sprintf("---\ndoc_id: %q\ntitle: Identity\n---\n# Identity\n", tc.DocID))
			snapshot, err := PersonalProjection(source, "identity", contextcore.Resource{
				Project: "legacy", Kind: "note", Sensitivity: "internal", SHA256: hash(source),
			})
			if !tc.Portable {
				if !errors.Is(err, ErrNotStandalone) {
					t.Fatalf("portable v0.1 should reject this legacy ID without rewriting it: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "test"+Extension)
			if err := Write(path, snapshot); err != nil {
				t.Fatal(err)
			}
			received, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			manifest := received.Manifest
			if manifest.RootResourceID != tc.DocID || manifest.RootRevisionID != hash(received.Markdown) ||
				manifest.RootRevisionID != manifest.Blob.SHA256 || manifest.RootRevisionID == hash(source) {
				t.Fatalf("wrong legacy identity or projection digest: %+v", manifest)
			}
		})
	}
}
