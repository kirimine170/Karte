package decisionlog

import (
	"fmt"
	"slices"
	"strings"
)

const (
	Internal     = "internal"
	AgentGrok    = "agent:grok"
	AgentMuse    = "agent:muse"
	AgentChatGPT = "agent:chatgpt"
)

func ValidAgent(tag string) bool {
	return tag == AgentGrok || tag == AgentMuse || tag == AgentChatGPT
}

func validateVisibilityTags(tags []string) error {
	if len(tags) > 16 || (slices.Contains(tags, Private) && len(tags) != 1) {
		return fmt.Errorf("invalid decision visibility tags")
	}
	seen := make(map[string]bool, len(tags))
	for _, tag := range tags {
		business, businessTag := strings.CutPrefix(tag, "business:")
		valid := tag == Public || tag == Internal || tag == Private || ValidAgent(tag) ||
			(businessTag && businessPattern.MatchString(business))
		if !valid || seen[tag] {
			return fmt.Errorf("invalid or duplicate decision visibility tag")
		}
		seen[tag] = true
	}
	return nil
}

// shareable preserves explicit publication of older records. New proposals
// start with a private tag and require an explicit sharing edit during review.
func (r Record) shareable() bool {
	if len(r.VisibilityTags) == 0 {
		return r.Visibility == Public
	}
	return !slices.Contains(r.VisibilityTags, Private)
}

func (r Record) VisibleTo(tags []string) bool {
	if r.PublicationRestriction != "" || !r.shareable() {
		return false
	}
	if len(r.VisibilityTags) == 0 {
		return slices.Contains(tags, Public)
	}
	for _, tag := range tags {
		if slices.Contains(r.VisibilityTags, tag) {
			return true
		}
	}
	return false
}

func (r Record) ContextSensitivity() string {
	if r.VisibleTo([]string{Public}) {
		return Public
	}
	return Internal
}
