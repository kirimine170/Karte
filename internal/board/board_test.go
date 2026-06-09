package board

import (
	"strings"
	"testing"
)

func TestParseBoardDocument(t *testing.T) {
	content := `---
type: karte-board
doc_id: board:test
title: Test Board
version: 1
created: 2026-06-06
updated: 2026-06-06
tags:
  - design
  - board
---

# Board

## Cards

### card:one

` + "```yaml" + `
type: resource
title: One
source: content/a.md
tags:
  - feature
created_by: user
` + "```" + `

Body one.

---

### card:two

` + "```yaml" + `
type: claim
title: Two
reviewed: true
` + "```" + `

Body two.

## Edges

` + "```yaml" + `
- id: edge:1
  from: card:one
  to: card:two
  relation: supports
  label: supports
  description: edge description
` + "```" + `

## Layout

` + "```yaml" + `
cards:
  card:one:
    x: 10
    y: 20
    width: 300
    height: 180
viewport:
  x: 0
  y: 0
  zoom: 1
` + "```" + `
`

	doc, err := Parse("content/test.board.md", content)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if doc.Type != BoardType {
		t.Fatalf("unexpected type: %s", doc.Type)
	}
	if len(doc.Cards) != 2 {
		t.Fatalf("expected 2 cards, got %d", len(doc.Cards))
	}
	if doc.Cards[0].Title != "One" || doc.Cards[0].Source != "content/a.md" {
		t.Fatalf("unexpected first card: %#v", doc.Cards[0])
	}
	if len(doc.Edges) != 1 || doc.Edges[0].Relation != "supports" {
		t.Fatalf("unexpected edges: %#v", doc.Edges)
	}
	if doc.Edges[0].Description != "edge description" {
		t.Fatalf("unexpected edge description: %#v", doc.Edges[0])
	}
	if doc.Layout.Cards["card:one"].Width != 300 {
		t.Fatalf("unexpected layout: %#v", doc.Layout.Cards["card:one"])
	}
}

func TestSerializeBoardDocumentStableSections(t *testing.T) {
	doc := &Document{
		Path:    "content/test.board.md",
		Title:   "Serialized Board",
		DocID:   "board:serialized",
		Type:    BoardType,
		Version: 1,
		Created: "2026-06-06",
		Updated: "2026-06-06",
		Tags:    []string{"karte", "board"},
		Cards: []Card{
			{
				ID:        "card:one",
				Type:      "resource",
				Title:     "One",
				Source:    "content/a.md",
				CreatedBy: "user",
				Body:      "Body one.",
			},
		},
		Edges: []Edge{
			{ID: "edge:1", From: "card:one", To: "card:one", Relation: "references", Description: "serialized edge"},
		},
		Layout: Layout{
			Cards: map[string]CardLayout{
				"card:one": {X: 10, Y: 20, Width: 300, Height: 180},
			},
			Viewport: Viewport{X: 0, Y: 0, Zoom: 1},
		},
	}

	serialized, err := Serialize(doc)
	if err != nil {
		t.Fatalf("Serialize failed: %v", err)
	}
	requiredSnippets := []string{
		"type: karte-board",
		"## Cards",
		"### card:one",
		"## Edges",
		"relation: references",
		"## Layout",
		"description: serialized edge",
	}
	for _, snippet := range requiredSnippets {
		if !strings.Contains(serialized, snippet) {
			t.Fatalf("serialized board missing %q:\n%s", snippet, serialized)
		}
	}
}

func TestParseRejectsMissingSections(t *testing.T) {
	content := `---
type: karte-board
title: Broken
---

# Board

## Cards
`
	_, err := Parse("content/broken.board.md", content)
	if err == nil {
		t.Fatalf("expected Parse to fail")
	}
	if err != ErrMissingEdges {
		t.Fatalf("expected ErrMissingEdges, got %v", err)
	}
}
