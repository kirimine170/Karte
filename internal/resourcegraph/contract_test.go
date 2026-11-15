package resourcegraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestContractFixtures(t *testing.T) {
	folder := filepath.Join("..", "..", "schemas", "hybrid-sharing", "v0.1", "resource-graph-fixtures")
	wantErrors := map[string]string{
		"invalid-domain-duplicate-legacy-id":   "duplicate_legacy_id",
		"invalid-domain-revision-cycle":        "revision_cycle",
		"invalid-domain-cross-resource-parent": "invalid_parent_resource",
		"invalid-domain-missing-target":        "unknown_target_revision",
		"invalid-domain-anchor-bounds":         "invalid_anchor",
		"invalid-domain-anchor-kind":           "invalid_anchor",
		"invalid-domain-duplicate-operation":   "duplicate_operation",
		"invalid-domain-snapshot-relation":     "unknown_snapshot_relation_endpoint",
		"invalid-domain-snapshot-orphan":       "unreachable_snapshot_item",
	}
	data, err := os.ReadFile(filepath.Join(folder, "valid-graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	var base Graph
	decode(t, data, &base)
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}

	paths, err := filepath.Glob(filepath.Join(folder, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	validated := 0
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if strings.HasPrefix(name, "invalid-schema-") {
			continue
		}
		validated++
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var header struct {
				Type string `json:"type"`
			}
			decode(t, raw, &header)
			var record any
			switch header.Type {
			case "resource_graph":
				var graph Graph
				decode(t, raw, &graph)
				record, err = graph, graph.Validate()
			case "share_snapshot":
				var snapshot ShareSnapshot
				decode(t, raw, &snapshot)
				record, err = snapshot, snapshot.Validate()
			case "share_request":
				var request ShareRequest
				decode(t, raw, &request)
				record, err = request, request.Validate(base)
			default:
				t.Fatalf("unhandled fixture type %q", header.Type)
			}
			want, invalid := wantErrors[name]
			if invalid {
				if err == nil || err.Error() != want {
					t.Fatalf("want %s, got %v", want, err)
				}
				return
			}
			if !strings.HasPrefix(name, "valid-") || err != nil {
				t.Fatalf("unexpected fixture outcome: %v", err)
			}
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			var got, expected any
			decode(t, encoded, &got)
			decode(t, raw, &expected)
			if !reflect.DeepEqual(got, expected) {
				t.Fatal("typed round trip changed the JSON contract")
			}
		})
	}
	if validated != len(wantErrors)+3 {
		t.Fatalf("checked %d fixtures, want %d", validated, len(wantErrors)+3)
	}
}

func decode(t *testing.T, raw []byte, value any) {
	t.Helper()
	if err := json.Unmarshal(raw, value); err != nil {
		t.Fatal(err)
	}
}

func TestShareRequestCannotCrossWorkspaces(t *testing.T) {
	graph := Graph{WorkspaceID: "workspace-a", Revisions: []Revision{{ID: "known-revision"}}}
	request := ShareRequest{Type: "share_request", Version: Version, WorkspaceID: "workspace-b",
		RootRevisionID: "known-revision", GrantRef: &GrantRef{Authority: "ephy_v2", PolicyID: "66666666-6666-4666-8666-666666666666", ScopeID: "22222222-2222-4222-8222-222222222222"}}
	if err := request.Validate(graph); err == nil || err.Error() != "invalid_request_scope" {
		t.Fatalf("expected invalid request scope with a grant reference, got %v", err)
	}
	request.WorkspaceID = graph.WorkspaceID
	request.RootRevisionID = "wrong-revision"
	if err := request.Validate(graph); err == nil || err.Error() != "unknown_request_revision" {
		t.Fatalf("expected unknown request revision, got %v", err)
	}
}

func TestShareRequestUsesExistingEphyScopeIDs(t *testing.T) {
	root := filepath.Join("..", "..", "schemas")
	grantJSON, err := os.ReadFile(filepath.Join(root, "karte-ephy", "v2", "fixtures", "grant.json"))
	if err != nil {
		t.Fatal(err)
	}
	requestJSON, err := os.ReadFile(filepath.Join(root, "hybrid-sharing", "v0.1", "resource-graph-fixtures", "valid-request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var grant struct {
		PolicyID         string `json:"policy_id"`
		ScopeID          string `json:"scope_id"`
		ExternalTransfer bool   `json:"external_transfer"`
	}
	var request ShareRequest
	decode(t, grantJSON, &grant)
	decode(t, requestJSON, &request)
	if grant.ExternalTransfer || request.GrantRef == nil || request.GrantRef.PolicyID != grant.PolicyID || request.GrantRef.ScopeID != grant.ScopeID {
		t.Fatal("fixture must reference the existing local-only Ephy grant by policy and scope")
	}
}
