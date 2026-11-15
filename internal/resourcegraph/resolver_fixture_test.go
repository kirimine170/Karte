package resourcegraph

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type resolverScenarios struct {
	GraphFixture string `json:"graph_fixture"`
	PathAliases  []struct {
		Kind       string `json:"kind"`
		Source     string `json:"source"`
		ResourceID string `json:"resource_id"`
		RevisionID string `json:"revision_id"`
	} `json:"path_aliases"`
	BoardMove struct {
		ResourceID       string `json:"resource_id"`
		HistoricalSource string `json:"historical_source"`
		CurrentSource    string `json:"current_source"`
		RevisionID       string `json:"revision_id"`
		MissingSource    string `json:"missing_source"`
		MissingOutcome   string `json:"missing_outcome"`
	} `json:"board_move"`
	ContextRead struct {
		DocID      string `json:"doc_id"`
		ResourceID string `json:"resource_id"`
	} `json:"context_read"`
	PDFAnchor struct {
		RelationID string `json:"relation_id"`
		RevisionID string `json:"revision_id"`
		BlobSHA256 string `json:"blob_sha256"`
		Page       int64  `json:"page"`
		Outcome    string `json:"outcome"`
	} `json:"pdf_anchor"`
	WebPDerivative struct {
		RelationID         string `json:"relation_id"`
		SourceResourceID   string `json:"source_resource_id"`
		DerivedResourceID  string `json:"derived_resource_id"`
		DerivedRevisionID  string `json:"derived_revision_id"`
		PreviousRevisionID string `json:"previous_revision_id"`
		ChunksAvailable    bool   `json:"chunks_available"`
		Outcome            string `json:"outcome"`
	} `json:"webp_derivative"`
	DependencyCycle struct {
		RootRevisionID     string   `json:"root_revision_id"`
		VisitedRevisionIDs []string `json:"visited_revision_ids"`
		BackEdgeID         string   `json:"back_edge_id"`
		Outcome            string   `json:"outcome"`
	} `json:"dependency_cycle"`
	FollowingHead struct {
		ResourceID      string   `json:"resource_id"`
		HeadRevisionIDs []string `json:"head_revision_ids"`
		Outcome         string   `json:"outcome"`
	} `json:"following_head"`
	FixedInConflict struct {
		ResourceID string `json:"resource_id"`
		RevisionID string `json:"revision_id"`
		Outcome    string `json:"outcome"`
	} `json:"fixed_in_conflict"`
}

func TestResolverDesignFixtures(t *testing.T) {
	var cases resolverScenarios
	readResolverFixture(t, filepath.Join("testdata", "hybrid-sharing", "resolver-scenarios.json"), &cases)
	var graph Graph
	readResolverFixture(t, filepath.Join("schemas", "hybrid-sharing", "v0.1", "resource-graph-fixtures", cases.GraphFixture), &graph)
	if err := graph.Validate(); err != nil {
		t.Fatal(err)
	}
	revisions := map[string]Revision{}
	resources := map[string]Resource{}
	relations := map[string]Relation{}
	for _, rev := range graph.Revisions {
		revisions[rev.ID] = rev
	}
	for _, res := range graph.Resources {
		resources[res.ID] = res
	}
	for _, rel := range graph.Relations {
		relations[rel.ID] = rel
	}

	t.Run("move and missing source", func(t *testing.T) {
		move := cases.BoardMove
		if move.CurrentSource == move.HistoricalSource || move.MissingSource == "" ||
			revisions[move.RevisionID].ResourceID != move.ResourceID || move.MissingOutcome != "missing" {
			t.Fatalf("invalid move fixture: %+v", move)
		}
		resolved := map[string]string{}
		for _, alias := range cases.PathAliases {
			if alias.ResourceID != move.ResourceID || alias.RevisionID != move.RevisionID || alias.Source == move.MissingSource {
				t.Fatalf("move aliases must resolve to the original identity: %+v", alias)
			}
			resolved[alias.Source] = alias.Kind
		}
		if resolved[move.HistoricalSource] != "history" || resolved[move.CurrentSource] != "current" {
			t.Fatal("missing current or historical path alias")
		}
	})
	t.Run("existing context, PDF and WebP bindings", func(t *testing.T) {
		ctx := cases.ContextRead
		if ctx.DocID == "" || resources[ctx.ResourceID].LegacyDocID != ctx.DocID {
			t.Fatal("Context doc_id must match an existing fixture alias exactly")
		}
		pdf := cases.PDFAnchor
		target := relations[pdf.RelationID].Target
		if pdf.Outcome != "resolved" || target.RevisionID != pdf.RevisionID ||
			revisions[pdf.RevisionID].Blob.SHA256 != pdf.BlobSHA256 ||
			target.Anchor == nil || target.Anchor.Kind != "pdf_page" || target.Anchor.Page != pdf.Page {
			t.Fatal("PDF digest and page must point to the same fixed revision")
		}
		webp := cases.WebPDerivative
		link := relations[webp.RelationID]
		derived := revisions[webp.DerivedRevisionID]
		original := revisions[link.Target.RevisionID]
		previous := revisions[webp.PreviousRevisionID]
		if webp.ChunksAvailable || webp.Outcome != "resolved_by_catalog" || link.Kind != "derived_from" ||
			link.SourceRevisionID != derived.ID || original.ResourceID != webp.SourceResourceID ||
			derived.ResourceID != webp.DerivedResourceID || previous.ResourceID != derived.ResourceID ||
			original.ResourceID == derived.ResourceID || original.Blob.MediaType != "image/webp" ||
			derived.Blob.MediaType != "image/webp" || derived.MetadataSHA256 == "" {
			t.Fatal("WebP fixture must retain original, derivative and metadata by catalog")
		}
	})
	t.Run("dependency cycle and multiple heads", func(t *testing.T) {
		cycle := cases.DependencyCycle
		root := revisions[cycle.RootRevisionID]
		back := relations[cycle.BackEdgeID]
		if root.ID == "" || cycle.Outcome != "complete" || back.Kind != "embeds" ||
			back.Target.RevisionID != root.ID || len(cycle.VisitedRevisionIDs) != 2 ||
			cycle.VisitedRevisionIDs[0] != root.ID || cycle.VisitedRevisionIDs[1] != back.SourceRevisionID ||
			!slices.ContainsFunc(graph.Relations, func(relation Relation) bool {
				return relation.Kind == "embeds" && relation.SourceRevisionID == root.ID &&
					relation.Target.RevisionID == back.SourceRevisionID
			}) {
			t.Fatal("fixture must close an embeds cycle over the two expected unique revisions")
		}

		live := cases.FollowingHead
		parents := map[string]bool{}
		for _, rev := range graph.Revisions {
			if rev.ResourceID == live.ResourceID {
				for _, id := range rev.Parents {
					parents[id] = true
				}
			}
		}
		var heads []string
		for _, rev := range graph.Revisions {
			if rev.ResourceID == live.ResourceID && !parents[rev.ID] {
				heads = append(heads, rev.ID)
			}
		}
		slices.Sort(heads)
		fixed := cases.FixedInConflict
		if live.Outcome != "conflict" || len(heads) < 2 || !slices.Equal(heads, live.HeadRevisionIDs) ||
			fixed.Outcome != "resolved" || fixed.ResourceID != live.ResourceID ||
			revisions[fixed.RevisionID].ResourceID != live.ResourceID || slices.Contains(heads, fixed.RevisionID) {
			t.Fatal("fixture must keep both live heads and a fixed non-head revision")
		}
	})
}

func readResolverFixture(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", path))
	if err != nil {
		t.Fatal(err)
	}
	decode(t, raw, value)
}
