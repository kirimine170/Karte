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
//
// The startup check is only a startup check. Every tools/call re-reads
// policy.json and mcp-scope.json from disk and re-validates the marker
// against the current policy, so a grant revoked, narrowed, or corrupted
// after the process started is denied on the very next call. The server
// never continues from the startup snapshot and never falls back to the
// shared DefaultPolicy: a dedicated root fails closed.
package mcp

import (
	"bufio"
	"bytes"
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
	maxFrameBytes   = 1 << 20
)

// Result payloads. They mirror contextcore types so a host can reuse the
// same rendering code for both the filesystem spool and this facade.
type SearchOutput struct {
	Status      string                     `json:"status"`
	Results     []contextcore.SearchResult `json:"results"`
	Diagnostics []contextcore.Diagnostic   `json:"diagnostics"`
}

type ReadOutput struct {
	Status      string                   `json:"status"`
	Document    *contextcore.Document    `json:"document"`
	Diagnostics []contextcore.Diagnostic `json:"diagnostics"`
}

// Server bundles the read-only contextcore services behind a small interface
// so the JSON-RPC dispatch in this file stays focused on the wire format.
type Server struct {
	service *contextcore.Service
	// policy and scope are the startup snapshot used only for diagnostics
	// (Summary). Every call re-derives the live grant from disk via
	// currentScope, so these fields are never an authorization source.
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
// the input closes or ctx is cancelled. It returns transport failures;
// malformed requests receive JSON-RPC error responses on out.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	// Closing an input stream on cancellation releases a blocked read. The
	// command passes os.Stdin; tests also use an io.Pipe.
	stop := make(chan struct{})
	if closer, ok := in.(io.Closer); ok {
		go func() {
			select {
			case <-ctx.Done():
				closer.Close()
			case <-stop:
			}
		}()
	}
	defer close(stop)
	reader := bufio.NewReaderSize(in, 32*1024)
	encoder := json.NewEncoder(out)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, oversized, readErr := readFrame(reader)
		if err := ctx.Err(); err != nil {
			return err
		}
		if oversized {
			if err := s.encodeError(encoder, nil, -32000, "frame too large"); err != nil {
				return err
			}
		} else if len(line) > 0 {
			if err := s.dispatch(encoder, string(line)); err != nil {
				return err
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

// readFrame bounds retained input to maxFrameBytes, excluding the line
// delimiter. After overflow it discards chunks until the next delimiter so
// the following JSON-RPC frame can still be processed.
func readFrame(reader *bufio.Reader) ([]byte, bool, error) {
	frame := make([]byte, 0, 32*1024)
	oversized := false
	for {
		part, err := reader.ReadSlice('\n')
		end := len(part) > 0 && part[len(part)-1] == '\n'
		if end {
			part = part[:len(part)-1]
		}
		if !oversized {
			if len(part) > maxFrameBytes-len(frame) {
				oversized = true
				frame = nil
			} else {
				frame = append(frame, part...)
			}
		}
		if end || err == io.EOF || (err != nil && err != bufio.ErrBufferFull) {
			return frame, oversized, err
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
	if req.ID == nil {
		// JSON-RPC notifications do not receive a response, including unknown
		// notifications. A request with an explicit null ID is still a request.
		return nil
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
					"name":        "karte_search",
					"description": "Search the dedicated Karte data root for Markdown documents. Honors project scope, tag scope, and the policy's sensitivity ceiling. Returns doc_id, relative path, SHA-256, tags, and snippet for each match.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query":       map[string]any{"type": "string", "description": "Text to search for in title, body, and frontmatter fields.", "minLength": 1, "maxLength": 2048},
							"top_k":       map[string]any{"type": "integer", "minimum": 1, "maximum": 20, "default": 10},
							"projects":    map[string]any{"type": "array", "maxItems": 64, "items": map[string]any{"type": "string", "maxLength": 64, "pattern": `^(\*|[a-z0-9][a-z0-9._-]{0,63})$`}, "description": "Restrict the search to these projects. Omit for all projects allowed by policy."},
							"tags":        map[string]any{"type": "array", "maxItems": 64, "items": map[string]any{"type": "string", "minLength": 1, "maxLength": 128}, "description": "Restrict the search to documents carrying every requested tag."},
							"sensitivity": map[string]any{"type": "string", "enum": []string{"public", "internal", "confidential", "restricted"}, "description": "Maximum sensitivity level to include. Defaults to the policy ceiling for the configured actor."},
						},
						"required":             []string{"query"},
						"additionalProperties": false,
					},
				},
				{
					"name":        "karte_read",
					"description": "Read a single document by its stable doc_id. The response carries the canonical body, relative path, SHA-256, and frontmatter metadata.",
					"inputSchema": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"doc_id": map[string]any{"type": "string", "description": "Stable document identifier from frontmatter (doc_id field).", "minLength": 1, "maxLength": 256},
						},
						"required":             []string{"doc_id"},
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
	Query       string          `json:"query"`
	TopK        json.RawMessage `json:"top_k"`
	Projects    json.RawMessage `json:"projects"`
	Tags        json.RawMessage `json:"tags"`
	Sensitivity json.RawMessage `json:"sensitivity"`
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
		// Redact filesystem details from error messages for security
		// Log the detailed error for local diagnostics but return a sanitized message to the client
		fmt.Fprintf(os.Stderr, "karte-mcp: tool error: %v\n", err)
		return s.encodeResult(encoder, req.ID, map[string]any{
			"content": []map[string]any{{"type": "text", "text": "processing failed"}},
			"isError": true,
		})
	}
	text, _ := json.Marshal(payload)
	return s.encodeResult(encoder, req.ID, map[string]any{
		"content": []map[string]any{{"type": "text", "text": string(text)}},
	})
}

// loadCurrentPolicy strictly re-reads policy.json for per-call
// authorization. Unlike contextcore.LoadPolicy, a missing, unreadable, or
// malformed file is an error: a dedicated root must fail closed instead of
// falling back to DefaultPolicy or to the startup snapshot.
func (s *Server) loadCurrentPolicy() (contextcore.Policy, error) {
	data, err := os.ReadFile(filepath.Join(s.dataRoot, ".mdsys", "context", "v1", "policy.json"))
	if err != nil {
		return contextcore.Policy{}, fmt.Errorf("reload context policy: %w", err)
	}
	return contextcore.ParsePolicy(data)
}

// currentScope re-derives the dedicated root's explicit grant for every
// call. The policy file must still exist and be valid, and the scope marker
// must still parse and name an actor whose current policy grants both
// search and read. Any missing, corrupted, or revoked state is an error,
// never a fallback to a stale in-memory state.
func (s *Server) currentScope() (contextcore.MCPScope, contextcore.Policy, error) {
	policy, err := s.loadCurrentPolicy()
	if err != nil {
		return contextcore.MCPScope{}, contextcore.Policy{}, err
	}
	scope, err := contextcore.LoadMCPScope(s.dataRoot, policy)
	if err != nil {
		return contextcore.MCPScope{}, contextcore.Policy{}, err
	}
	return scope, policy, nil
}

func (s *Server) search(raw json.RawMessage) (any, error) {
	requestID := newRequestID()
	var params searchParams
	if err := decodeArguments(raw, &params); err != nil {
		return nil, s.rejectInvalidArguments(requestID, "search", fmt.Errorf("invalid arguments: %w", err))
	}
	if strings.TrimSpace(params.Query) == "" {
		return nil, s.rejectInvalidArguments(requestID, "search", errors.New("query is required"))
	}
	topK := 10
	if params.TopK != nil {
		if bytes.Equal(bytes.TrimSpace(params.TopK), []byte("null")) || json.Unmarshal(params.TopK, &topK) != nil || topK < 1 || topK > 20 {
			return nil, s.rejectInvalidArguments(requestID, "search", errors.New("top_k must be between 1 and 20"))
		}
	}
	var projects, tags []string
	if err := decodeOptionalArgument(params.Projects, &projects); err != nil {
		return nil, s.rejectInvalidArguments(requestID, "search", fmt.Errorf("invalid projects: %w", err))
	}
	if err := decodeOptionalArgument(params.Tags, &tags); err != nil {
		return nil, s.rejectInvalidArguments(requestID, "search", fmt.Errorf("invalid tags: %w", err))
	}
	var sensitivity string
	if err := decodeOptionalArgument(params.Sensitivity, &sensitivity); err != nil {
		return nil, s.rejectInvalidArguments(requestID, "search", fmt.Errorf("invalid sensitivity: %w", err))
	}
	if params.Sensitivity != nil && sensitivity != "public" && sensitivity != "internal" && sensitivity != "confidential" && sensitivity != "restricted" {
		return nil, s.rejectInvalidArguments(requestID, "search", errors.New("invalid sensitivity"))
	}
	scope, policy, policyErr := s.currentScope()
	if policyErr != nil {
		// Post-revocation access attempts must still be audited, even if
		// they cannot be authorized.
		actor := contextcore.Actor{Type: "tool", ID: s.scope.Actor}
		auditErr := contextcore.RecordAudit(s.dataRoot, requestID, actor, "search", "error", 0, "policy_reload_failed")
		if auditErr != nil {
			fmt.Fprintf(os.Stderr, "karte-mcp: audit error: %v\n", auditErr)
		}
		return nil, policyErr
	}
	ceiling := sensitivity
	if ceiling == "" {
		ceiling = policy.Actors[scope.Actor].SensitivityCeiling
	}
	request := contextcore.Request{
		ProtocolVersion: contextcore.ProtocolVersion,
		RequestID:       requestID,
		Operation:       "search",
		Actor:           contextcore.Actor{Type: "tool", ID: scope.Actor},
		Scope:           contextcore.Scope{Projects: projects, Tags: tags, SensitivityCeiling: ceiling},
		Query:           &contextcore.SearchQuery{Text: params.Query, TopK: topK},
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	results, diagnostics, status, searchErr := s.service.Search(request, policy)
	// Record audit event for this MCP search call
	var auditErr error
	if status == "ok" || status == "denied" {
		auditResultCount := len(results)
		err := contextcore.RecordAudit(s.dataRoot, request.RequestID, request.Actor, "search", status, auditResultCount, "")
		if err != nil {
			auditErr = err
		}
	} else if status == "invalid" || status == "error" {
		// Record audit event for invalid or error operations
		err := contextcore.RecordAudit(s.dataRoot, request.RequestID, request.Actor, "search", status, 0, auditErrorCode(searchErr))
		if err != nil {
			auditErr = err
		}
	}
	if auditErr != nil {
		return nil, auditErr
	}
	// Return the original service error if there was one
	if searchErr != nil {
		return nil, searchErr
	}
	return SearchOutput{Status: status, Results: results, Diagnostics: diagnostics}, nil
}

func (s *Server) read(raw json.RawMessage) (any, error) {
	requestID := newRequestID()
	var params readParams
	if err := decodeArguments(raw, &params); err != nil {
		return nil, s.rejectInvalidArguments(requestID, "read", fmt.Errorf("invalid arguments: %w", err))
	}
	if strings.TrimSpace(params.DocID) == "" {
		return nil, s.rejectInvalidArguments(requestID, "read", errors.New("doc_id is required"))
	}
	scope, policy, policyErr := s.currentScope()
	if policyErr != nil {
		// Post-revocation access attempts must still be audited, even if
		// they cannot be authorized.
		actor := contextcore.Actor{Type: "tool", ID: s.scope.Actor}
		auditErr := contextcore.RecordAudit(s.dataRoot, requestID, actor, "read", "error", 0, "policy_reload_failed")
		if auditErr != nil {
			fmt.Fprintf(os.Stderr, "karte-mcp: audit error: %v\n", auditErr)
		}
		return nil, policyErr
	}
	ceiling := policy.Actors[scope.Actor].SensitivityCeiling
	request := contextcore.Request{
		ProtocolVersion: contextcore.ProtocolVersion,
		RequestID:       requestID,
		Operation:       "read",
		Actor:           contextcore.Actor{Type: "tool", ID: scope.Actor},
		Scope:           contextcore.Scope{Projects: []string{"*"}, SensitivityCeiling: ceiling},
		DocID:           &params.DocID,
		CreatedAt:       time.Now().UTC().Format(time.RFC3339),
	}
	document, diagnostics, status, readErr := s.service.Read(request, policy)
	// Record audit event for this MCP read call
	var auditErr error
	if status == "ok" || status == "denied" {
		auditResultCount := 1
		if document == nil {
			auditResultCount = 0
		}
		err := contextcore.RecordAudit(s.dataRoot, request.RequestID, request.Actor, "read", status, auditResultCount, "")
		if err != nil {
			auditErr = err
		}
	} else if status == "invalid" || status == "error" {
		// Record audit event for invalid or error operations
		err := contextcore.RecordAudit(s.dataRoot, request.RequestID, request.Actor, "read", status, 0, auditErrorCode(readErr))
		if err != nil {
			auditErr = err
		}
	}
	if auditErr != nil {
		return nil, auditErr
	}
	// Return the original service error if there was one
	if readErr != nil {
		return nil, readErr
	}
	return ReadOutput{Status: status, Document: document, Diagnostics: diagnostics}, nil
}

func (s *Server) rejectInvalidArguments(requestID, operation string, validationErr error) error {
	actor := contextcore.Actor{Type: "tool", ID: s.scope.Actor}
	status, code := "invalid", "invalid_arguments"
	if scope, _, err := s.currentScope(); err == nil {
		actor.ID = scope.Actor
	} else {
		// A broken live grant cannot provide an actor identity. Record the
		// reload failure against the startup actor, as for valid calls.
		status, code = "error", "policy_reload_failed"
		validationErr = errors.Join(validationErr, err)
	}
	if auditErr := contextcore.RecordAudit(s.dataRoot, requestID, actor, operation, status, 0, code); auditErr != nil {
		return errors.Join(validationErr, auditErr)
	}
	return validationErr
}

func auditErrorCode(err error) string {
	var validation *contextcore.ValidationError
	if errors.As(err, &validation) {
		return validation.Code
	}
	return "processing_failed"
}

func decodeArguments(raw json.RawMessage, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("trailing argument data")
	}
	return nil
}

func decodeOptionalArgument(raw json.RawMessage, out any) error {
	if raw == nil {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null is not allowed")
	}
	return json.Unmarshal(raw, out)
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
