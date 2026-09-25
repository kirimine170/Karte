// Package mcp serves a read-only, STDIO-bound Model Context Protocol facade
// over the Personal Context Core. It is deliberately thin: every capability
// maps one-to-one onto contextcore.Service, and no state is kept on the
// wire beyond the protocol envelope.
//
// The adapter refuses to start unless:
//   - KARTE_DATA_DIR is set, points at an existing directory, and is not the
//     shared local-only default used by the Wails app;
//   - .mdsys/context/v1/policy.json exists inside that directory and grants
//     the configured actor both "search" and "read";
//   - .mdsys/context/v1/mcp-scope.json exists, names that same actor, and
//     lists exactly [search read].
//
// All diagnostics go to stderr. stdout carries only JSON-RPC frames so a
// host can parse the stream unambiguously.
package mcp

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"karte/internal/contextcore"
)

// Protocol constants. We implement the 2025-06-18 wire version, which is the
// version every current Codex/inspector tooling speaks.
const (
	protocolVersion = "2025-06-18"
	serverName      = "karte-context"
	serverVersion   = "0.1.0"
)

// Result payloads. They mirror contextcore types so a host can reuse the
// same rendering code for both the filesystem spool and this facade.
type SearchOutput struct {
	Status      string                     `json:"status"`
	Results     []contextcore.SearchResult `json:"results"`
	Diagnostics []contextcore.Diagnostic   `json:"diagnostics"`
}

type ReadOutput struct {
	Status      string                    `json:"status"`
	Document    *contextcore.Document     `json:"document"`
	Diagnostics []contextcore.Diagnostic  `json:"diagnostics"`
}

// Server bundles the read-only contextcore services behind a small interface
// so the JSON-RPC dispatch in this file stays focused on the wire format.
type Server struct {
	service  *contextcore.Service
	policy   contextcore.Policy
	scope    contextcore.MCPScope
	dataRoot string
}

// NewServer builds the adapter against dataDir. Every failure mode is fatal:
// the adapter is intentionally simpler than the Wails app and does not fall
// back to defaults.
func NewServer(dataDir string) (*Server, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("KARTE_DATA_DIR must be set explicitly for the MCP adapter")
	}
	abs, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve KARTE_DATA_DIR: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("KARTE_DATA_DIR %q: %w", abs, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("KARTE_DATA_DIR %q is not a directory", abs)
	}
	realRoot, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolve KARTE_DATA_DIR symlinks: %w", err)
	}

	policy, err := contextcore.LoadPolicy(realRoot)
	if err != nil {
		return nil, fmt.Errorf("KARTE_DATA_DIR %q is not a dedicated Karte context root: %w", abs, err)
	}
	// Ensure policy.json actually exists in the root
	policyPath := filepath.Join(realRoot, ".mdsys", "context", "v1", "policy.json")
	if _, err := os.Stat(policyPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("KARTE_DATA_DIR %q does not contain policy.json, which is required for dedicated roots", abs)
	}
	scope, err := contextcore.LoadMCPScope(realRoot, policy)
	if err != nil {
		return nil, err
	}
	service, err := contextcore.NewService(realRoot)
	if err != nil {
		return nil, fmt.Errorf("open contextcore service: %w", err)
	}
	return &Server{
		service:  service,
		policy:   policy,
		scope:    scope,
		dataRoot: realRoot,
	}, nil
}

// DataRoot is the resolved directory the adapter is bound to. Tests use it to
// prove that two adapters bound to different roots never share documents.
func (s *Server) DataRoot() string { return s.dataRoot }

// Run reads JSON-RPC lines from in and writes JSON-RPC lines to out until
// either stream closes or ctx is cancelled. It returns the first protocol
// error so tests can assert on malformed streams.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	encoder := json.NewEncoder(out)
	for {
		line, readErr := reader.ReadString('\n')
		if len(line) > 0 {
			if err := s.dispatch(encoder, line); err != nil {
				// Protocol errors are already encoded as JSON-RPC error
				// responses; we surface them to the caller for logging.
				fmt.Fprintf(os.Stderr, "karte-mcp: %v\n", err)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

// ---------- JSON-RPC plumbing ----------

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) dispatch(encoder *json.Encoder, line string) error {
	var req request
	if err := json.Unmarshal([]byte(line), &req); err != nil {
		return s.encodeError(encoder, nil, -32700, "parse error")
	}
	if req.JSONRPC != "2.0" {
		return s.encodeError(encoder, req.ID, -32600, "invalid request: jsonrpc must be 2.0")
	}
	switch req.Method {
	case "initialize":
		result := map[string]any{
			"protocolVersion": protocolVersion,
			"serverInfo":      map[string]string{"name": serverName, "version": serverVersion},
			"capabilities": map[string]any{
				"tools": map[string]any{"listChanged": false},
			},
			"instructions": "Read-only facade over a dedicated Karte data root. Tools: karte_search, karte_read.",
		}
		return s.encodeResult(encoder, req.ID, result)
	case "notifications/initialized":
		// Notifications carry no response.
		return nil
	case "ping":
		return s.encodeResult(encoder, req.ID, map[string]any{})
	case "tools/list":
		return s.encodeResult(encoder, req.ID, map[string]any{
			"tools": []map[string]any{
				{
					"name": "karte_search",
					"description": "Search the dedicated Karte data root for Markdown documents. Honors project scope, tag scope, and the policy's sensitivity ceiling. Returns doc_id, relative path, SHA-256, tags, and snippet for each match.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query":         map[string]any{"type": "string", "description": "Text to search for in title, body, and frontmatter fields."},
							"top_k":         map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "default": 10},
							"projects":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Restrict the search to these projects. Omit for all projects allowed by policy."},
							"tags":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Restrict the search to documents carrying at least one of these tags."},
							"sensitivity":   map[string]any{"type": "string", "enum": []string{"public", "internal", "confidential", "restricted"}, "description": "Maximum sensitivity level to include. Defaults to the policy ceiling for the configured actor."},
						},
						"required": []string{"query"},
						"additionalProperties": false,
					},
				},
				{
					"name": "karte_read",
					"description": "Read a single document by its stable doc_id. The response carries the canonical body, relative path, SHA-256, and frontmatter metadata.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"doc_id": map[string]any{"type": "string", "description": "Stable document identifier from frontmatter (doc_id field)."},
						},
						"required": []string{"doc_id"},
						"additionalProperties": false,
					},
				},
			},
		})
	case "tools/call":
		return s.handleToolCall(encoder, req)
	default:
		return s.encodeError(encoder, req.ID, -32601, fmt.Sprintf("method not found: %s", req.Method))
	}
}

func (s *Server) encodeResult(encoder *json.Encoder, id json.RawMessage, result any) error {
	if id == nil {
		id = json.RawMessage("null")
	}
	return encoder.Encode(response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) encodeError(encoder *json.Encoder, id json.RawMessage, code int, message string) error {
	if id == nil {
		id = json.RawMessage("null")
	}
	return encoder.Encode(response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

// ---------- tool dispatch ----------

type searchParams struct {
	Query       string   `json:"query"`
	TopK        int      `json:"top_k"`
	Projects    []string `json:"projects"`
	Tags        []string `json:"tags"`
	Sensitivity string   `json:"sensitivity"`
}

type readParams struct {
	DocID string `json:"doc_id"`
}

func (s *Server) handleToolCall(encoder *json.Encoder, req request) error {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &call); err != nil {
		return s.encodeError(encoder, req.ID, -32602, "invalid params")
	}
	var payload any
	var err error
	switch call.Name {
	case "karte_search":
		payload, err = s.search(call.Arguments)
	case "karte_read":
		payload, err = s.read(call.Arguments)
	default:
		return s.encodeError(encoder, req.ID, -32602, fmt.Sprintf("unknown tool: %s", call.Name))
	}
	if err != nil {
		// Tool-level failures are returned as successful JSON-RPC frames
		// with isError=true, mirroring the MCP spec.
		return s.encodeResult(encoder, req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": err.Error()}},
			"isError": true,
		})
	}
	text, _ := json.Marshal(payload)
	return s.encodeResult(encoder, req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
	})
}

func (s *Server) search(raw json.RawMessage) (any, error) {
	var params searchParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(params.Query) == "" {
		return nil, errors.New("query is required")
	}
	if params.TopK <= 0 {
		params.TopK = 10
	}
	if params.TopK > 20 {
		params.TopK = 20
	}
	ceiling := params.Sensitivity
	if ceiling == "" {
		ceiling = s.policy.Actors[s.scope.Actor].SensitivityCeiling
	}
	request := contextcore.Request{
		ProtocolVersion: contextcore.ProtocolVersion,
		RequestID:       newRequestID(),
		Operation:       "search",
		Actor:           contextcore.Actor{Type: "tool", ID: s.scope.Actor},
		Scope:           contextcore.Scope{Projects: params.Projects, Tags: params.Tags, SensitivityCeiling: ceiling},
		Query:           &contextcore.SearchQuery{Text: params.Query, TopK: params.TopK},
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	results, diagnostics, status, err := s.service.Search(request, s.policy)
	// Record audit event for this MCP search call
	var auditResultCount int
	if status == "ok" || status == "denied" {
		auditResultCount = len(results)
		err = contextcore.RecordAudit(s.dataRoot, request.RequestID, request.Actor, "search", status, auditResultCount, "")
		if err != nil {
			return nil, err
		}
	} else if status == "invalid" || status == "error" {
		// Record audit event for invalid or error operations
		err = contextcore.RecordAudit(s.dataRoot, request.RequestID, request.Actor, "search", status, 0, err.Error())
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	return SearchOutput{Status: status, Results: results, Diagnostics: diagnostics}, nil
}

func (s *Server) read(raw json.RawMessage) (any, error) {
	var params readParams
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if strings.TrimSpace(params.DocID) == "" {
		return nil, errors.New("doc_id is required")
	}
	ceiling := s.policy.Actors[s.scope.Actor].SensitivityCeiling
	request := contextcore.Request{
		ProtocolVersion: contextcore.ProtocolVersion,
		RequestID:       newRequestID(),
		Operation:       "read",
		Actor:           contextcore.Actor{Type: "tool", ID: s.scope.Actor},
		Scope:           contextcore.Scope{Projects: []string{"*"}, SensitivityCeiling: ceiling},
		DocID:           &params.DocID,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	document, diagnostics, status, err := s.service.Read(request, s.policy)
	// Record audit event for this MCP read call
	var auditResultCount int
	if status == "ok" || status == "denied" {
		auditResultCount = 1
		if document == nil {
			auditResultCount = 0
		}
		err = contextcore.RecordAudit(s.dataRoot, request.RequestID, request.Actor, "read", status, auditResultCount, "")
		if err != nil {
			return nil, err
		}
	} else if status == "invalid" || status == "error" {
		// Record audit event for invalid or error operations
		err = contextcore.RecordAudit(s.dataRoot, request.RequestID, request.Actor, "read", status, 0, err.Error())
		if err != nil {
			return nil, err
		}
	}
	if err != nil {
		return nil, err
	}
	return ReadOutput{Status: status, Document: document, Diagnostics: diagnostics}, nil
}

func newRequestID() string {
	var buffer [12]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		// crypto/rand failures are unrecoverable on supported platforms;
		// fall back to a time-ordered identifier so the request still
		// satisfies the protocol pattern.
		return fmt.Sprintf("mcp-%d", time.Now().UnixNano())
	}
	return "mcp-" + hex.EncodeToString(buffer[:])
}

// ---------- diagnostics helpers ----------

// Summary returns a one-line description of the bound data root, useful for
// `--check` output. It deliberately omits any document content.
func (s *Server) Summary() string {
	actor := s.scope.Actor
	capabilities := []string{}
	for _, capability := range s.scope.Capabilities {
		capabilities = append(capabilities, capability)
	}
	sort.Strings(capabilities)
	digest := sha256.Sum256([]byte(s.dataRoot))
	return fmt.Sprintf("server=%s@%s data_root=%s (sha256=%s…) actor=%s capabilities=[%s] policy_ceiling=%s projects=%d",
		serverName, serverVersion, s.dataRoot, hex.EncodeToString(digest[:])[:12], actor,
		strings.Join(capabilities, ","), s.policy.Actors[actor].SensitivityCeiling,
		len(s.policy.Actors[actor].Projects))
}
