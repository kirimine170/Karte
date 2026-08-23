package themeasset

import (
	"fmt"
	"strings"
)

// scanCSS is deliberately a small fail-closed lexer，not a regular-expression
// URL finder. Unsupported escapes and ambiguous resource-producing functions
// are rejected so they cannot bypass the package inventory.
func scanCSS(owner, source string, defaultContext referenceContext) ([]rawReference, error) {
	scanner := cssScanner{
		owner:          owner,
		source:         source,
		defaultContext: defaultContext,
	}
	if err := scanner.scanRange(0, len(source), false); err != nil {
		return nil, err
	}
	return scanner.references, nil
}

type cssScanner struct {
	owner          string
	source         string
	defaultContext referenceContext
	references     []rawReference
}

func (s *cssScanner) scanRange(start, end int, inFontFace bool) error {
	for i := start; i < end; {
		switch {
		case isCSSSpace(s.source[i]):
			i++
		case startsCSSComment(s.source, i, end):
			next, err := skipCSSComment(s.source, i, end)
			if err != nil {
				return cssOffsetError(i, err.Error())
			}
			i = next
		case s.source[i] == '\'' || s.source[i] == '"':
			next, _, err := parseCSSString(s.source, i, end)
			if err != nil {
				return cssOffsetError(i, err.Error())
			}
			i = next
		case s.source[i] == '\\':
			return cssOffsetError(i, "CSS escapes are not supported by the portable asset contract")
		case s.source[i] == '@':
			nameStart := i + 1
			nameEnd := scanCSSIdentifier(s.source, nameStart, end)
			if nameEnd == nameStart {
				i++
				continue
			}
			name := strings.ToLower(s.source[nameStart:nameEnd])
			switch name {
			case "import":
				next, raw, err := s.parseImport(nameEnd, end)
				if err != nil {
					return cssOffsetError(i, err.Error())
				}
				s.references = append(s.references, rawReference{
					owner:    s.owner,
					context:  contextCSSImport,
					raw:      raw,
					expected: KindStylesheet,
				})
				i = next
			case "font-face":
				if inFontFace {
					return cssOffsetError(i, "nested @font-face is not allowed")
				}
				blockStart, err := skipCSSIgnorable(s.source, nameEnd, end)
				if err != nil {
					return cssOffsetError(i, err.Error())
				}
				if blockStart >= end || s.source[blockStart] != '{' {
					return cssOffsetError(i, "@font-face must be followed by a declaration block")
				}
				blockEnd, err := findCSSBlockEnd(s.source, blockStart, end)
				if err != nil {
					return cssOffsetError(i, err.Error())
				}
				beforeRefs := len(s.references)
				if err := s.scanRange(blockStart+1, blockEnd, true); err != nil {
					return err
				}
				fontRefs := 0
				for _, ref := range s.references[beforeRefs:] {
					if ref.context == contextFontFace {
						fontRefs++
					}
				}
				if fontRefs != 1 {
					return cssOffsetError(i, "@font-face must contain exactly one url() source")
				}
				formatOK, err := findWOFF2Format(s.source, blockStart+1, blockEnd)
				if err != nil {
					return cssOffsetError(i, err.Error())
				}
				if !formatOK {
					return cssOffsetError(i, `@font-face must declare format("woff2")`)
				}
				i = blockEnd + 1
			default:
				i = nameEnd
			}
		default:
			nameEnd := scanCSSIdentifier(s.source, i, end)
			if nameEnd == i {
				i++
				continue
			}
			name := strings.ToLower(s.source[i:nameEnd])
			open, err := skipCSSIgnorable(s.source, nameEnd, end)
			if err != nil {
				return cssOffsetError(i, err.Error())
			}
			if open >= end || s.source[open] != '(' {
				i = nameEnd
				continue
			}
			switch name {
			case "url":
				next, raw, err := parseCSSURL(s.source, open, end)
				if err != nil {
					return cssOffsetError(i, err.Error())
				}
				context := s.defaultContext
				expected := KindOther
				if inFontFace {
					context = contextFontFace
					expected = KindFont
				}
				s.references = append(s.references, rawReference{
					owner:    s.owner,
					context:  context,
					raw:      raw,
					expected: expected,
				})
				i = next
			case "local":
				return cssOffsetError(i, "local() font sources are nondeterministic and forbidden")
			case "image-set", "-webkit-image-set":
				return cssOffsetError(i, "image-set() is unsupported because string image candidates bypass url() inventory")
			default:
				i = nameEnd
			}
		}
	}
	return nil
}

func (s *cssScanner) parseImport(start, end int) (int, string, error) {
	i, err := skipCSSIgnorable(s.source, start, end)
	if err != nil {
		return i, "", err
	}
	if i >= end {
		return i, "", fmt.Errorf("@import is missing a target")
	}
	if s.source[i] == '\'' || s.source[i] == '"' {
		next, value, err := parseCSSString(s.source, i, end)
		return next, value, err
	}
	nameEnd := scanCSSIdentifier(s.source, i, end)
	if nameEnd == i || !strings.EqualFold(s.source[i:nameEnd], "url") {
		return i, "", fmt.Errorf("@import target must be a quoted string or url()")
	}
	open, err := skipCSSIgnorable(s.source, nameEnd, end)
	if err != nil {
		return open, "", err
	}
	if open >= end || s.source[open] != '(' {
		return open, "", fmt.Errorf("@import url must be followed by parentheses")
	}
	return parseCSSURL(s.source, open, end)
}

func parseCSSURL(source string, open, end int) (int, string, error) {
	i, err := skipCSSIgnorable(source, open+1, end)
	if err != nil {
		return i, "", err
	}
	if i >= end {
		return i, "", fmt.Errorf("unterminated url()")
	}
	if source[i] == '\'' || source[i] == '"' {
		next, value, err := parseCSSString(source, i, end)
		if err != nil {
			return next, "", err
		}
		next, err = skipCSSIgnorable(source, next, end)
		if err != nil {
			return next, "", err
		}
		if next >= end || source[next] != ')' {
			return next, "", fmt.Errorf("quoted url() must end after its string")
		}
		if value == "" {
			return next, "", fmt.Errorf("url() target is empty")
		}
		return next + 1, value, nil
	}

	valueStart := i
	for i < end {
		switch source[i] {
		case ')':
			value := strings.TrimSpace(source[valueStart:i])
			if value == "" {
				return i, "", fmt.Errorf("url() target is empty")
			}
			return i + 1, value, nil
		case '\\':
			return i, "", fmt.Errorf("CSS escapes in url() are not supported")
		case '\'', '"', '(':
			return i, "", fmt.Errorf("invalid character in unquoted url()")
		case '/', '*':
			if startsCSSComment(source, i, end) {
				return i, "", fmt.Errorf("comments inside an unquoted url() are not supported")
			}
		}
		if isCSSSpace(source[i]) {
			value := strings.TrimSpace(source[valueStart:i])
			next, err := skipCSSIgnorable(source, i, end)
			if err != nil {
				return next, "", err
			}
			if next >= end || source[next] != ')' {
				return next, "", fmt.Errorf("unquoted url() contains whitespace before more content")
			}
			if value == "" {
				return next, "", fmt.Errorf("url() target is empty")
			}
			return next + 1, value, nil
		}
		i++
	}
	return i, "", fmt.Errorf("unterminated url()")
}

func parseCSSString(source string, quote, end int) (int, string, error) {
	quoteByte := source[quote]
	for i := quote + 1; i < end; i++ {
		switch source[i] {
		case '\\':
			return i, "", fmt.Errorf("CSS escapes in strings are not supported")
		case '\n', '\r', '\f':
			return i, "", fmt.Errorf("CSS strings may not contain a raw newline")
		default:
			if source[i] == quoteByte {
				return i + 1, source[quote+1 : i], nil
			}
		}
	}
	return end, "", fmt.Errorf("unterminated CSS string")
}

func findWOFF2Format(source string, start, end int) (bool, error) {
	found := false
	for i := start; i < end; {
		switch {
		case isCSSSpace(source[i]):
			i++
		case startsCSSComment(source, i, end):
			next, err := skipCSSComment(source, i, end)
			if err != nil {
				return false, err
			}
			i = next
		case source[i] == '\'' || source[i] == '"':
			next, _, err := parseCSSString(source, i, end)
			if err != nil {
				return false, err
			}
			i = next
		case source[i] == '\\':
			return false, fmt.Errorf("CSS escapes are not supported")
		default:
			nameEnd := scanCSSIdentifier(source, i, end)
			if nameEnd == i {
				i++
				continue
			}
			if !strings.EqualFold(source[i:nameEnd], "format") {
				i = nameEnd
				continue
			}
			open, err := skipCSSIgnorable(source, nameEnd, end)
			if err != nil {
				return false, err
			}
			if open >= end || source[open] != '(' {
				i = nameEnd
				continue
			}
			arg, err := skipCSSIgnorable(source, open+1, end)
			if err != nil {
				return false, err
			}
			if arg >= end || (source[arg] != '\'' && source[arg] != '"') {
				return false, fmt.Errorf("format() must contain one quoted value")
			}
			next, value, err := parseCSSString(source, arg, end)
			if err != nil {
				return false, err
			}
			next, err = skipCSSIgnorable(source, next, end)
			if err != nil {
				return false, err
			}
			if next >= end || source[next] != ')' {
				return false, fmt.Errorf("format() must contain one quoted value")
			}
			if !strings.EqualFold(value, "woff2") {
				return false, fmt.Errorf("only format(\"woff2\") is supported")
			}
			found = true
			i = next + 1
		}
	}
	return found, nil
}

func findCSSBlockEnd(source string, open, end int) (int, error) {
	depth := 1
	for i := open + 1; i < end; {
		switch {
		case startsCSSComment(source, i, end):
			next, err := skipCSSComment(source, i, end)
			if err != nil {
				return i, err
			}
			i = next
		case source[i] == '\'' || source[i] == '"':
			next, _, err := parseCSSString(source, i, end)
			if err != nil {
				return i, err
			}
			i = next
		case source[i] == '\\':
			return i, fmt.Errorf("CSS escapes are not supported")
		case source[i] == '{':
			depth++
			i++
		case source[i] == '}':
			depth--
			if depth == 0 {
				return i, nil
			}
			i++
		default:
			i++
		}
	}
	return end, fmt.Errorf("unterminated CSS declaration block")
}

func skipCSSIgnorable(source string, start, end int) (int, error) {
	i := start
	for i < end {
		if isCSSSpace(source[i]) {
			i++
			continue
		}
		if startsCSSComment(source, i, end) {
			next, err := skipCSSComment(source, i, end)
			if err != nil {
				return i, err
			}
			i = next
			continue
		}
		break
	}
	return i, nil
}

func skipCSSComment(source string, start, end int) (int, error) {
	for i := start + 2; i+1 < end; i++ {
		if source[i] == '*' && source[i+1] == '/' {
			return i + 2, nil
		}
	}
	return end, fmt.Errorf("unterminated CSS comment")
}

func startsCSSComment(source string, i, end int) bool {
	return i+1 < end && source[i] == '/' && source[i+1] == '*'
}

func scanCSSIdentifier(source string, start, end int) int {
	i := start
	for i < end {
		b := source[i]
		if (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '-' || b == '_' {
			i++
			continue
		}
		break
	}
	return i
}

func isCSSSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func cssOffsetError(offset int, detail string) error {
	return fmt.Errorf("byte %d: %s", offset, detail)
}
