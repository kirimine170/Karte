package contextcore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const AuditVersion = "1.0"

var (
	auditCodePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)
	auditHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
	auditActors      = map[string]bool{"ephy": true, "human": true, "tool": true, "unknown": true}
	auditOperations  = map[string]bool{"search": true, "read": true, "propose": true, "review": true, "export": true, "learn": true, "invalid": true}
	auditStatuses    = map[string]bool{"allowed": true, "denied": true, "ok": true, "not_found": true, "invalid": true, "conflict": true, "error": true}
)

type AuditEvent struct {
	AuditVersion      string `json:"audit_version"`
	EventID           string `json:"event_id"`
	CorrelationSHA256 string `json:"correlation_sha256"`
	ActorType         string `json:"actor_type"`
	ActorIDSHA256     string `json:"actor_id_sha256,omitempty"`
	Operation         string `json:"operation"`
	Status            string `json:"status"`
	ResultCount       int    `json:"result_count"`
	ErrorCode         string `json:"error_code,omitempty"`
	RecordedAt        string `json:"recorded_at"`
}

func NewAuditEvent(correlation string, actor Actor, operation, status string, resultCount int, errorCode, recordedAt string) (AuditEvent, error) {
	correlation = strings.TrimSpace(correlation)
	operation = strings.TrimSpace(operation)
	status = strings.TrimSpace(status)
	errorCode = strings.TrimSpace(errorCode)
	actorType := strings.TrimSpace(actor.Type)
	if correlation == "" || !auditActors[actorType] || !auditOperations[operation] || !auditStatuses[status] || resultCount < 0 || (errorCode != "" && !auditCodePattern.MatchString(errorCode)) {
		return AuditEvent{}, fmt.Errorf("audit metadata is invalid")
	}
	if recordedAt == "" {
		recordedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if _, err := time.Parse(time.RFC3339, recordedAt); err != nil {
		return AuditEvent{}, fmt.Errorf("audit recorded_at must be RFC3339")
	}
	correlationHash := sha256.Sum256([]byte(correlation))
	actorIDHash := sha256.Sum256([]byte(strings.TrimSpace(actor.ID)))
	eventSeed := strings.Join([]string{
		hex.EncodeToString(correlationHash[:]), actorType, hex.EncodeToString(actorIDHash[:]),
		operation, status, fmt.Sprintf("%d", resultCount), errorCode,
	}, "\x00")
	eventHash := sha256.Sum256([]byte(eventSeed))
	return AuditEvent{
		AuditVersion: AuditVersion, EventID: hex.EncodeToString(eventHash[:]),
		CorrelationSHA256: hex.EncodeToString(correlationHash[:]), ActorType: actorType,
		ActorIDSHA256: hex.EncodeToString(actorIDHash[:]), Operation: operation, Status: status,
		ResultCount: resultCount, ErrorCode: errorCode, RecordedAt: recordedAt,
	}, nil
}

func RecordAudit(dataDir, correlation string, actor Actor, operation, status string, resultCount int, errorCode string) error {
	event, err := NewAuditEvent(correlation, actor, operation, status, resultCount, errorCode, "")
	if err != nil {
		return err
	}
	store, err := NewStore(dataDir)
	if err != nil {
		return err
	}
	return store.WriteAudit(event)
}

func (store *Store) WriteAudit(event AuditEvent) error {
	if event.AuditVersion != AuditVersion || !auditHashPattern.MatchString(event.EventID) || !auditHashPattern.MatchString(event.CorrelationSHA256) ||
		!auditActors[event.ActorType] || !auditHashPattern.MatchString(event.ActorIDSHA256) || !auditOperations[event.Operation] ||
		!auditStatuses[event.Status] || event.ResultCount < 0 || event.ErrorCode != "" && !auditCodePattern.MatchString(event.ErrorCode) {
		return fmt.Errorf("invalid context audit event")
	}
	if _, err := time.Parse(time.RFC3339, event.RecordedAt); err != nil {
		return fmt.Errorf("invalid context audit event")
	}
	if err := store.EnsureLayout(); err != nil {
		return err
	}
	destination := filepath.Join(store.auditDir, event.EventID+".json")
	if data, err := os.ReadFile(destination); err == nil {
		var existing AuditEvent
		if json.Unmarshal(data, &existing) != nil || existing.EventID != event.EventID || existing.CorrelationSHA256 != event.CorrelationSHA256 ||
			existing.ActorType != event.ActorType || existing.ActorIDSHA256 != event.ActorIDSHA256 || existing.Operation != event.Operation ||
			existing.Status != event.Status || existing.ResultCount != event.ResultCount || existing.ErrorCode != event.ErrorCode {
			return fmt.Errorf("context audit event already exists with different metadata")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return atomicWriteJSON(destination, event)
}
