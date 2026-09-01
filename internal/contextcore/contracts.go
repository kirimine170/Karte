package contextcore

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const ProtocolVersion = "1.0"

var (
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	projectPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

var sensitivityRank = map[string]int{
	"public":       0,
	"internal":     1,
	"confidential": 2,
	"restricted":   3,
}

type Actor struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type Scope struct {
	Projects           []string `json:"projects"`
	Tags               []string `json:"tags"`
	SensitivityCeiling string   `json:"sensitivity_ceiling"`
}

type SearchQuery struct {
	Text string `json:"text"`
	TopK int    `json:"top_k"`
}

type Request struct {
	ProtocolVersion string       `json:"protocol_version"`
	RequestID       string       `json:"request_id"`
	Operation       string       `json:"operation"`
	Actor           Actor        `json:"actor"`
	Scope           Scope        `json:"scope"`
	Query           *SearchQuery `json:"query"`
	DocID           *string      `json:"doc_id"`
	CreatedAt       string       `json:"created_at"`
}

type ProvenanceRef struct {
	Type      string `json:"type"`
	Reference string `json:"reference"`
	SHA256    string `json:"sha256"`
}

type SearchResult struct {
	DocID        string          `json:"doc_id"`
	Title        string          `json:"title"`
	Project      string          `json:"project"`
	Kind         string          `json:"kind"`
	Tags         []string        `json:"tags"`
	Sensitivity  string          `json:"sensitivity"`
	RelativePath string          `json:"relative_path"`
	UpdatedAt    string          `json:"updated_at"`
	SHA256       string          `json:"sha256"`
	Snippet      string          `json:"snippet"`
	Score        float64         `json:"score"`
	Provenance   []ProvenanceRef `json:"provenance"`
}

type Document struct {
	DocID        string          `json:"doc_id"`
	Title        string          `json:"title"`
	Project      string          `json:"project"`
	Kind         string          `json:"kind"`
	Tags         []string        `json:"tags"`
	Sensitivity  string          `json:"sensitivity"`
	RelativePath string          `json:"relative_path"`
	UpdatedAt    string          `json:"updated_at"`
	SHA256       string          `json:"sha256"`
	Body         string          `json:"body"`
	Provenance   []ProvenanceRef `json:"provenance"`
}

type Diagnostic struct {
	Code  string `json:"code"`
	Count int    `json:"count"`
}

type ProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type Response struct {
	ProtocolVersion string         `json:"protocol_version"`
	RequestID       string         `json:"request_id"`
	RequestSHA256   string         `json:"request_sha256"`
	Operation       string         `json:"operation"`
	Status          string         `json:"status"`
	Results         []SearchResult `json:"results"`
	Document        *Document      `json:"document"`
	Diagnostics     []Diagnostic   `json:"diagnostics"`
	Error           *ProtocolError `json:"error"`
	ProcessedAt     string         `json:"processed_at"`
}

type ValidationError struct {
	Code    string
	Message string
}

func (err *ValidationError) Error() string {
	return err.Message
}

func validationError(code, message string) error {
	return &ValidationError{Code: code, Message: message}
}

func (request Request) Validate() error {
	if request.ProtocolVersion != ProtocolVersion {
		return validationError("unsupported_protocol", "unsupported protocol_version")
	}
	if !requestIDPattern.MatchString(request.RequestID) {
		return validationError("invalid_request_id", "invalid request_id")
	}
	if request.Actor.Type != "ephy" && request.Actor.Type != "human" && request.Actor.Type != "tool" {
		return validationError("invalid_actor", "unsupported actor type")
	}
	if strings.TrimSpace(request.Actor.ID) == "" || utf8.RuneCountInString(request.Actor.ID) > 128 {
		return validationError("invalid_actor", "actor id is required")
	}
	if _, ok := sensitivityRank[request.Scope.SensitivityCeiling]; !ok {
		return validationError("invalid_scope", "unsupported sensitivity ceiling")
	}
	if len(request.Scope.Projects) > 64 || len(request.Scope.Tags) > 64 {
		return validationError("invalid_scope", "scope contains too many filters")
	}
	for _, project := range request.Scope.Projects {
		if project != "*" && !projectPattern.MatchString(project) {
			return validationError("invalid_scope", "scope project is invalid")
		}
	}
	for _, tag := range request.Scope.Tags {
		if strings.TrimSpace(tag) == "" || utf8.RuneCountInString(tag) > 128 {
			return validationError("invalid_scope", "scope tag is invalid")
		}
	}
	if _, err := time.Parse(time.RFC3339, request.CreatedAt); err != nil {
		return validationError("invalid_created_at", "created_at must be RFC3339")
	}

	switch request.Operation {
	case "search":
		if request.Query == nil || strings.TrimSpace(request.Query.Text) == "" || utf8.RuneCountInString(request.Query.Text) > 2048 {
			return validationError("invalid_search", "search requires a bounded query")
		}
		if request.Query.TopK < 1 || request.Query.TopK > 20 {
			return validationError("invalid_search", "top_k must be between 1 and 20")
		}
		if request.DocID != nil {
			return validationError("invalid_search", "search cannot include doc_id")
		}
	case "read":
		if request.Query != nil || request.DocID == nil || strings.TrimSpace(*request.DocID) == "" || utf8.RuneCountInString(*request.DocID) > 256 {
			return validationError("invalid_read", "read requires only doc_id")
		}
	default:
		return validationError("unsupported_operation", "unsupported context operation")
	}
	return nil
}

func sensitivityAtMost(value, ceiling string) bool {
	valueRank, valueOK := sensitivityRank[value]
	ceilingRank, ceilingOK := sensitivityRank[ceiling]
	return valueOK && ceilingOK && valueRank <= ceilingRank
}

func minSensitivity(left, right string) (string, error) {
	leftRank, leftOK := sensitivityRank[left]
	rightRank, rightOK := sensitivityRank[right]
	if !leftOK || !rightOK {
		return "", fmt.Errorf("unsupported sensitivity")
	}
	if leftRank <= rightRank {
		return left, nil
	}
	return right, nil
}
