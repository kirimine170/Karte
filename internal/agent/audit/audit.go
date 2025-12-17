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
