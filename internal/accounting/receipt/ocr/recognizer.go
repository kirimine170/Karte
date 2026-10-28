package ocr

import (
	"context"
	"karte/internal/accounting/receipt/domain"
)

type Recognizer interface {
	Recognize(context.Context, domain.Evidence) (*domain.OCRDocument, error)
	Health(context.Context) domain.OCRHealth
	Close() error
}

type RegionRecognizer interface {
	RecognizeRegion(context.Context, domain.Evidence, domain.BoundingBox, string) (*domain.OCRDocument, error)
}
