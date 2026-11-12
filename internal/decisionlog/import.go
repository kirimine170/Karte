package decisionlog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"karte/internal/canonical"
	"karte/internal/ephyoutbox"
)

var (
	dailyFilename   = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})_[^\\/]+$`)
	proposedDecided = []string{"決定", "決定・本人の方向", "決定事項"}
	proposedOpen    = []string{"未決", "依頼", "案", "検討", "未確認", "案・未確認", "案・検討中"}
)

const pendingDirectory = ".mdsys/ephy/outbox/pending"
const maxSummaryBytes = 1024 * 1024

type ImportResult struct {
	Added     int `json:"added"`
	Merged    int `json:"merged"`
	Unchanged int `json:"unchanged"`
}

func ReadSummary(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxSummaryBytes {
		return nil, fmt.Errorf("summary must be a regular file of at most 1 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSummaryBytes+1))
	if len(data) > maxSummaryBytes {
		return nil, fmt.Errorf("summary exceeds 1 MiB")
	}
	return data, err
}

// ExtractSummary reads explicitly marked entries and decision requests from the
// daily meeting template. Imported material is always private.
func (settings Settings) ExtractSummary(filename string, data []byte, sourceURL string, now time.Time) ([]Record, error) {
	if len(data) > maxSummaryBytes || !utf8.Valid(data) || !ValidSourceURL(sourceURL) {
		return nil, fmt.Errorf("summary requires an HTTPS source URL and UTF-8 text of at most 1 MiB")
	}
	name := filepath.Base(filename)
	for _, ext := range []string{".txt", ".md"} {
		if strings.HasSuffix(strings.ToLower(name), ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	match := dailyFilename.FindStringSubmatch(name)
	if match == nil {
		return nil, fmt.Errorf("summary name must be YYYY-MM-DD_bot or YYYY-MM-DD_まとめ")
	}
	if _, err := time.Parse(time.DateOnly, match[1]); err != nil {
		return nil, fmt.Errorf("invalid summary date")
	}

	businesses := settings.businessLabels()
	reviewer := settings.Decider
	if reviewer == "" {
		reviewer = "local-human"
	}
	source := Source{URL: sourceURL, SHA256: canonical.Hash(data)}
	seen := map[string]bool{}
	result := []Record{}
	business, section, sectionStatus := "overall", "", ""
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimLeft(strings.TrimSpace(line), "\ufeff")
		heading := strings.HasPrefix(line, "#")
		line = strings.TrimSpace(strings.TrimLeft(line, "#"))
		if strings.HasPrefix(line, "出典・参照") {
			break
		}
		if strings.HasPrefix(line, "初回追加") || len(line) >= 3 && line[0] >= 'A' && line[0] <= 'Z' && line[1:3] == ". " {
			section, business, sectionStatus = "", "overall", ""
			continue
		}
		switch {
		case strings.HasPrefix(line, "①"):
			section, business, sectionStatus = "activity", "overall", ""
			continue
		case strings.HasPrefix(line, "②"):
			section, business, sectionStatus = "plan", "overall", ""
			continue
		case strings.HasPrefix(line, "③"), strings.HasSuffix(line, "決めてほしいこと"):
			section, business, sectionStatus = "request", "overall", ""
			continue
		case strings.HasPrefix(line, "④"):
			section, business, sectionStatus = "delegation", "overall", ""
			continue
		}
		if line == "" {
			continue
		}
		bullet := strings.HasPrefix(line, "・") || strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") || strings.HasPrefix(line, "＊")
		line = strings.TrimSpace(strings.TrimLeft(line, "・-＊*"))
		itemBusiness, status, marked := business, "", false
		businessHeading := false
		for strings.HasPrefix(line, "【") {
			label, rest, ok := strings.Cut(strings.TrimPrefix(line, "【"), "】")
			if !ok {
				break
			}
			if id := businessID(businesses, label); id != "" {
				itemBusiness, businessHeading = id, true
			}
			if state := statusForLabel(label); state != "" {
				status, marked = state, true
			}
			line = strings.TrimSpace(rest)
		}
		if label, rest, ok := strings.Cut(line, "："); ok && statusForLabel(label) != "" {
			status, marked, line = statusForLabel(label), true, strings.TrimSpace(rest)
		}
		if state := statusForLabel(line); state != "" && (heading || !bullet) {
			sectionStatus = state
			continue
		}
		if !bullet && status != "" && line == "" {
			business, sectionStatus = itemBusiness, status
			continue
		}
		if heading {
			if id := businessID(businesses, line); id != "" {
				itemBusiness, businessHeading = id, true
			}
		}
		if !bullet && businessHeading && status == "" {
			business, sectionStatus = itemBusiness, ""
			continue
		}
		if heading || line == "" || strings.HasPrefix(line, "今回の作成を止める必須判断はなし") {
			continue
		}
		if !marked {
			switch {
			case bullet && sectionStatus != "":
				status = sectionStatus
			case bullet && (section == "request" || section == "delegation"):
				status = Open
			case bullet && section == "activity" && (strings.Contains(line, "運用を依頼された") || strings.Contains(line, "よう依頼された")):
				status = Decided
			case bullet && itemBusiness != "overall" && (strings.Contains(line, "：構想・検討") || strings.Contains(line, "：検討案")):
				status = Open
			}
		}
		if status == "" || isNoDecision(line) {
			continue
		}
		if itemBusiness == "overall" {
			if identified := businessMentioned(businesses, line); identified != "" {
				itemBusiness = identified
			}
		}
		confidence := "unverified"
		if status == Decided {
			confidence = "known"
		} else if strings.Contains(line, "構想") || strings.Contains(line, "検討案") {
			confidence = "hypothesis"
		}
		record := Record{
			Version: Version, ID: decisionID(itemBusiness, line), Date: match[1],
			Business: itemBusiness, BusinessLabel: businesses[itemBusiness], Content: line, Status: status, Decider: reviewer,
			Sources: []Source{source}, Confidence: confidence, Visibility: Private,
			UpdatedAt: now.UTC().Format(time.RFC3339Nano),
		}
		if settings.requiresPrivate(record) {
			record.PublicationRestriction = WorkspacePrivate
		}
		if seen[record.ID] {
			continue
		}
		if err := record.Validate(); err != nil {
			return nil, err
		}
		seen[record.ID] = true
		result = append(result, record)
	}
	return result, nil
}

func statusForLabel(label string) string {
	for _, known := range proposedDecided {
		if label == known {
			return Decided
		}
	}
	for _, known := range proposedOpen {
		if label == known {
			return Open
		}
	}
	return ""
}

func isNoDecision(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{"なし", "特になし", "該当なし", "未決事項なし", "決定事項なし", "決定なし"} {
		if strings.TrimRight(text, "．。.!！") == prefix {
			return true
		}
	}
	return false
}

func businessID(businesses map[string]string, label string) string {
	for id, name := range businesses {
		if normalizeName(label) == normalizeName(name) || normalizeName(label) == id {
			return id
		}
	}
	return ""
}

func businessMentioned(businesses map[string]string, text string) string {
	text = normalizeName(text)
	found := ""
	for id, label := range businesses {
		if id != "overall" && strings.Contains(text, normalizeName(label)) {
			if found != "" {
				return ""
			}
			found = id
		}
	}
	return found
}

func decisionID(business, content string) string {
	normalized := strings.ToLower(norm.NFKC.String(strings.Join(strings.Fields(content), " ")))
	normalized = strings.TrimRight(normalized, "。．.")
	return IDPrefix + canonical.Hash([]byte(business+"\n"+normalized))
}

func Proposal(record Record) (ephyoutbox.Proposal, error) {
	if err := record.Validate(); err != nil {
		return ephyoutbox.Proposal{}, err
	}
	encoded, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return ephyoutbox.Proposal{}, err
	}
	provenance := make([]ephyoutbox.SourceRef, 0, len(record.Sources))
	for _, source := range record.Sources {
		provenance = append(provenance, ephyoutbox.SourceRef{Type: "meeting-summary", Reference: source.URL, SHA256: source.SHA256})
	}
	const reason = "日次サマリの決定候補"
	confidence := 1.0
	proposal := ephyoutbox.Proposal{
		SchemaVersion: ephyoutbox.SchemaVersion, CandidateID: record.ID, Operation: "create",
		ProposedFrontmatter: map[string]any{"title": "決定ログの候補", "tags": []string{"decision-log"}},
		ProposedBody:        string(encoded), SourceRefs: provenance, Sensitivity: Sensitivity(record.Visibility),
		Placement: ephyoutbox.PlacementHint{
			Project: record.Business, Kind: "decision", YearMonth: record.Date[:7],
			Confidence: &confidence, PreferredFilename: record.ID + ".md",
			Candidates: []ephyoutbox.PlacementCandidate{{Project: record.Business, Kind: "decision", Confidence: &confidence, Reason: reason}},
		},
		CreatedAt: record.UpdatedAt,
	}
	return proposal, proposal.Validate()
}

func DecodeProposal(proposal ephyoutbox.Proposal) (Record, error) {
	record, err := Decode([]byte(proposal.ProposedBody))
	if err != nil {
		return Record{}, err
	}
	if proposal.Operation != "create" || proposal.Placement.Kind != "decision" ||
		proposal.Placement.Project != record.Business || proposal.Sensitivity != Sensitivity(record.Visibility) ||
		proposal.CandidateID != record.ID || record.ID != decisionID(record.Business, record.Content) ||
		(record.Status != Open && record.Status != Decided) {
		return Record{}, fmt.Errorf("invalid decision proposal")
	}
	if len(proposal.SourceRefs) != len(record.Sources) {
		return Record{}, fmt.Errorf("decision proposal sources do not match")
	}
	for i, source := range record.Sources {
		if proposal.SourceRefs[i] != (ephyoutbox.SourceRef{Type: "meeting-summary", Reference: source.URL, SHA256: source.SHA256}) {
			return Record{}, fmt.Errorf("decision proposal sources do not match")
		}
	}
	return record, nil
}

func Sensitivity(visibility string) string {
	if visibility == Public {
		return "public"
	}
	return "internal"
}

func Queue(w *canonical.Writer, store *ephyoutbox.Store, records []Record) (ImportResult, error) {
	var result ImportResult
	entries, err := load(w)
	if err != nil {
		return result, err
	}
	for _, record := range records {
		if entry, ok := entries[record.ID]; ok {
			if entry.Visibility != Private {
				result.Unchanged++
				continue
			}
			merged := MergeSources(entry.stored.Sources, record.Sources)
			if len(merged) == len(entry.stored.Sources) {
				result.Unchanged++
				continue
			}
			entry.stored.Sources, entry.stored.UpdatedAt = merged, record.UpdatedAt
			next, err := write(w, entry.stored, &entry.SHA256)
			if err != nil {
				return result, err
			}
			entries[record.ID] = next
			result.Merged++
			continue
		}
		receipt, err := store.ReadReceipt(record.ID)
		if err != nil {
			return result, err
		}
		if receipt != nil {
			result.Unchanged++
			continue
		}
		pendingPath := pendingDirectory + "/" + record.ID + ".json"
		var expected *string
		oldBytes, err := w.ReadLimit(pendingPath, 2*1024*1024)
		switch {
		case err == nil:
			oldProposal, err := ephyoutbox.DecodeProposal(oldBytes)
			if err != nil {
				return result, err
			}
			old, err := DecodeProposal(oldProposal)
			if err != nil {
				return result, err
			}
			sources := MergeSources(old.Sources, record.Sources)
			if len(sources) == len(old.Sources) {
				result.Unchanged++
				continue
			}
			old.Sources = sources
			record = old
			hash := canonical.Hash(oldBytes)
			expected = &hash
		case !errors.Is(err, os.ErrNotExist):
			return result, err
		}
		proposal, err := Proposal(record)
		if err != nil {
			return result, err
		}
		data, err := json.MarshalIndent(proposal, "", "  ")
		if err != nil {
			return result, err
		}
		if err := w.WriteCAS(pendingPath, expected, data, 0600); err != nil {
			return result, err
		}
		if expected == nil {
			result.Added++
		} else {
			result.Merged++
		}
	}
	return result, nil
}
