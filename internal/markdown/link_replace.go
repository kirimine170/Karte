package markdown

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ReplaceLink replaces markdown links that point to oldPath with newPath
// Supports wiki links [[title]] and markdown links [text](url)
// Returns the updated content
func ReplaceLink(content, oldPath, newPath string) (string, error) {
	// Normalize paths for comparison
	oldPath = filepath.ToSlash(oldPath)
	newPath = filepath.ToSlash(newPath)
	
	// Remove "content/" prefix if present
	oldPath = strings.TrimPrefix(oldPath, "content/")
	newPath = strings.TrimPrefix(newPath, "content/")
	
	// Remove .md extension for comparison
	oldPathBase := strings.TrimSuffix(oldPath, ".md")
	newPathBase := strings.TrimSuffix(newPath, ".md")
	
	result := content
	
	// Replace wiki links [[title]] or [[title|display]]
	wikiLinkRegex := regexp.MustCompile(`\[\[([^|\]]+)(?:\|([^\]]+))?\]\]`)
	result = wikiLinkRegex.ReplaceAllStringFunc(result, func(match string) string {
		submatches := wikiLinkRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}
		
		title := submatches[1]
		display := submatches[2]
		
		// Check if title matches oldPath (with or without .md)
		titleBase := strings.TrimSuffix(strings.ToLower(title), ".md")
		oldPathBaseLower := strings.ToLower(oldPathBase)
		
		if titleBase == oldPathBaseLower || titleBase == strings.ToLower(oldPath) {
			// Replace with new path
			newTitle := newPathBase
			if display != "" {
				return fmt.Sprintf("[[%s|%s]]", newTitle, display)
			}
			return fmt.Sprintf("[[%s]]", newTitle)
		}
		
		return match
	})
	
	// Replace markdown links [text](url)
	markdownLinkRegex := regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`)
	result = markdownLinkRegex.ReplaceAllStringFunc(result, func(match string) string {
		submatches := markdownLinkRegex.FindStringSubmatch(match)
		if len(submatches) < 3 {
			return match
		}
		
		text := submatches[1]
		url := submatches[2]
		
		// Check if URL is a markdown file link
		if !strings.HasSuffix(strings.ToLower(url), ".md") {
			return match
		}
		
		// Normalize URL for comparison
		urlBase := strings.TrimSuffix(strings.ToLower(url), ".md")
		oldPathBaseLower := strings.ToLower(oldPathBase)
		
		// Check if URL matches oldPath (handle both absolute and relative paths)
		if urlBase == oldPathBaseLower || 
		   urlBase == strings.ToLower(oldPath) ||
		   strings.HasSuffix(urlBase, "/"+oldPathBaseLower) ||
		   strings.HasSuffix(urlBase, "/"+strings.ToLower(oldPath)) {
			// Replace with new path
			return fmt.Sprintf("[%s](%s)", text, newPath)
		}
		
		// Also check relative paths
		urlNormalized := filepath.ToSlash(url)
		if strings.HasSuffix(strings.ToLower(urlNormalized), strings.ToLower(oldPath)) ||
		   strings.HasSuffix(strings.ToLower(urlNormalized), strings.ToLower(oldPathBase)+".md") {
			// Calculate relative path from oldPath to newPath
			// For simplicity, use newPath directly if it's a simple rename
			return fmt.Sprintf("[%s](%s)", text, newPath)
		}
		
		return match
	})
	
	return result, nil
}

// ReplaceLinksInContent replaces all links pointing to oldPath with newPath in the markdown content
// It preserves the frontmatter and only modifies the body
func ReplaceLinksInContent(content, oldPath, newPath string) (string, error) {
	// Split frontmatter and body
	frontMatterRegex := regexp.MustCompile(`(?s)^---\s*\n(.*?)\n---\s*(\n|$)`)
	match := frontMatterRegex.FindStringSubmatch(content)
	
	if match == nil {
		// No frontmatter, replace in entire content
		return ReplaceLink(content, oldPath, newPath)
	}
	
	// Extract frontmatter and body
	frontMatterEnd := len(match[0])
	frontMatter := content[:frontMatterEnd]
	body := content[frontMatterEnd:]
	
	// Replace links only in body
	updatedBody, err := ReplaceLink(body, oldPath, newPath)
	if err != nil {
		return "", err
	}
	
	// Reconstruct content
	return frontMatter + updatedBody, nil
}

