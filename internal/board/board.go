package board

import (
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	BoardType = "karte-board"
)

var (
	ErrMissingFrontMatter = errors.New("board front matter is required")
	ErrInvalidBoardType   = errors.New("board type must be karte-board")
	ErrMissingCards       = errors.New("board must contain a Cards section")
	ErrMissingEdges       = errors.New("board must contain an Edges section")
	ErrMissingLayout      = errors.New("board must contain a Layout section")
)

type Document struct {
	Path       string   `json:"path"`
	Title      string   `json:"title"`
	DocID      string   `json:"docId"`
	Type       string   `json:"type"`
	Version    int      `json:"version"`
	Created    string   `json:"created"`
	Updated    string   `json:"updated"`
	Tags       []string `json:"tags"`
	Cards      []Card   `json:"cards"`
	Edges      []Edge   `json:"edges"`
	Layout     Layout   `json:"layout"`
	Notes      string   `json:"notes"`
	RawContent string   `json:"rawContent"`
}

type Card struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Title      string         `json:"title"`
	Source     string         `json:"source,omitempty"`
	Tags       []string       `json:"tags,omitempty"`
	CreatedBy  string         `json:"createdBy,omitempty"`
	UpdatedBy  string         `json:"updatedBy,omitempty"`
	Reviewed   bool           `json:"reviewed,omitempty"`
	ReviewedBy string         `json:"reviewedBy,omitempty"`
	Model      string         `json:"model,omitempty"`
	Body       string         `json:"body"`
	Meta       map[string]any `json:"meta,omitempty"`
}

type Edge struct {
	ID          string `json:"id"`
	From        string `json:"from"`
	To          string `json:"to"`
	Relation    string `json:"relation"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
}

type Layout struct {
	Cards    map[string]CardLayout `json:"cards"`
	Viewport Viewport              `json:"viewport"`
}

type CardLayout struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type Viewport struct {
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	Zoom float64 `json:"zoom"`
}

// ViewState is the persisted Board view state stored as Layout.Viewport.
// selectedCardId and selectedEdgeId are ephemeral frontend state and are not
// part of the Board resource schema.
type ViewState = Viewport

type boardFrontMatter struct {
	Type    string   `yaml:"type"`
	DocID   string   `yaml:"doc_id,omitempty"`
	Title   string   `yaml:"title,omitempty"`
	Version int      `yaml:"version,omitempty"`
	Created string   `yaml:"created,omitempty"`
	Updated string   `yaml:"updated,omitempty"`
	Tags    []string `yaml:"tags,omitempty"`
}

type edgeYAML struct {
	ID          string `yaml:"id"`
	From        string `yaml:"from"`
	To          string `yaml:"to"`
	Relation    string `yaml:"relation"`
	Label       string `yaml:"label,omitempty"`
	Description string `yaml:"description,omitempty"`
}

type cardYAML struct {
	Type       string         `yaml:"type"`
	Title      string         `yaml:"title"`
	Source     string         `yaml:"source,omitempty"`
	Tags       []string       `yaml:"tags,omitempty"`
	CreatedBy  string         `yaml:"created_by,omitempty"`
	UpdatedBy  string         `yaml:"updated_by,omitempty"`
	Reviewed   bool           `yaml:"reviewed,omitempty"`
	ReviewedBy string         `yaml:"reviewed_by,omitempty"`
	Model      string         `yaml:"model,omitempty"`
	Meta       map[string]any `yaml:"meta,omitempty"`
}

type layoutYAML struct {
	Cards    map[string]CardLayout `yaml:"cards,omitempty"`
	Viewport Viewport              `yaml:"viewport,omitempty"`
}

// Parse decodes, migrates, and validates a Board resource. Whole-input byte
// limits belong at the file/IPC boundary before Parse; semantic collection and
// string limits are enforced here.
func Parse(path, content string) (*Document, error) {
	parsedFM, body, err := parseBoardFrontMatter(content)
	if err != nil {
		return nil, err
	}
	boardType := parsedFM.Type
	if boardType == "" {
		boardType = BoardType
	}
	if boardType != BoardType {
		return nil, ErrInvalidBoardType
	}

	sections, err := parseSections(body)
	if err != nil {
		return nil, err
	}
	cardsSection, ok := sections["cards"]
	if !ok {
		return nil, ErrMissingCards
	}
	edgesSection, ok := sections["edges"]
	if !ok {
		return nil, ErrMissingEdges
	}
	layoutSection, ok := sections["layout"]
	if !ok {
		return nil, ErrMissingLayout
	}

	doc := &Document{
		Path:       path,
		Title:      parsedFM.Title,
		DocID:      parsedFM.DocID,
		Type:       BoardType,
		Version:    parsedFM.Version,
		Created:    parsedFM.Created,
		Updated:    parsedFM.Updated,
		Tags:       parsedFM.Tags,
		Notes:      strings.TrimSpace(sections["notes"]),
		RawContent: content,
	}

	cards, err := parseCards(cardsSection)
	if err != nil {
		return nil, err
	}
	doc.Cards = cards

	edges, err := parseEdges(edgesSection)
	if err != nil {
		return nil, err
	}
	doc.Edges = edges

	layout, err := parseLayout(layoutSection)
	if err != nil {
		return nil, err
	}
	doc.Layout = layout
	if err := Migrate(doc); err != nil {
		return nil, err
	}
	if violations := ValidateDocument(doc); len(violations) > 0 {
		return nil, &ValidationError{Violations: violations}
	}

	return doc, nil
}

func Serialize(doc *Document) (string, error) {
	if doc == nil {
		return "", fmt.Errorf("board document is nil")
	}
	normalized := *doc
	if err := Migrate(&normalized); err != nil {
		return "", err
	}
	if violations := ValidateDocument(&normalized); len(violations) > 0 {
		return "", &ValidationError{Violations: violations}
	}
	frontMatter := boardFrontMatter{
		Type:    normalized.Type,
		DocID:   normalized.DocID,
		Title:   normalized.Title,
		Version: normalized.Version,
		Created: normalized.Created,
		Updated: normalized.Updated,
		Tags:    normalized.Tags,
	}

	fmBytes, err := yaml.Marshal(frontMatter)
	if err != nil {
		return "", fmt.Errorf("marshal board front matter: %w", err)
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.Write(fmBytes)
	b.WriteString("---\n\n")
	b.WriteString("# Board\n\n")
	b.WriteString("## Cards\n\n")
	for i, card := range normalized.Cards {
		b.WriteString(fmt.Sprintf("### %s\n\n", card.ID))
		b.WriteString("```yaml\n")
		cardBytes, err := serializeCardYAML(card)
		if err != nil {
			return "", err
		}
		b.Write(cardBytes)
		b.WriteString("```\n\n")
		if strings.TrimSpace(card.Body) != "" {
			b.WriteString(strings.TrimSpace(card.Body))
			b.WriteString("\n\n")
		}
		if i < len(normalized.Cards)-1 {
			b.WriteString("---\n\n")
		}
	}
	b.WriteString("## Edges\n\n")
	b.WriteString("```yaml\n")
	edgesBytes, err := yaml.Marshal(toEdgeYAML(normalized.Edges))
	if err != nil {
		return "", fmt.Errorf("marshal edges: %w", err)
	}
	b.Write(edgesBytes)
	b.WriteString("```\n\n")
	b.WriteString("## Layout\n\n")
	b.WriteString("```yaml\n")
	layoutBytes, err := yaml.Marshal(layoutYAML{
		Cards:    normalized.Layout.Cards,
		Viewport: normalized.Layout.Viewport,
	})
	if err != nil {
		return "", fmt.Errorf("marshal layout: %w", err)
	}
	b.Write(layoutBytes)
	b.WriteString("```\n")
	if strings.TrimSpace(normalized.Notes) != "" {
		b.WriteString("\n## Notes\n\n")
		b.WriteString(strings.TrimSpace(normalized.Notes))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func parseCards(section string) ([]Card, error) {
	trimmed := strings.TrimSpace(section)
	if trimmed == "" {
		return []Card{}, nil
	}
	parts := splitCardBlocks(trimmed)
	cards := make([]Card, 0, len(parts))
	for _, part := range parts {
		card, err := parseCard(part)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	return cards, nil
}

func splitCardBlocks(section string) []string {
	lines := strings.Split(section, "\n")
	var blocks []string
	var current []string
	for _, line := range lines {
		if strings.HasPrefix(line, "### ") && len(current) > 0 {
			blocks = append(blocks, strings.Join(current, "\n"))
			current = []string{line}
			continue
		}
		if strings.TrimSpace(line) == "---" {
			continue
		}
		current = append(current, line)
	}
	if len(current) > 0 {
		blocks = append(blocks, strings.Join(current, "\n"))
	}
	return blocks
}

func parseCard(block string) (Card, error) {
	lines := strings.Split(block, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "### ") {
		return Card{}, fmt.Errorf("invalid card block header")
	}
	card := Card{
		ID:   strings.TrimSpace(strings.TrimPrefix(lines[0], "### ")),
		Meta: map[string]any{},
	}
	rest := strings.Join(lines[1:], "\n")
	yamlBlock, body, err := extractFirstYAMLBlock(rest)
	if err != nil {
		return Card{}, err
	}
	var typed cardYAML
	if err := yaml.Unmarshal([]byte(yamlBlock), &typed); err != nil {
		return Card{}, fmt.Errorf("unmarshal card yaml %s: %w", card.ID, err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(yamlBlock), &raw); err != nil {
		return Card{}, fmt.Errorf("unmarshal card yaml %s: %w", card.ID, err)
	}
	card.Type = typed.Type
	card.Title = typed.Title
	card.Source = typed.Source
	card.CreatedBy = typed.CreatedBy
	card.UpdatedBy = typed.UpdatedBy
	card.ReviewedBy = typed.ReviewedBy
	card.Model = typed.Model
	card.Reviewed = typed.Reviewed
	card.Tags = typed.Tags

	for _, key := range []string{"type", "title", "source", "created_by", "updated_by", "reviewed_by", "model", "reviewed", "tags", "meta"} {
		delete(raw, key)
	}
	card.Meta = make(map[string]any, len(typed.Meta)+len(raw))
	for key, value := range typed.Meta {
		card.Meta[key] = normalizeMetaValue(value)
	}
	for key, value := range raw {
		if _, exists := card.Meta[key]; exists {
			return Card{}, fmt.Errorf("card %s meta key %q is present in both legacy flat and nested meta", card.ID, key)
		}
		card.Meta[key] = normalizeMetaValue(value)
	}
	card.Body = strings.TrimSpace(body)
	return card, nil
}

func parseEdges(section string) ([]Edge, error) {
	yamlBlock, _, err := extractFirstYAMLBlock(section)
	if err != nil {
		return nil, err
	}
	var raw []edgeYAML
	if err := decodeStrictYAML(yamlBlock, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal edges: %w", err)
	}
	edges := make([]Edge, 0, len(raw))
	for _, item := range raw {
		edges = append(edges, Edge{
			ID:          item.ID,
			From:        item.From,
			To:          item.To,
			Relation:    item.Relation,
			Label:       item.Label,
			Description: item.Description,
		})
	}
	return edges, nil
}

func parseLayout(section string) (Layout, error) {
	yamlBlock, _, err := extractFirstYAMLBlock(section)
	if err != nil {
		return Layout{}, err
	}
	var raw layoutYAML
	if err := decodeStrictYAML(yamlBlock, &raw); err != nil {
		return Layout{}, fmt.Errorf("unmarshal layout: %w", err)
	}
	if raw.Cards == nil {
		raw.Cards = map[string]CardLayout{}
	}
	return Layout{
		Cards:    raw.Cards,
		Viewport: raw.Viewport,
	}, nil
}

func parseSections(body string) (map[string]string, error) {
	lines := strings.Split(body, "\n")
	sections := map[string]string{}
	current := ""
	var buf strings.Builder
	flush := func() error {
		if current == "" {
			return nil
		}
		if _, exists := sections[current]; exists {
			return &ValidationError{Violations: []Violation{{
				Code: "board.section.duplicate", Path: "/sections/" + escapeJSONPointer(current), Message: "Board section is duplicated",
			}}}
		}
		sections[current] = strings.TrimSpace(buf.String())
		buf.Reset()
		return nil
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") && current != "notes" {
			if err := flush(); err != nil {
				return nil, err
			}
			current = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			switch current {
			case "cards", "edges", "layout", "notes":
			default:
				return nil, &ValidationError{Violations: []Violation{{
					Code: "board.section.unknown", Path: "/sections/" + escapeJSONPointer(current), Message: "Board section is not part of the v1 contract",
				}}}
			}
			continue
		}
		if current == "" {
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return sections, nil
}

func extractFirstYAMLBlock(section string) (string, string, error) {
	normalized := strings.ReplaceAll(section, "\r\n", "\n")
	start := strings.Index(normalized, "```yaml")
	if start < 0 {
		return "", "", fmt.Errorf("yaml code block not found")
	}
	afterStart := normalized[start+len("```yaml"):]
	if strings.HasPrefix(afterStart, "\n") {
		afterStart = afterStart[1:]
	}
	end := strings.Index(afterStart, "\n```")
	if end < 0 {
		return "", "", fmt.Errorf("yaml code block is not closed")
	}
	yamlBlock := strings.TrimSpace(afterStart[:end])
	body := strings.TrimSpace(afterStart[end+len("\n```"):])
	return yamlBlock, body, nil
}

func serializeCardYAML(card Card) ([]byte, error) {
	encoded, err := yaml.Marshal(cardYAML{
		Type:       card.Type,
		Title:      card.Title,
		Source:     card.Source,
		Tags:       card.Tags,
		CreatedBy:  card.CreatedBy,
		UpdatedBy:  card.UpdatedBy,
		Reviewed:   card.Reviewed,
		ReviewedBy: card.ReviewedBy,
		Model:      card.Model,
		Meta:       card.Meta,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal card %s: %w", card.ID, err)
	}
	return encoded, nil
}

func toEdgeYAML(edges []Edge) []edgeYAML {
	items := make([]edgeYAML, 0, len(edges))
	for _, edge := range edges {
		items = append(items, edgeYAML{
			ID:          edge.ID,
			From:        edge.From,
			To:          edge.To,
			Relation:    edge.Relation,
			Label:       edge.Label,
			Description: edge.Description,
		})
	}
	return items
}
