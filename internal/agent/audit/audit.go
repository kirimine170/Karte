package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// LogEntry represents an audit log entry
type LogEntry struct {
	Timestamp string                 `json:"timestamp"`
	Type      string                 `json:"type"` // "search" or "write"
	ProjectID string                 `json:"project_id,omitempty"`
	Query     string                 `json:"query,omitempty"`      // For search
	RequestID string                 `json:"request_id,omitempty"` // For write
	DocID     string                 `json:"doc_id,omitempty"`     // For write
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Logger handles audit logging
type Logger struct {
	logPath string
	mu      sync.Mutex
}

// NewLogger creates a new audit logger
func NewLogger(dataDir string) (*Logger, error) {
	mdsysDir := filepath.Join(dataDir, ".mdsys")
	if err := os.MkdirAll(mdsysDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create .mdsys directory: %w", err)
	}

	logPath := filepath.Join(mdsysDir, "audit.log")

	return &Logger{
		logPath: logPath,
	}, nil
}

// LogSearch logs a search request
func (l *Logger) LogSearch(projectID, query string) error {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Type:      "search",
		ProjectID: projectID,
		Query:     query,
	}

	return l.logEntry(entry)
}

// SearchResult represents a search result for logging
type SearchResult struct {
	DocID    string  `json:"doc_id"`
	Path     string  `json:"path"`
	ChunkID  string  `json:"chunk_id"`
	Score    float64 `json:"score"`
	Title    string  `json:"title,omitempty"`
	Text     string  `json:"text,omitempty"` // Truncated to first 200 chars
}

// LogSearchWithResults logs a search request with results
func (l *Logger) LogSearchWithResults(projectID, query string, results []SearchResult) error {
	// Truncate text in results to avoid huge log entries
	logResults := make([]SearchResult, len(results))
	for i, r := range results {
		logResults[i] = r
		if len(r.Text) > 200 {
			logResults[i].Text = r.Text[:200] + "..."
		}
	}

	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Type:      "search",
		ProjectID: projectID,
		Query:     query,
		Metadata: map[string]interface{}{
			"result_count": len(results),
			"results":      logResults,
		},
	}

	return l.logEntry(entry)
}

// LogWrite logs a document write
func (l *Logger) LogWrite(projectID, requestID, docID string, metadata map[string]interface{}) error {
	entry := LogEntry{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Type:      "write",
		ProjectID: projectID,
		RequestID: requestID,
		DocID:     docID,
		Metadata:  metadata,
	}

	return l.logEntry(entry)
}

// logEntry writes a log entry to the audit log file (JSON Lines format)
func (l *Logger) logEntry(entry LogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal log entry: %w", err)
	}

	file, err := os.OpenFile(l.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open audit log: %w", err)
	}
	defer file.Close()

	if _, err := file.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write audit log: %w", err)
	}

	return nil
}
