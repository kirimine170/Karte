package contextcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Capability string

const (
	CapabilitySearch  Capability = "search"
	CapabilityRead    Capability = "read"
	CapabilityPropose Capability = "propose"
	CapabilityReview  Capability = "review"
	CapabilityExport  Capability = "export"
	CapabilityLearn   Capability = "learn"
)

var (
	capabilitySet = map[Capability]bool{
		CapabilitySearch: true, CapabilityRead: true, CapabilityPropose: true,
		CapabilityReview: true, CapabilityExport: true, CapabilityLearn: true,
	}
	provenanceTypePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

type ActorPolicy struct {
	SensitivityCeiling string       `json:"sensitivity_ceiling"`
	Projects           []string     `json:"projects"`
	AllowedTags        []string     `json:"allowed_tags,omitempty"`
	DeniedTags         []string     `json:"denied_tags,omitempty"`
	ProvenanceTypes    []string     `json:"provenance_types,omitempty"`
	Capabilities       []Capability `json:"capabilities,omitempty"`
}

type Policy struct {
	ProtocolVersion string                 `json:"protocol_version"`
	MasterProjects  []string               `json:"master_projects,omitempty"`
	Actors          map[string]ActorPolicy `json:"actors"`
}

type Resource struct {
	Project         string
	Kind            string
	Tags            []string
	Sensitivity     string
	ProvenanceTypes []string
	SHA256          string
}

type AuthorizationDecision struct {
	Allowed    bool
	ReasonCode string
	Capability Capability
}

type EffectiveScope struct {
	SensitivityCeiling string
	Projects           map[string]bool
	AllProjects        bool
	RequestedTags      map[string]bool
	AllowedTags        map[string]bool
	DeniedTags         map[string]bool
	AllowedProvenance  map[string]bool
	AllProvenance      bool
	Allowed            bool
}

func DefaultPolicy() Policy {
	return Policy{
		ProtocolVersion: ProtocolVersion,
		Actors: map[string]ActorPolicy{
			"ephy": {
				SensitivityCeiling: "internal",
				Projects:           []string{"*"},
				ProvenanceTypes:    []string{"*"},
				Capabilities:       []Capability{CapabilitySearch, CapabilityRead, CapabilityPropose},
			},
			"human": {
				SensitivityCeiling: "restricted",
				Projects:           []string{"*"},
				ProvenanceTypes:    []string{"*"},
				Capabilities:       []Capability{CapabilitySearch, CapabilityRead, CapabilityReview, CapabilityExport},
			},
		},
	}
}

func LoadPolicy(dataRoot string) (Policy, error) {
	path := filepath.Join(dataRoot, ".mdsys", "context", "v1", "policy.json")
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return DefaultPolicy(), nil
	}
	if err != nil {
		return Policy{}, fmt.Errorf("read context policy: %w", err)
	}
	var policy Policy
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("decode context policy: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return Policy{}, fmt.Errorf("decode context policy: trailing JSON")
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, err
	}
	return policy, nil
}

func (policy Policy) Validate() error {
	if policy.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported context policy version")
	}
	for _, project := range policy.MasterProjects {
		if !projectPattern.MatchString(project) {
			return fmt.Errorf("context policy master project is invalid")
		}
	}
	for key, actorPolicy := range policy.Actors {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("context policy actor key is empty")
		}
		if _, ok := sensitivityRank[actorPolicy.SensitivityCeiling]; !ok {
			return fmt.Errorf("context policy sensitivity is invalid")
		}
		if len(actorPolicy.Projects) == 0 || len(actorPolicy.Projects) > 64 {
			return fmt.Errorf("context policy projects are invalid")
		}
		for _, project := range actorPolicy.Projects {
			if project != "*" && !projectPattern.MatchString(project) {
				return fmt.Errorf("context policy project is invalid")
			}
		}
		if err := validatePolicyTags(actorPolicy.AllowedTags); err != nil {
			return fmt.Errorf("context policy allowed_tags are invalid: %w", err)
		}
		if err := validatePolicyTags(actorPolicy.DeniedTags); err != nil {
			return fmt.Errorf("context policy denied_tags are invalid: %w", err)
		}
		allowedTags, _ := normalizedSet(actorPolicy.AllowedTags)
		deniedTags, _ := normalizedSet(actorPolicy.DeniedTags)
		for tag := range allowedTags {
			if deniedTags[tag] {
				return fmt.Errorf("context policy tag cannot be both allowed and denied")
			}
		}
		if err := validateProvenanceTypes(actorPolicy.ProvenanceTypes); err != nil {
			return err
		}
		seenCapabilities := map[Capability]bool{}
		for _, capability := range actorPolicy.Capabilities {
			if !capabilitySet[capability] || seenCapabilities[capability] {
				return fmt.Errorf("context policy capability is invalid")
			}
			seenCapabilities[capability] = true
		}
	}
	return nil
}

func validatePolicyTags(tags []string) error {
	if len(tags) > 64 {
		return fmt.Errorf("too many tags")
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		normalized := strings.ToLower(strings.TrimSpace(tag))
		if normalized == "" || utf8.RuneCountInString(normalized) > 128 || strings.ContainsAny(normalized, "\r\n\x00") || seen[normalized] {
			return fmt.Errorf("invalid tag")
		}
		seen[normalized] = true
	}
	return nil
}

func validateProvenanceTypes(values []string) error {
	if len(values) > 64 {
		return fmt.Errorf("context policy has too many provenance types")
	}
	seen := map[string]bool{}
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized != "*" && !provenanceTypePattern.MatchString(normalized) {
			return fmt.Errorf("context policy provenance type is invalid")
		}
		if seen[normalized] {
			return fmt.Errorf("context policy provenance type is duplicated")
		}
		seen[normalized] = true
	}
	return nil
}

func (policy Policy) Resolve(actor Actor, requested Scope) (EffectiveScope, error) {
	return policy.resolve(actor, requested, "")
}

func (policy Policy) ResolveFor(actor Actor, requested Scope, capability Capability) (EffectiveScope, error) {
	return policy.resolve(actor, requested, capability)
}

func (policy Policy) resolve(actor Actor, requested Scope, capability Capability) (EffectiveScope, error) {
	actorPolicy, ok := policy.Actors[actor.ID]
	if !ok {
		actorPolicy, ok = policy.Actors[actor.Type]
	}
	if !ok {
		return EffectiveScope{Allowed: false}, nil
	}
	if capability != "" && !actorPolicyAllows(actorPolicy, actor.Type, capability) {
		return EffectiveScope{Allowed: false}, nil
	}
	ceiling, err := minSensitivity(actorPolicy.SensitivityCeiling, requested.SensitivityCeiling)
	if err != nil {
		return EffectiveScope{}, err
	}
	effective := EffectiveScope{
		SensitivityCeiling: ceiling,
		Projects:           map[string]bool{},
		RequestedTags:      map[string]bool{},
		AllowedTags:        map[string]bool{},
		DeniedTags:         map[string]bool{},
		AllowedProvenance:  map[string]bool{},
		Allowed:            true,
	}
	allowedProjects, policyAll := normalizedSet(actorPolicy.Projects)
	requestedProjects, requestAll := normalizedSet(requested.Projects)
	if len(requested.Projects) == 0 {
		requestAll = true
	}
	switch {
	case policyAll && requestAll:
		effective.AllProjects = true
	case policyAll:
		effective.Projects = requestedProjects
	case requestAll:
		effective.Projects = allowedProjects
	default:
		for project := range requestedProjects {
			if allowedProjects[project] {
				effective.Projects[project] = true
			}
		}
	}
	for _, tag := range requested.Tags {
		effective.RequestedTags[strings.ToLower(strings.TrimSpace(tag))] = true
	}
	effective.AllowedTags, _ = normalizedSet(actorPolicy.AllowedTags)
	effective.DeniedTags, _ = normalizedSet(actorPolicy.DeniedTags)
	effective.AllowedProvenance, effective.AllProvenance = normalizedSet(actorPolicy.ProvenanceTypes)
	if actorPolicy.ProvenanceTypes == nil {
		effective.AllProvenance = true
	}
	return effective, nil
}

func actorPolicyAllows(policy ActorPolicy, actorType string, capability Capability) bool {
	capabilities := policy.Capabilities
	if capabilities == nil {
		switch actorType {
		case "ephy":
			capabilities = []Capability{CapabilitySearch, CapabilityRead, CapabilityPropose}
		case "human":
			capabilities = []Capability{CapabilitySearch, CapabilityRead, CapabilityReview, CapabilityExport}
		default:
			capabilities = []Capability{}
		}
	}
	for _, allowed := range capabilities {
		if allowed == capability {
			return true
		}
	}
	return false
}

func (policy Policy) Authorize(actor Actor, capability Capability, resource Resource) (AuthorizationDecision, error) {
	decision := AuthorizationDecision{Capability: capability, ReasonCode: "scope_denied"}
	if !capabilitySet[capability] {
		return decision, fmt.Errorf("unsupported context capability")
	}
	project := strings.ToLower(strings.TrimSpace(resource.Project))
	sensitivity := strings.ToLower(strings.TrimSpace(resource.Sensitivity))
	if !projectPattern.MatchString(project) {
		return decision, fmt.Errorf("resource project is invalid")
	}
	if _, ok := sensitivityRank[sensitivity]; !ok {
		return decision, fmt.Errorf("resource sensitivity is invalid")
	}
	if err := validatePolicyTags(resource.Tags); err != nil {
		return decision, fmt.Errorf("resource tags are invalid")
	}
	if err := validateProvenanceTypes(resource.ProvenanceTypes); err != nil {
		return decision, fmt.Errorf("resource provenance is invalid")
	}
	effective, err := policy.ResolveFor(actor, Scope{Projects: []string{project}, SensitivityCeiling: "restricted"}, capability)
	if err != nil {
		return decision, err
	}
	if !effective.Allowed {
		decision.ReasonCode = "capability_denied"
		return decision, nil
	}
	document := indexedDocument{
		Project: project, Kind: strings.ToLower(strings.TrimSpace(resource.Kind)),
		Tags: resource.Tags, Sensitivity: sensitivity,
	}
	for _, provenanceType := range resource.ProvenanceTypes {
		document.Provenance = append(document.Provenance, ProvenanceRef{Type: strings.ToLower(strings.TrimSpace(provenanceType))})
	}
	if !effective.permits(document) {
		return decision, nil
	}
	decision.Allowed = true
	decision.ReasonCode = "allowed"
	return decision, nil
}

func (policy Policy) IsMasterProject(project string) bool {
	normalized := strings.ToLower(strings.TrimSpace(project))
	for _, candidate := range policy.MasterProjects {
		if normalized == strings.ToLower(candidate) {
			return true
		}
	}
	return false
}

func normalizedSet(values []string) (map[string]bool, bool) {
	result := map[string]bool{}
	all := false
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if normalized == "*" {
			all = true
			continue
		}
		if normalized != "" {
			result[normalized] = true
		}
	}
	return result, all
}

func (scope EffectiveScope) permits(document indexedDocument) bool {
	if !scope.Allowed || !sensitivityAtMost(document.Sensitivity, scope.SensitivityCeiling) {
		return false
	}
	if !scope.AllProjects && !scope.Projects[strings.ToLower(document.Project)] {
		return false
	}
	documentTags, _ := normalizedSet(document.Tags)
	for tag := range scope.DeniedTags {
		if documentTags[tag] {
			return false
		}
	}
	if len(scope.AllowedTags) > 0 {
		matched := false
		for tag := range scope.AllowedTags {
			if documentTags[tag] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	for tag := range scope.RequestedTags {
		if !documentTags[tag] {
			return false
		}
	}
	if !scope.AllProvenance {
		if len(document.Provenance) == 0 {
			return false
		}
		for _, provenance := range document.Provenance {
			if !scope.AllowedProvenance[strings.ToLower(strings.TrimSpace(provenance.Type))] {
				return false
			}
		}
	}
	return true
}

func (scope EffectiveScope) hasCompleteVisibility() bool {
	return scope.Allowed && scope.AllProjects && scope.SensitivityCeiling == "restricted" &&
		len(scope.RequestedTags) == 0 && len(scope.AllowedTags) == 0 && len(scope.DeniedTags) == 0 && scope.AllProvenance
}
