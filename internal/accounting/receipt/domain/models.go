package domain

import "time"

type ProcessingState string

const (
	StateImported    ProcessingState = "imported"
	StateQueued      ProcessingState = "queued"
	StateRecognizing ProcessingState = "recognizing"
	StateExtracting  ProcessingState = "extracting"
	StateNeedsReview ProcessingState = "needs_review"
	StateReviewed    ProcessingState = "reviewed"
	StateFailed      ProcessingState = "failed"
)

type Evidence struct {
	ID              string          `json:"id"`
	OriginalName    string          `json:"original_name"`
	OriginalPath    string          `json:"original_path"`
	StoredPath      string          `json:"stored_path"`
	MediaType       string          `json:"media_type"`
	SizeBytes       int64           `json:"size_bytes"`
	SHA256          string          `json:"sha256"`
	ImportedAt      time.Time       `json:"imported_at"`
	ProcessingState ProcessingState `json:"processing_state"`
	LastError       string          `json:"last_error,omitempty"`
}

type EvidenceCandidate struct {
	Path       string    `json:"path"`
	SourceType string    `json:"source_type"`
	CapturedAt time.Time `json:"captured_at"`
}

type BoundingBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type TextBlock struct {
	ID         string      `json:"id"`
	Text       string      `json:"text"`
	Confidence float64     `json:"confidence"`
	Box        BoundingBox `json:"box"`
	Order      int         `json:"order"`
	Direction  string      `json:"direction"`
	Role       string      `json:"role"`
}

type OCRPage struct {
	PageNumber int         `json:"page_number"`
	WidthPX    int         `json:"width_px"`
	HeightPX   int         `json:"height_px"`
	Blocks     []TextBlock `json:"blocks"`
}

type OCRDocument struct {
	ID                string    `json:"id"`
	EvidenceID        string    `json:"evidence_id"`
	Engine            string    `json:"engine"`
	EngineVersion     string    `json:"engine_version"`
	Device            string    `json:"device"`
	Mode              string    `json:"mode"`
	StartedAt         time.Time `json:"started_at"`
	CompletedAt       time.Time `json:"completed_at"`
	DurationMS        int64     `json:"duration_ms"`
	Pages             []OCRPage `json:"pages"`
	RawResultPath     string    `json:"raw_result_path"`
	VisualizationPath string    `json:"visualization_path"`
}

type ExtractedString struct {
	Value          *string  `json:"value"`
	Confidence     float64  `json:"confidence"`
	SourceBlockIDs []string `json:"source_block_ids"`
	Method         string   `json:"method"`
}

type ExtractedMoney struct {
	Value          *int64   `json:"value"`
	Currency       string   `json:"currency"`
	Confidence     float64  `json:"confidence"`
	SourceBlockIDs []string `json:"source_block_ids"`
	Method         string   `json:"method"`
}

const (
	TaxBasisIncluded = "tax_included"
	TaxBasisExcluded = "tax_excluded"
	TaxBasisUnknown  = "unknown"
	RoundingFloor    = "floor"
	RoundingNearest  = "round"
	RoundingCeil     = "ceil"
	RoundingUnknown  = "unknown"
)

// MoneyCandidate is retained when the extractor cannot safely auto-confirm a
// value. It deliberately contains only values actually observed by OCR.
type MoneyCandidate struct {
	Value          int64              `json:"value"`
	Score          float64            `json:"score"`
	PriceBasis     string             `json:"price_basis,omitempty"`
	RoundingMode   string             `json:"rounding_mode,omitempty"`
	SourceBlockIDs []string           `json:"source_block_ids"`
	Components     map[string]float64 `json:"components,omitempty"`
}

type TaxSummary struct {
	Rate                  int              `json:"rate"`
	TaxableAmount         ExtractedMoney   `json:"taxable_amount"` // printed amount (compatibility field)
	NormalizedNetAmount   ExtractedMoney   `json:"normalized_net_amount"`
	TaxAmount             ExtractedMoney   `json:"tax_amount"`
	PriceBasis            string           `json:"price_basis"`
	RoundingMode          string           `json:"rounding_mode"`
	CalculationDifference int64            `json:"calculation_difference"`
	Status                string           `json:"status"`
	Confidence            float64          `json:"confidence"`
	SourceBlockIDs        []string         `json:"source_block_ids"`
	Candidates            []MoneyCandidate `json:"candidates"`
}

type ReceiptRegion struct {
	ID             string      `json:"id"`
	Box            BoundingBox `json:"box"`
	Confidence     float64     `json:"confidence"`
	SourceBlockIDs []string    `json:"source_block_ids"`
}

type AccountingBreakdown struct {
	NonTaxableAmount ExtractedMoney `json:"non_taxable_amount"`
	DiscountAmount   ExtractedMoney `json:"discount_amount"`
	TenderedAmount   ExtractedMoney `json:"tendered_amount"`
	ChangeAmount     ExtractedMoney `json:"change_amount"`
	CalculatedTotal  *int64         `json:"calculated_total,omitempty"`
	Difference       int64          `json:"difference"`
	Status           string         `json:"status"`
}

// ReceiptItem is an OCR observation of one purchased item. UsageCategory is
// intentionally not inferred here: the same product can have a different
// accounting treatment depending on how it was used.
type ReceiptItem struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Quantity       float64  `json:"quantity"`
	UnitPrice      *int64   `json:"unit_price,omitempty"`
	Amount         *int64   `json:"amount,omitempty"`
	TaxRate        *int     `json:"tax_rate,omitempty"`
	TaxIncluded    *bool    `json:"tax_included,omitempty"`
	Confidence     float64  `json:"confidence"`
	SourceBlockIDs []string `json:"source_block_ids"`
	Method         string   `json:"method"`
	UsageCategory  string   `json:"usage_category"`
}

type ValidationWarning struct {
	Code    string `json:"code"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

type ReceiptObservation struct {
	ID                 string              `json:"id"`
	EvidenceID         string              `json:"evidence_id"`
	OCRRunID           string              `json:"ocr_run_id"`
	Merchant           ExtractedString     `json:"merchant"`
	TransactionDate    ExtractedString     `json:"transaction_date"`
	TransactionTime    ExtractedString     `json:"transaction_time"`
	SubtotalAmount     ExtractedMoney      `json:"subtotal_amount"`
	TotalAmount        ExtractedMoney      `json:"total_amount"`
	TaxableAmount10    ExtractedMoney      `json:"taxable_amount_10"`
	TaxAmount10        ExtractedMoney      `json:"tax_amount_10"`
	TaxableAmount8     ExtractedMoney      `json:"taxable_amount_8"`
	TaxAmount8         ExtractedMoney      `json:"tax_amount_8"`
	RegistrationNumber ExtractedString     `json:"registration_number"`
	PaymentMethod      ExtractedString     `json:"payment_method"`
	TaxSummaries       []TaxSummary        `json:"tax_summaries"`
	Items              []ReceiptItem       `json:"items"`
	Region             *ReceiptRegion      `json:"region,omitempty"`
	RegionAmbiguous    bool                `json:"region_ambiguous"`
	Accounting         AccountingBreakdown `json:"accounting"`
	Currency           string              `json:"currency"`
	Warnings           []ValidationWarning `json:"warnings"`
	CreatedAt          time.Time           `json:"created_at"`
}

type TaxSummaryCorrection struct {
	Rate          int    `json:"rate"`
	PrintedAmount *int64 `json:"printed_amount,omitempty"`
	TaxAmount     *int64 `json:"tax_amount,omitempty"`
	PriceBasis    string `json:"price_basis"`
}

type ManualItemCorrection struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Amount         *int64   `json:"amount,omitempty"`
	TaxRate        *int     `json:"tax_rate,omitempty"`
	TaxIncluded    *bool    `json:"tax_included,omitempty"`
	SourceBlockIDs []string `json:"source_block_ids"`
}

type ReceiptCorrection struct {
	Merchant           *string                `json:"merchant,omitempty"`
	TransactionDate    *string                `json:"transaction_date,omitempty"`
	TransactionTime    *string                `json:"transaction_time,omitempty"`
	SubtotalAmount     *int64                 `json:"subtotal_amount,omitempty"`
	TotalAmount        *int64                 `json:"total_amount,omitempty"`
	TaxableAmount10    *int64                 `json:"taxable_amount_10,omitempty"`
	TaxAmount10        *int64                 `json:"tax_amount_10,omitempty"`
	TaxableAmount8     *int64                 `json:"taxable_amount_8,omitempty"`
	TaxAmount8         *int64                 `json:"tax_amount_8,omitempty"`
	RegistrationNumber *string                `json:"registration_number,omitempty"`
	PaymentMethod      *string                `json:"payment_method,omitempty"`
	TaxSummaries       []TaxSummaryCorrection `json:"tax_summaries,omitempty"`
	ManualItems        []ManualItemCorrection `json:"manual_items,omitempty"`
	ManualAssignments  map[string][]string    `json:"manual_assignments,omitempty"`
}

type CorrectionDiagnosis struct {
	Field   string `json:"field"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type ReceiptReview struct {
	ID                   string                `json:"id"`
	EvidenceID           string                `json:"evidence_id"`
	ObservationID        string                `json:"observation_id"`
	Corrected            ReceiptCorrection     `json:"corrected"`
	Reviewer             string                `json:"reviewer"`
	ReviewedAt           time.Time             `json:"reviewed_at"`
	Note                 string                `json:"note"`
	Diagnostics          []CorrectionDiagnosis `json:"diagnostics"`
	ValidationErrors     []string              `json:"validation_errors"`
	GroundTruthConfirmed bool                  `json:"ground_truth_confirmed"`
}

type EvaluationGroundTruth struct {
	EvidenceID  string            `json:"evidence_id"`
	ReviewID    string            `json:"review_id"`
	Corrected   ReceiptCorrection `json:"corrected"`
	Reviewer    string            `json:"reviewer"`
	ConfirmedAt time.Time         `json:"confirmed_at"`
}

type JournalLine struct {
	Side        string `json:"side"`
	AccountCode string `json:"account_code"`
	AccountName string `json:"account_name"`
	Amount      int64  `json:"amount"`
	TaxCode     string `json:"tax_code"`
	Memo        string `json:"memo"`
}

type JournalDraft struct {
	ID          string        `json:"id"`
	EvidenceID  string        `json:"evidence_id"`
	ItemID      string        `json:"item_id,omitempty"`
	Scope       string        `json:"scope"`
	UsageStatus string        `json:"usage_status"`
	Description string        `json:"description"`
	Confidence  float64       `json:"confidence"`
	ReasonCodes []string      `json:"reason_codes"`
	Lines       []JournalLine `json:"lines"`
	Status      string        `json:"status"`
	CreatedAt   time.Time     `json:"created_at"`
}

type ReceiptSummary struct {
	EvidenceID string          `json:"evidence_id"`
	FileName   string          `json:"file_name"`
	State      ProcessingState `json:"state"`
	Merchant   string          `json:"merchant"`
	Date       string          `json:"date"`
	Amount     *int64          `json:"amount"`
	Warnings   int             `json:"warnings"`
	ImportedAt time.Time       `json:"imported_at"`
}

type ReceiptDetail struct {
	Evidence    Evidence               `json:"evidence"`
	OCR         *OCRDocument           `json:"ocr,omitempty"`
	Observation *ReceiptObservation    `json:"observation,omitempty"`
	Review      *ReceiptReview         `json:"review,omitempty"`
	GroundTruth *EvaluationGroundTruth `json:"ground_truth,omitempty"`
	Drafts      []JournalDraft         `json:"drafts"`
	ImageURL    string                 `json:"image_url"`
}

type ReceiptFilter struct {
	State string `json:"state"`
}

type OCRHealth struct {
	Status           string `json:"status"`
	Engine           string `json:"engine"`
	Version          string `json:"version"`
	Device           string `json:"device"`
	Mode             string `json:"mode"`
	Message          string `json:"message"`
	InitializationMS int64  `json:"initialization_ms"`
}
