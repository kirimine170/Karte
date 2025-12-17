package agent

import (
	"context"
	"fmt"
	"sync"

	"karte/internal/agent/project"
	"karte/internal/agent/rag"
	"karte/internal/agent/server"
	"karte/internal/agent/write"
)

// Agent is the main agent instance
type Agent struct {
	ctx      context.Context
	dataDir  string
	server   *server.Server
	project  *project.Manager
	rag      *rag.Engine
	write    *write.Manager
	mu       sync.RWMutex
	started  bool
}

// NewAgent creates a new agent instance
func NewAgent(ctx context.Context, dataDir string) (*Agent, error) {
	// Initialize project manager
	projMgr, err := project.NewManager(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create project manager: %w", err)
	}

	// Initialize RAG engine
	ragEngine, err := rag.NewEngine(dataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create RAG engine: %w", err)
	}

	// Initialize write manager
	writeMgr, err := write.NewManager(dataDir, projMgr, ragEngine)
	if err != nil {
		return nil, fmt.Errorf("failed to create write manager: %w", err)
	}

	// Initialize IPC server
	ipcServer, err := server.NewServer(ctx, dataDir, projMgr, ragEngine, writeMgr)
	if err != nil {
		return nil, fmt.Errorf("failed to create IPC server: %w", err)
	}

	return &Agent{
		ctx:     ctx,
		dataDir: dataDir,
		server:  ipcServer,
		project: projMgr,
		rag:     ragEngine,
		write:   writeMgr,
	}, nil
}

// Start starts the agent
func (a *Agent) Start() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return fmt.Errorf("agent already started")
	}

	if err := a.server.Start(); err != nil {
		return fmt.Errorf("failed to start IPC server: %w", err)
	}

	a.started = true
	return nil
}

// Stop stops the agent
func (a *Agent) Stop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if !a.started {
		return nil
	}

	if err := a.server.Stop(); err != nil {
		return fmt.Errorf("failed to stop IPC server: %w", err)
	}

	a.started = false
	return nil
}
