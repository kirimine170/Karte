package contextcore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type ActorPolicy struct {
	SensitivityCeiling string   `json:"sensitivity_ceiling"`
	Projects           []string `json:"projects"`
}

type Policy struct {
	ProtocolVersion string                 `json:"protocol_version"`
	Actors          map[string]ActorPolicy `json:"actors"`
}

type EffectiveScope struct {
	SensitivityCeiling string
	Projects           map[string]bool
	AllProjects        bool
	Tags               map[string]bool
	Allowed            bool
}

func DefaultPolicy() Policy {
	return Policy{
		ProtocolVersion: ProtocolVersion,
		Actors: map[string]ActorPolicy{
			"ephy":  {SensitivityCeiling: "internal", Projects: []string{"*"}},
			"human": {SensitivityCeiling: "restricted", Projects: []string{"*"}},
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
	if policy.ProtocolVersion != ProtocolVersion {
		return Policy{}, fmt.Errorf("unsupported context policy version")
	}
	for key, actorPolicy := range policy.Actors {
		if strings.TrimSpace(key) == "" {
			return Policy{}, fmt.Errorf("context policy actor key is empty")
		}
		if _, ok := sensitivityRank[actorPolicy.SensitivityCeiling]; !ok {
			return Policy{}, fmt.Errorf("context policy sensitivity is invalid")
		}
		for _, project := range actorPolicy.Projects {
			if project != "*" && !projectPattern.MatchString(project) {
				return Policy{}, fmt.Errorf("context policy project is invalid")
			}
		}
	}
	return policy, nil
}

func (policy Policy) Resolve(actor Actor, requested Scope) (EffectiveScope, error) {
	actorPolicy, ok := policy.Actors[actor.ID]
	if !ok {
		actorPolicy, ok = policy.Actors[actor.Type]
	}
	if !ok {
		return EffectiveScope{Allowed: false}, nil
	}
	ceiling, err := minSensitivity(actorPolicy.SensitivityCeiling, requested.SensitivityCeiling)
	if err != nil {
		return EffectiveScope{}, err
	}
	effective := EffectiveScope{
		SensitivityCeiling: ceiling,
		Projects:           map[string]bool{},
		Tags:               map[string]bool{},
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
		effective.Tags[strings.ToLower(strings.TrimSpace(tag))] = true
	}
	return effective, nil
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
	if len(scope.Tags) == 0 {
		return true
	}
	documentTags, _ := normalizedSet(document.Tags)
	for tag := range scope.Tags {
		if !documentTags[tag] {
			return false
		}
	}
	return true
}
