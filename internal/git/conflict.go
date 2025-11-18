package git

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ConflictSeverity indicates how severe a conflict is
type ConflictSeverity int

const (
	ConflictNone           ConflictSeverity = iota
	ConflictAutoResolvable                  // Can be automatically merged
	ConflictWarning                         // Needs user attention but can be auto-resolved with caution
	ConflictCritical                        // Requires manual resolution
)

// ConflictInfo contains information about a file conflict
type ConflictInfo struct {
	Path          string           `json:"path"`
	LocalHash     string           `json:"local_hash"`  // Current working directory hash
	RemoteHash    string           `json:"remote_hash"` // Remote/HEAD hash
	BaseHash      string           `json:"base_hash"`   // Common ancestor hash
	LocalContent  string           `json:"local_content"`
	RemoteContent string           `json:"remote_content"`
	BaseContent   string           `json:"base_content"`
	Timestamp     string           `json:"timestamp"`
	Severity      ConflictSeverity `json:"severity"`
}

// DetectConflict checks if there's a conflict for a given file
func DetectConflict(vcs *VCS, repoPath, relativePath string) (*ConflictInfo, error) {
	if vcs == nil || vcs.Repository() == nil {
		return nil, fmt.Errorf("repository not initialized")
	}

	repo := vcs.Repository()

	// Get current file content from working directory
	absPath := filepath.Join(repoPath, relativePath)
	localContent, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read local file: %v", err)
	}
	localHash := CalculateHash(string(localContent))

	// Get HEAD commit
	ref, err := repo.Head()
	if err != nil {
		// No HEAD means it's a new repository, no conflict possible
		return nil, nil
	}

	headCommit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD commit: %v", err)
	}

	// Get file from HEAD
	tree, err := headCommit.Tree()
	if err != nil {
		return nil, fmt.Errorf("failed to get tree: %v", err)
	}

	// Try to get file from HEAD
	file, err := tree.File(relativePath)
	if err != nil {
		// File doesn't exist in HEAD, this is a new file - no conflict
		return nil, nil
	}

	remoteContent, err := file.Contents()
	if err != nil {
		return nil, fmt.Errorf("failed to read remote file: %v", err)
	}
	remoteHash := CalculateHash(remoteContent)

	// If hashes match, no conflict
	if localHash == remoteHash {
		return nil, nil
	}

	// Try to find common ancestor for 3-way merge
	// For now, use HEAD as base (simplified - full 3-way merge would need merge base calculation)
	baseContent := remoteContent
	baseHash := remoteHash

	// Assess conflict severity
	severity := assessConflictSeverity(baseContent, string(localContent), remoteContent)

	conflict := &ConflictInfo{
		Path:          relativePath,
		LocalHash:     localHash,
		RemoteHash:    remoteHash,
		BaseHash:      baseHash,
		LocalContent:  string(localContent),
		RemoteContent: remoteContent,
		BaseContent:   baseContent,
		Severity:      severity,
	}

	return conflict, nil
}

// assessConflictSeverity determines how severe a conflict is
func assessConflictSeverity(base, local, remote string) ConflictSeverity {
	// Simple heuristic: if local and remote are both different from base
	// and don't overlap significantly, it's a critical conflict

	// Count line differences
	localDiff := countDifferentLines(base, local)
	remoteDiff := countDifferentLines(base, remote)
	overlap := countCommonLines(local, remote)

	// If changes are small and mostly non-overlapping, auto-resolvable
	if localDiff < 10 && remoteDiff < 10 && overlap > 0 {
		return ConflictAutoResolvable
	}

	// If one side has no changes, it's auto-resolvable
	if localDiff == 0 || remoteDiff == 0 {
		return ConflictAutoResolvable
	}

	// If changes overlap significantly, warning level
	if overlap > (localDiff+remoteDiff)/2 {
		return ConflictWarning
	}

	// Otherwise, critical
	return ConflictCritical
}

// countDifferentLines counts how many lines differ between two strings
func countDifferentLines(a, b string) int {
	aLines := splitLines(a)
	bLines := splitLines(b)

	diff := 0
	maxLen := len(aLines)
	if len(bLines) > maxLen {
		maxLen = len(bLines)
	}

	for i := 0; i < maxLen; i++ {
		aLine := ""
		bLine := ""
		if i < len(aLines) {
			aLine = aLines[i]
		}
		if i < len(bLines) {
			bLine = bLines[i]
		}
		if aLine != bLine {
			diff++
		}
	}

	return diff
}

// countCommonLines counts common lines between two strings
func countCommonLines(a, b string) int {
	aLines := splitLines(a)
	bLines := splitLines(b)

	common := 0
	seen := make(map[string]bool)
	for _, line := range aLines {
		seen[line] = true
	}

	for _, line := range bLines {
		if seen[line] {
			common++
		}
	}

	return common
}

// splitLines splits a string into lines (handles both \n and \r\n)
func splitLines(s string) []string {
	// Remove \r and split by \n
	s = strings.ReplaceAll(s, "\r", "")
	return strings.Split(s, "\n")
}
