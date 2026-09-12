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
	LocalHash     string           `json:"local_hash"`  // Local candidate content hash
	RemoteHash    string           `json:"remote_hash"` // Competing content hash
	BaseHash      string           `json:"base_hash"`   // Merge-base content hash
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

	// Get current file content from working directory
	absPath := filepath.Join(repoPath, relativePath)
	localContent, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read local file: %v", err)
	}
	localHash := CalculateHash(string(localContent))
	remoteContent, found, err := headFileContent(vcs, relativePath)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	remoteHash := CalculateHash(remoteContent)

	// If hashes match, no conflict
	if localHash == remoteHash {
		return nil, nil
	}

	// Preserve the original DetectConflict semantics for callers that only have
	// the working-tree version available.
	baseContent := remoteContent
	baseHash := remoteHash
	severity := assessConflictSeverity(baseContent, string(localContent), remoteContent)

	return &ConflictInfo{
		Path:          relativePath,
		LocalHash:     localHash,
		RemoteHash:    remoteHash,
		BaseHash:      baseHash,
		LocalContent:  string(localContent),
		RemoteContent: remoteContent,
		BaseContent:   baseContent,
		Severity:      severity,
	}, nil
}

// DetectConflictWithContent checks proposed content against both the current
// working-tree file and the version recorded in HEAD. It does not modify the
// working-tree file.
func DetectConflictWithContent(vcs *VCS, repoPath, relativePath, proposedContent string) (*ConflictInfo, error) {
	if vcs == nil || vcs.Repository() == nil {
		return nil, fmt.Errorf("repository not initialized")
	}

	absPath := filepath.Join(repoPath, relativePath)
	currentContent, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read current file: %v", err)
	}

	baseContent, found, err := headFileContent(vcs, relativePath)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}

	localHash := CalculateHash(proposedContent)
	remoteHash := CalculateHash(string(currentContent))
	baseHash := CalculateHash(baseContent)

	// The disk still matches HEAD, so there is no external edit to merge.
	if remoteHash == baseHash {
		return nil, nil
	}
	// The editor already contains the disk version, so saving is also safe.
	if localHash == remoteHash {
		return nil, nil
	}

	return &ConflictInfo{
		Path:          relativePath,
		LocalHash:     localHash,
		RemoteHash:    remoteHash,
		BaseHash:      baseHash,
		LocalContent:  proposedContent,
		RemoteContent: string(currentContent),
		BaseContent:   baseContent,
		Severity:      assessConflictSeverity(baseContent, proposedContent, string(currentContent)),
	}, nil
}

func headFileContent(vcs *VCS, relativePath string) (string, bool, error) {
	repo := vcs.Repository()

	// Get HEAD commit
	ref, err := repo.Head()
	if err != nil {
		// No HEAD means it's a new repository, no conflict possible
		return "", false, nil
	}

	headCommit, err := repo.CommitObject(ref.Hash())
	if err != nil {
		return "", false, fmt.Errorf("failed to get HEAD commit: %v", err)
	}

	// Get file from HEAD
	tree, err := headCommit.Tree()
	if err != nil {
		return "", false, fmt.Errorf("failed to get tree: %v", err)
	}

	// Try to get file from HEAD
	file, err := tree.File(relativePath)
	if err != nil {
		// File doesn't exist in HEAD, this is a new file - no conflict
		return "", false, nil
	}

	content, err := file.Contents()
	if err != nil {
		return "", false, fmt.Errorf("failed to read HEAD file: %v", err)
	}
	return content, true, nil
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
