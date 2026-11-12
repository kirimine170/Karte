package decisionlog

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Version          = "0.1"
	IDPrefix         = "decision-"
	Directory        = "content/.decision-log"
	Decided          = "decided"
	Open             = "open"
	Withdrawn        = "withdrawn"
	Superseded       = "superseded"
	Private          = "private"
	Public           = "public"
	WorkspacePrivate = "workspace-private"
)

var idPattern = regexp.MustCompile(`^decision-[a-f0-9]{64}$`)

var businessPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type Source struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

type Record struct {
	Version                string   `json:"schema_version"`
	ID                     string   `json:"decision_id"`
	Date                   string   `json:"date"`
	Business               string   `json:"business"`
	BusinessLabel          string   `json:"business_label,omitempty"`
	Content                string   `json:"content"`
	Status                 string   `json:"status"`
	Decider                string   `json:"decider"`
	Sources                []Source `json:"sources"`
	Supersedes             string   `json:"supersedes"`
	ReplacedBy             string   `json:"replaced_by"`
	Confidence             string   `json:"confidence"`
	Visibility             string   `json:"visibility"`
	PublicationRestriction string   `json:"publication_restriction,omitempty"`
	UpdatedAt              string   `json:"updated_at"`
}

func (r Record) Validate() error {
	if r.Version != Version || !ValidID(r.ID) {
		return fmt.Errorf("invalid decision identity")
	}
	if _, err := time.Parse(time.DateOnly, r.Date); err != nil {
		return fmt.Errorf("decision date must use YYYY-MM-DD")
	}
	if _, err := time.Parse(time.RFC3339, r.UpdatedAt); err != nil {
		return fmt.Errorf("decision updated_at must use RFC3339")
	}
	if !businessPattern.MatchString(r.Business) || (r.BusinessLabel != "" && !validLabel(r.BusinessLabel)) || strings.TrimSpace(r.Content) == "" || utf8.RuneCountInString(r.Content) > 4000 {
		return fmt.Errorf("invalid decision business or content")
	}
	if !validLabel(r.Decider) {
		return fmt.Errorf("invalid decision reviewer")
	}
	switch r.Status {
	case Decided, Open, Withdrawn, Superseded:
	default:
		return fmt.Errorf("invalid decision status")
	}
	if (r.Status == Superseded) != (r.ReplacedBy != "") || (r.Status == Open && r.Supersedes != "") {
		return fmt.Errorf("decision status does not match its replacement links")
	}
	for _, id := range []string{r.Supersedes, r.ReplacedBy} {
		if id != "" && (!ValidID(id) || id == r.ID) {
			return fmt.Errorf("invalid decision replacement link")
		}
	}
	switch r.Confidence {
	case "known", "hypothesis", "unverified":
	default:
		return fmt.Errorf("invalid decision confidence")
	}
	if len(r.Sources) == 0 || len(r.Sources) > 64 {
		return fmt.Errorf("a decision requires 1 to 64 sources")
	}
	seen := make(map[string]bool)
	for _, source := range r.Sources {
		if !ValidSourceURL(source.URL) || !ValidID(IDPrefix+source.SHA256) || seen[source.URL] {
			return fmt.Errorf("invalid or duplicate decision source")
		}
		seen[source.URL] = true
	}
	if r.PublicationRestriction != "" && r.PublicationRestriction != WorkspacePrivate {
		return fmt.Errorf("invalid decision publication restriction")
	}
	switch r.Visibility {
	case Private:
	case Public:
		if r.PublicationRestriction != "" {
			return fmt.Errorf("decision is restricted to the workspace")
		}
	default:
		return fmt.Errorf("invalid decision visibility")
	}
	return nil
}

func ValidID(id string) bool { return idPattern.MatchString(id) }

func ValidSourceURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil
}

func (r Record) DisplayBusiness() string {
	if r.BusinessLabel != "" {
		return r.BusinessLabel
	}
	return r.Business
}

func StatusLabel(status string) string {
	return map[string]string{Open: "未決", Decided: "決定", Withdrawn: "撤回", Superseded: "置換"}[status]
}
