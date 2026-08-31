package ephyoutbox

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"
	"time"
)

const SchemaVersion = "1.0"

var candidateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type SourceRef struct {
	Type      string `json:"type"`
	Reference string `json:"reference"`
	SHA256    string `json:"sha256,omitempty"`
}

type Proposal struct {
	SchemaVersion       string         `json:"schema_version"`
	CandidateID         string         `json:"candidate_id"`
	Operation           string         `json:"operation"`
	TargetDocID         *string        `json:"target_doc_id"`
	TargetRelativePath  string         `json:"target_relative_path"`
	BaseSHA256          *string        `json:"base_sha256"`
	ProposedFrontmatter map[string]any `json:"proposed_frontmatter"`
	ProposedBody        string         `json:"proposed_body"`
	SourceRefs          []SourceRef    `json:"source_refs"`
	Sensitivity         string         `json:"sensitivity"`
	CreatedAt           string         `json:"created_at"`
}

type Receipt struct {
	SchemaVersion   string  `json:"schema_version"`
	CandidateID     string  `json:"candidate_id"`
	Result          string  `json:"result"`
	DocID           *string `json:"doc_id"`
	RelativePath    *string `json:"relative_path"`
	ResultingSHA256 *string `json:"resulting_sha256"`
	ProcessedAt     string  `json:"processed_at"`
	ErrorCode       *string `json:"error_code"`
	Message         *string `json:"message"`
}

type ProposalError struct {
	Filename    string `json:"filename"`
	CandidateID string `json:"candidate_id,omitempty"`
	Code        string `json:"code"`
	Message     string `json:"message"`
}

type ProposalReview struct {
	Proposal        Proposal `json:"proposal"`
	CurrentContent  string   `json:"current_content"`
	ProposedContent string   `json:"proposed_content"`
	Diff            string   `json:"diff"`
	CurrentSHA256   *string  `json:"current_sha256"`
}

type Inbox struct {
	Proposals []ProposalReview `json:"proposals"`
	Errors    []ProposalError  `json:"errors"`
}

type Transaction struct {
	SchemaVersion   string  `json:"schema_version"`
	CandidateID     string  `json:"candidate_id"`
	RelativePath    string  `json:"relative_path"`
	DocID           string  `json:"doc_id"`
	BaseSHA256      *string `json:"base_sha256"`
	PreparedContent string  `json:"prepared_content"`
	State           string  `json:"state"`
	ResultingSHA256 *string `json:"resulting_sha256"`
	StartedAt       string  `json:"started_at"`
}

func (transaction Transaction) Validate() error {
	if transaction.SchemaVersion != SchemaVersion || !candidateIDPattern.MatchString(transaction.CandidateID) {
		return fmt.Errorf("invalid transaction identity")
	}
	if err := ValidateContentPath(transaction.RelativePath); err != nil {
		return err
	}
	if strings.TrimSpace(transaction.DocID) == "" || len(transaction.DocID) > 256 {
		return fmt.Errorf("invalid transaction doc_id")
	}
	if len(transaction.PreparedContent) > 2*1024*1024 {
		return fmt.Errorf("transaction content is too large")
	}
	if _, err := time.Parse(time.RFC3339, transaction.StartedAt); err != nil {
		return fmt.Errorf("started_at must be RFC3339")
	}
	switch transaction.State {
	case "prepared":
		if transaction.ResultingSHA256 != nil {
			return fmt.Errorf("prepared transaction cannot have resulting_sha256")
		}
	case "saved":
		if transaction.ResultingSHA256 == nil || !isSHA256(*transaction.ResultingSHA256) {
			return fmt.Errorf("saved transaction requires resulting_sha256")
		}
	default:
		return fmt.Errorf("invalid transaction state")
	}
	if transaction.BaseSHA256 != nil && !isSHA256(*transaction.BaseSHA256) {
		return fmt.Errorf("invalid transaction base_sha256")
	}
	return nil
}

func DecodeProposal(data []byte) (Proposal, error) {
	var proposal Proposal
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proposal); err != nil {
		return Proposal{}, fmt.Errorf("decode proposal: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Proposal{}, fmt.Errorf("decode proposal: trailing JSON value")
	}
	if err := proposal.Validate(); err != nil {
		return Proposal{}, err
	}
	return proposal, nil
}

func (proposal Proposal) Validate() error {
	if proposal.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported schema_version")
	}
	if !candidateIDPattern.MatchString(proposal.CandidateID) {
		return fmt.Errorf("invalid candidate_id")
	}
	if err := ValidateContentPath(proposal.TargetRelativePath); err != nil {
		return err
	}
	if len(proposal.ProposedFrontmatter) > 64 {
		return fmt.Errorf("proposed_frontmatter has too many fields")
	}
	for key := range proposal.ProposedFrontmatter {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("proposed_frontmatter keys must be non-empty")
		}
	}
	if len(proposal.ProposedBody) > 1024*1024 {
		return fmt.Errorf("proposed_body is too large")
	}
	if len(proposal.SourceRefs) == 0 || len(proposal.SourceRefs) > 64 {
		return fmt.Errorf("source_refs must contain between 1 and 64 entries")
	}
	for _, ref := range proposal.SourceRefs {
		if strings.TrimSpace(ref.Type) == "" || strings.TrimSpace(ref.Reference) == "" {
			return fmt.Errorf("source_refs require type and reference")
		}
		if ref.SHA256 != "" && !isSHA256(ref.SHA256) {
			return fmt.Errorf("source_refs sha256 is invalid")
		}
		if len(ref.Type) > 64 || len(ref.Reference) > 2048 {
			return fmt.Errorf("source_refs field exceeds size limit")
		}
	}
	switch proposal.Sensitivity {
	case "public", "internal", "confidential", "restricted":
	default:
		return fmt.Errorf("unsupported sensitivity")
	}
	if _, err := time.Parse(time.RFC3339, proposal.CreatedAt); err != nil {
		return fmt.Errorf("created_at must be RFC3339")
	}

	switch proposal.Operation {
	case "create":
		if proposal.TargetDocID != nil || proposal.BaseSHA256 != nil {
			return fmt.Errorf("create requires null target_doc_id and base_sha256")
		}
	case "update":
		if proposal.TargetDocID == nil || strings.TrimSpace(*proposal.TargetDocID) == "" {
			return fmt.Errorf("update requires target_doc_id")
		}
		if proposal.BaseSHA256 == nil || !isSHA256(*proposal.BaseSHA256) {
			return fmt.Errorf("update requires base_sha256")
		}
		if value, ok := proposal.ProposedFrontmatter["doc_id"]; ok {
			docID, ok := value.(string)
			if !ok || docID != *proposal.TargetDocID {
				return fmt.Errorf("proposed doc_id must match target_doc_id")
			}
		}
	default:
		return fmt.Errorf("unsupported operation")
	}
	return nil
}

func (receipt Receipt) Validate() error {
	if receipt.SchemaVersion != SchemaVersion || !candidateIDPattern.MatchString(receipt.CandidateID) {
		return fmt.Errorf("invalid receipt identity")
	}
	switch receipt.Result {
	case "accepted", "rejected", "conflict", "invalid":
	default:
		return fmt.Errorf("unsupported receipt result")
	}
	if _, err := time.Parse(time.RFC3339, receipt.ProcessedAt); err != nil {
		return fmt.Errorf("processed_at must be RFC3339")
	}
	if receipt.RelativePath != nil {
		if err := ValidateContentPath(*receipt.RelativePath); err != nil {
			return err
		}
	}
	if receipt.ResultingSHA256 != nil && !isSHA256(*receipt.ResultingSHA256) {
		return fmt.Errorf("invalid resulting_sha256")
	}
	if receipt.ErrorCode != nil && (strings.TrimSpace(*receipt.ErrorCode) == "" || len(*receipt.ErrorCode) > 128) {
		return fmt.Errorf("invalid receipt error_code")
	}
	if receipt.Message != nil && len(*receipt.Message) > 2048 {
		return fmt.Errorf("receipt message is too long")
	}
	if receipt.Result == "accepted" {
		if receipt.DocID == nil || receipt.RelativePath == nil || receipt.ResultingSHA256 == nil || receipt.ErrorCode != nil {
			return fmt.Errorf("accepted receipt is incomplete")
		}
	}
	return nil
}

func ValidateContentPath(value string) error {
	if value == "" || strings.Contains(value, `\`) || strings.HasPrefix(value, "/") {
		return fmt.Errorf("target_relative_path must use a relative forward-slash path")
	}
	cleaned := path.Clean(value)
	if cleaned != value || !strings.HasPrefix(cleaned, "content/") || strings.EqualFold(path.Ext(cleaned), ".md") == false {
		return fmt.Errorf("target_relative_path must name a Markdown file below content")
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("target_relative_path contains traversal")
		}
	}
	return nil
}

func SHA256Bytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func isSHA256(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
