package board

import (
	"bytes"
	"errors"
	"fmt"
	fm "karte/internal/frontmatter"
	"sort"
	"strconv"
	"strings"
	"time"

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

type layoutYAML struct {
	Cards    map[string]CardLayout `yaml:"cards,omitempty"`
	Viewport Viewport              `yaml:"viewport,omitempty"`
}

func Parse(path, content string) (*Document, error) {
	parsedFM, body := fm.ParseFrontMatter(content)
	if parsedFM == nil {
		return nil, ErrMissingFrontMatter
	}
	boardType := extractRawString(parsedFM.Raw, "type")
	if boardType == "" {
		boardType = BoardType
	}
	if boardType != BoardType {
		return nil, ErrInvalidBoardType
	}

	sections := parseSections(body)
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
		Version:    extractRawInt(parsedFM.Raw, "version", 1),
		Created:    extractRawString(parsedFM.Raw, "created"),
		Updated:    extractRawString(parsedFM.Raw, "updated"),
		Tags:       fm.NormalizeTags(parsedFM.Tags),
		Notes:      strings.TrimSpace(sections["notes"]),
		RawContent: content,
	}
	if doc.Version == 0 {
		doc.Version = 1
	}
	if doc.Tags == nil {
		doc.Tags = []string{}
	}
	if doc.Created == "" {
		doc.Created = time.Now().Format("2006-01-02")
	}
	if doc.Updated == "" {
		doc.Updated = doc.Created
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

	return doc, nil
}

func Serialize(doc *Document) (string, error) {
	if doc == nil {
		return "", fmt.Errorf("board document is nil")
	}
	frontMatter := boardFrontMatter{
		Type:    BoardType,
		DocID:   doc.DocID,
		Title:   doc.Title,
		Version: doc.Version,
		Created: doc.Created,
		Updated: doc.Updated,
		Tags:    doc.Tags,
	}
	if frontMatter.Version == 0 {
		frontMatter.Version = 1
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
	for i, card := range doc.Cards {
		b.WriteString(fmt.Sprintf("### %s\n\n", card.ID))
		b.WriteString("```yaml\n")
		b.WriteString(serializeCardYAML(card))
		b.WriteString("```\n\n")
		if strings.TrimSpace(card.Body) != "" {
			b.WriteString(strings.TrimSpace(card.Body))
			b.WriteString("\n\n")
		}
		if i < len(doc.Cards)-1 {
			b.WriteString("---\n\n")
		}
	}
	b.WriteString("## Edges\n\n")
	b.WriteString("```yaml\n")
	edgesBytes, err := yaml.Marshal(toEdgeYAML(doc.Edges))
	if err != nil {
		return "", fmt.Errorf("marshal edges: %w", err)
	}
	b.Write(edgesBytes)
	b.WriteString("```\n\n")
	b.WriteString("## Layout\n\n")
	b.WriteString("```yaml\n")
	layoutBytes, err := yaml.Marshal(layoutYAML{
		Cards:    doc.Layout.Cards,
		Viewport: doc.Layout.Viewport,
	})
	if err != nil {
		return "", fmt.Errorf("marshal layout: %w", err)
	}
	b.Write(layoutBytes)
	b.WriteString("```\n")
	if strings.TrimSpace(doc.Notes) != "" {
		b.WriteString("\n## Notes\n\n")
		b.WriteString(strings.TrimSpace(doc.Notes))
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
	var meta map[string]any
	if err := yaml.Unmarshal([]byte(yamlBlock), &meta); err != nil {
		return Card{}, fmt.Errorf("unmarshal card yaml %s: %w", card.ID, err)
	}
	card.Type = extractMapString(meta, "type")
	card.Title = extractMapString(meta, "title")
	card.Source = extractMapString(meta, "source")
	card.CreatedBy = extractMapString(meta, "created_by")
	card.UpdatedBy = extractMapString(meta, "updated_by")
	card.ReviewedBy = extractMapString(meta, "reviewed_by")
	card.Model = extractMapString(meta, "model")
	card.Reviewed = extractMapBool(meta, "reviewed")
	card.Tags = extractMapStringSlice(meta, "tags")

	delete(meta, "type")
	delete(meta, "title")
	delete(meta, "source")
	delete(meta, "created_by")
	delete(meta, "updated_by")
	delete(meta, "reviewed_by")
	delete(meta, "model")
	delete(meta, "reviewed")
	delete(meta, "tags")
	card.Meta = meta
	card.Body = strings.TrimSpace(body)
	return card, nil
}

func parseEdges(section string) ([]Edge, error) {
	yamlBlock, _, err := extractFirstYAMLBlock(section)
	if err != nil {
		return nil, err
	}
	var raw []edgeYAML
	if err := yaml.Unmarshal([]byte(yamlBlock), &raw); err != nil {
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
	if err := yaml.Unmarshal([]byte(yamlBlock), &raw); err != nil {
		return Layout{}, fmt.Errorf("unmarshal layout: %w", err)
	}
	if raw.Cards == nil {
		raw.Cards = map[string]CardLayout{}
	}
	if raw.Viewport.Zoom == 0 {
		raw.Viewport.Zoom = 1
	}
	return Layout{
		Cards:    raw.Cards,
		Viewport: raw.Viewport,
	}, nil
}

func parseSections(body string) map[string]string {
	lines := strings.Split(body, "\n")
	sections := map[string]string{}
	current := ""
	var buf strings.Builder
	flush := func() {
		if current == "" {
			return
		}
		sections[current] = strings.TrimSpace(buf.String())
		buf.Reset()
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			flush()
			current = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(line, "## ")))
			continue
		}
		if current == "" {
			continue
		}
		buf.WriteString(line)
		buf.WriteString("\n")
	}
	flush()
	return sections
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

func serializeCardYAML(card Card) string {
	var buf bytes.Buffer
	writeYAMLKV(&buf, "type", card.Type)
	writeYAMLKV(&buf, "title", card.Title)
	if card.Source != "" {
		writeYAMLKV(&buf, "source", card.Source)
	}
	if len(card.Tags) > 0 {
		writeYAMLStringList(&buf, "tags", card.Tags)
	}
	if card.CreatedBy != "" {
		writeYAMLKV(&buf, "created_by", card.CreatedBy)
	}
	if card.UpdatedBy != "" {
		writeYAMLKV(&buf, "updated_by", card.UpdatedBy)
	}
	if card.ReviewedBy != "" {
		writeYAMLKV(&buf, "reviewed_by", card.ReviewedBy)
	}
	if card.Reviewed {
		buf.WriteString("reviewed: true\n")
	}
	if card.Model != "" {
		writeYAMLKV(&buf, "model", card.Model)
	}
	if len(card.Meta) > 0 {
		keys := make([]string, 0, len(card.Meta))
		for key := range card.Meta {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			writeAnyValue(&buf, key, card.Meta[key])
		}
	}
	return buf.String()
}

func writeYAMLKV(buf *bytes.Buffer, key, value string) {
	if value == "" {
		return
	}
	encoded, _ := yaml.Marshal(value)
	buf.WriteString(key)
	buf.WriteString(": ")
	buf.Write(bytes.TrimSpace(encoded))
	buf.WriteByte('\n')
}

func writeYAMLStringList(buf *bytes.Buffer, key string, values []string) {
	if len(values) == 0 {
		return
	}
	buf.WriteString(key)
	buf.WriteString(":\n")
	for _, value := range values {
		encoded, _ := yaml.Marshal(value)
		buf.WriteString("  - ")
		buf.Write(bytes.TrimSpace(encoded))
		buf.WriteByte('\n')
	}
}

func writeAnyValue(buf *bytes.Buffer, key string, value any) {
	m := map[string]any{key: value}
	encoded, err := yaml.Marshal(m)
	if err != nil {
		return
	}
	buf.Write(encoded)
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

func extractRawString(raw map[string]any, key string) string {
	if raw == nil {
		return ""
	}
	if value, ok := raw[key].(string); ok {
		return value
	}
	return ""
}

func extractRawInt(raw map[string]any, key string, defaultValue int) int {
	if raw == nil {
		return defaultValue
	}
	switch value := raw[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		parsed, err := strconv.Atoi(value)
		if err == nil {
			return parsed
		}
	}
	return defaultValue
}

func extractMapString(m map[string]any, key string) string {
	if value, ok := m[key].(string); ok {
		return value
	}
	return ""
}

func extractMapBool(m map[string]any, key string) bool {
	if value, ok := m[key].(bool); ok {
		return value
	}
	return false
}

func extractMapStringSlice(m map[string]any, key string) []string {
	if value, ok := m[key]; ok {
		return anyToStringSlice(value)
	}
	return nil
}

func anyToStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	case string:
		if typed == "" {
			return []string{}
		}
		parts := strings.Split(typed, ",")
		result := make([]string, 0, len(parts))
		for _, part := range parts {
			trimmed := strings.TrimSpace(part)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
		return result
	}
	return []string{}
}
