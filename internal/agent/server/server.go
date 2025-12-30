package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"sync"

	"karte/internal/agent/audit"
	"karte/internal/agent/project"
	"karte/internal/agent/rag"
	"karte/internal/agent/write"
)

// JSONRPCRequest represents a JSON-RPC 2.0 request
type JSONRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	ID      interface{}     `json:"id"`
}

// JSONRPCResponse represents a JSON-RPC 2.0 response
type JSONRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	Result  interface{} `json:"result,omitempty"`
	Error   *RPCError   `json:"error,omitempty"`
	ID      interface{} `json:"id"`
}

// RPCError represents a JSON-RPC error
type RPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// Server is the IPC server
type Server struct {
	ctx      context.Context
	dataDir  string
	listener net.Listener
	project  *project.Manager
	rag      *rag.Engine
	write    *write.Manager
	audit    *audit.Logger
	mu       sync.RWMutex
	started  bool
}

// NewServer creates a new IPC server
func NewServer(ctx context.Context, dataDir string, projMgr *project.Manager, ragEngine *rag.Engine, writeMgr *write.Manager) (*Server, error) {
	auditLogger, err := audit.NewLogger(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create audit logger: %w", err)
	}

	return &Server{
		ctx:     ctx,
		dataDir: dataDir,
		project: projMgr,
		rag:     ragEngine,
		write:   writeMgr,
		audit:   auditLogger,
	}, nil
}

// Start starts the IPC server
func (s *Server) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("server already started")
	}

	// Determine socket path based on OS
	socketPath, err := s.getSocketPath()
	if err != nil {
		return fmt.Errorf("failed to get socket path: %w", err)
	}

	// Remove existing socket if it exists
	if runtime.GOOS != "windows" {
		if _, err := os.Stat(socketPath); err == nil {
			os.Remove(socketPath)
		}
	}

	// Create listener
	var listener net.Listener
	if runtime.GOOS == "windows" {
		// Named Pipe for Windows
		listener, err = net.Listen("tcp", socketPath)
		if err != nil {
			return fmt.Errorf("failed to listen on named pipe: %w", err)
		}
	} else {
		// Unix Domain Socket for macOS/Linux
		listener, err = net.Listen("unix", socketPath)
		if err != nil {
			return fmt.Errorf("failed to listen on unix socket: %w", err)
		}
	}

	s.listener = listener
	s.started = true

	// Start accepting connections
	go s.acceptConnections()

	log.Printf("IPC server started on %s", socketPath)
	return nil
}

// Stop stops the IPC server
func (s *Server) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started {
		return nil
	}

	if s.listener != nil {
		s.listener.Close()
		socketPath, _ := s.getSocketPath()
		if runtime.GOOS != "windows" {
			os.Remove(socketPath)
		}
	}

	s.started = false
	return nil
}

// getSocketPath returns the socket path based on OS
func (s *Server) getSocketPath() (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	username := currentUser.Username
	// Sanitize username for use in path
	username = filepath.Base(username)

	if runtime.GOOS == "windows" {
		// Named Pipe: \\.\pipe\karte-agent-{user}
		return fmt.Sprintf("\\\\.\\pipe\\karte-agent-%s", username), nil
	}

	// Unix Domain Socket: ~/.karte/agent.sock
	homeDir := currentUser.HomeDir
	socketDir := filepath.Join(homeDir, ".karte")
	if err := os.MkdirAll(socketDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create socket directory: %w", err)
	}

	return filepath.Join(socketDir, "agent.sock"), nil
}

// acceptConnections accepts incoming connections
func (s *Server) acceptConnections() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if s.ctx.Err() != nil {
				return
			}
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		go s.handleConnection(conn)
	}
}

// handleConnection handles a single connection
func (s *Server) handleConnection(conn net.Conn) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req JSONRPCRequest
		if err := decoder.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			log.Printf("Failed to decode request: %v", err)
			return
		}

		// Handle request
		resp := s.handleRequest(&req)

		// Send response
		if err := encoder.Encode(resp); err != nil {
			log.Printf("Failed to encode response: %v", err)
			return
		}
	}
}

// handleRequest handles a JSON-RPC request
func (s *Server) handleRequest(req *JSONRPCRequest) *JSONRPCResponse {
	// Ensure JSON-RPC version
	if req.JSONRPC != "2.0" {
		return &JSONRPCResponse{
			JSONRPC: "2.0",
			Error: &RPCError{
				Code:    -32600,
				Message: "Invalid Request",
			},
			ID: req.ID,
		}
	}

	// Route method
	var result interface{}
	var rpcErr *RPCError

	switch req.Method {
	case "RAG.Search":
		result, rpcErr = s.handleRAGSearch(req.Params)
	case "RAG.GetContext":
		result, rpcErr = s.handleRAGGetContext(req.Params)
	case "Write.CreateDocument":
		result, rpcErr = s.handleWriteCreateDocument(req.Params)
	default:
		rpcErr = &RPCError{
			Code:    -32601,
			Message: "Method not found",
		}
	}

	resp := &JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
	}

	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}

	return resp
}

// RAGSearchParams represents parameters for RAG.Search
type RAGSearchParams struct {
	Query     string `json:"query"`
	ProjectID string `json:"project_id,omitempty"`
	K         int    `json:"k,omitempty"`
}

// handleRAGSearch handles RAG.Search method
func (s *Server) handleRAGSearch(params json.RawMessage) (interface{}, *RPCError) {
	var p RAGSearchParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    err.Error(),
		}
	}

	if p.K <= 0 {
		p.K = 5 // Default to 5 results
	}

	// Resolve project ID
	projectID, err := s.project.ResolveProject(p.ProjectID)
	if err != nil {
		return nil, &RPCError{
			Code:    -32000,
			Message: "Failed to resolve project",
			Data:    err.Error(),
		}
	}

	// Search
	contexts, err := s.rag.Search(p.Query, projectID, p.K)
	if err != nil {
		return nil, &RPCError{
			Code:    -32000,
			Message: "RAG search failed",
			Data:    err.Error(),
		}
	}

	// Log search request with results
	if s.audit != nil {
		results := make([]audit.SearchResult, len(contexts))
		for i, ctx := range contexts {
			title := ""
			if ctx.Metadata != nil {
				if t, ok := ctx.Metadata["title"].(string); ok {
					title = t
				}
			}
			results[i] = audit.SearchResult{
				DocID:   ctx.DocID,
				Path:    ctx.Path,
				ChunkID: ctx.ChunkID,
				Score:   ctx.Score,
				Title:   title,
				Text:    ctx.Text,
			}
		}
		_ = s.audit.LogSearchWithResults(projectID, p.Query, results)
	}

	return contexts, nil
}

// RAGGetContextParams represents parameters for RAG.GetContext
type RAGGetContextParams struct {
	DocRefs      []string `json:"doc_refs"`
	ProjectID    string   `json:"project_id,omitempty"`
	BudgetTokens int      `json:"budget_tokens,omitempty"`
}

// handleRAGGetContext handles RAG.GetContext method
func (s *Server) handleRAGGetContext(params json.RawMessage) (interface{}, *RPCError) {
	var p RAGGetContextParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    err.Error(),
		}
	}

	if p.BudgetTokens <= 0 {
		p.BudgetTokens = 2000 // Default budget
	}

	// Resolve project ID
	projectID, err := s.project.ResolveProject(p.ProjectID)
	if err != nil {
		return nil, &RPCError{
			Code:    -32000,
			Message: "Failed to resolve project",
			Data:    err.Error(),
		}
	}

	// Get context
	contexts, err := s.rag.GetContext(p.DocRefs, projectID, p.BudgetTokens)
	if err != nil {
		return nil, &RPCError{
			Code:    -32000,
			Message: "RAG get context failed",
			Data:    err.Error(),
		}
	}

	return contexts, nil
}

// WriteCreateDocumentParams represents parameters for Write.CreateDocument
type WriteCreateDocumentParams struct {
	ProjectID string                 `json:"project_id,omitempty"`
	PathHint  string                 `json:"path_hint,omitempty"`
	Markdown  string                 `json:"markdown"`
	Meta      map[string]interface{} `json:"meta,omitempty"`
	RequestID string                 `json:"request_id"`
}

// handleWriteCreateDocument handles Write.CreateDocument method
func (s *Server) handleWriteCreateDocument(params json.RawMessage) (interface{}, *RPCError) {
	var p WriteCreateDocumentParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &RPCError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    err.Error(),
		}
	}

	// Resolve project ID
	projectID, err := s.project.ResolveProject(p.ProjectID)
	if err != nil {
		return nil, &RPCError{
			Code:    -32000,
			Message: "Failed to resolve project",
			Data:    err.Error(),
		}
	}

	// Create document
	result, err := s.write.CreateDocument(projectID, p.PathHint, p.Markdown, p.Meta, p.RequestID)
	if err != nil {
		return nil, &RPCError{
			Code:    -32000,
			Message: "Failed to create document",
			Data:    err.Error(),
		}
	}

	// Log write request
	if s.audit != nil {
		_ = s.audit.LogWrite(projectID, p.RequestID, result.DocID, p.Meta)
	}

	return result, nil
}
