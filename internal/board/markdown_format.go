package board

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

var boardFrontMatterPattern = regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*(\n|$)`)

func parseBoardFrontMatter(content string) (boardFrontMatter, string, error) {
	match := boardFrontMatterPattern.FindStringSubmatchIndex(content)
	if match == nil {
		return boardFrontMatter{}, content, ErrMissingFrontMatter
	}
	yamlContent := content[match[2]:match[3]]
	if err := validateFrontMatterVersionType(yamlContent); err != nil {
		return boardFrontMatter{}, content, err
	}
	var frontMatter boardFrontMatter
	if err := decodeStrictYAML(yamlContent, &frontMatter); err != nil {
		return boardFrontMatter{}, content, fmt.Errorf("unmarshal board front matter: %w", err)
	}
	return frontMatter, content[match[1]:], nil
}

func validateFrontMatterVersionType(content string) error {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return fmt.Errorf("unmarshal board front matter: %w", err)
	}
	if len(document.Content) == 0 {
		return nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return fmt.Errorf("unmarshal board front matter: expected a mapping")
	}
	allowed := stringSet([]string{"type", "doc_id", "title", "version", "created", "updated", "tags"})
	seen := make(map[string]struct{}, len(root.Content)/2)
	for index := 0; index+1 < len(root.Content); index += 2 {
		key := root.Content[index]
		value := root.Content[index+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
			return &ValidationError{Violations: []Violation{{
				Code: "board.front-matter.key.invalid", Path: "/frontMatter", Message: "front matter keys must be strings",
			}}}
		}
		keyPath := "/frontMatter/" + escapeJSONPointer(key.Value)
		if _, duplicate := seen[key.Value]; duplicate {
			return &ValidationError{Violations: []Violation{{
				Code: "board.front-matter.key.duplicate", Path: keyPath, Message: "front matter key is duplicated",
			}}}
		}
		seen[key.Value] = struct{}{}
		if _, known := allowed[key.Value]; !known {
			return &ValidationError{Violations: []Violation{{
				Code: "board.front-matter.key.unknown", Path: keyPath, Message: "front matter key is not part of the v1 contract",
			}}}
		}
		if key.Value != "version" {
			continue
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
			return versionDecodeError("board.version.type", "version must be an integer")
		}
		parsed, err := strconv.ParseInt(value.Value, 0, 64)
		if err != nil {
			return versionDecodeError("board.version.range", "version is outside the integer range")
		}
		converted := int(parsed)
		if int64(converted) != parsed {
			return versionDecodeError("board.version.range", "version is outside the platform integer range")
		}
	}
	return nil
}

func versionDecodeError(code, message string) error {
	return &ValidationError{Violations: []Violation{{
		Code: code, Path: "/version", Message: message,
	}}}
}

func decodeStrictYAML(content string, target any) error {
	decoder := yaml.NewDecoder(bytes.NewReader([]byte(content)))
	decoder.KnownFields(true)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple YAML documents are not allowed")
		}
		return err
	}
	return nil
}

func normalizeMetaValue(value any) any {
	switch typed := value.(type) {
	case time.Time:
		if typed.Hour() == 0 && typed.Minute() == 0 && typed.Second() == 0 && typed.Nanosecond() == 0 {
			return typed.Format(time.DateOnly)
		}
		return typed.Format(time.RFC3339Nano)
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizeMetaValue(item)
		}
		return result
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = normalizeMetaValue(item)
		}
		return result
	case map[any]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			stringKey, ok := key.(string)
			if !ok {
				return value
			}
			result[stringKey] = normalizeMetaValue(item)
		}
		return result
	default:
		return value
	}
}
