package frontmatter

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// FrontMatter represents the YAML frontmatter structure
type FrontMatter struct {
	Title string         `yaml:"title"`
	Tags  string         `yaml:"tags"` // Comma-separated tags string
	Theme string         `yaml:"theme"`
	Marp  bool           `yaml:"marp"` // Marp presentation mode
	DocID string         `yaml:"doc_id"` // Document ID (logical document identifier)
	Raw   map[string]any // Capture remaining custom fields
}

// UnmarshalYAML implements custom unmarshaling to handle both known and custom fields
func (fm *FrontMatter) UnmarshalYAML(unmarshal func(interface{}) error) error {
	// First, unmarshal into a map
	var raw map[string]any
	if err := unmarshal(&raw); err != nil {
		return err
	}

	fm.Raw = make(map[string]any)

	// Extract known fields
	if title, ok := raw["title"].(string); ok {
		fm.Title = title
		delete(raw, "title")
	}
	if tags, ok := raw["tags"].(string); ok {
		fm.Tags = tags
		delete(raw, "tags")
	}
	if theme, ok := raw["theme"].(string); ok {
		fm.Theme = theme
		delete(raw, "theme")
	}
	if marp, ok := raw["marp"].(bool); ok {
		fm.Marp = marp
		delete(raw, "marp")
	}
	if docID, ok := raw["doc_id"].(string); ok {
		fm.DocID = docID
		delete(raw, "doc_id")
	}

	// Store remaining fields in Raw
	for k, v := range raw {
		fm.Raw[k] = v
	}

	return nil
}

var frontMatterRegex = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*(\n|$)`)

// normalizeYAMLKeyValue adds space after colon if missing (for quoted values)
// Converts `title:"value"` to `title: "value"` for YAML compatibility
func normalizeYAMLKeyValue(yamlContent string) string {
	// Pattern: key:"value" -> key: "value"
	// Match word boundary, key name, colon, optional space, quoted string
	re := regexp.MustCompile(`(\w+):(".*?")`)
	return re.ReplaceAllString(yamlContent, `$1: $2`)
}

// ParseFrontMatter extracts and parses YAML frontmatter from markdown content
// Returns the parsed frontmatter and the remaining markdown body
// If no frontmatter exists, returns nil and the original content
func ParseFrontMatter(content string) (*FrontMatter, string) {
	match := frontMatterRegex.FindStringSubmatch(content)
	if match == nil {
		return nil, content
	}

	yamlContent := match[1]
	// Normalize YAML: add space after colon for quoted values
	yamlContent = normalizeYAMLKeyValue(yamlContent)
	frontMatterEnd := len(match[0])
	body := content[frontMatterEnd:]

	var fm FrontMatter
	if err := yaml.Unmarshal([]byte(yamlContent), &fm); err != nil {
		// If YAML parsing fails, return nil (no frontmatter)
		// Log error for debugging (but we don't have access to logger here)
		return nil, content
	}

	return &fm, body
}

// NormalizeTag normalizes a tag string:
// - Converts full-width spaces to half-width
// - Unifies consecutive half-width spaces to single space
// - Trims leading and trailing spaces
func NormalizeTag(tag string) string {
	// Convert full-width space (U+3000) to half-width
	tag = strings.ReplaceAll(tag, "　", " ")

	// Convert to runes for proper handling
	runes := []rune(tag)
	var result []rune
	prevSpace := false

	for _, r := range runes {
		if unicode.IsSpace(r) {
			if !prevSpace {
				result = append(result, ' ')
				prevSpace = true
			}
		} else {
			result = append(result, r)
			prevSpace = false
		}
	}

	// Trim spaces
	normalized := strings.TrimSpace(string(result))
	return normalized
}

// NormalizeTags parses comma-separated tags and normalizes each tag
func NormalizeTags(tagsString string) []string {
	if tagsString == "" {
		return []string{}
	}

	// Split by comma
	tags := strings.Split(tagsString, ",")
	var normalized []string
	seen := make(map[string]bool)

	for _, tag := range tags {
		normalizedTag := NormalizeTag(tag)
		if normalizedTag != "" && !seen[normalizedTag] {
			normalized = append(normalized, normalizedTag)
			seen[normalizedTag] = true
		}
	}

	return normalized
}

// FormatFrontMatter formats a FrontMatter struct back to YAML string
// with quotes around title, tags, and theme values
func FormatFrontMatter(fm *FrontMatter) string {
	if fm == nil {
		return ""
	}

	var lines []string
	lines = append(lines, "---")

	// Format title with quotes
	if fm.Title != "" {
		lines = append(lines, fmt.Sprintf(`title: "%s"`, escapeYAMLString(fm.Title)))
	}

	// Format tags with quotes (normalized)
	if fm.Tags != "" {
		normalizedTags := NormalizeTags(fm.Tags)
		if len(normalizedTags) > 0 {
			tagsString := strings.Join(normalizedTags, ", ")
			lines = append(lines, fmt.Sprintf(`tags: "%s"`, escapeYAMLString(tagsString)))
		}
	}

	// Format theme with quotes
	if fm.Theme != "" {
		lines = append(lines, fmt.Sprintf(`theme: "%s"`, escapeYAMLString(fm.Theme)))
	}

	// Format doc_id with quotes
	if fm.DocID != "" {
		lines = append(lines, fmt.Sprintf(`doc_id: "%s"`, escapeYAMLString(fm.DocID)))
	}

	// Format custom fields from Raw
	if fm.Raw != nil {
		for key, value := range fm.Raw {
			// Skip already handled fields
			if key == "title" || key == "tags" || key == "theme" || key == "doc_id" {
				continue
			}
			// Format with quotes for string values
			if str, ok := value.(string); ok {
				lines = append(lines, fmt.Sprintf(`%s: "%s"`, key, escapeYAMLString(str)))
			} else {
				// For non-string values, use YAML marshal
				yamlBytes, err := yaml.Marshal(map[string]any{key: value})
				if err == nil {
					yamlStr := strings.TrimSpace(string(yamlBytes))
					lines = append(lines, yamlStr)
				}
			}
		}
	}

	lines = append(lines, "---")
	return strings.Join(lines, "\n") + "\n"
}

// escapeYAMLString escapes special characters in YAML string
func escapeYAMLString(s string) string {
	// Escape quotes
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}

// ExtractTitle extracts title from frontmatter or returns default
func ExtractTitle(content, defaultTitle string) string {
	fm, _ := ParseFrontMatter(content)
	if fm != nil && fm.Title != "" {
		return fm.Title
	}
	return defaultTitle
}

// ExtractTags extracts and normalizes tags from frontmatter
func ExtractTags(content string) []string {
	fm, _ := ParseFrontMatter(content)
	if fm != nil && fm.Tags != "" {
		return NormalizeTags(fm.Tags)
	}
	return []string{}
}

// ExtractTheme extracts theme from frontmatter
func ExtractTheme(content string) string {
	fm, _ := ParseFrontMatter(content)
	if fm != nil && fm.Theme != "" {
		return fm.Theme
	}
	return ""
}

// ExtractDocID extracts doc_id from frontmatter
func ExtractDocID(content string) string {
	fm, _ := ParseFrontMatter(content)
	if fm != nil && fm.DocID != "" {
		return fm.DocID
	}
	return ""
}
