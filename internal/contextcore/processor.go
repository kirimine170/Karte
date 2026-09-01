package contextcore

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type ProcessSummary struct {
	Processed int `json:"processed"`
	Failed    int `json:"failed"`
}

type Processor struct {
	store   *Store
	service *Service
}

func NewProcessor(dataDir string) (*Processor, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, err
	}
	service, err := NewService(dataDir)
	if err != nil {
		return nil, err
	}
	if err := store.EnsureLayout(); err != nil {
		return nil, err
	}
	return &Processor{store: store, service: service}, nil
}

func (processor *Processor) ProcessPending(limit int) (ProcessSummary, error) {
	pending, err := processor.store.ListPending(limit)
	if err != nil {
		return ProcessSummary{}, err
	}
	policy, err := LoadPolicy(processor.store.dataRoot)
	if err != nil {
		return ProcessSummary{}, err
	}
	summary := ProcessSummary{}
	for _, item := range pending {
		if err := processor.processOne(item, policy); err != nil {
			summary.Failed++
			continue
		}
		summary.Processed++
	}
	return summary, nil
}

func (processor *Processor) processOne(pending PendingRequest, policy Policy) error {
	request, err := DecodeRequest(pending.Data)
	if err != nil {
		requestID := strings.TrimSuffix(pending.Filename, ".json")
		if !requestIDPattern.MatchString(requestID) {
			return err
		}
		code := "invalid_request"
		var validation *ValidationError
		if errors.As(err, &validation) {
			code = validation.Code
		}
		response := Response{
			ProtocolVersion: ProtocolVersion, RequestID: requestID, RequestSHA256: pending.SHA256,
			Operation: "invalid", Status: "invalid", Results: []SearchResult{}, Diagnostics: []Diagnostic{},
			Error:       &ProtocolError{Code: code, Message: "Context request could not be processed."},
			ProcessedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if writeErr := processor.store.WriteResponse(response); writeErr != nil {
			return writeErr
		}
		return processor.store.ArchiveRequest(pending, requestID)
	}
	if strings.TrimSuffix(pending.Filename, ".json") != request.RequestID {
		return fmt.Errorf("request filename does not match request_id")
	}
	if existing, readErr := processor.store.ReadResponse(request.RequestID); readErr != nil {
		return readErr
	} else if existing != nil {
		if existing.RequestSHA256 != pending.SHA256 {
			return fmt.Errorf("request_id was reused with different content")
		}
		return processor.store.ArchiveRequest(pending, request.RequestID)
	}

	response := Response{
		ProtocolVersion: ProtocolVersion, RequestID: request.RequestID, RequestSHA256: pending.SHA256,
		Operation: request.Operation, Results: []SearchResult{}, Diagnostics: []Diagnostic{},
		ProcessedAt: time.Now().UTC().Format(time.RFC3339),
	}
	switch request.Operation {
	case "search":
		response.Results, response.Diagnostics, response.Status, err = processor.service.Search(request, policy)
	case "read":
		response.Document, response.Diagnostics, response.Status, err = processor.service.Read(request, policy)
	}
	if err != nil {
		code := "processing_failed"
		var validation *ValidationError
		if errors.As(err, &validation) {
			code = validation.Code
		}
		response.Status = "invalid"
		response.Error = &ProtocolError{Code: code, Message: "Context request could not be processed."}
		response.Results = []SearchResult{}
		response.Document = nil
	}
	if err := processor.store.WriteResponse(response); err != nil {
		return err
	}
	return processor.store.ArchiveRequest(pending, request.RequestID)
}
