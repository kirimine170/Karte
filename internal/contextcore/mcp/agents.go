package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"

	"karte/internal/canonical"
	"karte/internal/contextcore"
	"karte/internal/decisionlog"
	"karte/internal/ephyoutbox"
)

func NewAgentServer(dataDir, token string) (*Server, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("KARTE_DATA_DIR must be set explicitly")
	}
	service, err := contextcore.NewService(dataDir)
	if err != nil {
		return nil, err
	}
	agent, err := decisionlog.AuthenticateAgent(dataDir, token)
	if err != nil {
		return nil, decisionlog.ErrAgentUnauthorized
	}
	server := &Server{
		service:  service.WithDecisionRecipient(agent),
		dataRoot: dataDir, agentToken: token, scope: contextcore.MCPScope{Actor: agent},
	}
	policy, err := server.loadCurrentPolicy()
	if err != nil {
		return nil, decisionlog.ErrAgentUnauthorized
	}
	grant, ok := policy.Actors[agent]
	if !ok {
		return nil, decisionlog.ErrAgentUnauthorized
	}
	server.policy = policy
	for _, capability := range grant.Capabilities {
		server.scope.Capabilities = append(server.scope.Capabilities, string(capability))
	}
	return server, nil
}

func (s *Server) agentScope(policy contextcore.Policy) (contextcore.MCPScope, contextcore.Policy, error) {
	agent, err := decisionlog.AuthenticateAgent(s.dataRoot, s.agentToken)
	if err != nil || agent != s.scope.Actor {
		return contextcore.MCPScope{}, contextcore.Policy{}, decisionlog.ErrAgentUnauthorized
	}
	if _, ok := policy.Actors[agent]; !ok {
		return contextcore.MCPScope{}, contextcore.Policy{}, decisionlog.ErrAgentUnauthorized
	}
	return s.scope, policy, nil
}

func proposalTool() map[string]any {
	return map[string]any{
		"name":        "karte_propose_decision",
		"description": "Submit a private decision proposal for human review.",
		"inputSchema": map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"business", "date", "content", "source"},
			"properties": map[string]any{
				"business": map[string]any{"type": "string"},
				"date":     map[string]any{"type": "string", "format": "date"},
				"content":  map[string]any{"type": "string", "maxLength": 4000},
				"source": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{"url", "sha256"},
					"properties": map[string]any{
						"url":    map[string]any{"type": "string", "format": "uri"},
						"sha256": map[string]any{"type": "string", "pattern": "^[a-f0-9]{64}$"},
					},
				},
			},
		},
	}
}

func (s *Server) proposeDecision(raw json.RawMessage) (any, error) {
	requestID := newRequestID()
	var input decisionlog.AgentProposal
	if err := decodeArguments(raw, &input); err != nil {
		return nil, s.rejectInvalidArguments(requestID, "propose", errors.New("invalid decision proposal arguments"))
	}
	scope, policy, err := s.currentScope()
	if err != nil {
		return nil, s.rejectInvalidArguments(requestID, "propose", err)
	}
	actor := contextcore.Actor{Type: "tool", ID: scope.Actor}
	store, err := ephyoutbox.NewStore(s.dataRoot)
	if err != nil {
		return nil, err
	}
	err = canonical.WithWriter(s.dataRoot, func(w *canonical.Writer) error {
		settings, err := decisionlog.LoadSettings(w)
		if err != nil {
			return err
		}
		record, err := input.Draft(settings, time.Now())
		if err != nil {
			return err
		}
		grant, err := policy.Authorize(actor, contextcore.CapabilityPropose, contextcore.Resource{
			Project: record.Business, Kind: "decision", Sensitivity: decisionlog.Internal,
			Tags: []string{"decision-log", "decision:open"}, ProvenanceTypes: []string{"meeting-summary"},
		})
		if err != nil || !grant.Allowed {
			return decisionlog.ErrAgentUnauthorized
		}
		return decisionlog.QueueAgentProposal(w, store, record, actor.ID)
	})
	if err != nil {
		status, code := "error", "proposal_failed"
		if errors.Is(err, decisionlog.ErrAgentUnauthorized) {
			status, code = "denied", "unauthorized"
		}
		return nil, errors.Join(err, contextcore.RecordAudit(s.dataRoot, requestID, actor, "propose", status, 0, code))
	}
	if err := contextcore.RecordAudit(s.dataRoot, requestID, actor, "propose", "ok", 1, ""); err != nil {
		return nil, err
	}
	return map[string]bool{"queued": true}, nil
}

func agentHTTPHandler(dataDir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/mcp" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Origin") != "" {
			http.Error(w, "Origin is not allowed", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		headers := r.Header.Values("Authorization")
		var token string
		if len(headers) == 1 {
			scheme, value, found := strings.Cut(headers[0], " ")
			if found && strings.EqualFold(scheme, "Bearer") {
				token = value
			}
		}
		server, err := NewAgentServer(dataDir, token)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		version := r.Header.Get("MCP-Protocol-Version")
		if version != "" && version != protocolVersion && version != "2025-03-26" {
			http.Error(w, "Unsupported MCP protocol version", http.StatusBadRequest)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "Expected application/json", http.StatusUnsupportedMediaType)
			return
		}
		input, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxFrameBytes))
		if err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}
		var response bytes.Buffer
		if err := server.dispatch(json.NewEncoder(&response), string(input)); err != nil {
			http.Error(w, "MCP processing failed", http.StatusInternalServerError)
			return
		}
		if response.Len() == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = response.WriteTo(w)
	})
}

func ServeAgents(ctx context.Context, dataDir, address string) error {
	if strings.TrimSpace(dataDir) == "" {
		return errors.New("KARTE_DATA_DIR must be set explicitly")
	}
	if _, err := contextcore.NewService(dataDir); err != nil {
		return err
	}
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("agent MCP requires an explicit loopback address")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := &http.Server{
		Handler: agentHTTPHandler(dataDir), ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 15 * time.Second, WriteTimeout: 45 * time.Second, IdleTimeout: 30 * time.Second,
		MaxHeaderBytes: 16 << 10,
	}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = server.Close()
		case <-done:
		}
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
