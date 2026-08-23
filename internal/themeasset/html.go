package themeasset

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

var srcsetDescriptorPattern = regexp.MustCompile(`^(?:[1-9][0-9]*w|(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)x)$`)

func scanHTML(owner string, source []byte) ([]rawReference, []Violation) {
	document, err := html.Parse(bytes.NewReader(source))
	if err != nil {
		return nil, []Violation{{Code: "invalid-html", Owner: owner, Detail: err.Error()}}
	}
	refs := make([]rawReference, 0)
	violations := make([]Violation, 0)
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			element := strings.ToLower(node.Data)
			switch element {
			case "script", "iframe", "object", "embed":
				violations = append(violations, Violation{
					Code:   "active-html",
					Owner:  owner,
					Detail: "portable theme templates may not contain <" + element + ">",
				})
			case "base":
				violations = append(violations, Violation{
					Code:   "base-url-forbidden",
					Owner:  owner,
					Detail: "<base> would change package-relative reference resolution",
				})
			}

			rel := attributeValue(node, "rel")
			for _, attribute := range node.Attr {
				name := strings.ToLower(attribute.Key)
				if strings.HasPrefix(name, "on") {
					violations = append(violations, Violation{
						Code:   "event-handler-forbidden",
						Owner:  owner,
						Detail: "inline event handlers are not part of the declarative theme contract: " + name,
					})
					continue
				}
				switch name {
				case "style":
					styleRefs, scanErr := scanCSS(owner, attribute.Val, contextHTMLStyle)
					if scanErr != nil {
						violations = append(violations, Violation{Code: "unsupported-css", Owner: owner, Reference: attribute.Val, Detail: "style attribute: " + scanErr.Error()})
					} else {
						refs = append(refs, styleRefs...)
					}
				case "src":
					if isHTMLImageSourceElement(element) {
						refs = append(refs, rawReference{owner: owner, context: contextHTMLSrc, raw: attribute.Val, expected: KindImage})
					} else {
						refs = append(refs, rawReference{owner: owner, context: contextHTMLSrc, raw: attribute.Val, expected: KindOther})
					}
				case "href":
					expected := KindOther
					if element == "link" && relContains(rel, "stylesheet") {
						expected = KindStylesheet
					} else if element == "link" && (relContains(rel, "icon") || relContains(rel, "apple-touch-icon")) {
						expected = KindImage
					}
					refs = append(refs, rawReference{owner: owner, context: contextHTMLHref, raw: attribute.Val, expected: expected})
				case "srcset":
					candidates, parseErr := parseSrcset(attribute.Val)
					if parseErr != nil {
						violations = append(violations, Violation{Code: "invalid-srcset", Owner: owner, Reference: attribute.Val, Detail: parseErr.Error()})
					} else {
						for _, candidate := range candidates {
							refs = append(refs, rawReference{owner: owner, context: contextHTMLSrcset, raw: candidate, expected: KindImage})
						}
					}
				case "poster":
					refs = append(refs, rawReference{owner: owner, context: contextHTMLSrc, raw: attribute.Val, expected: KindImage})
				case "data":
					refs = append(refs, rawReference{owner: owner, context: contextHTMLSrc, raw: attribute.Val, expected: KindOther})
				case "srcdoc":
					violations = append(violations, Violation{Code: "active-html", Owner: owner, Detail: "srcdoc is forbidden"})
				case "http-equiv":
					if element == "meta" && strings.EqualFold(strings.TrimSpace(attribute.Val), "refresh") {
						violations = append(violations, Violation{Code: "meta-refresh-forbidden", Owner: owner, Detail: "meta refresh can navigate outside the package"})
					}
				}
			}

			if element == "style" {
				var css strings.Builder
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					if child.Type == html.TextNode {
						css.WriteString(child.Data)
					}
				}
				styleRefs, scanErr := scanCSS(owner, css.String(), contextHTMLStyleTag)
				if scanErr != nil {
					violations = append(violations, Violation{Code: "unsupported-css", Owner: owner, Detail: "<style>: " + scanErr.Error()})
				} else {
					refs = append(refs, styleRefs...)
				}
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return refs, violations
}

func parseSrcset(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("srcset is empty")
	}
	parts := strings.Split(value, ",")
	candidates := make([]string, 0, len(parts))
	for index, part := range parts {
		fields := strings.Fields(part)
		if len(fields) == 0 || len(fields) > 2 {
			return nil, fmt.Errorf("candidate %d must contain one URL and at most one descriptor", index+1)
		}
		if strings.ContainsAny(fields[0], "'\"") {
			return nil, fmt.Errorf("candidate %d URL may not be quoted", index+1)
		}
		if len(fields) == 2 && !srcsetDescriptorPattern.MatchString(fields[1]) {
			return nil, fmt.Errorf("candidate %d has an unsupported descriptor", index+1)
		}
		candidates = append(candidates, fields[0])
	}
	return candidates, nil
}

func attributeValue(node *html.Node, name string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, name) {
			return attribute.Val
		}
	}
	return ""
}

func relContains(rel, token string) bool {
	for _, field := range strings.Fields(strings.ToLower(rel)) {
		if field == token {
			return true
		}
	}
	return false
}

func isHTMLImageSourceElement(element string) bool {
	switch element {
	case "img", "source", "input":
		return true
	default:
		return false
	}
}
