package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"

	"karte/internal/agent/rag"
)

// ExportData represents the complete TF-IDF export structure
type ExportData struct {
	Metadata        Metadata                  `json:"metadata"`
	IDFScores       map[string]float64       `json:"idf_scores"`
	Documents       []DocumentExport          `json:"documents"`
	TokenStatistics map[string]TokenStats    `json:"token_statistics"`
}

// Metadata contains summary information about the export
type Metadata struct {
	TotalDocuments    int    `json:"total_documents"`
	TotalChunks       int    `json:"total_chunks"`
	TotalUniqueTokens int    `json:"total_unique_tokens"`
	GeneratedAt       string `json:"generated_at"`
}

// DocumentExport represents a document with its chunks and TF-IDF data
type DocumentExport struct {
	DocID   string          `json:"doc_id"`
	Path    string          `json:"path"`
	Title   string          `json:"title"`
	Chunks  []ChunkExport   `json:"chunks"`
}

// ChunkExport represents a chunk with its TF-IDF data
type ChunkExport struct {
	ChunkID     string             `json:"chunk_id"`
	Text        string             `json:"text"`
	Tokens      []string           `json:"tokens"`
	TFScores    map[string]float64 `json:"tf_scores"`
	TFIDFScores map[string]float64 `json:"tfidf_scores"`
}

// TokenStats contains statistics for a token
type TokenStats struct {
	DocumentFrequency int `json:"document_frequency"`
	IDF               float64 `json:"idf"`
	AppearsInChunks   int `json:"appears_in_chunks"`
}

func main() {
	var dataDir string
	var outputPath string
	var reindex bool
	flag.StringVar(&dataDir, "data-dir", "", "Path to karte_data directory (default: auto-detect)")
	flag.StringVar(&outputPath, "output", "tfidf_export.json", "Output file path")
	flag.BoolVar(&reindex, "reindex", false, "Rebuild RAG index (rag_index.json) before exporting (recommended after tokenizer changes)")
	flag.Parse()

	// Auto-detect data directory if not provided
	if dataDir == "" {
		exePath, err := os.Executable()
		if err != nil {
			log.Fatalf("Failed to get executable path: %v", err)
		}
		exeDir := filepath.Dir(exePath)
		// Check if running inside .app bundle
		if filepath.Base(exeDir) == "MacOS" {
			contentsDir := filepath.Dir(exeDir)
			appBundleDir := filepath.Dir(contentsDir)
			dataDir = filepath.Join(filepath.Dir(appBundleDir), "karte_data")
		} else {
			dataDir = filepath.Join(exeDir, "karte_data")
		}
	}

	// Ensure data directory exists
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		log.Fatalf("Data directory does not exist: %s", dataDir)
	}

	// Ensure .mdsys exists (Index.Save does not mkdir)
	if err := os.MkdirAll(filepath.Join(dataDir, ".mdsys"), 0755); err != nil {
		log.Fatalf("Failed to create .mdsys directory: %v", err)
	}

	// Optionally rebuild index using the current code (ensures tokenizer changes are reflected)
	if reindex {
		e, err := rag.NewEngine(dataDir)
		if err != nil {
			log.Fatalf("Failed to create RAG engine: %v", err)
		}
		if err := e.UpdateIndex(""); err != nil {
			log.Fatalf("Failed to rebuild index: %v", err)
		}
	}

	// Load index
	indexPath := filepath.Join(dataDir, ".mdsys", "rag_index.json")
	index, err := rag.LoadIndex(indexPath)
	if err != nil {
		log.Fatalf("Failed to load index: %v", err)
	}

	// Check if index is empty
	if len(index.Chunks) == 0 {
		log.Fatalf("Index is empty. Please run index update first.")
	}

	// Calculate IDF for all chunks
	idf := rag.CalculateIDF(index.Chunks)

	// Calculate TF-IDF for all chunks (recalculate to ensure consistency)
	for i := range index.Chunks {
		index.Chunks[i].TFIDF = rag.CalculateTFIDF(index.Chunks[i], idf)
	}

	// Build export data
	exportData := buildExportData(index, idf)

	// Write to file
	outputJSON, err := json.MarshalIndent(exportData, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal export data: %v", err)
	}

	if err := os.WriteFile(outputPath, outputJSON, 0644); err != nil {
		log.Fatalf("Failed to write output file: %v", err)
	}

	fmt.Printf("TF-IDF export completed successfully!\n")
	fmt.Printf("Output file: %s\n", outputPath)
	fmt.Printf("Total documents: %d\n", exportData.Metadata.TotalDocuments)
	fmt.Printf("Total chunks: %d\n", exportData.Metadata.TotalChunks)
	fmt.Printf("Total unique tokens: %d\n", exportData.Metadata.TotalUniqueTokens)
}

func buildExportData(index *rag.Index, idf map[string]float64) *ExportData {
	// Collect unique tokens
	uniqueTokens := make(map[string]bool)
	tokenChunkCount := make(map[string]int)
	docTokenMap := make(map[string]map[string]bool) // docID -> token set

	// Group chunks by document
	docChunks := make(map[string][]rag.Chunk)
	for _, chunk := range index.Chunks {
		docChunks[chunk.DocID] = append(docChunks[chunk.DocID], chunk)

		// Collect tokens and statistics
		for _, token := range chunk.Tokens {
			uniqueTokens[token] = true
			tokenChunkCount[token]++

			if docTokenMap[chunk.DocID] == nil {
				docTokenMap[chunk.DocID] = make(map[string]bool)
			}
			docTokenMap[chunk.DocID][token] = true
		}
	}

	// Build token statistics
	tokenStats := make(map[string]TokenStats)
	for token := range uniqueTokens {
		docFreq := 0
		for _, tokenSet := range docTokenMap {
			if tokenSet[token] {
				docFreq++
			}
		}

		tokenStats[token] = TokenStats{
			DocumentFrequency: docFreq,
			IDF:               idf[token],
			AppearsInChunks:   tokenChunkCount[token],
		}
	}

	// Build document exports
	documents := make([]DocumentExport, 0, len(docChunks))
	for docID, chunks := range docChunks {
		meta := index.DocumentMeta[docID]
		
		// Sort chunks by chunk ID for consistency
		sort.Slice(chunks, func(i, j int) bool {
			return chunks[i].ChunkID < chunks[j].ChunkID
		})

		chunkExports := make([]ChunkExport, 0, len(chunks))
		for _, chunk := range chunks {
			tf := rag.CalculateTF(chunk.Tokens)
			
			chunkExports = append(chunkExports, ChunkExport{
				ChunkID:     chunk.ChunkID,
				Text:        chunk.Text,
				Tokens:      chunk.Tokens,
				TFScores:    tf,
				TFIDFScores: chunk.TFIDF,
			})
		}

		documents = append(documents, DocumentExport{
			DocID:  docID,
			Path:   meta.Path,
			Title:  meta.Title,
			Chunks: chunkExports,
		})
	}

	// Sort documents by doc ID for consistency
	sort.Slice(documents, func(i, j int) bool {
		return documents[i].DocID < documents[j].DocID
	})

	return &ExportData{
		Metadata: Metadata{
			TotalDocuments:    len(docChunks),
			TotalChunks:       len(index.Chunks),
			TotalUniqueTokens: len(uniqueTokens),
			GeneratedAt:       time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		},
		IDFScores:       idf,
		Documents:       documents,
		TokenStatistics: tokenStats,
	}
}
