package ephyrecordsv2

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"karte/internal/canonical"
	"karte/internal/contextcore"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Classification struct {
	Kind            string   `json:"kind"`
	Sensitivity     string   `json:"sensitivity"`
	Tags            []string `json:"tags"`
	ProvenanceTypes []string `json:"provenance_types"`
}
type Grant struct {
	SchemaVersion    string                    `json:"schema_version"`
	PolicyID         string                    `json:"policy_id"`
	Revision         int64                     `json:"policy_revision"`
	Enabled          bool                      `json:"enabled"`
	UserID           string                    `json:"user_id"`
	ActorID          string                    `json:"actor_id"`
	ProducerID       string                    `json:"producer_instance_id"`
	ScopeID          string                    `json:"scope_id"`
	StorageAreaID    string                    `json:"storage_area_id"`
	Project          string                    `json:"project"`
	Records          map[string]Classification `json:"records"`
	Operations       []string                  `json:"operations"`
	DeniedTags       []string                  `json:"denied_tags"`
	ValidFrom        string                    `json:"valid_from"`
	ExpiresAt        string                    `json:"expires_at,omitempty"`
	ConsentEpoch     int64                     `json:"consent_epoch"`
	ScopeGeneration  int64                     `json:"scope_generation"`
	Storage          bool                      `json:"storage"`
	Training         bool                      `json:"training"`
	ExternalTransfer bool                      `json:"external_transfer"`
	KeyID            string                    `json:"key_id"`
}
type registration struct {
	Grant Grant  `json:"grant"`
	Key   string `json:"key"`
}
type registrations struct {
	Scopes map[string]registration `json:"scopes"`
}

type Service struct {
	DataRoot     string
	ConfigRoot   string
	Now          func() time.Time
	AfterReceive func() error
	Fault        func(string) error
}

func New(dataRoot, configRoot string) (*Service, error) {
	root, e := physicalPath(dataRoot)
	if e != nil {
		return nil, e
	}
	if st, e := os.Stat(root); e != nil || !st.IsDir() {
		return nil, fmt.Errorf("invalid_data_root")
	}
	if configRoot == "" {
		base, e := os.UserConfigDir()
		if e != nil {
			return nil, e
		}
		configRoot = filepath.Join(base, "Karte", "ephy-v2", canonical.Hash([]byte(root)))
	}
	configRoot, e = physicalPath(configRoot)
	if e != nil {
		return nil, e
	}
	if e := ValidateCredentialDirectory(root, configRoot); e != nil {
		return nil, e
	}

	return &Service{DataRoot: root, ConfigRoot: configRoot, Now: time.Now}, nil
}
func (g Grant) Validate() error {
	if g.SchemaVersion != Version || !validID(g.PolicyID) || !validID(g.ScopeID) || !validID(g.ProducerID) || !token.MatchString(g.UserID) || !token.MatchString(g.ActorID) || !token.MatchString(g.StorageAreaID) || !token.MatchString(g.KeyID) || !projectToken.MatchString(g.Project) || g.Revision < 1 || g.ConsentEpoch < 1 || g.ScopeGeneration < 1 || !timestamp(g.ValidFrom) || (g.ExpiresAt != "" && !timestamp(g.ExpiresAt)) || !g.Storage || g.Training || g.ExternalTransfer || len(g.Records) == 0 || len(g.Records) > 3 || len(g.Operations) == 0 {
		return fmt.Errorf("invalid_grant")
	}
	if g.ExpiresAt != "" {
		a, _ := time.Parse(time.RFC3339, g.ValidFrom)
		b, _ := time.Parse(time.RFC3339, g.ExpiresAt)
		if !b.After(a) {
			return fmt.Errorf("invalid_grant")
		}
	}
	for typ, c := range g.Records {
		kind := "note"
		if typ == "ephy_diary" {
			kind = "journal"
		}
		if !oneOf(typ, "conversation", "summary", "ephy_diary") || c.Kind != kind || !oneOf(c.Sensitivity, "public", "internal", "confidential", "restricted") || len(c.ProvenanceTypes) == 0 {
			return fmt.Errorf("invalid_classification")
		}
		for _, t := range c.Tags {
			if !token.MatchString(t) || oneOf(t, g.DeniedTags...) {
				return fmt.Errorf("invalid_tags")
			}
		}
		for _, p := range c.ProvenanceTypes {
			if !token.MatchString(p) {
				return fmt.Errorf("invalid_provenance")
			}
		}
	}
	for _, op := range g.Operations {
		if !oneOf(op, "create_record", "append_events", "revise_derivation") {
			return fmt.Errorf("invalid_operation")
		}
	}
	return nil
}
func (s *Service) loadRegistrations() (registrations, error) {
	r := registrations{Scopes: map[string]registration{}}
	root, e := os.OpenRoot(s.ConfigRoot)
	if errors.Is(e, os.ErrNotExist) {
		return r, nil
	}
	if e != nil {
		return r, e
	}
	defer root.Close()
	st, e := root.Lstat("registrations.json")
	if errors.Is(e, os.ErrNotExist) {
		return r, nil
	}
	if e != nil {
		return r, e
	}
	if !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 {
		return r, fmt.Errorf("unsafe_credentials")
	}
	data, e := root.ReadFile("registrations.json")
	if e != nil {
		return r, e
	}
	e = strict(data, &r)
	if r.Scopes == nil {
		r.Scopes = map[string]registration{}
	}
	return r, e
}

// Configure is a local human control plane．It is intentionally absent from
// proposal/context operations and Wails bindings．Each change fences old work．
func (s *Service) Configure(g Grant, key []byte) error {
	if e := g.Validate(); e != nil {
		return e
	}
	if len(key) != 32 {
		return fmt.Errorf("credential_requires_32_bytes")
	}
	return canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		all, e := s.loadRegistrations()
		if e != nil {
			return e
		}
		if old, ok := all.Scopes[g.ScopeID]; ok {
			o := old.Grant
			if g.Revision <= o.Revision || g.ConsentEpoch <= o.ConsentEpoch || g.ScopeGeneration < o.ScopeGeneration || g.PolicyID != o.PolicyID || g.ProducerID != o.ProducerID || g.ActorID != o.ActorID || g.UserID != o.UserID || g.StorageAreaID != o.StorageAreaID || g.Project != o.Project {
				return fmt.Errorf("invalid_policy_transition")
			}
		}
		// The private directory must also stay outside a Git work tree．
		for p := s.ConfigRoot; ; p = filepath.Dir(p) {
			if _, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
				return fmt.Errorf("credentials_inside_git")
			}
			if filepath.Dir(p) == p {
				break
			}
		}
		if e := os.MkdirAll(s.ConfigRoot, 0700); e != nil {
			return e
		}
		if e := os.Chmod(s.ConfigRoot, 0700); e != nil {
			return e
		}
		all.Scopes[g.ScopeID] = registration{Grant: g, Key: hex.EncodeToString(key)}
		b, e := canonicalJSONMarshal(all)
		if e != nil {
			return e
		}
		if err := purgeResponses(w); err != nil {
			return err
		}
		return canonical.WithWriter(s.ConfigRoot, func(c *canonical.Writer) error { return c.Write("registrations.json", b, 0600) })
	})
}
func canonicalJSONMarshal(v any) ([]byte, error) { return encodeCanonical(v) }
func loadPrivacy(w *canonical.Writer) (contextcore.Policy, error) {
	b, e := w.Read(filepath.Join(".mdsys", "context", "v1", "policy.json"))
	if errors.Is(e, os.ErrNotExist) {
		return contextcore.DefaultPolicy(), nil
	}
	if e != nil {
		return contextcore.Policy{}, e
	}
	var p contextcore.Policy
	if e = strict(b, &p); e != nil {
		return p, e
	}
	return p, p.Validate()
}
func (s *Service) SetPrivacyPolicy(p contextcore.Policy) error {
	if e := p.Validate(); e != nil {
		return e
	}
	return canonical.WithWriter(s.DataRoot, func(w *canonical.Writer) error {
		b, e := encodeCanonical(p)
		if e != nil {
			return e
		}
		if err := purgeResponses(w); err != nil {
			return err
		}
		return w.Write(filepath.Join(".mdsys", "context", "v1", "policy.json"), b, 0600)
	})
}
func signingBytes(raw []byte) ([]byte, error) {
	b, e := CanonicalJSON(raw)
	if e != nil {
		return nil, e
	}
	var m map[string]any
	d := json.NewDecoder(strings.NewReader(string(b)))
	d.UseNumber()
	if e = d.Decode(&m); e != nil {
		return nil, e
	}
	a, ok := m["auth"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing_auth")
	}
	delete(a, "mac")
	return encodeCanonical(m)
}
func Sign(raw, key []byte) (string, error) {
	b, e := signingBytes(raw)
	if e != nil {
		return "", e
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(b)
	return hex.EncodeToString(mac.Sum(nil)), nil
}
func (s *Service) authorize(w *canonical.Writer, p Proposal, raw []byte, cap contextcore.Capability) (Grant, error) {
	all, e := s.loadRegistrations()
	if e != nil {
		return Grant{}, e
	}
	r, ok := all.Scopes[p.ScopeID]
	if !ok {
		return Grant{}, fmt.Errorf("configuration_required")
	}
	g := r.Grant
	if e = g.Validate(); e != nil {
		return Grant{}, e
	}
	now := s.Now()
	from, _ := time.Parse(time.RFC3339, g.ValidFrom)
	expired := false
	if g.ExpiresAt != "" {
		until, _ := time.Parse(time.RFC3339, g.ExpiresAt)
		expired = !now.Before(until)
	}
	if !g.Enabled || now.Before(from) || expired || p.Actor.Type != "ephy" || p.Actor.ID != g.ActorID || p.ProducerID != g.ProducerID || p.PolicyID != g.PolicyID || p.PolicyRevision != g.Revision || p.ConsentEpoch != g.ConsentEpoch || p.ScopeGeneration != g.ScopeGeneration || p.Auth.KeyID != g.KeyID {
		return Grant{}, fmt.Errorf("permission_denied")
	}
	if cap == contextcore.CapabilityPropose && !oneOf(p.Operation, g.Operations...) {
		return Grant{}, fmt.Errorf("permission_denied")
	}
	key, e := hex.DecodeString(r.Key)
	if e != nil || len(key) != 32 {
		return Grant{}, fmt.Errorf("invalid_credentials")
	}
	mac, e := Sign(raw, key)
	if e != nil {
		return Grant{}, e
	}
	got, e := hex.DecodeString(p.Auth.MAC)
	expected, _ := hex.DecodeString(mac)
	if e != nil || !hmac.Equal(got, expected) {
		return Grant{}, fmt.Errorf("permission_denied")
	}
	policy, e := loadPrivacy(w)
	if e != nil {
		return Grant{}, e
	}
	if p.Record.Type != "" {
		c, ok := g.Records[p.Record.Type]
		decision, err := policy.Authorize(p.Actor, cap, resource(g, c))
		if !ok || err != nil || !decision.Allowed {
			return Grant{}, fmt.Errorf("permission_denied")
		}
	}
	return g, nil
}
func resource(g Grant, c Classification) contextcore.Resource {
	return contextcore.Resource{Project: g.Project, Kind: c.Kind, Tags: c.Tags, Sensitivity: c.Sensitivity, ProvenanceTypes: c.ProvenanceTypes}
}

type ProducerCredential struct {
	ProducerID string `json:"producer_instance_id"`
	KeyID      string `json:"key_id"`
	Key        string `json:"key"`
}

func DecodeProducerCredential(raw []byte) (ProducerCredential, error) {
	var c ProducerCredential
	if e := strict(raw, &c); e != nil {
		return c, e
	}
	key, e := hex.DecodeString(c.Key)
	if e != nil || len(key) != 32 || !validID(c.ProducerID) || !token.MatchString(c.KeyID) {
		return c, fmt.Errorf("invalid_credentials")
	}
	return c, nil
}
func DecodeGrant(raw []byte) (Grant, error) {
	var g Grant
	if e := strict(raw, &g); e != nil {
		return g, e
	}
	if e := requireWireFields(raw, []string{"schema_version", "policy_id", "policy_revision", "enabled", "user_id", "actor_id", "producer_instance_id", "scope_id", "storage_area_id", "project", "records", "operations", "denied_tags", "valid_from", "consent_epoch", "scope_generation", "storage", "training", "external_transfer", "key_id"}); e != nil {
		return g, e
	}
	return g, g.Validate()
}
func DecodePrivacyPolicy(raw []byte) (contextcore.Policy, error) {
	var p contextcore.Policy
	if e := strict(raw, &p); e != nil {
		return p, e
	}
	return p, p.Validate()
}

// Resolve physical ancestors before checking containment，including /var aliases
// and a not-yet-created config directory under a symlinked parent．
func physicalPath(path string) (string, error) {
	abs, e := filepath.Abs(path)
	if e != nil {
		return "", e
	}
	cursor := abs
	for {
		if _, e := os.Lstat(cursor); e == nil {
			physical, e := filepath.EvalSymlinks(cursor)
			if e != nil {
				return "", e
			}
			tail, e := filepath.Rel(cursor, abs)
			if e != nil {
				return "", e
			}
			return filepath.Join(physical, tail), nil
		} else if !os.IsNotExist(e) {
			return "", e
		}
		next := filepath.Dir(cursor)
		if next == cursor {
			return "", fmt.Errorf("invalid_path")
		}
		cursor = next
	}
}
func ValidateCredentialDirectory(dataRoot, dir string) error {
	data, e := physicalPath(dataRoot)
	if e != nil {
		return e
	}
	config, e := physicalPath(dir)
	if e != nil {
		return e
	}
	if filepath.VolumeName(data) == filepath.VolumeName(config) {
		rel, e := filepath.Rel(data, config)
		if e != nil || filepath.IsLocal(rel) {
			return fmt.Errorf("credential_directory_must_be_outside_data_root")
		}
	}

	for p := config; ; p = filepath.Dir(p) {
		if _, e := os.Lstat(filepath.Join(p, ".git")); e == nil {
			return fmt.Errorf("credentials_inside_git")
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
