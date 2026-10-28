package ocr

import (
	"encoding/json"
	"fmt"
	"karte/internal/accounting/receipt/domain"
	"os"
)

type adapterDocument struct {
	Pages []adapterPage `json:"pages"`
}
type adapterPage struct {
	Width  int            `json:"width"`
	Height int            `json:"height"`
	Blocks []adapterBlock `json:"blocks"`
}
type adapterBlock struct {
	Text       string    `json:"text"`
	Confidence float64   `json:"confidence"`
	BBox       []float64 `json:"bbox"`
	Role       string    `json:"role"`
	Direction  string    `json:"direction"`
}

func AdaptYomiToku(path string) ([]domain.OCRPage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc adapterDocument
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("YomiToku JSONを解析できません: %w", err)
	}
	if len(doc.Pages) == 0 {
		return nil, fmt.Errorf("OCR結果にページがありません")
	}
	pages := make([]domain.OCRPage, 0, len(doc.Pages))
	order := 0
	for pi, p := range doc.Pages {
		if p.Width <= 0 || p.Height <= 0 {
			return nil, fmt.Errorf("ページ%dの画像サイズが不正です", pi+1)
		}
		page := domain.OCRPage{PageNumber: pi + 1, WidthPX: p.Width, HeightPX: p.Height, Blocks: []domain.TextBlock{}}
		for _, b := range p.Blocks {
			if len(b.BBox) != 4 || b.Text == "" {
				continue
			}
			x1, y1, x2, y2 := b.BBox[0], b.BBox[1], b.BBox[2], b.BBox[3]
			box := domain.BoundingBox{X: clamp01(x1 / float64(p.Width)), Y: clamp01(y1 / float64(p.Height)), Width: clamp01((x2 - x1) / float64(p.Width)), Height: clamp01((y2 - y1) / float64(p.Height))}
			direction := b.Direction
			if direction == "" {
				direction = "horizontal"
			}
			role := b.Role
			if role == "" {
				role = "text"
			}
			page.Blocks = append(page.Blocks, domain.TextBlock{ID: domain.NewID("blk"), Text: b.Text, Confidence: clamp01(b.Confidence), Box: box, Order: order, Direction: direction, Role: role})
			order++
		}
		pages = append(pages, page)
	}
	return pages, nil
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
