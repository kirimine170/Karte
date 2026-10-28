package ocr

import "karte/internal/accounting/receipt/domain"

import "encoding/json"

type request struct {
	RequestID string        `json:"request_id"`
	Method    string        `json:"method"`
	Params    requestParams `json:"params"`
}

type requestParams struct {
	Path              string              `json:"path"`
	OutputDir         string              `json:"output_dir"`
	Visualize         bool                `json:"visualize"`
	DocumentType      string              `json:"document_type"`
	Region            *domain.BoundingBox `json:"region,omitempty"`
	PreprocessVariant string              `json:"preprocess_variant,omitempty"`
}

type workerEvent struct {
	Type            string `json:"type"`
	RequestID       string `json:"request_id,omitempty"`
	ProtocolVersion string `json:"protocol_version,omitempty"`
	Engine          struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"engine,omitempty"`
	Device   string        `json:"device,omitempty"`
	Mode     string        `json:"mode,omitempty"`
	Stage    string        `json:"stage,omitempty"`
	Progress *float64      `json:"progress,omitempty"`
	Message  string        `json:"message,omitempty"`
	Result   *workerResult `json:"result,omitempty"`
	Error    *workerError  `json:"error,omitempty"`
	Code     string        `json:"code,omitempty"`
}

type workerResult struct {
	Engine            string `json:"engine"`
	EngineVersion     string `json:"engine_version"`
	Device            string `json:"device"`
	Mode              string `json:"mode"`
	DurationMS        int64  `json:"duration_ms"`
	PageCount         int    `json:"page_count"`
	RawResultPath     string `json:"raw_result_path"`
	VisualizationPath string `json:"visualization_path"`
}

type workerError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func decodeEvent(line []byte) (workerEvent, error) {
	var e workerEvent
	err := json.Unmarshal(line, &e)
	return e, err
}
