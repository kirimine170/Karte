package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Project represents a project configuration
type Project struct {
	ProjectID string    `json:"project_id"`
	CreatedAt string    `json:"created_at"`
	Name      string    `json:"name,omitempty"`
}

// Manager manages project identification
type Manager struct {
	dataDir     string
	projectPath string
	mu          sync.RWMutex
}

// NewManager creates a new project manager
func NewManager(dataDir string) (*Manager, error) {
	projectPath := filepath.Join(dataDir, "karte.project.json")
	
	// Ensure .mdsys directory exists
	mdsysDir := filepath.Join(dataDir, ".mdsys")
	if err := os.MkdirAll(mdsysDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .mdsys directory: %w", err)
	}

	mgr := &Manager{
		dataDir:     dataDir,
		projectPath: projectPath,
	}

	// Initialize project if it doesn't exist
	if _, err := os.Stat(projectPath); os.IsNotExist(err) {
		if err := mgr.initializeProject(); err != nil {
			return nil, fmt.Errorf("failed to initialize project: %w", err)
		}
	}

	return mgr, nil
}

// initializeProject creates a new project configuration
func (m *Manager) initializeProject() error {
	project := Project{
		ProjectID: uuid.New().String(),
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	return m.saveProject(&project)
}

// GetProjectID returns the current project ID
func (m *Manager) GetProjectID() (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	project, err := m.loadProject()
	if err != nil {
		return "", err
	}

	return project.ProjectID, nil
}

// ResolveProject resolves project ID using the specified priority:
// 1. Explicit project_id from context
// 2. LRU cache
// 3. Default project
func (m *Manager) ResolveProject(explicitID string) (string, error) {
	if explicitID != "" {
		return explicitID, nil
	}

	// For now, always return the default project ID
	// LRU cache will be implemented later
	return m.GetProjectID()
}

// loadProject loads project configuration from file
func (m *Manager) loadProject() (*Project, error) {
	data, err := os.ReadFile(m.projectPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read project file: %w", err)
	}

	var project Project
	if err := json.Unmarshal(data, &project); err != nil {
		return nil, fmt.Errorf("failed to parse project file: %w", err)
	}

	return &project, nil
}

// saveProject saves project configuration to file
func (m *Manager) saveProject(project *Project) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := json.MarshalIndent(project, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal project: %w", err)
	}

	if err := os.WriteFile(m.projectPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write project file: %w", err)
	}

	return nil
}
