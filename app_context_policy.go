package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"karte/internal/contextcore"
	"karte/internal/ephyoutbox"
	fm "karte/internal/frontmatter"
)

var (
	localHumanActor  = contextcore.Actor{Type: "human", ID: "local-human"}
	ephyContextActor = contextcore.Actor{Type: "ephy", ID: "ephy"}
)

func (a *App) authorizeEphyProposal(proposal ephyoutbox.Proposal, editedFrontmatter map[string]any) (contextcore.Policy, contextcore.Resource, error) {
	policy, err := contextcore.LoadPolicy(a.dataDir)
	if err != nil {
		return contextcore.Policy{}, contextcore.Resource{}, err
	}
	original := proposalResource(proposal, proposal.ProposedFrontmatter)
	if _, _, tagsErr := policyTagsFromFrontmatter(proposal.ProposedFrontmatter); tagsErr != nil {
		return policy, contextcore.Resource{}, tagsErr
	}
	finalResource := original
	if proposal.Operation == "append" {
		if proposal.TargetRelativePath == nil {
			return policy, contextcore.Resource{}, fmt.Errorf("proposal policy denied")
		}
		service, serviceErr := contextcore.NewService(a.dataDir)
		if serviceErr != nil {
			return policy, contextcore.Resource{}, serviceErr
		}
		canonical, resourceErr := service.ResourceByRelativePath(*proposal.TargetRelativePath)
		if resourceErr != nil {
			_ = contextcore.RecordAudit(a.dataDir, "proposal:"+proposal.CandidateID+":propose", ephyContextActor, string(contextcore.CapabilityPropose), "denied", 0, "resource_unavailable")
			return policy, contextcore.Resource{}, fmt.Errorf("proposal policy denied")
		}
		if proposal.Sensitivity != canonical.Sensitivity || proposal.Placement.Project != canonical.Project || proposal.Placement.Kind != canonical.Kind {
			_ = contextcore.RecordAudit(a.dataDir, "proposal:"+proposal.CandidateID+":propose", ephyContextActor, string(contextcore.CapabilityPropose), "denied", 0, "classification_mismatch")
			return policy, contextcore.Resource{}, fmt.Errorf("proposal policy denied")
		}
		canonical.ProvenanceTypes = append(canonical.ProvenanceTypes, proposalProvenanceTypes(proposal)...)
		original = canonical
		if tags, ok, tagsErr := policyTagsFromFrontmatter(proposal.ProposedFrontmatter); tagsErr != nil {
			return policy, contextcore.Resource{}, tagsErr
		} else if ok {
			original.Tags = tags
		}
		finalResource = original
	}
	if editedFrontmatter != nil {
		if tags, ok, tagsErr := policyTagsFromFrontmatter(editedFrontmatter); tagsErr != nil {
			return policy, contextcore.Resource{}, tagsErr
		} else if ok {
			finalResource.Tags = tags
		}
		if sensitivity, ok := editedFrontmatter["sensitivity"].(string); ok && strings.TrimSpace(sensitivity) != "" {
			finalResource.Sensitivity = strings.ToLower(strings.TrimSpace(sensitivity))
		}
	}

	ephyDecision, err := a.recordPolicyDecision(policy, proposal.CandidateID, ephyContextActor, contextcore.CapabilityPropose, original)
	if err != nil {
		return policy, contextcore.Resource{}, err
	}
	if !ephyDecision.Allowed {
		return policy, contextcore.Resource{}, fmt.Errorf("proposal policy denied")
	}
	humanDecision, err := a.recordPolicyDecision(policy, proposal.CandidateID, localHumanActor, contextcore.CapabilityReview, finalResource)
	if err != nil {
		return policy, contextcore.Resource{}, err
	}
	if !humanDecision.Allowed {
		return policy, contextcore.Resource{}, fmt.Errorf("proposal policy denied")
	}
	return policy, finalResource, nil
}

func (a *App) recordPolicyDecision(policy contextcore.Policy, correlation string, actor contextcore.Actor, capability contextcore.Capability, resource contextcore.Resource) (contextcore.AuthorizationDecision, error) {
	decision, err := policy.Authorize(actor, capability, resource)
	if err != nil {
		return decision, err
	}
	status := "denied"
	if decision.Allowed {
		status = "allowed"
	}
	if err := contextcore.RecordAudit(a.dataDir, "proposal:"+correlation+":"+string(capability), actor, string(capability), status, 0, decision.ReasonCode); err != nil {
		return decision, err
	}
	return decision, nil
}

func proposalResource(proposal ephyoutbox.Proposal, frontmatter map[string]any) contextcore.Resource {
	resource := contextcore.Resource{
		Project: proposal.Placement.Project, Kind: proposal.Placement.Kind,
		Sensitivity: proposal.Sensitivity, ProvenanceTypes: proposalProvenanceTypes(proposal),
	}
	if tags, ok, _ := policyTagsFromFrontmatter(frontmatter); ok {
		resource.Tags = tags
	}
	return resource
}

func proposalProvenanceTypes(proposal ephyoutbox.Proposal) []string {
	result := make([]string, 0, len(proposal.SourceRefs))
	for _, source := range proposal.SourceRefs {
		result = append(result, strings.ToLower(strings.TrimSpace(source.Type)))
	}
	return result
}

func policyTagsFromFrontmatter(frontmatter map[string]any) ([]string, bool, error) {
	value, ok := frontmatter["tags"]
	if !ok {
		return nil, false, nil
	}
	switch tags := value.(type) {
	case string:
		return fm.NormalizeTags(tags), true, nil
	case []string:
		return normalizePolicyTags(tags), true, nil
	case []any:
		values := make([]string, 0, len(tags))
		for _, value := range tags {
			text, textOK := value.(string)
			if !textOK {
				return nil, true, fmt.Errorf("proposal policy denied")
			}
			values = append(values, text)
		}
		return normalizePolicyTags(values), true, nil
	default:
		return nil, true, fmt.Errorf("proposal policy denied")
	}
}

func normalizePolicyTags(tags []string) []string {
	return fm.NormalizeTags(strings.Join(tags, ","))
}

func (a *App) authorizeDocumentExport(relativePath string) error {
	_, err := a.authorizedDocumentExportSHA(relativePath)
	return err
}

func (a *App) authorizedDocumentExportSHA(relativePath string) (string, error) {
	service, err := contextcore.NewService(a.dataDir)
	if err != nil {
		return "", err
	}
	resource, err := service.ResourceByRelativePath(relativePath)
	if err != nil {
		return "", fmt.Errorf("document export is not authorized")
	}
	policy, err := contextcore.LoadPolicy(a.dataDir)
	if err != nil {
		return "", err
	}
	decision, err := policy.Authorize(localHumanActor, contextcore.CapabilityExport, resource)
	if err != nil {
		return "", err
	}
	status := "denied"
	if decision.Allowed {
		status = "allowed"
	}
	correlation := strings.Join([]string{"export", relativePath, time.Now().UTC().Format(time.RFC3339Nano)}, ":")
	if err := contextcore.RecordAudit(a.dataDir, correlation, localHumanActor, string(contextcore.CapabilityExport), status, 0, decision.ReasonCode); err != nil {
		return "", err
	}
	if !decision.Allowed {
		return "", fmt.Errorf("document export is not authorized")
	}
	return resource.SHA256, nil
}

func (a *App) renderAuthorizedDocumentForExport(relativePath string) (string, error) {
	expectedSHA256, err := a.authorizedDocumentExportSHA(relativePath)
	if err != nil {
		return "", err
	}
	absPath, ok := a.resolveContentPath(relativePath)
	if !ok {
		return "", fmt.Errorf("document export is not authorized")
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		return "", fmt.Errorf("document export source is unavailable")
	}
	if ephyoutbox.SHA256Bytes(content) != expectedSHA256 {
		return "", fmt.Errorf("document export source changed during authorization")
	}
	html, err := a.PreviewMarkdownForPath(relativePath, string(content))
	if err != nil {
		return "", fmt.Errorf("document export render failed")
	}
	return html, nil
}
