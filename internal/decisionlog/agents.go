package decisionlog

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"karte/internal/canonical"
	"karte/internal/ephyoutbox"
)

const agentTokensPath = ".mdsys/decision-agents.json"

var ErrAgentUnauthorized = errors.New("decision agent is not authorized")

func IssueAgentToken(root, agent string) (string, error) {
	if !ValidAgent(agent) {
		return "", ErrAgentUnauthorized
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", err
	}
	digest := sha256.Sum256(secret[:])
	err := canonical.WithWriter(root, func(w *canonical.Writer) error {
		tokens, err := readAgentTokens(w)
		if err != nil {
			return err
		}
		tokens[agent] = hex.EncodeToString(digest[:])
		data, err := json.Marshal(tokens)
		if err != nil {
			return err
		}
		return w.Write(agentTokensPath, data, 0600)
	})
	if err != nil {
		return "", err
	}
	return agent + "." + base64.RawURLEncoding.EncodeToString(secret[:]), nil
}

func AuthenticateAgent(root, token string) (string, error) {
	agent, secret, ok := strings.Cut(token, ".")
	if !ok || !ValidAgent(agent) || len(secret) != 43 {
		return "", ErrAgentUnauthorized
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(secret)
	if err != nil || len(raw) != 32 {
		return "", ErrAgentUnauthorized
	}
	digest := sha256.Sum256(raw)
	err = canonical.WithWriter(root, func(w *canonical.Writer) error {
		tokens, err := readAgentTokens(w)
		if err != nil {
			return err
		}
		expected, err := hex.DecodeString(tokens[agent])
		if err != nil || subtle.ConstantTimeCompare(digest[:], expected) != 1 {
			return ErrAgentUnauthorized
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return agent, nil
}

func readAgentTokens(w *canonical.Writer) (map[string]string, error) {
	tokens := make(map[string]string)
	data, err := w.ReadLimit(agentTokensPath, 8192)
	if errors.Is(err, os.ErrNotExist) {
		return tokens, nil
	}
	if err != nil {
		return nil, err
	}
	if json.Unmarshal(data, &tokens) != nil || tokens == nil {
		return nil, fmt.Errorf("invalid decision agent credentials")
	}
	for agent, digest := range tokens {
		decoded, err := hex.DecodeString(digest)
		if !ValidAgent(agent) || err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("invalid decision agent credentials")
		}
	}
	return tokens, nil
}

type AgentProposal struct {
	Business string `json:"business"`
	Date     string `json:"date"`
	Content  string `json:"content"`
	Source   Source `json:"source"`
}

func (input AgentProposal) Draft(settings Settings, now time.Time) (Record, error) {
	label, ok := settings.businessLabels()[input.Business]
	if !ok {
		return Record{}, fmt.Errorf("decision proposal business is not configured")
	}
	content := strings.TrimSpace(input.Content)
	record := Record{
		Version: Version, ID: decisionID(input.Business, content), Date: input.Date,
		Business: input.Business, BusinessLabel: label, Content: content, Decider: Reviewer,
		Status: Open, Confidence: "unverified", Sources: []Source{input.Source},
		Visibility: Private, VisibilityTags: []string{Private}, UpdatedAt: now.UTC().Format(time.RFC3339Nano),
	}
	if settings.requiresPrivate(record) {
		record.PublicationRestriction = WorkspacePrivate
	}
	return record, record.Validate()
}

func QueueAgentProposal(w *canonical.Writer, store *ephyoutbox.Store, record Record, agent string) error {
	if !ValidAgent(agent) || record.Decider != Reviewer || record.shareable() || record.Status != Open ||
		record.Supersedes != "" || record.ReplacedBy != "" {
		return ErrAgentUnauthorized
	}
	_, err := queue(w, store, []Record{record}, agent)
	return err
}
