package git

import (
	"fmt"
	"strings"
)

// AutoMergeMarkdown attempts to automatically merge markdown content using 3-way merge
func AutoMergeMarkdown(base, local, remote string) (string, ConflictSeverity, error) {
	// Split into paragraphs
	baseParagraphs := splitParagraphs(base)
	localParagraphs := splitParagraphs(local)
	remoteParagraphs := splitParagraphs(remote)

	var merged []string
	var hasConflict bool

	maxLen := len(baseParagraphs)
	if len(localParagraphs) > maxLen {
		maxLen = len(localParagraphs)
	}
	if len(remoteParagraphs) > maxLen {
		maxLen = len(remoteParagraphs)
	}

	for i := 0; i < maxLen; i++ {
		baseP := getParagraph(baseParagraphs, i)
		localP := getParagraph(localParagraphs, i)
		remoteP := getParagraph(remoteParagraphs, i)

		if localP == remoteP && localP != baseP {
			// Both sides made the same change - use it
			merged = append(merged, localP)
		} else if localP != baseP && remoteP == baseP {
			// Only local changed - use local
			merged = append(merged, localP)
		} else if remoteP != baseP && localP == baseP {
			// Only remote changed - use remote
			merged = append(merged, remoteP)
		} else if localP == baseP && remoteP == baseP {
			// No change - keep base
			merged = append(merged, baseP)
		} else if localP == remoteP {
			// Both are the same but different from base - use it
			merged = append(merged, localP)
		} else {
			// Different changes - conflict
			// For now, prefer local but mark as having conflicts
			merged = append(merged, localP)
			hasConflict = true
		}
	}

	result := strings.Join(merged, "\n\n")

	if hasConflict {
		return result, ConflictWarning, fmt.Errorf("automatic merge completed but conflicts detected")
	}

	return result, ConflictAutoResolvable, nil
}

// splitParagraphs splits content into paragraphs (double newline separated)
func splitParagraphs(content string) []string {
	// Normalize line endings
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")

	paragraphs := strings.Split(content, "\n\n")

	// Filter empty paragraphs
	var result []string
	for _, p := range paragraphs {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}

	return result
}

// getParagraph safely gets a paragraph at index, returns empty string if out of bounds
func getParagraph(paragraphs []string, index int) string {
	if index < 0 || index >= len(paragraphs) {
		return ""
	}
	return paragraphs[index]
}
