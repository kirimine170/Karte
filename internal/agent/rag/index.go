package rag

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

// Index represents the RAG index structure
type Index struct {
	Version      int                     `json:"version"`
	Chunks       []Chunk                 `json:"chunks"`
	DocumentMeta map[string]DocumentMeta `json:"document_meta"`
	mu           sync.RWMutex
}

// Chunk represents a text chunk in the index
type Chunk struct {
	DocID   string             `json:"doc_id"`
	Path    string             `json:"path"`
	ChunkID string             `json:"chunk_id"`
	Text    string             `json:"text"`
	Tokens  []string           `json:"tokens"`
	TFIDF   map[string]float64 `json:"tfidf"`
}

// DocumentMeta represents metadata for a document
type DocumentMeta struct {
	Path      string `json:"path"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

// NewIndex creates a new empty index
func NewIndex() *Index {
	return &Index{
		Version:      1,
		Chunks:       []Chunk{},
		DocumentMeta: make(map[string]DocumentMeta),
	}
}

// LoadIndex loads an index from file
func LoadIndex(filePath string) (*Index, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return NewIndex(), nil
		}
		return nil, fmt.Errorf("failed to read index file: %w", err)
	}

	var index Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil, fmt.Errorf("failed to parse index file: %w", err)
	}

	return &index, nil
}

// Save saves the index to file
func (idx *Index) Save(filePath string) error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal index: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write index file: %w", err)
	}

	return nil
}
