package collab

import (
	"bufio"
	"fmt"
	"strings"
)

// SectionOutline represents a heading-derived range within a markdown document.
type SectionOutline struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Level int    `json:"level"`
	Start int    `json:"start"` // rune index
	End   int    `json:"end"`   // rune index (exclusive)
}

// Outline walks a markdown string and returns heading-based sections.
// If no headings exist、ドキュメント全体を section "000" として返す。
func Outline(content string) []SectionOutline {
	var sections []SectionOutline
	var offsets []int
	var titles []string
	var levels []int

	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Split(bufio.ScanLines)

	runeOffset := 0
	for scanner.Scan() {
		line := scanner.Text()
		level := headingLevel(line)
		if level > 0 {
			title := strings.TrimSpace(line[level+1:])
			if title == "" {
				title = "Untitled section"
			}
			offsets = append(offsets, runeOffset)
			titles = append(titles, title)
			levels = append(levels, level)
		}
		// account for newline (Scanner drops it)
		runeOffset += len([]rune(line)) + 1
	}

	// finalize sections
	if len(offsets) == 0 {
		sections = append(sections, SectionOutline{
			ID:    "000",
			Title: "Document",
			Level: 1,
			Start: 0,
			End:   len([]rune(content)),
		})
		return sections
	}

	totalRunes := len([]rune(content))
	for i := 0; i < len(offsets); i++ {
		start := offsets[i]
		end := totalRunes
		if i+1 < len(offsets) {
			end = offsets[i+1]
		}
		id := fmt.Sprintf("%03d", i)
		sections = append(sections, SectionOutline{
			ID:    id,
			Title: titles[i],
			Level: levels[i],
			Start: start,
			End:   end,
		})
	}

	return sections
}

func headingLevel(line string) int {
	if !strings.HasPrefix(line, "#") {
		return 0
	}
	level := 0
	for _, r := range line {
		if r == '#' {
			level++
		} else {
			break
		}
		if level >= 6 {
			break
		}
	}
	if level == 0 {
		return 0
	}
	if len(line) > level && line[level] == ' ' {
		return level
	}
	return 0
}
