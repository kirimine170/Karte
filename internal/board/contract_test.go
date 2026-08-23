package board

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func TestBoardSchemaMatchesGoContract(t *testing.T) {
	var schema map[string]any
	if err := json.Unmarshal(Schema(), &schema); err != nil {
		t.Fatalf("embedded schema is invalid JSON: %v", err)
	}
	if got := int(schemaNumber(t, schema, "properties", "version", "const")); got != CurrentVersion {
		t.Fatalf("schema version = %d, want %d", got, CurrentVersion)
	}

	definitions := schemaMap(t, schema, "$defs")
	assertSchemaProperties(t, schema, reflect.TypeOf(Document{}))
	assertSchemaProperties(t, schemaMap(t, definitions, "Card"), reflect.TypeOf(Card{}))
	assertSchemaProperties(t, schemaMap(t, definitions, "Edge"), reflect.TypeOf(Edge{}))
	assertSchemaProperties(t, schemaMap(t, definitions, "Layout"), reflect.TypeOf(Layout{}))
	assertSchemaProperties(t, schemaMap(t, definitions, "CardLayout"), reflect.TypeOf(CardLayout{}))
	assertSchemaProperties(t, schemaMap(t, definitions, "ViewState"), reflect.TypeOf(Viewport{}))

	if got := schemaStrings(t, definitions, "CardType", "enum"); !reflect.DeepEqual(got, SupportedCardTypes()) {
		t.Fatalf("schema CardType enum = %v, Go enum = %v", got, SupportedCardTypes())
	}
	if got := schemaStrings(t, definitions, "Relation", "enum"); !reflect.DeepEqual(got, SupportedRelations()) {
		t.Fatalf("schema Relation enum = %v, Go enum = %v", got, SupportedRelations())
	}

	layout := schemaMap(t, definitions, "Layout")
	viewport := schemaMap(t, schemaMap(t, layout, "properties"), "viewport")
	if got := viewport["$ref"]; got != "#/$defs/ViewState" {
		t.Fatalf("layout.viewport ref = %#v", got)
	}
	var alias ViewState = Viewport{X: 1, Y: 2, Zoom: 1}
	if alias != (Viewport{X: 1, Y: 2, Zoom: 1}) {
		t.Fatal("ViewState alias does not preserve Viewport")
	}
	if schemaContainsKey(schema, "selectedCardId") || schemaContainsKey(schema, "selectedEdgeId") {
		t.Fatal("ephemeral selection leaked into the persisted schema")
	}

	rawContent := schemaMap(t, schemaMap(t, schema, "properties"), "rawContent")
	if got := rawContent["x-karte-sizeBoundary"]; got != "caller-before-parse" {
		t.Fatalf("rawContent size responsibility = %#v", got)
	}
	cardMeta := schemaMap(t, schemaMap(t, schemaMap(t, definitions, "Card"), "properties"), "meta")
	if got := int(cardMeta["x-karte-maxNodes"].(float64)); got != maxMetaNodes {
		t.Fatalf("schema meta node limit = %d, Go limit = %d", got, maxMetaNodes)
	}
	if got := int(cardMeta["maxProperties"].(float64)); got != maxMetaCollection {
		t.Fatalf("schema meta property limit = %d, Go limit = %d", got, maxMetaCollection)
	}
	propertyNames := schemaMap(t, cardMeta, "propertyNames")
	allOf, ok := propertyNames["allOf"].([]any)
	if !ok || len(allOf) != 2 {
		t.Fatalf("schema Card.meta propertyNames = %#v", propertyNames)
	}
	not := schemaMap(t, allOf[1].(map[string]any), "not")
	gotReserved := stringSlice(t, not["enum"])
	wantReserved := make([]string, 0, len(reservedCardMetaKeys))
	for key := range reservedCardMetaKeys {
		wantReserved = append(wantReserved, key)
	}
	sort.Strings(gotReserved)
	sort.Strings(wantReserved)
	if !reflect.DeepEqual(gotReserved, wantReserved) {
		t.Fatalf("schema reserved meta keys = %v, Go keys = %v", gotReserved, wantReserved)
	}
	metaValue := schemaMap(t, definitions, "MetaValue")
	if got := int(metaValue["x-karte-maxDepth"].(float64)); got != maxMetaDepth {
		t.Fatalf("schema meta depth = %d, Go limit = %d", got, maxMetaDepth)
	}
	if got := int(schemaVariant(t, metaValue, "string")["maxLength"].(float64)); got != maxMetaString {
		t.Fatalf("schema meta string limit = %d, Go limit = %d", got, maxMetaString)
	}
	if got := int(schemaVariant(t, metaValue, "array")["maxItems"].(float64)); got != maxMetaCollection {
		t.Fatalf("schema meta array limit = %d, Go limit = %d", got, maxMetaCollection)
	}
	if got := int(schemaVariant(t, metaValue, "object")["maxProperties"].(float64)); got != maxMetaCollection {
		t.Fatalf("schema meta object limit = %d, Go limit = %d", got, maxMetaCollection)
	}

	pathSchema := schemaMap(t, schemaMap(t, schema, "properties"), "path")
	pathPattern := regexp.MustCompile(pathSchema["pattern"].(string))
	for _, candidate := range []string{"content/board.board.md", "content/nested/board.board.md"} {
		if !validBoardPath(candidate) || !pathPattern.MatchString(candidate) {
			t.Fatalf("schema/validator path contract rejected %q", candidate)
		}
	}
	for _, candidate := range []string{"content/board.BOARD.MD", "content/board.md"} {
		if validBoardPath(candidate) || pathPattern.MatchString(candidate) {
			t.Fatalf("schema/validator suffix contract accepted %q", candidate)
		}
	}
}

func TestBoardMigrationIsDeterministicAndIdempotent(t *testing.T) {
	tests := []struct {
		name        string
		created     string
		updated     string
		wantCreated string
		wantUpdated string
	}{
		{name: "both missing", wantCreated: LegacyUnknownDate, wantUpdated: LegacyUnknownDate},
		{name: "created from updated", updated: "2026-08-01", wantCreated: "2026-08-01", wantUpdated: "2026-08-01"},
		{name: "updated from created", created: "2026-07-31", wantCreated: "2026-07-31", wantUpdated: "2026-07-31"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			doc := validBoardDocument()
			doc.Version = 0
			doc.Created = test.created
			doc.Updated = test.updated
			if err := Migrate(&doc); err != nil {
				t.Fatal(err)
			}
			if doc.Version != CurrentVersion || doc.Created != test.wantCreated || doc.Updated != test.wantUpdated {
				t.Fatalf("migration = version %d, %q, %q", doc.Version, doc.Created, doc.Updated)
			}
			first := doc
			if err := Migrate(&doc); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(doc, first) {
				t.Fatalf("migration is not idempotent:\nfirst=%#v\nsecond=%#v", first, doc)
			}
		})
	}
	for _, version := range []int{-1, CurrentVersion + 1} {
		doc := validBoardDocument()
		doc.Version = version
		assertViolationError(t, Migrate(&doc), "board.version.unsupported", "/version")
	}
}

func TestParseRejectsInvalidVersionTypesAndUnsupportedVersions(t *testing.T) {
	for _, version := range []string{"1.5", `"1"`, "999999999999999999999999999999999999"} {
		t.Run(version, func(t *testing.T) {
			_, err := Parse("content/version.board.md", minimalBoardMarkdown("version: "+version, "[]", validLayoutYAML()))
			if err == nil {
				t.Fatal("Parse accepted a non-integer or overflowing version")
			}
			var validationErr *ValidationError
			if !errors.As(err, &validationErr) || len(validationErr.Violations) != 1 || validationErr.Violations[0].Path != "/version" {
				t.Fatalf("version error is not stable: %T %v", err, err)
			}
		})
	}
	for _, version := range []string{"-1", "2"} {
		t.Run(version, func(t *testing.T) {
			_, err := Parse("content/version.board.md", minimalBoardMarkdown("version: "+version, "[]", validLayoutYAML()))
			assertViolationError(t, err, "board.version.unsupported", "/version")
		})
	}
}

func TestBoardGoldenLegacyFlatMetaMigrationRoundTrip(t *testing.T) {
	input, err := os.ReadFile("testdata/golden/v0-flat-meta.board.md")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := Parse("content/legacy.board.md", string(input))
	if err != nil {
		t.Fatal(err)
	}
	wantBytes, err := os.ReadFile("testdata/golden/v0-flat-meta.want.json")
	if err != nil {
		t.Fatal(err)
	}
	gotBytes, err := json.Marshal(map[string]any{
		"version":  doc.Version,
		"created":  doc.Created,
		"updated":  doc.Updated,
		"cardMeta": doc.Cards[0].Meta,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, gotBytes, wantBytes)

	serialized, err := Serialize(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(serialized, "\nmeta:\n") {
		t.Fatalf("canonical nested meta missing:\n%s", serialized)
	}
	for _, legacyFlat := range []string{"\ncolor:", "\npriority:", "\nreleased:"} {
		if strings.Contains(serialized, legacyFlat) {
			t.Fatalf("legacy flat meta %q survived canonical serialization", legacyFlat)
		}
	}
	roundTrip, err := Parse("content/legacy.board.md", serialized)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip.Cards[0].Meta, doc.Cards[0].Meta) {
		t.Fatalf("meta round trip = %#v, want %#v", roundTrip.Cards[0].Meta, doc.Cards[0].Meta)
	}
	serializedAgain, err := Serialize(roundTrip)
	if err != nil {
		t.Fatal(err)
	}
	if serializedAgain != serialized {
		t.Fatal("canonical serialization is not stable after round trip")
	}
}

func TestInvalidBoardFixtureAggregatesStableViolations(t *testing.T) {
	input, err := os.ReadFile("testdata/invalid/aggregate.board.md")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Parse("content/invalid.board.md", string(input))
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("Parse error = %T %v, want ValidationError", err, err)
	}
	got, err := json.MarshalIndent(validationErr.Violations, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/invalid/aggregate.violations.json")
	if err != nil {
		t.Fatalf("read violation golden: %v\nactual:\n%s", err, got)
	}
	assertJSONEqual(t, got, want)
}

func TestBoardRejectsUnknownTypedFieldsButAllowsMetaExtension(t *testing.T) {
	for name, input := range map[string]string{
		"document":  `{"path":"content/a.board.md","unexpected":true}`,
		"card":      `{"cards":[{"unexpected":true}]}`,
		"edge":      `{"edges":[{"unexpected":true}]}`,
		"layout":    `{"layout":{"unexpected":true}}`,
		"viewState": `{"layout":{"viewport":{"unexpected":true}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			var doc Document
			if err := json.Unmarshal([]byte(input), &doc); err == nil {
				t.Fatal("unknown typed field was accepted")
			}
		})
	}
	var doc Document
	if err := json.Unmarshal([]byte(`{"cards":[{"meta":{"extension":{"enabled":true}}}]}`), &doc); err != nil {
		t.Fatalf("Card.Meta extension rejected: %v", err)
	}
}

func TestBoardMarkdownRejectsUnknownFrontMatterEdgeAndLayoutFields(t *testing.T) {
	tests := []struct {
		name       string
		frontExtra string
		edges      string
		layout     string
	}{
		{name: "front matter", frontExtra: "unexpected: true\n", edges: "[]", layout: validLayoutYAML()},
		{name: "edge", edges: "- id: edge:a\n  from: card:a\n  to: card:b\n  relation: supports\n  unexpected: true", layout: validLayoutYAML()},
		{name: "layout", edges: "[]", layout: validLayoutYAML() + "unexpected: true\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			content := minimalBoardMarkdown("version: 1\n"+test.frontExtra, test.edges, test.layout)
			_, err := Parse("content/strict.board.md", content)
			if err == nil {
				t.Fatal("unknown YAML field was accepted")
			}
			if test.name == "front matter" {
				assertViolationError(t, err, "board.front-matter.key.unknown", "/frontMatter/unexpected")
			}
		})
	}
}

func TestBoardMarkdownRejectsUnknownAndDuplicateSections(t *testing.T) {
	unknown := strings.Replace(minimalBoardMarkdown("version: 1", "[]", validLayoutYAML()), "## Edges", "## Unknown\n\nvalue\n\n## Edges", 1)
	_, err := Parse("content/sections.board.md", unknown)
	assertViolationError(t, err, "board.section.unknown", "/sections/unknown")

	duplicate := minimalBoardMarkdown("version: 1", "[]", validLayoutYAML()) + "\n## Cards\n"
	_, err = Parse("content/sections.board.md", duplicate)
	assertViolationError(t, err, "board.section.duplicate", "/sections/cards")

	withNotesHeading := minimalBoardMarkdown("version: 1", "[]", validLayoutYAML()) + "\n## Notes\n\nText\n\n## Nested note heading\n\nMore.\n"
	if _, err := Parse("content/sections.board.md", withNotesHeading); err != nil {
		t.Fatalf("Markdown heading inside Notes rejected: %v", err)
	}
}

func TestBoardEnumsAcceptEveryPersistedValue(t *testing.T) {
	for _, cardType := range SupportedCardTypes() {
		doc := validBoardDocument()
		doc.Cards[0].Type = cardType
		if violations := ValidateDocument(&doc); len(violations) != 0 {
			t.Fatalf("card type %q rejected: %#v", cardType, violations)
		}
	}
	doc := validTwoCardBoardDocument()
	for index, relation := range SupportedRelations() {
		doc.Edges = append(doc.Edges, Edge{
			ID: "edge:r" + strings.Repeat("x", index+1), From: "card:a", To: "card:b", Relation: relation,
		})
	}
	if violations := ValidateDocument(&doc); len(violations) != 0 {
		t.Fatalf("persisted relation enum rejected: %#v", violations)
	}
}

func TestBoardSourceAndMetaResourceBounds(t *testing.T) {
	validSources := []string{"", "content/note.md", "content/docs/report.pdf", "data/image/nested/a.webp", "data/audio/session.wav", "data/csv/table.csv"}
	for _, source := range validSources {
		doc := validBoardDocument()
		doc.Cards[0].Source = source
		if violations := ValidateDocument(&doc); findViolation(violations, "card.source.invalid") != nil {
			t.Fatalf("canonical source %q rejected: %#v", source, violations)
		}
	}
	invalidSources := []string{"../outside.md", "/content/a.md", `content\\a.md`, "content/../a.md", "data/csv/nested/a.csv", "data/csv/a.CSV", "data/csv/a.txt"}
	for _, source := range invalidSources {
		doc := validBoardDocument()
		doc.Cards[0].Source = source
		if findViolation(ValidateDocument(&doc), "card.source.invalid") == nil {
			t.Fatalf("unsafe or noncanonical source %q accepted", source)
		}
	}

	doc := validBoardDocument()
	doc.Cards[0].Meta = map[string]any{"title": "collision"}
	if findViolation(ValidateDocument(&doc), "card.meta.reserved") == nil {
		t.Fatal("reserved meta key accepted")
	}
	doc.Cards[0].Meta = map[string]any{"large": strings.Repeat("x", maxMetaString+1)}
	if findViolation(ValidateDocument(&doc), "card.meta.string.limit") == nil {
		t.Fatal("oversized meta string accepted")
	}
	doc.Cards[0].Meta = map[string]any{"items": make([]any, maxMetaCollection+1)}
	if findViolation(ValidateDocument(&doc), "card.meta.collection.limit") == nil {
		t.Fatal("oversized meta collection accepted")
	}
	doc.Cards[0].Meta = map[string]any{
		"a": make([]any, maxMetaCollection),
		"b": make([]any, maxMetaCollection),
		"c": make([]any, maxMetaCollection),
		"d": make([]any, maxMetaCollection),
	}
	if findViolation(ValidateDocument(&doc), "card.meta.nodes.limit") == nil {
		t.Fatal("oversized meta node graph accepted")
	}
}

func TestBoardGeometryAndReferenceValidation(t *testing.T) {
	doc := validTwoCardBoardDocument()
	doc.Layout.Cards["card:a"] = CardLayout{X: math.NaN(), Y: maxCoordinate + 1, Width: math.Inf(1), Height: 0}
	doc.Layout.Cards["card:orphan"] = CardLayout{Width: 1, Height: 1}
	delete(doc.Layout.Cards, "card:b")
	doc.Layout.Viewport.Zoom = math.Inf(-1)
	doc.Edges = []Edge{
		{ID: "edge:a", From: "card:a", To: "card:a", Relation: RelationSupports},
		{ID: "edge:a", From: "card:a", To: "card:missing", Relation: "unknown"},
	}
	wantCodes := []string{
		"edge.id.duplicate", "edge.relation.invalid", "edge.self", "edge.to.missing",
		"layout.card.missing", "layout.card.orphan", "layout.geometry.non-finite",
		"layout.geometry.range", "view-state.geometry.non-finite",
	}
	for _, code := range wantCodes {
		if findViolation(ValidateDocument(&doc), code) == nil {
			t.Errorf("missing violation %s", code)
		}
	}
}

func validBoardDocument() Document {
	return Document{
		Path: "content/valid.board.md", Title: "Valid", DocID: "board:valid", Type: BoardType,
		Version: CurrentVersion, Created: "2026-08-01", Updated: "2026-08-01", Tags: []string{},
		Cards: []Card{{ID: "card:a", Type: CardTypeResource, Title: "A", Body: "", Meta: map[string]any{}}},
		Edges: []Edge{},
		Layout: Layout{
			Cards:    map[string]CardLayout{"card:a": {X: 0, Y: 0, Width: 300, Height: 180}},
			Viewport: Viewport{X: 0, Y: 0, Zoom: 1},
		},
	}
}

func validTwoCardBoardDocument() Document {
	doc := validBoardDocument()
	doc.Cards = append(doc.Cards, Card{ID: "card:b", Type: CardTypeThought, Title: "B", Body: "", Meta: map[string]any{}})
	doc.Layout.Cards["card:b"] = CardLayout{X: 400, Y: 0, Width: 300, Height: 180}
	return doc
}

func minimalBoardMarkdown(versionFields, edges, layout string) string {
	return "---\n" +
		"type: karte-board\n" +
		"title: Minimal\n" +
		versionFields + "\n" +
		"created: 2026-08-01\n" +
		"updated: 2026-08-01\n" +
		"tags: []\n" +
		"---\n\n# Board\n\n## Cards\n\n## Edges\n\n```yaml\n" + edges + "\n```\n\n## Layout\n\n```yaml\n" + layout + "```\n"
}

func validLayoutYAML() string {
	return "cards: {}\nviewport:\n  x: 0\n  y: 0\n  zoom: 1\n"
}

func assertViolationError(t *testing.T, err error, code, path string) {
	t.Helper()
	var validationErr *ValidationError
	if !errors.As(err, &validationErr) || len(validationErr.Violations) != 1 {
		t.Fatalf("error = %T %v, want one ValidationError", err, err)
	}
	if got := validationErr.Violations[0]; got.Code != code || got.Path != path {
		t.Fatalf("violation = %#v, want %s at %s", got, code, path)
	}
}

func findViolation(violations []Violation, code string) *Violation {
	for index := range violations {
		if violations[index].Code == code {
			return &violations[index]
		}
	}
	return nil
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode actual JSON: %v", err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatalf("decode expected JSON: %v", err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("JSON mismatch:\nactual: %s\nexpected: %s", got, want)
	}
}

func assertSchemaProperties(t *testing.T, schema map[string]any, typ reflect.Type) {
	t.Helper()
	properties := schemaMap(t, schema, "properties")
	want := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		name := strings.Split(typ.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			want = append(want, name)
		}
	}
	got := make([]string, 0, len(properties))
	for name := range properties {
		got = append(got, name)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("schema properties for %s = %v, Go fields = %v", typ.Name(), got, want)
	}
}

func schemaMap(t *testing.T, value map[string]any, keys ...string) map[string]any {
	t.Helper()
	current := value
	for _, key := range keys {
		next, ok := current[key].(map[string]any)
		if !ok {
			t.Fatalf("schema path %s is not an object", strings.Join(keys, "/"))
		}
		current = next
	}
	return current
}

func schemaNumber(t *testing.T, value map[string]any, keys ...string) float64 {
	t.Helper()
	current := value
	for _, key := range keys[:len(keys)-1] {
		current = schemaMap(t, current, key)
	}
	result, ok := current[keys[len(keys)-1]].(float64)
	if !ok {
		t.Fatalf("schema path %s is not a number", strings.Join(keys, "/"))
	}
	return result
}

func schemaStrings(t *testing.T, value map[string]any, keys ...string) []string {
	t.Helper()
	current := value
	for _, key := range keys[:len(keys)-1] {
		current = schemaMap(t, current, key)
	}
	return stringSlice(t, current[keys[len(keys)-1]])
}

func schemaVariant(t *testing.T, definition map[string]any, typeName string) map[string]any {
	t.Helper()
	variants, ok := definition["oneOf"].([]any)
	if !ok {
		t.Fatalf("schema definition has no oneOf: %#v", definition)
	}
	for _, raw := range variants {
		variant, ok := raw.(map[string]any)
		if ok && variant["type"] == typeName {
			return variant
		}
	}
	t.Fatalf("schema definition has no %s variant", typeName)
	return nil
}

func stringSlice(t *testing.T, value any) []string {
	t.Helper()
	raw, ok := value.([]any)
	if !ok {
		t.Fatalf("value %#v is not an array", value)
	}
	result := make([]string, len(raw))
	for index, item := range raw {
		result[index], ok = item.(string)
		if !ok {
			t.Fatalf("array value %d is not a string", index)
		}
	}
	return result
}

func schemaContainsKey(value any, target string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			if key == target || schemaContainsKey(nested, target) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if schemaContainsKey(nested, target) {
				return true
			}
		}
	}
	return false
}
