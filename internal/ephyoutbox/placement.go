package ephyoutbox

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	fm "karte/internal/frontmatter"
)

const collisionDocIDPrefixLength = 8

type PlacementDecision struct {
	DocID        string
	RelativePath string
	Reason       string
	Alternatives []string
}

// ResolvePlacement applies Karte's V1.1 project-first policy. Ephy supplies
// semantic hints，while Karte owns the final canonical relative path.
func ResolvePlacement(dataRoot string, proposal Proposal, docID string) (PlacementDecision, error) {
	if err := proposal.Validate(); err != nil {
		return PlacementDecision{}, err
	}
	if err := proposal.RequirePublishable(); err != nil {
		return PlacementDecision{}, err
	}
	if proposal.Operation == "append" {
		if proposal.TargetRelativePath == nil || proposal.TargetDocID == nil {
			return PlacementDecision{}, fmt.Errorf("append target is incomplete")
		}
		return PlacementDecision{
			DocID:        *proposal.TargetDocID,
			RelativePath: *proposal.TargetRelativePath,
			Reason:       "Existing doc_id and canonical path determine append placement.",
			Alternatives: []string{},
		}, nil
	}
	if docID == "" {
		return PlacementDecision{}, fmt.Errorf("create placement requires a Karte-owned doc_id")
	}

	primary := buildPlacementPath(
		proposal.Placement.Project,
		proposal.Placement.Kind,
		proposal.Placement.YearMonth,
		proposal.Placement.PreferredFilename,
	)
	resolved, err := resolveCollision(dataRoot, primary, docID)
	if err != nil {
		return PlacementDecision{}, err
	}
	alternatives := make([]string, 0, len(proposal.Placement.Candidates)-1)
	seen := map[string]bool{primary: true}
	for _, candidate := range proposal.Placement.Candidates {
		candidatePath := buildPlacementPath(
			candidate.Project,
			candidate.Kind,
			proposal.Placement.YearMonth,
			proposal.Placement.PreferredFilename,
		)
		if seen[candidatePath] {
			continue
		}
		seen[candidatePath] = true
		candidateResolved, candidateErr := resolveCollision(dataRoot, candidatePath, docID)
		if candidateErr != nil {
			return PlacementDecision{}, candidateErr
		}
		alternatives = append(alternatives, candidateResolved)
	}
	reason := fmt.Sprintf(
		"Project-first policy selected project=%s，kind=%s，year_month=%s (confidence %.2f).",
		proposal.Placement.Project,
		proposal.Placement.Kind,
		proposal.Placement.YearMonth,
		*proposal.Placement.Confidence,
	)
	if resolved != primary {
		reason += fmt.Sprintf(" The preferred path already belonged to another doc_id，so Karte added --%s.", docID[:collisionDocIDPrefixLength])
	}
	return PlacementDecision{DocID: docID, RelativePath: resolved, Reason: reason, Alternatives: alternatives}, nil
}

func buildPlacementPath(project, kind, yearMonth, filename string) string {
	return path.Join("content", "projects", project, kind, yearMonth, filename)
}

func resolveCollision(dataRoot, relativePath, docID string) (string, error) {
	if err := ValidateContentPath(relativePath); err != nil {
		return "", err
	}
	if len(docID) < collisionDocIDPrefixLength {
		return "", fmt.Errorf("doc_id is too short for collision handling")
	}
	if available, err := pathAvailableForDocID(dataRoot, relativePath, docID); err != nil {
		return "", err
	} else if available {
		return relativePath, nil
	}
	ext := path.Ext(relativePath)
	stem := strings.TrimSuffix(path.Base(relativePath), ext)
	directory := path.Dir(relativePath)
	for length := collisionDocIDPrefixLength; length <= len(docID); length += 4 {
		if length > len(docID) {
			length = len(docID)
		}
		candidate := path.Join(directory, fmt.Sprintf("%s--%s%s", stem, docID[:length], ext))
		available, err := pathAvailableForDocID(dataRoot, candidate, docID)
		if err != nil {
			return "", err
		}
		if available {
			return candidate, nil
		}
		if length == len(docID) {
			break
		}
	}
	return "", fmt.Errorf("could not allocate a unique path for doc_id")
}

func pathAvailableForDocID(dataRoot, relativePath, docID string) (bool, error) {
	absolutePath := filepath.Join(dataRoot, filepath.FromSlash(relativePath))
	info, err := os.Lstat(absolutePath)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, fmt.Errorf("placement target must be a regular file")
	}
	content, err := os.ReadFile(absolutePath)
	if err != nil {
		return false, err
	}
	return fm.ExtractDocID(string(content)) == docID, nil
}
