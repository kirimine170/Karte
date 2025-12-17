package rag

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"karte/internal/frontmatter"
)

// Context represents a search result context
type Context struct {
	DocID    string                 `json:"doc_id"`
	Path     string                 `json:"path"`
	ChunkID  string                 `json:"chunk_id"`
	Text     string                 `json:"text"`
	Score    float64                `json:"score"`
	Metadata map[string]interface{} `json:"metadata"`
}

// Engine is the RAG engine
type Engine struct {
	dataDir   string
	indexPath string
	index     *Index
	mu        sync.RWMutex
	watcher   *Watcher
}

// NewEngine creates a new RAG engine
func NewEngine(dataDir string) (*Engine, error) {
	indexPath := filepath.Join(dataDir, ".mdsys", "rag_index.json")

	index, err := LoadIndex(indexPath)
	if err != nil {
		// Create new index if it doesn't exist
		index = NewIndex()
	}

	engine := &Engine{
		dataDir:   dataDir,
		indexPath: indexPath,
		index:     index,
	}

	// Initialize watcher for async updates
	watcher, err := NewWatcher(engine, dataDir)
	if err != nil {
		// Log error but don't fail - watcher is optional
		fmt.Printf("Failed to create watcher: %v\n", err)
	} else {
		engine.watcher = watcher
		if err := watcher.Start(); err != nil {
			fmt.Printf("Failed to start watcher: %v\n", err)
		}
	}

	return engine, nil
}

// Search searches for relevant contexts using TF-IDF
func (e *Engine) Search(query string, projectID string, k int) ([]Context, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	if k <= 0 {
		k = 5
	}

	// Tokenize query
	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return []Context{}, nil
	}

	// Calculate IDF for all chunks
	idf := calculateIDF(e.index.Chunks)

	// Calculate query TF-IDF
	queryTFIDF := calculateQueryTFIDF(queryTokens, idf)

	// Calculate similarity scores for all chunks
	type scoredChunk struct {
		chunk Chunk
		score float64
	}

	var scoredChunks []scoredChunk
	for _, chunk := range e.index.Chunks {
		// Only consider chunks with TF-IDF scores
		if len(chunk.TFIDF) == 0 {
			continue
		}

		score := cosineSimilarity(queryTFIDF, chunk.TFIDF)
		if score > 0 {
			scoredChunks = append(scoredChunks, scoredChunk{
				chunk: chunk,
				score: score,
			})
		}
	}

	// Sort by score (descending)
	sort.Slice(scoredChunks, func(i, j int) bool {
		return scoredChunks[i].score > scoredChunks[j].score
	})

	// Take top k results
	if len(scoredChunks) > k {
		scoredChunks = scoredChunks[:k]
	}

	// Convert to Context
	contexts := make([]Context, 0, len(scoredChunks))
	for _, sc := range scoredChunks {
		meta := e.index.DocumentMeta[sc.chunk.DocID]
		contexts = append(contexts, Context{
			DocID:    sc.chunk.DocID,
			Path:     sc.chunk.Path,
			ChunkID:  sc.chunk.ChunkID,
			Text:     sc.chunk.Text,
			Score:    sc.score,
			Metadata: map[string]interface{}{
				"title":      meta.Title,
				"updated_at": meta.UpdatedAt,
			},
		})
	}

	return contexts, nil
}

// GetContext retrieves contexts for given document references
func (e *Engine) GetContext(docRefs []string, projectID string, budgetTokens int) ([]Context, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	var contexts []Context
	tokenCount := 0

	for _, docRef := range docRefs {
		if tokenCount >= budgetTokens {
			break
		}

		// Find chunks for this document
		for _, chunk := range e.index.Chunks {
			if chunk.DocID == docRef || chunk.Path == docRef {
				// Estimate tokens (rough: 1 token per 4 characters)
				chunkTokens := len(chunk.Text) / 4
				if tokenCount+chunkTokens > budgetTokens {
					break
				}

				meta := e.index.DocumentMeta[chunk.DocID]
				contexts = append(contexts, Context{
					DocID:    chunk.DocID,
					Path:     chunk.Path,
					ChunkID:  chunk.ChunkID,
					Text:     chunk.Text,
					Score:    1.0, // Full score for explicit references
					Metadata: map[string]interface{}{
						"title":      meta.Title,
						"updated_at": meta.UpdatedAt,
					},
				})

				tokenCount += chunkTokens
			}
		}
	}

	return contexts, nil
}

// UpdateIndex updates the RAG index by scanning content directory
func (e *Engine) UpdateIndex(projectID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Create new index
	newIndex := NewIndex()

	// Scan content directory
	contentDir := filepath.Join(e.dataDir, "content")
	if err := e.scanDirectory(contentDir, newIndex); err != nil {
		return fmt.Errorf("failed to scan content directory: %w", err)
	}

	// Calculate TF-IDF for all chunks
	e.calculateAllTFIDF(newIndex)

	// Save index
	if err := newIndex.Save(e.indexPath); err != nil {
		return fmt.Errorf("failed to save index: %w", err)
	}

	e.index = newIndex
	return nil
}

// scanDirectory scans a directory for markdown files and adds them to the index
func (e *Engine) scanDirectory(dir string, index *Index) error {
	return filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		if !strings.HasSuffix(path, ".md") {
			return nil
		}

		// Read file
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}

		// Parse frontmatter
		fm, body := frontmatter.ParseFrontMatter(string(content))

		// Get doc_id
		docID := ""
		if fm != nil && fm.DocID != "" {
			docID = fm.DocID
		}
		if docID == "" {
			// Skip files without doc_id
			return nil
		}

		// Get relative path
		relPath, err := filepath.Rel(e.dataDir, path)
		if err != nil {
			return err
		}
		relPath = filepath.ToSlash(relPath)

		// Get title
		title := ""
		if fm != nil && fm.Title != "" {
			title = fm.Title
		}
		if title == "" {
			title = filepath.Base(path)
			title = strings.TrimSuffix(title, ".md")
		}

		// Chunk the body (split by double newlines)
		chunks := strings.Split(body, "\n\n")
		for i, chunkText := range chunks {
			chunkText = strings.TrimSpace(chunkText)
			if chunkText == "" {
				continue
			}

			// Tokenize
			tokens := tokenize(chunkText)

			chunk := Chunk{
				DocID:   docID,
				Path:    relPath,
				ChunkID: fmt.Sprintf("chunk-%d", i),
				Text:    chunkText,
				Tokens:  tokens,
				TFIDF:   make(map[string]float64), // Will be calculated later
			}

			index.Chunks = append(index.Chunks, chunk)
		}

		// Update document metadata
		index.DocumentMeta[docID] = DocumentMeta{
			Path:      relPath,
			Title:     title,
			UpdatedAt: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		}

		return nil
	})
}

// calculateAllTFIDF calculates TF-IDF scores for all chunks in the index
func (e *Engine) calculateAllTFIDF(index *Index) {
	// Calculate IDF
	idf := calculateIDF(index.Chunks)

	// Calculate TF-IDF for each chunk
	for i := range index.Chunks {
		index.Chunks[i].TFIDF = calculateTFIDF(index.Chunks[i], idf)
	}
}
