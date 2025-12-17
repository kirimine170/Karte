package index

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Chunk represents a text chunk in the index
type Chunk struct {
	DocID   string            `json:"doc_id"`
	Path    string            `json:"path"`
	ChunkID string            `json:"chunk_id"`
	Text    string            `json:"text"`
	Tokens  []string          `json:"tokens"`
	TFIDF   map[string]float64 `json:"tfidf"`
}

// DocumentMeta represents document metadata
type DocumentMeta struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

// Index represents the RAG index structure
type Index struct {
	Version      int                      `json:"version"`
	Chunks       []Chunk                 `json:"chunks"`
	DocumentMeta map[string]DocumentMeta `json:"document_meta"`
}

// Manager manages the RAG index
type Manager struct {
	indexPath string
	index     *Index
	mu        sync.RWMutex
}

// NewManager creates a new index manager
func NewManager(dataDir string) (*Manager, error) {
	mdsysDir := filepath.Join(dataDir, ".mdsys")
	if err := os.MkdirAll(mdsysDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .mdsys directory: %w", err)
	}

	indexPath := filepath.Join(mdsysDir, "rag_index.json")

	mgr := &Manager{
		indexPath: indexPath,
		index: &Index{
			Version:      1,
			Chunks:       []Chunk{},
			DocumentMeta: make(map[string]DocumentMeta),
		},
	}

	// Load existing index if it exists
	if _, err := os.Stat(indexPath); err == nil {
		if err := mgr.load(); err != nil {
			return nil, fmt.Errorf("failed to load index: %w", err)
		}
	}

	return mgr, nil
}

// load loads the index from file
func (m *Manager) load() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := os.ReadFile(m.indexPath)
	if err != nil {
		return err
	}

	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return err
	}

	m.index = &index
	return nil
}

// save saves the index to file
func (m *Manager) save() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	data, err := json.MarshalIndent(m.index, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(m.indexPath, data, 0644)
}

// GetIndex returns a copy of the index
func (m *Manager) GetIndex() *Index {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy
	indexCopy := *m.index
	indexCopy.Chunks = make([]Chunk, len(m.index.Chunks))
	copy(indexCopy.Chunks, m.index.Chunks)
	indexCopy.DocumentMeta = make(map[string]DocumentMeta)
	for k, v := range m.index.DocumentMeta {
		indexCopy.DocumentMeta[k] = v
	}

	return &indexCopy
}

