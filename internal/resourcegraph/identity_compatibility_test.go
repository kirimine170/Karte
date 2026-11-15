package resourcegraph

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestLegacyAliasesRemainDistinct(t *testing.T) {
	var fixture struct {
		Cases []struct {
			DocID      string `json:"doc_id"`
			GraphAlias bool   `json:"graph_alias"`
		} `json:"cases"`
	}
	readResolverFixture(t, filepath.Join("testdata", "hybrid-sharing", "id-compatibility.json"), &fixture)
	graph := Graph{Type: "resource_graph", Version: Version, WorkspaceID: "fixture-workspace"}
	for i, tc := range fixture.Cases {
		if tc.GraphAlias {
			graph.Resources = append(graph.Resources, Resource{ID: fmt.Sprintf("local-%d", i), Kind: "markdown", LegacyDocID: tc.DocID})
		}
	}
	if len(graph.Resources) != len(fixture.Cases) || len(graph.Resources) == 0 {
		t.Fatal("fixture must include distinct G0 aliases")
	}
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Graph
	decode(t, raw, &decoded)
	for i, resource := range decoded.Resources {
		if resource.LegacyDocID != fixture.Cases[i].DocID || resource.LegacyDocID == resource.ID {
			t.Fatalf("legacy ID was changed or used as canonical Resource ID: %+v", resource)
		}
	}
}
