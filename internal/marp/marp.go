package marp

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

// ParseSlides parses Marp markdown content into individual slides
// Slides are separated by `---` (horizontal rule)
func ParseSlides(content string) []string {
	// Remove leading/trailing whitespace
	content = strings.TrimSpace(content)
	if content == "" {
		return []string{}
	}

	// Split by horizontal rule (---) that appears on its own line
	// This regex matches `---` surrounded by newlines or at start/end
	slideRegex := regexp.MustCompile(`(?m)^---\s*$`)

	// Split content by slide separators
	parts := slideRegex.Split(content, -1)

	var slides []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			slides = append(slides, part)
		}
	}

	// If no slides were found (no separators), treat entire content as one slide
	if len(slides) == 0 {
		slides = []string{content}
	}

	return slides
}

// parseSlideClasses extracts class directives from slide content
// Example: <!-- _class: lead invert --> -> ["lead", "invert"]
func parseSlideClasses(slideContent string) []string {
	var classes []string
	lines := strings.Split(slideContent, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "<!--") && strings.Contains(trimmed, "_class:") {
			// Extract class names from <!-- _class: lead invert -->
			re := regexp.MustCompile(`_class:\s*([^-->]+)`)
			matches := re.FindStringSubmatch(trimmed)
			if len(matches) > 1 {
				classStr := strings.TrimSpace(matches[1])
				classNames := strings.Fields(classStr)
				classes = append(classes, classNames...)
			}
		}
	}
	return classes
}

// RenderSlideHTML converts a single slide markdown content to HTML
// This is a simplified markdown to HTML converter for slides
func RenderSlideHTML(slideContent string) string {
	lines := strings.Split(slideContent, "\n")
	var htmlLines []string
	// List nesting management
	type listCtx struct {
		ordered bool
		level   int
	}
	var listStack []listCtx
	var inTable bool
	var tableRows []string
	var inCodeBlock bool
	var codeIsIndented bool
	var codeLang string
	var codeBuffer []string
	var inBlockMath bool
	var blockMathBuffer []string

	// Remove HTML comments (like <!-- _class: lead -->) but keep them for class parsing
	filteredLines := []string{}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Keep _class directives for parsing, but don't render them
		if strings.HasPrefix(trimmed, "<!--") && strings.Contains(trimmed, "_class:") {
			continue // Skip class directive lines
		}
		if !strings.HasPrefix(trimmed, "<!--") || !strings.HasSuffix(trimmed, "-->") {
			filteredLines = append(filteredLines, line)
		}
	}
	lines = filteredLines

	flushCodeBlock := func() {
		if !inCodeBlock {
			return
		}
		// Close any pending table before emitting code block
		if inTable && len(tableRows) > 0 {
			htmlLines = append(htmlLines, processTableRows(tableRows))
			tableRows = []string{}
			inTable = false
		}
		codeContent := html.EscapeString(strings.Join(codeBuffer, "\n"))
		classAttr := ""
		if codeLang != "" {
			classAttr = fmt.Sprintf(` class="language-%s"`, html.EscapeString(codeLang))
		}
		htmlLines = append(htmlLines, fmt.Sprintf("<pre><code%s>%s</code></pre>", classAttr, codeContent))
		// reset
		inCodeBlock = false
		codeIsIndented = false
		codeLang = ""
		codeBuffer = nil
	}

	flushBlockMath := func() {
		if !inBlockMath {
			return
		}
		// Join all lines in the block math buffer
		mathContent := strings.Join(blockMathBuffer, "\n")
		// Remove the opening and closing $$$ markers
		mathContent = strings.TrimPrefix(mathContent, "$$$")
		mathContent = strings.TrimSuffix(mathContent, "$$$")
		mathContent = strings.TrimSpace(mathContent)
		// Decode HTML entities if any
		mathContent = html.UnescapeString(mathContent)
		htmlLines = append(htmlLines, fmt.Sprintf(`<div class="katex-block">%s</div>`, mathContent))
		// reset
		inBlockMath = false
		blockMathBuffer = nil
	}

	for i, line := range lines {
		line = strings.TrimRight(line, " \t")
		trimmed := strings.TrimSpace(line)

		// Helpers for list management
		closeAllLists := func() {
			for len(listStack) > 0 {
				// Close the deepest list
				if listStack[len(listStack)-1].ordered {
					htmlLines = append(htmlLines, "</ol>")
				} else {
					htmlLines = append(htmlLines, "</ul>")
				}
				listStack = listStack[:len(listStack)-1]
			}
		}
		openList := func(ordered bool) {
			if ordered {
				htmlLines = append(htmlLines, "<ol>")
			} else {
				htmlLines = append(htmlLines, "<ul>")
			}
		}
		adjustListStack := func(targetLevel int, ordered bool) {
			// Current level is len(listStack)
			currentLevel := len(listStack)
			// If switching type at same level, close one and reopen as needed
			if currentLevel > 0 && currentLevel == targetLevel && listStack[currentLevel-1].ordered != ordered {
				// Close current list
				if listStack[currentLevel-1].ordered {
					htmlLines = append(htmlLines, "</ol>")
				} else {
					htmlLines = append(htmlLines, "</ul>")
				}
				listStack = listStack[:currentLevel-1]
				// Open new list of desired type
				openList(ordered)
				listStack = append(listStack, listCtx{ordered: ordered, level: targetLevel})
				return
			}
			// If need to go deeper
			for currentLevel < targetLevel {
				openList(ordered)
				listStack = append(listStack, listCtx{ordered: ordered, level: currentLevel + 1})
				currentLevel++
			}
			// If need to go shallower
			for currentLevel > targetLevel {
				if listStack[currentLevel-1].ordered {
					htmlLines = append(htmlLines, "</ol>")
				} else {
					htmlLines = append(htmlLines, "</ul>")
				}
				listStack = listStack[:currentLevel-1]
				currentLevel--
			}
			// If exact level but different type handled above, otherwise ensure stack top matches type
			if currentLevel > 0 && listStack[currentLevel-1].ordered != ordered {
				// Replace top-level type
				if listStack[currentLevel-1].ordered {
					htmlLines = append(htmlLines, "</ol>")
				} else {
					htmlLines = append(htmlLines, "</ul>")
				}
				listStack = listStack[:currentLevel-1]
				openList(ordered)
				listStack = append(listStack, listCtx{ordered: ordered, level: currentLevel})
			}
		}
		getIndentLevel := func(s string) int {
			// Count leading spaces as 1, tabs as 4 spaces, then map to 2-space levels
			count := 0
			for _, r := range s {
				if r == ' ' {
					count++
				} else if r == '\t' {
					count += 4
				} else {
					break
				}
			}
			if count <= 0 {
				return 0
			}
			return count / 2 // 2-space granularity for nesting
		}
		isUnorderedMarker := func(s string) bool {
			return strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "+ ")
		}
		isOrderedMarker := func(s string) bool {
			return regexp.MustCompile(`^\d+\.\s+`).MatchString(s)
		}

		// Block math start/end $$$ ... $$$
		if strings.Contains(trimmed, "$$$") {
			if inBlockMath {
				// Closing block math - add this line and flush
				blockMathBuffer = append(blockMathBuffer, line)
				flushBlockMath()
				continue
			} else {
				// Opening block math
				closeAllLists()
				if inTable && len(tableRows) > 0 {
					htmlLines = append(htmlLines, processTableRows(tableRows))
					tableRows = []string{}
					inTable = false
				}
				// Check if it's a single-line block math ($$$...$$$)
				if strings.Count(trimmed, "$$$") >= 2 {
					// Single line block math
					mathContent := trimmed
					// Extract content between $$$ markers
					mathRegex := regexp.MustCompile(`\$\$\$([\s\S]*?)\$\$\$`)
					matches := mathRegex.FindStringSubmatch(mathContent)
					if len(matches) > 1 {
						mathContent = strings.TrimSpace(matches[1])
						mathContent = html.UnescapeString(mathContent)
						htmlLines = append(htmlLines, fmt.Sprintf(`<div class="katex-block">%s</div>`, mathContent))
					}
					continue
				} else {
					// Multi-line block math - start collecting
					inBlockMath = true
					blockMathBuffer = []string{line}
					continue
				}
			}
		}

		// If inside block math, accumulate lines
		if inBlockMath {
			blockMathBuffer = append(blockMathBuffer, line)
			continue
		}

		// Fenced code block start/end ```lang ... ```
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence := trimmed[:3]
			if inCodeBlock && !codeIsIndented {
				// Closing fence
				if strings.HasPrefix(trimmed, fence) {
					flushCodeBlock()
					continue
				}
			} else if !inCodeBlock {
				// Opening fence
				// Close lists before starting code
				closeAllLists()
				if inTable && len(tableRows) > 0 {
					htmlLines = append(htmlLines, processTableRows(tableRows))
					tableRows = []string{}
					inTable = false
				}
				inCodeBlock = true
				codeIsIndented = false
				codeLang = strings.TrimSpace(strings.TrimPrefix(trimmed, fence))
				codeBuffer = nil
				continue
			}
		}

		// Indented code block (4 spaces or tab)
		// ただし、インデントの後ろがリスト記号（-, *, +, 1. など）の場合はコードではなくリストとして扱う
		if !inCodeBlock && (strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t")) {
			leftAfterIndent := strings.TrimLeft(line, " \t")
			if isUnorderedMarker(leftAfterIndent) || isOrderedMarker(leftAfterIndent) {
				// これはネストしたリスト。コードブロック処理は行わずこの後のリスト分岐に任せる
			} else {
				// Close lists/tables before starting
				closeAllLists()
				if inTable && len(tableRows) > 0 {
					htmlLines = append(htmlLines, processTableRows(tableRows))
					tableRows = []string{}
					inTable = false
				}
				inCodeBlock = true
				codeIsIndented = true
				codeLang = ""
				codeBuffer = []string{strings.TrimPrefix(strings.TrimPrefix(line, "    "), "\t")}
				continue
			}
		} else if inCodeBlock && codeIsIndented {
			// Continue indented code while lines keep indentation; stop on blank line or non-indented
			if trimmed == "" {
				flushCodeBlock()
				htmlLines = append(htmlLines, "<br>")
				continue
			}
			if strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "\t") {
				codeBuffer = append(codeBuffer, strings.TrimPrefix(strings.TrimPrefix(line, "    "), "\t"))
				continue
			}
			// Non-indented -> flush and continue normal processing of this line
			flushCodeBlock()
		}

		// Headers
		if strings.HasPrefix(line, "# ") {
			closeAllLists()
			text := strings.TrimPrefix(line, "# ")
			htmlLines = append(htmlLines, fmt.Sprintf("<h1>%s</h1>", processInlineFormatting(text)))
		} else if strings.HasPrefix(line, "## ") {
			closeAllLists()
			text := strings.TrimPrefix(line, "## ")
			htmlLines = append(htmlLines, fmt.Sprintf("<h2>%s</h2>", processInlineFormatting(text)))
		} else if strings.HasPrefix(line, "### ") {
			closeAllLists()
			text := strings.TrimPrefix(line, "### ")
			htmlLines = append(htmlLines, fmt.Sprintf("<h3>%s</h3>", processInlineFormatting(text)))
		} else if strings.HasPrefix(line, "#### ") {
			closeAllLists()
			text := strings.TrimPrefix(line, "#### ")
			htmlLines = append(htmlLines, fmt.Sprintf("<h4>%s</h4>", processInlineFormatting(text)))
		} else if strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|") {
			// Table row
			if !inTable {
				inTable = true
			}
			tableRows = append(tableRows, line)
		} else if isUnorderedMarker(strings.TrimLeft(line, " \t")) || isOrderedMarker(strings.TrimLeft(line, " \t")) {
			// List item with potential indentation
			// Determine indent level based on original line (before full trim)
			leftTrimmed := strings.TrimLeft(line, " \t")
			level := getIndentLevel(line)
			isOrdered := isOrderedMarker(leftTrimmed)
			adjustListStack(level+1, isOrdered) // levels start at 1 for top-level list
			var itemText string
			if isOrdered {
				itemText = regexp.MustCompile(`^\d+\.\s+`).ReplaceAllString(leftTrimmed, "")
			} else {
				itemText = leftTrimmed
				itemText = strings.TrimPrefix(itemText, "- ")
				itemText = strings.TrimPrefix(itemText, "* ")
				itemText = strings.TrimPrefix(itemText, "+ ")
			}
			htmlLines = append(htmlLines, fmt.Sprintf("<li>%s</li>", processInlineFormatting(itemText)))
		} else if trimmed == "" {
			// Empty line
			// Close any open lists on blank line to avoid accidental continuation
			closeAllLists()
			if inTable && len(tableRows) > 0 {
				// Process accumulated table rows
				htmlLines = append(htmlLines, processTableRows(tableRows))
				tableRows = []string{}
				inTable = false
			}
			// If inside fenced code block, keep empty line
			if inCodeBlock && !codeIsIndented {
				codeBuffer = append(codeBuffer, "")
				continue
			}
			htmlLines = append(htmlLines, "<br>")
		} else {
			// Regular paragraph
			closeAllLists()
			if inTable && len(tableRows) > 0 {
				htmlLines = append(htmlLines, processTableRows(tableRows))
				tableRows = []string{}
				inTable = false
			}
			// If inside fenced code, accumulate raw line
			if inCodeBlock && !codeIsIndented {
				codeBuffer = append(codeBuffer, line)
				continue
			}
			// Process inline formatting
			processed := processInlineFormatting(line)
			htmlLines = append(htmlLines, fmt.Sprintf("<p>%s</p>", processed))
		}

		// Close lists at end of content
		if i == len(lines)-1 {
			// Close pending fenced/indented code
			if inCodeBlock {
				flushCodeBlock()
			}
			// Close pending block math
			if inBlockMath {
				flushBlockMath()
			}
			closeAllLists()
			if inTable && len(tableRows) > 0 {
				htmlLines = append(htmlLines, processTableRows(tableRows))
			}
		}
	}

	htmlResult := strings.Join(htmlLines, "\n")

	// Process block math: $$$...$$$ (can span multiple lines)
	// Exclude code blocks
	blockMathRegex := regexp.MustCompile(`\$\$\$([\s\S]*?)\$\$\$`)
	htmlResult = blockMathRegex.ReplaceAllStringFunc(htmlResult, func(match string) string {
		// Check if this match is inside a <pre><code> block
		// Find the position of this match in the HTML
		matchIndex := strings.Index(htmlResult, match)
		if matchIndex == -1 {
			return match
		}
		// Check if we're inside a code block
		beforeMatch := htmlResult[:matchIndex]
		// Count unclosed <pre> and <code> tags
		preOpen := strings.Count(beforeMatch, "<pre>")
		preClose := strings.Count(beforeMatch, "</pre>")
		codeOpen := strings.Count(beforeMatch, "<code>")
		codeClose := strings.Count(beforeMatch, "</code>")
		if preOpen > preClose || codeOpen > codeClose {
			// Inside code block, don't process
			return match
		}
		// Extract math content
		mathContent := blockMathRegex.FindStringSubmatch(match)[1]
		// Decode HTML entities if any
		mathContent = html.UnescapeString(mathContent)
		// Trim whitespace
		mathContent = strings.TrimSpace(mathContent)
		return fmt.Sprintf(`<div class="katex-block">%s</div>`, mathContent)
	})

	// Remove <p> tags that wrap block math divs
	// Pattern: <p><div class="katex-block">...</div></p>
	pWrappedBlockMathRegex := regexp.MustCompile(`<p>\s*<div class="katex-block">([\s\S]*?)</div>\s*</p>`)
	htmlResult = pWrappedBlockMathRegex.ReplaceAllString(htmlResult, `<div class="katex-block">$1</div>`)

	return htmlResult
}

// processTableRows processes markdown table rows into HTML table
func processTableRows(rows []string) string {
	if len(rows) == 0 {
		return ""
	}

	var htmlRows []string
	var headerCellCount int = 0

	for _, row := range rows {
		// Remove leading/trailing |
		row = strings.Trim(row, "|")
		cells := strings.Split(row, "|")

		// Clean up cells
		var cleanCells []string
		for _, cell := range cells {
			cell = strings.TrimSpace(cell)
			cleanCells = append(cleanCells, cell)
		}

		// Check if this is a separator row (contains only dashes and colons)
		isSeparator := true
		for _, cell := range cleanCells {
			cellTrimmed := strings.TrimSpace(cell)
			if cellTrimmed != "" && !regexp.MustCompile(`^:?-+:?$`).MatchString(cellTrimmed) {
				isSeparator = false
				break
			}
		}

		if isSeparator {
			continue // Skip separator rows
		}

		// Determine if this is a header row (first non-separator row)
		var tag string
		if len(htmlRows) == 0 {
			tag = "th"
			headerCellCount = len(cleanCells)
		} else {
			tag = "td"
		}

		// Ensure all rows have the same number of cells as the header
		// Pad with empty cells if necessary
		for len(cleanCells) < headerCellCount {
			cleanCells = append(cleanCells, "")
		}
		// Truncate if too many cells (shouldn't happen, but handle gracefully)
		if len(cleanCells) > headerCellCount && headerCellCount > 0 {
			cleanCells = cleanCells[:headerCellCount]
		}

		var cellHTMLs []string
		for _, cell := range cleanCells {
			// Always include cells, even if empty
			cellContent := cell
			if cellContent == "" {
				cellContent = "&nbsp;" // Non-breaking space for empty cells
			} else {
				cellContent = processInlineFormatting(cell)
			}
			cellHTMLs = append(cellHTMLs, fmt.Sprintf("<%s>%s</%s>", tag, cellContent, tag))
		}

		if len(cellHTMLs) > 0 {
			htmlRows = append(htmlRows, fmt.Sprintf("<tr>%s</tr>", strings.Join(cellHTMLs, "")))
		}
	}

	if len(htmlRows) == 0 {
		return ""
	}
	return fmt.Sprintf("<table>%s</table>", strings.Join(htmlRows, ""))
}

// processInlineFormatting processes markdown inline formatting
func processInlineFormatting(text string) string {
	// Process markdown patterns first, using placeholders to avoid conflicts
	// Bold: **text**
	text = regexp.MustCompile(`\*\*(.*?)\*\*`).ReplaceAllStringFunc(text, func(match string) string {
		content := regexp.MustCompile(`\*\*(.*?)\*\*`).FindStringSubmatch(match)[1]
		// Escape content but use placeholder for tags
		escapedContent := html.EscapeString(content)
		return fmt.Sprintf("<STRONG_PLACEHOLDER>%s</STRONG_PLACEHOLDER>", escapedContent)
	})

	// Italic: *text* (but not **text**)
	// Process after bold, so remaining single * are italic
	// Match single asterisk that's not adjacent to another asterisk
	text = regexp.MustCompile(`([^*]|^)\*([^*]+?)\*([^*]|$)`).ReplaceAllStringFunc(text, func(match string) string {
		// After bold processing, if we find ** it means it wasn't processed (shouldn't happen)
		// But we check anyway to be safe
		if strings.Contains(match, "**") {
			return match
		}
		// Extract the italic content (group 2)
		submatch := regexp.MustCompile(`([^*]|^)\*([^*]+?)\*([^*]|$)`).FindStringSubmatch(match)
		if len(submatch) >= 4 {
			prefix := submatch[1]
			content := submatch[2]
			suffix := submatch[3]
			escapedContent := html.EscapeString(content)
			return fmt.Sprintf("%s<EM_PLACEHOLDER>%s</EM_PLACEHOLDER>%s", prefix, escapedContent, suffix)
		}
		return match
	})

	// Code: `text`
	text = regexp.MustCompile("`([^`]+)`").ReplaceAllStringFunc(text, func(match string) string {
		content := regexp.MustCompile("`([^`]+)`").FindStringSubmatch(match)[1]
		escapedContent := html.EscapeString(content)
		return fmt.Sprintf("<CODE_PLACEHOLDER>%s</CODE_PLACEHOLDER>", escapedContent)
	})

	// Images: ![alt](path "title") or ![alt](path)
	// Process before HTML escaping to preserve image tags
	// Match pattern: ![alt](path) or ![alt](path "title")
	// Note: path can contain spaces, but title is in quotes
	imageRegex := regexp.MustCompile(`!\[([^\]]*)\]\(([^)]+)\)`)
	text = imageRegex.ReplaceAllStringFunc(text, func(match string) string {
		parts := imageRegex.FindStringSubmatch(match)
		if len(parts) < 3 {
			return match
		}
		alt := parts[1]
		pathAndTitle := parts[2] // This may contain path "title"

		// Parse path and title from pathAndTitle
		// Format: path or path "title"
		var path, title string
		titleMatch := regexp.MustCompile(`^(.+?)\s+"([^"]+)"$`).FindStringSubmatch(pathAndTitle)
		if len(titleMatch) >= 3 {
			// Has title
			path = titleMatch[1]
			title = titleMatch[2]
		} else {
			// No title, entire string is path
			path = pathAndTitle
			title = ""
		}

		// Escape alt and title for HTML attributes, but keep path unescaped for URL processing
		escapedAlt := html.EscapeString(alt)
		escapedTitle := html.EscapeString(title)

		// Build img tag with unescaped path (will be properly escaped in the final output)
		// Use special markers to protect the path from HTML escaping
		return fmt.Sprintf("<IMAGE_PLACEHOLDER>__IMAGE_PATH_START__%s__IMAGE_PATH_END____IMAGE_ALT_START__%s__IMAGE_ALT_END____IMAGE_TITLE_START__%s__IMAGE_TITLE_END__</IMAGE_PLACEHOLDER>", path, escapedAlt, escapedTitle)
	})

	// Inline math: $...$ (but not inside code blocks)
	// Process after code to avoid matching $ inside code
	// Use special markers to protect LaTeX content from HTML escaping
	text = regexp.MustCompile(`\$([^$\n]+?)\$`).ReplaceAllStringFunc(text, func(match string) string {
		content := regexp.MustCompile(`\$([^$\n]+?)\$`).FindStringSubmatch(match)[1]
		// Use markers to protect LaTeX content
		return fmt.Sprintf("<KATEX_INLINE_PLACEHOLDER>__KATEX_CONTENT_START__%s__KATEX_CONTENT_END__</KATEX_INLINE_PLACEHOLDER>", content)
	})

	// Escape remaining HTML (this will escape placeholders too, but we'll fix that)
	text = html.EscapeString(text)

	// Replace escaped placeholders with actual HTML tags
	text = strings.ReplaceAll(text, "&lt;STRONG_PLACEHOLDER&gt;", "<strong>")
	text = strings.ReplaceAll(text, "&lt;/STRONG_PLACEHOLDER&gt;", "</strong>")
	text = strings.ReplaceAll(text, "&lt;EM_PLACEHOLDER&gt;", "<em>")
	text = strings.ReplaceAll(text, "&lt;/EM_PLACEHOLDER&gt;", "</em>")
	text = strings.ReplaceAll(text, "&lt;CODE_PLACEHOLDER&gt;", "<code>")
	text = strings.ReplaceAll(text, "&lt;/CODE_PLACEHOLDER&gt;", "</code>")

	// Replace image placeholder (image tags should not be escaped)
	// First, handle escaped placeholders
	text = strings.ReplaceAll(text, "&lt;IMAGE_PLACEHOLDER&gt;", "<IMAGE_PLACEHOLDER>")
	text = strings.ReplaceAll(text, "&lt;/IMAGE_PLACEHOLDER&gt;", "</IMAGE_PLACEHOLDER>")

	// Extract and restore image tags
	imagePlaceholderRegex := regexp.MustCompile(`<IMAGE_PLACEHOLDER>(.*?)</IMAGE_PLACEHOLDER>`)
	text = imagePlaceholderRegex.ReplaceAllStringFunc(text, func(match string) string {
		content := imagePlaceholderRegex.FindStringSubmatch(match)[1]
		// Decode HTML entities in the content
		content = strings.ReplaceAll(content, "&amp;", "&")
		content = strings.ReplaceAll(content, "&lt;", "<")
		content = strings.ReplaceAll(content, "&gt;", ">")
		content = strings.ReplaceAll(content, "&quot;", "\"")
		content = strings.ReplaceAll(content, "&#39;", "'")

		// Extract path, alt, and title from markers
		pathMatch := regexp.MustCompile(`__IMAGE_PATH_START__(.*?)__IMAGE_PATH_END__`).FindStringSubmatch(content)
		altMatch := regexp.MustCompile(`__IMAGE_ALT_START__(.*?)__IMAGE_ALT_END__`).FindStringSubmatch(content)
		titleMatch := regexp.MustCompile(`__IMAGE_TITLE_START__(.*?)__IMAGE_TITLE_END__`).FindStringSubmatch(content)

		if len(pathMatch) < 2 || len(altMatch) < 2 {
			// Log error for debugging
			fmt.Printf("[Marp] Failed to parse image placeholder: content=%q\n", content)
			return match // Return original if parsing fails
		}

		imgPath := pathMatch[1]
		imgAlt := altMatch[1]
		imgTitle := ""
		if len(titleMatch) >= 2 && titleMatch[1] != "" {
			imgTitle = titleMatch[1]
		}

		// Decode any HTML entities that might have been encoded in the path
		// This handles cases where the path was partially escaped
		imgPath = strings.ReplaceAll(imgPath, "&amp;", "&")
		imgPath = strings.ReplaceAll(imgPath, "&lt;", "<")
		imgPath = strings.ReplaceAll(imgPath, "&gt;", ">")
		imgPath = strings.ReplaceAll(imgPath, "&quot;", "\"")
		imgPath = strings.ReplaceAll(imgPath, "&#34;", "\"")
		imgPath = strings.ReplaceAll(imgPath, "&#39;", "'")
		imgPath = strings.ReplaceAll(imgPath, "&amp;#34;", "\"")
		imgPath = strings.ReplaceAll(imgPath, "&amp;#39;", "'")

		// Remove any title that might have been included in the path
		// Pattern: path "title" -> path
		titleInPathRegex := regexp.MustCompile(`^(.+?)\s+"[^"]*"$`)
		if titleMatch := titleInPathRegex.FindStringSubmatch(imgPath); len(titleMatch) >= 2 {
			imgPath = titleMatch[1]
		}

		// Escape path for HTML attribute (but keep it as a URL path)
		escapedPath := html.EscapeString(imgPath)

		// Build img tag
		imgTag := fmt.Sprintf(`<img src="%s" alt="%s"`, escapedPath, imgAlt)
		if imgTitle != "" {
			imgTag += fmt.Sprintf(` title="%s"`, imgTitle)
		}
		imgTag += `>`

		fmt.Printf("[Marp] Generated img tag: %s (path: %s, title: %s)\n", imgTag, imgPath, imgTitle)

		return imgTag
	})

	// Replace KaTeX placeholder and decode the content
	katexInlineRegex := regexp.MustCompile(`&lt;KATEX_INLINE_PLACEHOLDER&gt;(.*?)&lt;/KATEX_INLINE_PLACEHOLDER&gt;`)
	text = katexInlineRegex.ReplaceAllStringFunc(text, func(match string) string {
		content := katexInlineRegex.FindStringSubmatch(match)[1]
		// Decode HTML entities in the content (between markers)
		content = strings.ReplaceAll(content, "__KATEX_CONTENT_START__", "")
		content = strings.ReplaceAll(content, "__KATEX_CONTENT_END__", "")
		// Decode HTML entities
		content = strings.ReplaceAll(content, "&amp;", "&")
		content = strings.ReplaceAll(content, "&lt;", "<")
		content = strings.ReplaceAll(content, "&gt;", ">")
		content = strings.ReplaceAll(content, "&quot;", "\"")
		content = strings.ReplaceAll(content, "&#39;", "'")
		return fmt.Sprintf(`<span class="katex-inline">%s</span>`, content)
	})

	return text
}

// getMarpThemeCSS returns CSS variables for the specified Marp theme
func getMarpThemeCSS(theme string) string {
	switch theme {
	case "gaia":
		return `
			--color-background: #f8f8f8;
			--color-foreground: #1a1a1a;
			--color-highlight: #4285f4;
			--color-sub-background: #e8f0fe;
		`
	case "uncover":
		return `
			--color-background: #1a1a1a;
			--color-foreground: #ffffff;
			--color-highlight: #ff6b6b;
			--color-sub-background: #2d2d2d;
		`
	case "lead":
		return `
			--color-background: #ffffff;
			--color-foreground: #2c3e50;
			--color-highlight: #3498db;
			--color-sub-background: #ecf0f1;
		`
	default: // "default"
		return `
			--color-background: #ffffff;
			--color-foreground: #363636;
			--color-highlight: #96368f;
			--color-sub-background: #e3cafa;
		`
	}
}

// RenderMarpHTML generates complete HTML for Marp presentation
func RenderMarpHTML(slides []string, title string, header string, footer string, paginate bool, aspectRatio string, theme string) string {
	// Default aspect ratio
	if aspectRatio == "" {
		aspectRatio = "16:9"
	}

	// Default theme
	if theme == "" {
		theme = "default"
	}

	// Get theme CSS variables
	themeCSS := getMarpThemeCSS(theme)

	// Calculate aspect ratio for CSS
	var aspectRatioCSS string
	var aspectRatioWidth string
	var aspectRatioHeight string
	var baseWidth, baseHeight float64 // Base size (long edge = 1920px)

	switch aspectRatio {
	case "16:9":
		aspectRatioCSS = "16 / 9"
		aspectRatioWidth = "16"
		aspectRatioHeight = "9"
		baseWidth = 1920
		baseHeight = 1080
	case "4:3":
		aspectRatioCSS = "4 / 3"
		aspectRatioWidth = "4"
		aspectRatioHeight = "3"
		baseWidth = 1920
		baseHeight = 1440
	case "1:1":
		aspectRatioCSS = "1 / 1"
		aspectRatioWidth = "1"
		aspectRatioHeight = "1"
		baseWidth = 1920
		baseHeight = 1920
	case "21:9":
		aspectRatioCSS = "21 / 9"
		aspectRatioWidth = "21"
		aspectRatioHeight = "9"
		baseWidth = 1920
		baseHeight = 823 // 1920 * 9 / 21
	default:
		// Custom ratio (e.g., "3:2")
		parts := strings.Split(aspectRatio, ":")
		if len(parts) == 2 {
			aspectRatioCSS = fmt.Sprintf("%s / %s", parts[0], parts[1])
			aspectRatioWidth = parts[0]
			aspectRatioHeight = parts[1]
			// Parse ratio to calculate base size
			var w, h float64
			fmt.Sscanf(parts[0], "%f", &w)
			fmt.Sscanf(parts[1], "%f", &h)
			if w > h {
				// Landscape: width is long edge
				baseWidth = 1920
				baseHeight = 1920 * h / w
			} else {
				// Portrait: height is long edge
				baseHeight = 1920
				baseWidth = 1920 * w / h
			}
		} else {
			aspectRatioCSS = "16 / 9" // Default fallback
			aspectRatioWidth = "16"
			aspectRatioHeight = "9"
			baseWidth = 1920
			baseHeight = 1080
		}
	}
	var slideHTMLs []string

	for i, slide := range slides {
		slideHTML := RenderSlideHTML(slide)
		slideNum := i + 1
		headerHTML := ""
		footerHTML := ""

		// Parse slide content for class directives (like <!-- _class: lead invert -->)
		slideClasses := parseSlideClasses(slide)

		if header != "" {
			headerHTML = fmt.Sprintf(`<header class="slide-header">%s</header>`, html.EscapeString(header))
		}
		if footer != "" {
			footerHTML = fmt.Sprintf(`<div class="slide-footer">%s</div>`, html.EscapeString(footer))
		}

		// Add paginate class if pagination is enabled
		if paginate {
			slideClasses = append(slideClasses, "paginate")
		}

		classAttr := ""
		if len(slideClasses) > 0 {
			classAttr = fmt.Sprintf(` %s`, strings.Join(slideClasses, " "))
		}

		dataAttrs := fmt.Sprintf(`data-slide-index="%d" data-slide-current="%d" data-slide-total="%d"`, i, slideNum, len(slides))

		slideHTMLs = append(slideHTMLs, fmt.Sprintf(`
			<section class="slide%s" %s>
				%s
				<div class="slide-content">
					%s
				</div>
				%s
			</section>`, classAttr, dataAttrs, headerHTML, slideHTML, footerHTML))
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="ja">
<head>
	<meta charset="UTF-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>%s</title>
	<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Noto+Sans+JP:wght@400;700&display=swap">
	<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Source+Code+Pro&display=swap">
	<link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.css">
	<style>
		* {
			margin: 0;
			padding: 0;
			box-sizing: border-box;
		}
		
		:root {
			%s
			--slide-base-width: %.0fpx;
			--slide-base-height: %.0fpx;
			--slide-scale: 1;
		}
		
		body {
			font-family: 'Noto Sans JP', sans-serif;
			background: var(--color-background);
			color: var(--color-foreground);
			overflow: hidden;
		}
		
		#presentation {
			width: 100%%;
			height: 100%%;
			display: flex;
			align-items: center;
			justify-content: center;
			position: relative;
			overflow: hidden;
		}
		
		.slide-container {
			position: relative;
			aspect-ratio: %s;
			width: min(100%%, calc(100vh * (%s / %s)));
			height: min(100%%, calc(100vw / (%s / %s)));
			max-width: 100%%;
			max-height: 100%%;
			/* Base font size based on reference size (1920px long edge) */
			font-size: calc(var(--slide-base-width) * 0.02 * var(--slide-scale));
		}
		
		.slide {
			display: none;
			width: 100%%;
			height: 100%%;
			background: var(--color-background);
			background-repeat: no-repeat;
			background-position: right bottom;
			background-size: 25%%;
			padding-top: 5%%;
			padding-left: 3%%;
			padding-right: 3%%;
			padding-bottom: 5%%;
			overflow-y: auto;
			position: absolute;
			top: 0;
			left: 0;
		}
		
		.slide.active {
			display: block;
		}
		
		.slide.paginate::after {
			position: absolute;
			bottom: 2%%;
			right: 3%%;
			font-weight: 963;
			content: attr(data-slide-current) '/' attr(data-slide-total);
			color: var(--color-foreground);
			font-size: clamp(0.7em, calc(var(--slide-base-width) * 0.02 * var(--slide-scale)), 0.9em);
		}
		
		.slide-content {
			max-width: 1200px;
			width: 100%%;
			padding-bottom: 5%%;
		}
		
		.slide-header {
			position: absolute;
			top: 2%%;
			left: 0;
			right: 0;
			text-align: center;
			font-size: clamp(0.8em, calc(var(--slide-base-width) * 0.025 * var(--slide-scale)), 1em);
			font-weight: 963;
			background-color: var(--color-highlight);
			color: var(--color-background);
			padding: 1%% 3%%;
			z-index: 10;
		}
		
		.slide-footer {
			position: absolute;
			bottom: 2%%;
			left: 0;
			right: 0;
			text-align: center;
			font-size: clamp(0.7em, calc(var(--slide-base-width) * 0.02 * var(--slide-scale)), 0.9em);
			color: var(--color-foreground);
			padding: 0 3%%;
			z-index: 10;
			pointer-events: none;
		}
		
		.slide-page-number {
			position: absolute;
			bottom: 2%%;
			right: 3%%;
			font-size: 0.9em;
			color: var(--color-foreground);
		}
		
		.slide h1 {
			font-size: clamp(2em, calc(var(--slide-base-width) * 0.08 * var(--slide-scale)), 3.5em);
			margin-bottom: 0.5em;
			color: var(--color-foreground);
		}
		
		.slide h2 {
			font-size: clamp(1.5em, calc(var(--slide-base-width) * 0.06 * var(--slide-scale)), 2.5em);
			margin-bottom: 0.5em;
			color: var(--color-foreground);
		}
		
		.slide h3 {
			font-size: clamp(1.2em, calc(var(--slide-base-width) * 0.05 * var(--slide-scale)), 2em);
			margin-bottom: 0.5em;
			color: var(--color-foreground);
		}
		
		.slide h4 {
			font-size: clamp(1em, calc(var(--slide-base-width) * 0.04 * var(--slide-scale)), 1.5em);
			margin-bottom: 0.5em;
			color: var(--color-foreground);
		}
		
		.slide h5 {
			font-size: clamp(0.9em, calc(var(--slide-base-width) * 0.03 * var(--slide-scale)), 1.2em);
			margin-bottom: 0.5em;
			color: var(--color-foreground);
		}
		
		.slide p {
			font-size: clamp(1em, calc(var(--slide-base-width) * 0.035 * var(--slide-scale)), 1.5em);
			line-height: 1.6;
			margin: 1em 0;
			color: var(--color-foreground);
		}
		
		.slide ul, .slide ol {
			text-align: left;
			margin: 1em 0;
			padding-left: 2em;
		}
		
		.slide ul ul {
			font-size: clamp(0.7em, calc(var(--slide-base-width) * 0.02 * var(--slide-scale)), 0.8em);
		}
		
		.slide li {
			font-size: clamp(0.9em, calc(var(--slide-base-width) * 0.03 * var(--slide-scale)), 1.3em);
			line-height: 1.6;
			margin: 0.5em 0;
			color: var(--color-foreground);
		}
		
		.slide table {
			width: 80%%;
			border-collapse: collapse;
			margin: 1em 0;
			text-align: left;
			position: relative;
			z-index: 1;
		}
		
		.slide th, .slide td {
			padding: 0.5em 1em;
			border: 1px solid rgba(0, 0, 0, 0.1);
			font-size: clamp(0.8em, calc(var(--slide-base-width) * 0.025 * var(--slide-scale)), 1.2em);
			position: relative;
		}
		
		.slide th {
			background-color: var(--color-highlight) !important;
			color: #fff !important;
			font-weight: 600;
		}
		
		.slide td {
			background-color: #f8f8f8;
			color: var(--color-foreground);
		}
		
		.slide code {
			font-family: 'Source Code Pro', monospace;
			font-size: clamp(0.7em, calc(var(--slide-base-width) * 0.02 * var(--slide-scale)), 0.8em);
			white-space: pre;
			background: rgba(0, 0, 0, 0.05);
			padding: 0.2em 0.4em;
			border-radius: 3px;
		}
		
		/* Images */
		.slide img {
			max-width: 100%%;
			max-height: 100%%;
			width: auto;
			height: auto;
			object-fit: contain;
			display: block;
			margin: 0.5em auto;
		}
		
		/* Image size classes - based on slide size (100%% = full slide) */
		.slide img[style*="width:"] {
			/* Custom width styles are preserved */
		}
		
		/* Default image size is 80%% of slide width for better readability */
		.slide img:not([style*="width:"]) {
			max-width: 80%%;
		}
		
		/* Block code */
		.slide pre {
			background: #0b1021;
			color: #e6e6e6;
			border-radius: 8px;
			padding: 1em;
			overflow: auto;
			line-height: 1.5;
			margin: 1em 0;
		}
		.slide pre code {
			background: transparent;
			padding: 0;
			font-size: clamp(0.75em, calc(var(--slide-base-width) * 0.02 * var(--slide-scale)), 0.95em);
		}
		
		.slide strong {
			color: var(--color-foreground);
			font-weight: 700;
		}
		
		.slide em {
			font-style: italic;
		}
		
		/* Lead class styles - all content centered */
		.slide.lead .slide-content {
			text-align: center;
			margin: 0 auto;
			height: 100%%;
			display: flex;
			flex-direction: column;
			justify-content: center;
			align-items: center;
		}
		
		/* Remove vertical padding for lead to allow perfect centering */
		.slide.lead {
			padding-top: 0;
			padding-bottom: 0;
		}
		
		.slide.lead h1 {
			color: var(--color-highlight);
			text-align: center;
		}
		
		.slide.lead h1 strong {
			-webkit-text-stroke: 1px var(--color-highlight);
		}
		
		.slide.lead h2 {
			color: var(--color-highlight);
			text-align: center;
		}
		
		.slide.lead h2 strong {
			-webkit-text-stroke: 1px var(--color-highlight);
		}
		
		.slide.lead h3 {
			color: var(--color-highlight);
			text-align: center;
		}
		
		.slide.lead h3 strong {
			-webkit-text-stroke: 1px var(--color-highlight);
		}
		
		.slide.lead h4 {
			color: var(--color-highlight);
			text-align: center;
		}
		
		.slide.lead h4 strong {
			-webkit-text-stroke: 1px var(--color-highlight);
		}
		
		.slide.lead h5 {
			color: var(--color-highlight);
			text-align: center;
		}
		
		.slide.lead h5 strong {
			-webkit-text-stroke: 1px var(--color-highlight);
		}
		
		.slide.lead p {
			text-align: center;
		}
		
		.slide.lead ul, .slide.lead ol {
			text-align: center;
			display: inline-block;
			margin: 1em auto;
		}
		
		.slide.lead table {
			margin: 1em auto;
			text-align: center;
		}
		
		.slide.lead th, .slide.lead td {
			text-align: center;
		}
		
		/* Invert class styles */
		.slide.invert {
			background-color: var(--color-highlight);
			color: var(--color-background);
		}
		
		.slide.invert h1 {
			background-color: var(--color-highlight);
			color: var(--color-background);
		}
		
		.slide.invert h1 strong {
			-webkit-text-stroke: 1px var(--color-background);
		}
		
		.slide.invert h2 {
			color: var(--color-background);
		}
		
		.slide.invert h2 strong {
			-webkit-text-stroke: 1px var(--color-background);
		}
		
		.slide.invert h3 {
			color: var(--color-background);
		}
		
		.slide.invert h3 strong {
			-webkit-text-stroke: 1px var(--color-background);
		}
		
		.slide.invert h4 {
			color: var(--color-background);
		}
		
		.slide.invert h4 strong {
			-webkit-text-stroke: 1px var(--color-background);
		}
		
		.slide.invert h5 {
			color: var(--color-background);
		}
		
		.slide.invert h5 strong {
			-webkit-text-stroke: 1px var(--color-background);
		}
		
		.slide.invert p {
			color: var(--color-background);
		}
		
		.slide.invert li {
			color: var(--color-background);
		}
		
		/* Noheader class styles */
		.slide.noheader {
			background-color: var(--color-sub-background);
		}
		
		blockquote::before {
			content: "";
		}
		
		blockquote::after {
			content: "";
		}
		
		blockquote {
			position: absolute;
			max-width: 90%%;
			border-top: 0.1em solid #999;
			bottom: 3%%;
			font-size: 50%%;
		}
		
		.navigation {
			position: fixed;
			bottom: 2%%;
			left: 50%%;
			transform: translateX(-50%%);
			display: flex;
			gap: 0.7em;
			z-index: 1000;
		}
		
		.nav-button {
			background: #7c3aed;
			border: 1px solid #7c3aed;
			color: #ffffff;
			padding: 0.7em 1.4em;
			border-radius: 5px;
			cursor: pointer;
			font-size: 0.875em;
			transition: background 0.2s, opacity 0.2s;
		}
		
		.nav-button:hover {
			background: #6d28d9;
			border-color: #6d28d9;
		}
		
		.nav-button:disabled {
			opacity: 0.5;
			cursor: not-allowed;
			background: #9ca3af;
			border-color: #9ca3af;
		}
		
		.slide-counter {
			position: fixed;
			top: 2%%;
			right: 2%%;
			color: rgba(255, 255, 255, 0.6);
			font-size: 0.875em;
			z-index: 1000;
		}
		
		/* Keyboard navigation hint */
		.keyboard-hint {
			position: fixed;
			bottom: 5%%;
			left: 50%%;
			transform: translateX(-50%%);
			color: rgba(255, 255, 255, 0.4);
			font-size: 0.75em;
			z-index: 1000;
		}
	</style>
</head>
<body>
	<div id="presentation">
		<div class="slide-container">
			%s
		</div>
	</div>
	
	<div class="slide-counter">
		<span id="current-slide">1</span> / <span id="total-slides">%d</span>
	</div>
	
	<div class="navigation">
		<button class="nav-button" id="prev-btn" onclick="previousSlide()">← 前へ</button>
		<button class="nav-button" id="next-btn" onclick="nextSlide()">次へ →</button>
	</div>
	
	<div class="keyboard-hint">
		キーボード: ← → キーでスライドを切り替え
	</div>
	
	<script>
		let currentSlide = 0;
		const slides = document.querySelectorAll('.slide');
		const totalSlides = slides.length;
		
		document.getElementById('total-slides').textContent = totalSlides;
		
		function showSlide(index) {
			if (index < 0 || index >= totalSlides) return;
			
			slides.forEach(slide => slide.classList.remove('active'));
			slides[index].classList.add('active');
			currentSlide = index;
			
			document.getElementById('current-slide').textContent = index + 1;
			
			// Update navigation buttons
			document.getElementById('prev-btn').disabled = (index === 0);
			document.getElementById('next-btn').disabled = (index === totalSlides - 1);
		}
		
		function nextSlide() {
			if (currentSlide < totalSlides - 1) {
				showSlide(currentSlide + 1);
			}
		}
		
		function previousSlide() {
			if (currentSlide > 0) {
				showSlide(currentSlide - 1);
			}
		}
		
		// Keyboard navigation
		document.addEventListener('keydown', (e) => {
			if (e.key === 'ArrowRight' || e.key === ' ') {
				e.preventDefault();
				nextSlide();
			} else if (e.key === 'ArrowLeft') {
				e.preventDefault();
				previousSlide();
			}
		});
		
		// Update font scale based on actual slide container size
		function updateFontScale() {
			const container = document.querySelector('.slide-container');
			if (container) {
				const rect = container.getBoundingClientRect();
				const baseWidth = parseFloat(getComputedStyle(document.documentElement)
					.getPropertyValue('--slide-base-width').replace('px', ''));
				if (baseWidth > 0) {
					const scale = rect.width / baseWidth;
					document.documentElement.style.setProperty('--slide-scale', scale);
				}
			}
		}
		
		// Wrap showSlide to update scale
		const originalShowSlide = showSlide;
		showSlide = function(index) {
			originalShowSlide(index);
			setTimeout(updateFontScale, 0);
		}
		
		// Update scale on load and resize
		updateFontScale();
		window.addEventListener('resize', updateFontScale);
		
		// Initialize first slide
		showSlide(0);
	</script>
	<script defer src="https://cdn.jsdelivr.net/npm/katex@latest/dist/katex.min.js"></script>
	<script>
		// Helper function to decode HTML entities
		function decodeHtmlEntities(text) {
			const textarea = document.createElement('textarea');
			textarea.innerHTML = text;
			return textarea.value;
		}
		
		// Initialize KaTeX for math rendering after KaTeX is loaded
		window.addEventListener('DOMContentLoaded', function() {
			// Wait for KaTeX to be available
			function initKaTeX() {
				if (typeof katex === 'undefined') {
					setTimeout(initKaTeX, 50);
					return;
				}
				
				// Render inline math
				document.querySelectorAll('.katex-inline').forEach(function(el) {
					try {
						// Use innerHTML to get the content, then decode HTML entities
						const rawContent = el.innerHTML.trim();
						const math = decodeHtmlEntities(rawContent);
						katex.render(math, el, {
							throwOnError: false,
							displayMode: false
						});
					} catch (e) {
						console.error('KaTeX inline rendering error:', e);
					}
				});
				
				// Render block math
				document.querySelectorAll('.katex-block').forEach(function(el) {
					try {
						// Use innerHTML to get the content, then decode HTML entities
						const rawContent = el.innerHTML.trim();
						const math = decodeHtmlEntities(rawContent);
						katex.render(math, el, {
							throwOnError: false,
							displayMode: true
						});
					} catch (e) {
						console.error('KaTeX block rendering error:', e);
					}
				});
			}
			initKaTeX();
		});
	</script>
</body>
</html>`, html.EscapeString(title), themeCSS, baseWidth, baseHeight, aspectRatioCSS, aspectRatioWidth, aspectRatioHeight, aspectRatioWidth, aspectRatioHeight, strings.Join(slideHTMLs, "\n"), len(slides))

	return html
}
