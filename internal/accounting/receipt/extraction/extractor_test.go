package extraction

import (
	"context"
	"fmt"
	"karte/internal/accounting/receipt/domain"
	"testing"
	"time"
)

func TestExtractReceiptFields(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "b1", Text: "KIRI LAB書店", Confidence: .98, Box: domain.BoundingBox{Y: .04, Height: .06}, Order: 0},
		{ID: "b2", Text: "2026/07/21 10:32", Confidence: .97, Box: domain.BoundingBox{Y: .15}, Order: 1},
		{ID: "b3", Text: "登録番号 T1234567890123", Confidence: .96, Box: domain.BoundingBox{Y: .22}, Order: 2},
		{ID: "b4", Text: "10%対象 ¥1,100", Confidence: .95, Box: domain.BoundingBox{Y: .65}, Order: 3},
		{ID: "b5", Text: "消費税10% ¥100", Confidence: .95, Box: domain.BoundingBox{Y: .70}, Order: 4},
		{ID: "b6", Text: "合計 ¥1,100", Confidence: .99, Box: domain.BoundingBox{Y: .79}, Order: 5},
		{ID: "b7", Text: "VISA **** 1234", Confidence: .93, Box: domain.BoundingBox{Y: .87}, Order: 6},
	}
	doc := domain.OCRDocument{ID: "ocr1", EvidenceID: "ev1", Pages: []domain.OCRPage{{PageNumber: 1, Blocks: blocks}}}
	e := domain.Evidence{ID: "ev1", ImportedAt: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)}
	obs, err := (Extractor{}).Extract(context.Background(), e, doc)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Merchant.Value == nil || *obs.Merchant.Value != "KIRI LAB書店" {
		t.Fatalf("merchant=%v", obs.Merchant.Value)
	}
	if obs.TransactionDate.Value == nil || *obs.TransactionDate.Value != "2026-07-21" {
		t.Fatalf("date=%v", obs.TransactionDate.Value)
	}
	if obs.TotalAmount.Value == nil || *obs.TotalAmount.Value != 1100 {
		t.Fatalf("total=%v", obs.TotalAmount.Value)
	}
	if obs.TaxAmount10.Value == nil || *obs.TaxAmount10.Value != 100 {
		t.Fatalf("tax=%v", obs.TaxAmount10.Value)
	}
	if obs.RegistrationNumber.Value == nil || *obs.RegistrationNumber.Value != "T1234567890123" {
		t.Fatalf("registration=%v", obs.RegistrationNumber.Value)
	}
	if obs.PaymentMethod.Value == nil || *obs.PaymentMethod.Value != "credit_card" {
		t.Fatalf("payment=%v", obs.PaymentMethod.Value)
	}
}

func TestTotalDoesNotUseChange(t *testing.T) {
	blocks := []domain.TextBlock{{ID: "b1", Text: "合計 ¥980", Confidence: .9, Box: domain.BoundingBox{Y: .7}, Order: 0}, {ID: "b2", Text: "お預り ¥5,000", Confidence: .99, Box: domain.BoundingBox{Y: .8}, Order: 1}, {ID: "b3", Text: "おつり ¥4,020", Confidence: .99, Box: domain.BoundingBox{Y: .9}, Order: 2}}
	got := labeledMoney(blocks, []string{"合計"}, []string{"お預り", "おつり"})
	if got.Value == nil || *got.Value != 980 {
		t.Fatalf("total=%v", got.Value)
	}
}

func TestYenThousandsDotOCRCorrection(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "amount", Text: "¥1.100", Confidence: .97, Box: domain.BoundingBox{X: .68, Y: .55, Width: .18, Height: .04}, Order: 0},
		{ID: "label", Text: "合計", Confidence: .95, Box: domain.BoundingBox{X: .22, Y: .55, Width: .12, Height: .04}, Order: 1},
	}
	got := labeledMoney(blocks, []string{"合計"}, nil)
	if got.Value == nil || *got.Value != 1100 {
		t.Fatalf("dot-separated yen was not normalized: %+v", got)
	}
	if normalize("2026.10.26") != "2026.10.26" || normalize("1.100 kg") != "1.100 kg" {
		t.Fatal("non-currency decimal text changed")
	}
	if normalize("￥１．１００") != "¥1,100" {
		t.Fatal("full-width yen normalization failed")
	}
}

func TestLabeledMoneyUsesSpatialNeighborInsteadOfOCRSequence(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "amount", Text: "¥1,280", Confidence: .97, Box: domain.BoundingBox{X: .68, Y: .55, Width: .18, Height: .04}, Order: 0},
		{ID: "unrelated", Text: "会員番号 12345", Confidence: .92, Box: domain.BoundingBox{X: .20, Y: .72, Width: .30, Height: .03}, Order: 1},
		{ID: "label", Text: "合計", Confidence: .95, Box: domain.BoundingBox{X: .22, Y: .55, Width: .12, Height: .04}, Order: 2},
	}
	got := labeledMoney(blocks, []string{"合計"}, []string{"お預り", "おつり"})
	if got.Value == nil || *got.Value != 1280 {
		t.Fatalf("total=%v", got.Value)
	}
	if len(got.SourceBlockIDs) != 2 || got.SourceBlockIDs[0] != "label" || got.SourceBlockIDs[1] != "amount" {
		t.Fatalf("source blocks=%v", got.SourceBlockIDs)
	}
}

func TestTotalFallsBackToProminentStandaloneYenAmount(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "merchant", Text: "サンプル眼鏡店", Confidence: .91, Box: domain.BoundingBox{X: .40, Y: .12, Width: .22, Height: .025}, Order: 0},
		{ID: "title", Text: "領収書", Confidence: .90, Box: domain.BoundingBox{X: .43, Y: .46, Width: .12, Height: .035}, Order: 1},
		{ID: "total", Text: "¥5,060-", Confidence: .94, Box: domain.BoundingBox{X: .35, Y: .555, Width: .15, Height: .038}, Order: 2},
		{ID: "tax-note", Text: "(460 の消費税を含みます)", Confidence: .92, Box: domain.BoundingBox{X: .31, Y: .603, Width: .25, Height: .026}, Order: 3},
		{ID: "payment", Text: "JCB", Confidence: .91, Box: domain.BoundingBox{X: .30, Y: .645, Width: .08, Height: .024}, Order: 4},
		{ID: "payment-amount", Text: "5,060", Confidence: .93, Box: domain.BoundingBox{X: .46, Y: .645, Width: .09, Height: .024}, Order: 5},
		{ID: "taxable", Text: "課税対象 4,600 消費税10% 460", Confidence: .90, Box: domain.BoundingBox{X: .24, Y: .715, Width: .34, Height: .025}, Order: 6},
	}
	got := positionedYenTotal(blocks)
	if got.Value == nil || *got.Value != 5060 {
		t.Fatalf("total=%v", got.Value)
	}
	if got.Method != "positioned_yen_amount_score_v2" || len(got.SourceBlockIDs) != 1 || got.SourceBlockIDs[0] != "total" {
		t.Fatalf("unexpected evidence: method=%s blocks=%v", got.Method, got.SourceBlockIDs)
	}
}

func TestTotalUsesTaxSummaryPositionAndRejectsPaymentAmounts(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "merchant", Text: "サンプル雑貨店", Confidence: .94, Box: domain.BoundingBox{X: .35, Y: .14, Width: .25, Height: .027}, Order: 0},
		{ID: "subtotal-label", Text: "小計", Confidence: .92, Box: domain.BoundingBox{X: .33, Y: .398, Width: .06, Height: .027}, Order: 1},
		{ID: "subtotal", Text: "200", Confidence: .93, Box: domain.BoundingBox{X: .65, Y: .390, Width: .04, Height: .026}, Order: 2},
		{ID: "tax-label", Text: "10%対象", Confidence: .91, Box: domain.BoundingBox{X: .33, Y: .420, Width: .08, Height: .026}, Order: 3},
		{ID: "tax-detail", Text: "220(内税額 20)", Confidence: .81, Box: domain.BoundingBox{X: .47, Y: .412, Width: .22, Height: .032}, Order: 4},
		{ID: "total", Text: "¥220", Confidence: .93, Box: domain.BoundingBox{X: .59, Y: .433, Width: .09, Height: .027}, Order: 5},
		{ID: "payment-label-noisy", Text: "整系IC", Confidence: .40, Box: domain.BoundingBox{X: .37, Y: .444, Width: .14, Height: .046}, Order: 6},
		{ID: "payment", Text: "¥220", Confidence: .94, Box: domain.BoundingBox{X: .59, Y: .454, Width: .09, Height: .028}, Order: 7},
		{ID: "payment-detail-label", Text: "交通系支払", Confidence: .95, Box: domain.BoundingBox{X: .44, Y: .581, Width: .12, Height: .032}, Order: 8},
		{ID: "payment-detail", Text: "¥220", Confidence: .94, Box: domain.BoundingBox{X: .63, Y: .624, Width: .08, Height: .026}, Order: 9},
		{ID: "balance-label", Text: "交通系残高", Confidence: .91, Box: domain.BoundingBox{X: .31, Y: .647, Width: .15, Height: .030}, Order: 10},
		{ID: "balance", Text: "¥1,093", Confidence: .94, Box: domain.BoundingBox{X: .61, Y: .647, Width: .10, Height: .026}, Order: 11},
	}
	got := positionedYenTotal(blocks)
	if got.Value == nil || *got.Value != 220 {
		t.Fatalf("total=%v", got.Value)
	}
	if len(got.SourceBlockIDs) != 1 || got.SourceBlockIDs[0] != "total" {
		t.Fatalf("selected wrong ¥220 block: %v", got.SourceBlockIDs)
	}
}

func TestPositionedTotalDoesNotTreatTelephoneAsMoney(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "phone", Text: "電話:03-1234-5678", Confidence: .98, Box: domain.BoundingBox{X: .2, Y: .6, Width: .4, Height: .03}, Order: 0},
		{ID: "id", Text: "0005-5333291176", Confidence: .96, Box: domain.BoundingBox{X: .2, Y: .7, Width: .4, Height: .03}, Order: 1},
	}
	if got := positionedYenTotal(blocks); got.Value != nil {
		t.Fatalf("unexpected total=%v", *got.Value)
	}
}

func TestTaxAndSubtotalUseSpatialRowsWithoutReadingRateAsMoney(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "item", Text: "温・湿度計壁掛け式", Confidence: .94, Box: domain.BoundingBox{X: .24, Y: .05, Width: .22, Height: .04}, Order: 0},
		{ID: "item-price", Text: "¥100外", Confidence: .95, Box: domain.BoundingBox{X: .52, Y: .07, Width: .08, Height: .03}, Order: 1},
		{ID: "subtotal-label", Text: "小計", Confidence: .95, Box: domain.BoundingBox{X: .25, Y: .10, Width: .06, Height: .03}, Order: 2},
		{ID: "subtotal", Text: "¥100", Confidence: .95, Box: domain.BoundingBox{X: .52, Y: .10, Width: .07, Height: .03}, Order: 3},
		{ID: "target-label", Text: "10%税抜対象額", Confidence: .94, Box: domain.BoundingBox{X: .25, Y: .13, Width: .16, Height: .03}, Order: 4},
		{ID: "target", Text: "¥100", Confidence: .96, Box: domain.BoundingBox{X: .52, Y: .13, Width: .07, Height: .03}, Order: 5},
		{ID: "tax-label", Text: "10%税額", Confidence: .94, Box: domain.BoundingBox{X: .25, Y: .16, Width: .10, Height: .03}, Order: 6},
		{ID: "tax", Text: "¥10", Confidence: .96, Box: domain.BoundingBox{X: .52, Y: .16, Width: .06, Height: .03}, Order: 7},
		{ID: "total-label", Text: "合計", Confidence: .96, Box: domain.BoundingBox{X: .25, Y: .19, Width: .07, Height: .03}, Order: 8},
		{ID: "total", Text: "¥110", Confidence: .98, Box: domain.BoundingBox{X: .52, Y: .19, Width: .08, Height: .03}, Order: 9},
	}
	doc := domain.OCRDocument{ID: "ocr", EvidenceID: "ev", Pages: []domain.OCRPage{{PageNumber: 1, Blocks: blocks}}}
	obs, err := (Extractor{}).Extract(context.Background(), domain.Evidence{ID: "ev", ImportedAt: time.Now()}, doc)
	if err != nil {
		t.Fatal(err)
	}
	assertMoney := func(label string, got *int64, want int64) {
		t.Helper()
		if got == nil || *got != want {
			t.Fatalf("%s=%v want %d", label, got, want)
		}
	}
	assertMoney("subtotal", obs.SubtotalAmount.Value, 100)
	assertMoney("target", obs.TaxableAmount10.Value, 100)
	assertMoney("tax", obs.TaxAmount10.Value, 10)
	assertMoney("total", obs.TotalAmount.Value, 110)
	if len(obs.Items) != 1 || obs.Items[0].Name != "温・湿度計壁掛け式" || obs.Items[0].Amount == nil || *obs.Items[0].Amount != 100 {
		t.Fatalf("items=%+v", obs.Items)
	}
}

func TestSplitInclusiveTaxDetailIsAssignedByMeaningAndPosition(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "subtotal-label", Text: "小計", Confidence: .9, Box: domain.BoundingBox{X: .32, Y: .40, Width: .05, Height: .03}, Order: 0},
		{ID: "subtotal", Text: "200", Confidence: .9, Box: domain.BoundingBox{X: .65, Y: .40, Width: .05, Height: .03}, Order: 1},
		{ID: "target-label", Text: "10%対象", Confidence: .9, Box: domain.BoundingBox{X: .32, Y: .43, Width: .09, Height: .03}, Order: 2},
		{ID: "target", Text: "220(内税額", Confidence: .9, Box: domain.BoundingBox{X: .48, Y: .43, Width: .14, Height: .03}, Order: 3},
		{ID: "tax", Text: "20)", Confidence: .9, Box: domain.BoundingBox{X: .65, Y: .43, Width: .04, Height: .03}, Order: 4},
		{ID: "total-label", Text: "合計", Confidence: .9, Box: domain.BoundingBox{X: .32, Y: .46, Width: .06, Height: .03}, Order: 5},
		{ID: "total", Text: "¥220", Confidence: .9, Box: domain.BoundingBox{X: .62, Y: .46, Width: .08, Height: .03}, Order: 6},
	}
	doc := domain.OCRDocument{ID: "ocr", EvidenceID: "ev", Pages: []domain.OCRPage{{PageNumber: 1, Blocks: blocks}}}
	obs, err := (Extractor{}).Extract(context.Background(), domain.Evidence{ID: "ev", ImportedAt: time.Now()}, doc)
	if err != nil {
		t.Fatal(err)
	}
	if obs.TaxableAmount10.Value == nil || *obs.TaxableAmount10.Value != 220 || obs.TaxAmount10.Value == nil || *obs.TaxAmount10.Value != 20 {
		t.Fatalf("tax target=%v tax=%v", obs.TaxableAmount10.Value, obs.TaxAmount10.Value)
	}
}

func TestCandidateRerankingSelects290OverNearby200(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "target-label", Text: "10%内税対象", Confidence: .96, Box: domain.BoundingBox{X: .20, Y: .40, Width: .20, Height: .03}, Order: 0},
		{ID: "wrong", Text: "¥200", Confidence: .99, Box: domain.BoundingBox{X: .52, Y: .40, Width: .10, Height: .03}, Order: 1},
		{ID: "target", Text: "¥290", Confidence: .88, Box: domain.BoundingBox{X: .65, Y: .40, Width: .10, Height: .03}, Order: 2},
		{ID: "tax-label", Text: "10%内消費税", Confidence: .95, Box: domain.BoundingBox{X: .20, Y: .44, Width: .20, Height: .03}, Order: 3},
		{ID: "tax", Text: "¥26", Confidence: .92, Box: domain.BoundingBox{X: .65, Y: .44, Width: .08, Height: .03}, Order: 4},
		{ID: "total-label", Text: "合計", Confidence: .97, Box: domain.BoundingBox{X: .20, Y: .48, Width: .08, Height: .03}, Order: 5},
		{ID: "total", Text: "¥290", Confidence: .93, Box: domain.BoundingBox{X: .65, Y: .48, Width: .10, Height: .03}, Order: 6},
	}
	obs, err := (Extractor{}).Extract(context.Background(), domain.Evidence{ID: "ev", ImportedAt: time.Now()}, domain.OCRDocument{ID: "ocr", Pages: []domain.OCRPage{{Blocks: blocks}}})
	if err != nil {
		t.Fatal(err)
	}
	if obs.TaxableAmount10.Value == nil || *obs.TaxableAmount10.Value != 290 {
		t.Fatalf("target=%v summaries=%+v", obs.TaxableAmount10.Value, obs.TaxSummaries)
	}
}

func TestInconsistent200IsLeftAmbiguousInsteadOfBecoming290(t *testing.T) {
	blocks := []domain.TextBlock{
		{ID: "target-label", Text: "10%内税対象", Confidence: .98, Box: domain.BoundingBox{X: .20, Y: .40, Width: .20, Height: .03}, Order: 0},
		{ID: "target", Text: "¥200", Confidence: .99, Box: domain.BoundingBox{X: .65, Y: .40, Width: .10, Height: .03}, Order: 1},
		{ID: "tax-label", Text: "10%内消費税", Confidence: .98, Box: domain.BoundingBox{X: .20, Y: .44, Width: .20, Height: .03}, Order: 2},
		{ID: "tax", Text: "¥26", Confidence: .99, Box: domain.BoundingBox{X: .65, Y: .44, Width: .08, Height: .03}, Order: 3},
		{ID: "total-label", Text: "合計", Confidence: .98, Box: domain.BoundingBox{X: .20, Y: .48, Width: .08, Height: .03}, Order: 4},
		{ID: "total", Text: "¥290", Confidence: .99, Box: domain.BoundingBox{X: .65, Y: .48, Width: .10, Height: .03}, Order: 5},
	}
	obs, err := (Extractor{}).Extract(context.Background(), domain.Evidence{ID: "ev", ImportedAt: time.Now()}, domain.OCRDocument{ID: "ocr", Pages: []domain.OCRPage{{Blocks: blocks}}})
	if err != nil {
		t.Fatal(err)
	}
	if obs.TaxableAmount10.Value != nil {
		t.Fatalf("unsafe auto confirmation: %d", *obs.TaxableAmount10.Value)
	}
	if len(obs.TaxSummaries) != 1 || obs.TaxSummaries[0].Status != "ambiguous" {
		t.Fatalf("summaries=%+v", obs.TaxSummaries)
	}
}

func TestTaxBasisIncludedAndExcludedHypotheses(t *testing.T) {
	tests := []struct {
		name, label        string
		target, tax, total int64
		basis              string
	}{
		{"included", "10%内税対象", 490, 44, 490, domain.TaxBasisIncluded},
		{"excluded", "10%税抜対象", 446, 44, 490, domain.TaxBasisExcluded},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			blocks := []domain.TextBlock{
				{ID: "target-label", Text: tc.label, Confidence: .97, Box: domain.BoundingBox{X: .20, Y: .40, Width: .20, Height: .03}, Order: 0},
				{ID: "target", Text: fmt.Sprintf("¥%d", tc.target), Confidence: .96, Box: domain.BoundingBox{X: .65, Y: .40, Width: .10, Height: .03}, Order: 1},
				{ID: "tax-label", Text: "10%消費税", Confidence: .96, Box: domain.BoundingBox{X: .20, Y: .44, Width: .20, Height: .03}, Order: 2},
				{ID: "tax", Text: fmt.Sprintf("¥%d", tc.tax), Confidence: .96, Box: domain.BoundingBox{X: .65, Y: .44, Width: .08, Height: .03}, Order: 3},
				{ID: "total-label", Text: "合計", Confidence: .98, Box: domain.BoundingBox{X: .20, Y: .48, Width: .08, Height: .03}, Order: 4},
				{ID: "total", Text: fmt.Sprintf("¥%d", tc.total), Confidence: .98, Box: domain.BoundingBox{X: .65, Y: .48, Width: .10, Height: .03}, Order: 5},
			}
			obs, err := (Extractor{}).Extract(context.Background(), domain.Evidence{ID: "ev", ImportedAt: time.Now()}, domain.OCRDocument{ID: "ocr", Pages: []domain.OCRPage{{Blocks: blocks}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(obs.TaxSummaries) != 1 || obs.TaxSummaries[0].PriceBasis != tc.basis {
				t.Fatalf("summaries=%+v", obs.TaxSummaries)
			}
		})
	}
}
