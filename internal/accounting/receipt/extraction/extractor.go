package extraction

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"karte/internal/accounting/receipt/domain"
)

type Extractor struct{ Config ScoringConfig }

type candidate[T any] struct {
	value T
	score float64
	ids   []string
}

var (
	yenThousandsRE       = regexp.MustCompile(`(¥\s*[0-9][0-9,]*)[.．]([0-9]{3})([^0-9]|$)`)
	moneyRE              = regexp.MustCompile(`(?:[¥￥]\s*)?([0-9０-９][0-9０-９,，]*)\s*円?`)
	standaloneMoneyRE    = regexp.MustCompile(`^\s*(?:[¥￥]\s*)?([0-9０-９][0-9０-９,，]*)\s*円?\s*[内外※*]?\s*[)）]?\s*[-ー―‐−]?\s*$`)
	explicitYenMoneyRE   = regexp.MustCompile(`^\s*[¥￥]\s*([0-9０-９][0-9０-９,，]*)\s*円?\s*[-ー―‐−]?\s*$`)
	registrationRE       = regexp.MustCompile(`(?i)T\s*([0-9０-９\s]{13,20})`)
	dateYMDRE            = regexp.MustCompile(`(20[0-9０-９]{2})\s*[/\-.年]\s*([0-9０-９]{1,2})\s*[/\-.月]\s*([0-9０-９]{1,2})\s*日?`)
	eraRE                = regexp.MustCompile(`令和\s*([0-9０-９]{1,2})\s*年\s*([0-9０-９]{1,2})\s*月\s*([0-9０-９]{1,2})\s*日`)
	timeRE               = regexp.MustCompile(`([0-2]?[0-9０-９])\s*[:：]\s*([0-5][0-9０-９])`)
	totalContextKeywords = []string{"総合計", "税込合計", "合計", "小計", "課税", "対象", "消費税", "税額", "内税", "外税"}
	paymentKeywords      = []string{"お預り", "預り", "釣銭", "おつり", "残高", "支払", "現金", "カード", "クレジット", "系IC", "ポイント", "チャージ", "電話"}
)

func (e Extractor) Extract(_ context.Context, evidence domain.Evidence, doc domain.OCRDocument) (domain.ReceiptObservation, error) {
	cfg := e.Config.normalized()
	blocks := flatten(doc)
	obs := domain.ReceiptObservation{
		ID: domain.NewID("obs"), EvidenceID: evidence.ID, OCRRunID: doc.ID, Currency: "JPY", CreatedAt: time.Now().UTC(),
		SubtotalAmount: emptyMoney(), TotalAmount: emptyMoney(), TaxableAmount10: emptyMoney(), TaxAmount10: emptyMoney(), TaxableAmount8: emptyMoney(), TaxAmount8: emptyMoney(),
		TaxSummaries: []domain.TaxSummary{}, Items: []domain.ReceiptItem{},
	}
	obs.Merchant = merchant(blocks)
	obs.TransactionDate, obs.TransactionTime = transactionDateTime(blocks)
	obs.TotalAmount = labeledMoney(blocks, []string{"総合計", "税込合計", "お買上計", "今回お支払額", "請求額", "合計"}, []string{"小計", "お預り", "預り", "釣銭", "おつり", "ポイント", "残高"})
	if obs.TotalAmount.Value == nil {
		obs.TotalAmount = positionedYenTotal(blocks)
	}
	region, regionAmbiguous := isolateReceiptRegion(blocks, obs.TotalAmount)
	obs.Region, obs.RegionAmbiguous = region, regionAmbiguous
	summaryBlocks := receiptRegion(blocks, obs.TotalAmount, .22, .42)
	if region != nil {
		summaryBlocks = blocksInBox(blocks, region.Box)
	}
	obs.SubtotalAmount = labeledMoney(summaryBlocks, []string{"小計", "税抜小計"}, []string{"合計", "対象", "消費税", "税額"})
	obs.TaxSummaries = extractTaxSummaries(summaryBlocks, obs.TotalAmount, cfg)
	for _, summary := range obs.TaxSummaries {
		switch summary.Rate {
		case 10:
			obs.TaxableAmount10, obs.TaxAmount10 = summary.TaxableAmount, summary.TaxAmount
		case 8:
			obs.TaxableAmount8, obs.TaxAmount8 = summary.TaxableAmount, summary.TaxAmount
		}
	}
	obs.TotalAmount = reconcileTotalWithTax(summaryBlocks, obs.TotalAmount, obs.TaxSummaries, cfg)
	obs.Accounting = extractAccountingBreakdown(summaryBlocks, obs)
	obs.RegistrationNumber = registration(blocks)
	obs.PaymentMethod = payment(blocks)
	itemAnchor := obs.TotalAmount
	itemRadius := .35
	if obs.SubtotalAmount.Value != nil {
		itemAnchor = obs.SubtotalAmount
		itemRadius = .20
	}
	itemBlocks := receiptRegion(blocks, itemAnchor, itemRadius, .42)
	if region != nil {
		itemBlocks = blocksInBox(blocks, region.Box)
	}
	obs.Items = extractItems(itemBlocks, obs)
	if regionAmbiguous {
		// Precision first: line items and tax facts must not combine two receipts.
		obs.Items = []domain.ReceiptItem{}
		obs.TaxSummaries = []domain.TaxSummary{}
		obs.TaxableAmount10, obs.TaxAmount10, obs.TaxableAmount8, obs.TaxAmount8 = emptyMoney(), emptyMoney(), emptyMoney(), emptyMoney()
		obs.Accounting = domain.AccountingBreakdown{NonTaxableAmount: emptyMoney(), DiscountAmount: emptyMoney(), TenderedAmount: emptyMoney(), ChangeAmount: emptyMoney(), Status: "ambiguous"}
	}
	obs.Warnings = validate(obs, evidence.ImportedAt)
	for _, block := range blocks {
		if strings.Contains(block.Text, "¥") || strings.Contains(block.Text, "￥") {
			if strings.Contains(block.Text, ".") || strings.Contains(block.Text, "．") {
				obs.Warnings = append(obs.Warnings, domain.ValidationWarning{Code: "YEN_SEPARATOR_REVIEW", Field: "total_amount", Message: "円記号付きの金額にピリオドが含まれています．3桁区切りとして補正した候補を原本で確認してください"})
				break
			}
		}
	}
	if suspectedMultipleReceipts(blocks) {
		obs.Warnings = append(obs.Warnings, domain.ValidationWarning{Code: "MULTIPLE_RECEIPTS_SUSPECTED", Field: "items", Message: "1枚の画像に複数のレシートが含まれる可能性があります．分割取り込みを推奨します"})
	}
	return obs, nil
}

func emptyMoney() domain.ExtractedMoney {
	return domain.ExtractedMoney{Currency: "JPY", Method: "not_found", SourceBlockIDs: []string{}}
}

func flatten(doc domain.OCRDocument) []domain.TextBlock {
	var result []domain.TextBlock
	for _, p := range doc.Pages {
		result = append(result, p.Blocks...)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Order < result[j].Order })
	return result
}

func normalize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= '０' && r <= '９':
			r = '0' + (r - '０')
		case r >= 'Ａ' && r <= 'Ｚ':
			r = 'A' + (r - 'Ａ')
		case r >= 'ａ' && r <= 'ｚ':
			r = 'a' + (r - 'ａ')
		case r == '，':
			r = ','
		case r == '：':
			r = ':'
		case r == '￥':
			r = '¥'
		}
		b.WriteRune(r)
	}
	normalized := strings.Join(strings.Fields(b.String()), " ")
	for {
		next := yenThousandsRE.ReplaceAllString(normalized, "${1},${2}${3}")
		if next == normalized {
			return normalized
		}
		normalized = next
	}
}

func merchant(blocks []domain.TextBlock) domain.ExtractedString {
	var best candidate[string]
	exclude := []string{"TEL", "電話", "〒", "レシート", "領収", "日時", "レジ", "担当", "登録番号", "合計"}
	for _, b := range blocks {
		t := strings.TrimSpace(normalize(b.Text))
		if len([]rune(t)) < 2 {
			continue
		}
		skip := false
		for _, x := range exclude {
			if strings.Contains(strings.ToUpper(t), strings.ToUpper(x)) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}
		if moneyRE.MatchString(t) || dateYMDRE.MatchString(t) {
			continue
		}
		score := 0.38 + 0.25*(1-clamp(b.Box.Y)) + 0.1*clamp(b.Box.Height*15) + b.Confidence*0.2
		if strings.Contains(t, "株式会社") || strings.Contains(t, "支店") || strings.HasSuffix(t, "店") {
			score += 0.12
		}
		if score > best.score {
			best = candidate[string]{t, clamp(score), []string{b.ID}}
		}
	}
	if best.score == 0 {
		return domain.ExtractedString{Method: "not_found", SourceBlockIDs: []string{}}
	}
	return domain.ExtractedString{Value: &best.value, Confidence: best.score, SourceBlockIDs: best.ids, Method: "merchant_position_score_v1"}
}

func transactionDateTime(blocks []domain.TextBlock) (domain.ExtractedString, domain.ExtractedString) {
	date := domain.ExtractedString{Method: "not_found", SourceBlockIDs: []string{}}
	tm := domain.ExtractedString{Method: "not_found", SourceBlockIDs: []string{}}
	for _, b := range blocks {
		t := normalize(b.Text)
		if date.Value == nil {
			if m := dateYMDRE.FindStringSubmatch(t); m != nil {
				if y, mo, d, ok := validDate(m[1], m[2], m[3]); ok {
					v := fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
					date = domain.ExtractedString{Value: &v, Confidence: clamp(.72 + b.Confidence*.23), SourceBlockIDs: []string{b.ID}, Method: "date_regex_ymd_v1"}
				}
			} else if m := eraRE.FindStringSubmatch(t); m != nil {
				year, _ := strconv.Atoi(m[1])
				if y, mo, d, ok := validDate(strconv.Itoa(2018+year), m[2], m[3]); ok {
					v := fmt.Sprintf("%04d-%02d-%02d", y, mo, d)
					date = domain.ExtractedString{Value: &v, Confidence: clamp(.68 + b.Confidence*.23), SourceBlockIDs: []string{b.ID}, Method: "date_regex_reiwa_v1"}
				}
			}
		}
		if tm.Value == nil {
			if m := timeRE.FindStringSubmatch(t); m != nil {
				h, _ := strconv.Atoi(m[1])
				mi, _ := strconv.Atoi(m[2])
				if h < 24 {
					v := fmt.Sprintf("%02d:%02d", h, mi)
					tm = domain.ExtractedString{Value: &v, Confidence: clamp(.66 + b.Confidence*.25), SourceBlockIDs: []string{b.ID}, Method: "time_regex_v1"}
				}
			}
		}
	}
	return date, tm
}

func validDate(ys, ms, ds string) (int, int, int, bool) {
	y, _ := strconv.Atoi(normalize(ys))
	m, _ := strconv.Atoi(normalize(ms))
	d, _ := strconv.Atoi(normalize(ds))
	if y < 2000 || y > 2100 || m < 1 || m > 12 || d < 1 || d > 31 {
		return 0, 0, 0, false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return y, m, d, t.Year() == y && int(t.Month()) == m && t.Day() == d
}

func labeledMoney(blocks []domain.TextBlock, include, exclude []string) domain.ExtractedMoney {
	var best candidate[int64]
	for _, b := range blocks {
		line := normalize(b.Text)
		included := false
		rank := 0.0
		for j, k := range include {
			if containsCompact(line, k) {
				included = true
				rank = float64(len(include)-j) / float64(len(include))
				break
			}
		}
		bad := false
		for _, k := range exclude {
			if containsCompact(line, k) {
				bad = true
				break
			}
		}
		if bad {
			continue
		}
		texts := []moneyText{{text: line, id: b.ID}}
		if included {
			texts = append(texts, spatialMoneyNeighbors(blocks, b)...)
		}
		for _, entry := range texts {
			tokens := monetaryTokens(entry.text)
			if entry.id != b.ID && standaloneMoneyRE.FindStringSubmatch(normalize(entry.text)) == nil {
				tokens = nil
			}
			for _, token := range tokens {
				amount, ok := token.amount, token.amount > 0
				if !ok || amount <= 0 {
					continue
				}
				score := .25 + b.Confidence*.15 + (1-clamp(b.Box.Y))*.05
				if included {
					score += .42 + rank*.12
				} else {
					continue
				}
				score -= entry.distance
				if score > best.score {
					ids := []string{b.ID}
					if entry.id != b.ID {
						ids = append(ids, entry.id)
					}
					best = candidate[int64]{amount, clamp(score), ids}
				}
			}
		}
	}
	if best.score == 0 {
		return emptyMoney()
	}
	return domain.ExtractedMoney{Value: &best.value, Currency: "JPY", Confidence: best.score, SourceBlockIDs: best.ids, Method: "labeled_amount_score_v1"}
}

type moneyText struct {
	text, id string
	distance float64
}

func spatialMoneyNeighbors(blocks []domain.TextBlock, label domain.TextBlock) []moneyText {
	type neighbor struct {
		moneyText
		sortDistance float64
	}
	var found []neighbor
	lcx, lcy := boxCenter(label.Box)
	for _, b := range blocks {
		if b.ID == label.ID || standaloneMoneyRE.FindStringSubmatch(normalize(b.Text)) == nil {
			continue
		}
		cx, cy := boxCenter(b.Box)
		dx, dy := cx-lcx, math.Abs(cy-lcy)
		sameRow := dy <= math.Max(.035, math.Max(label.Box.Height, b.Box.Height)*1.25)
		justBelow := cy >= lcy && cy-lcy <= .085
		if (!sameRow || dx < -.04 || dx > .65) && (!justBelow || math.Abs(dx) > .45) {
			continue
		}
		distance := dy*1.6 + math.Max(0, dx)*.12
		found = append(found, neighbor{moneyText: moneyText{text: normalize(b.Text), id: b.ID, distance: math.Min(.18, distance)}, sortDistance: distance})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].sortDistance < found[j].sortDistance })
	result := make([]moneyText, 0, len(found))
	for _, n := range found {
		result = append(result, n.moneyText)
	}
	return result
}

// positionedYenTotal handles receipts where OCR reads the amount but drops or
// splits the large "合計" label. It only considers standalone values with an
// explicit yen sign, then combines typography and receipt geometry with nearby
// subtotal/tax/payment labels. This deliberately avoids treating arbitrary IDs,
// dates, telephone numbers, or item prices as a total.
func positionedYenTotal(blocks []domain.TextBlock) domain.ExtractedMoney {
	if len(blocks) == 0 {
		return emptyMoney()
	}
	minX, minY, maxX, maxY, medianHeight := textBounds(blocks)
	spanX, spanY := math.Max(.01, maxX-minX), math.Max(.01, maxY-minY)
	var best candidate[int64]
	for _, b := range blocks {
		line := normalize(b.Text)
		match := explicitYenMoneyRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		amount, ok := parseMoney(match[1])
		if !ok || amount <= 0 || amount > 1000000000 {
			continue
		}

		cx, cy := boxCenter(b.Box)
		relativeY := (cy - minY) / spanY
		relativeX := (cx - minX) / spanX
		prominence := b.Box.Height / math.Max(.005, medianHeight)
		positive := nearbyKeywordScore(blocks, b, totalContextKeywords, false)
		negative := nearbyKeywordScore(blocks, b, paymentKeywords, true)

		score := .23 + clamp(b.Confidence)*.22 + .24 // OCR quality + explicit yen sign.
		score += .10                                 // The entire block is a single monetary value.
		if relativeY >= .25 && relativeY <= .78 {
			score += .08
		} else {
			score -= .08
		}
		if relativeX >= .58 {
			score += .05
		}
		if prominence > 1 {
			score += math.Min(.18, (prominence-1)*.24)
		}
		score += math.Min(.24, positive)
		score -= math.Min(.42, negative)
		if positive == 0 && prominence < 1.18 {
			score -= .12
		}

		if score > best.score {
			// A missing total label means this remains an inference even when the
			// geometric evidence is strong; do not report artificial certainty.
			best = candidate[int64]{value: amount, score: math.Min(.92, clamp(score)), ids: []string{b.ID}}
		}
	}
	if best.score < .58 {
		return emptyMoney()
	}
	return domain.ExtractedMoney{Value: &best.value, Currency: "JPY", Confidence: best.score, SourceBlockIDs: best.ids, Method: "positioned_yen_amount_score_v2"}
}

func nearbyKeywordScore(blocks []domain.TextBlock, amount domain.TextBlock, keywords []string, sameRowOnly bool) float64 {
	acx, acy := boxCenter(amount.Box)
	score := 0.0
	for _, b := range blocks {
		if b.ID == amount.ID {
			continue
		}
		line := normalize(b.Text)
		matched := false
		for _, keyword := range keywords {
			if strings.Contains(line, keyword) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		cx, cy := boxCenter(b.Box)
		dy := math.Abs(cy - acy)
		leftOfAmount := cx <= acx+.06
		if !leftOfAmount {
			continue
		}
		if dy <= .045 {
			score += .22 * (1 - dy/.055)
		} else if !sameRowOnly && dy <= .09 {
			score += .08 * (1 - (dy-.045)/.045)
		}
	}
	return score
}

func textBounds(blocks []domain.TextBlock) (minX, minY, maxX, maxY, medianHeight float64) {
	minX, minY = 1, 1
	var heights []float64
	for _, b := range blocks {
		if strings.TrimSpace(b.Text) == "" || b.Confidence < .15 {
			continue
		}
		minX = math.Min(minX, b.Box.X)
		minY = math.Min(minY, b.Box.Y)
		maxX = math.Max(maxX, b.Box.X+b.Box.Width)
		maxY = math.Max(maxY, b.Box.Y+b.Box.Height)
		if b.Box.Height > 0 {
			heights = append(heights, b.Box.Height)
		}
	}
	if len(heights) == 0 {
		return minX, minY, maxX, maxY, .02
	}
	sort.Float64s(heights)
	medianHeight = heights[len(heights)/2]
	return
}

func boxCenter(box domain.BoundingBox) (float64, float64) {
	return box.X + box.Width/2, box.Y + box.Height/2
}

func taxMoney(blocks []domain.TextBlock, rate string, taxOnly bool) domain.ExtractedMoney {
	var best candidate[int64]
	for i, b := range blocks {
		t := normalize(b.Text)
		if !strings.Contains(t, rate+"%") && !strings.Contains(t, rate+" %") {
			continue
		}
		isTax := strings.Contains(t, "消費税") || strings.Contains(t, "税額") || strings.Contains(t, "内税") || strings.Contains(t, "外税")
		isTarget := strings.Contains(t, "対象") || strings.Contains(t, "課税")
		if taxOnly && !isTax {
			continue
		}
		if !taxOnly && !isTarget {
			continue
		}
		search := []domain.TextBlock{b}
		if i+1 < len(blocks) {
			search = append(search, blocks[i+1])
		}
		for _, sb := range search {
			matches := moneyRE.FindAllStringSubmatch(normalize(sb.Text), -1)
			if len(matches) == 0 {
				continue
			}
			raw := matches[len(matches)-1]
			amount, ok := parseMoney(raw[1])
			if !ok {
				continue
			}
			score := .65 + b.Confidence*.25
			if sb.ID != b.ID {
				score -= .08
			}
			if score > best.score {
				best = candidate[int64]{amount, clamp(score), []string{b.ID, sb.ID}}
			}
		}
	}
	if best.score == 0 {
		return emptyMoney()
	}
	return domain.ExtractedMoney{Value: &best.value, Currency: "JPY", Confidence: best.score, SourceBlockIDs: unique(best.ids), Method: "tax_rate_label_v1"}
}

func registration(blocks []domain.TextBlock) domain.ExtractedString {
	for _, b := range blocks {
		if m := registrationRE.FindStringSubmatch(normalize(b.Text)); m != nil {
			digits := onlyDigits(m[1])
			if len(digits) == 13 {
				v := "T" + digits
				return domain.ExtractedString{Value: &v, Confidence: clamp(.78 + b.Confidence*.2), SourceBlockIDs: []string{b.ID}, Method: "registration_regex_v1"}
			}
		}
	}
	return domain.ExtractedString{Method: "not_found", SourceBlockIDs: []string{}}
}

func payment(blocks []domain.TextBlock) domain.ExtractedString {
	methods := []struct {
		keys  []string
		value string
	}{{[]string{"現金"}, "cash"}, {[]string{"VISA", "MASTERCARD", "JCB", "AMEX", "クレジット", "カード"}, "credit_card"}, {[]string{"SUICA", "PASMO", "交通系IC"}, "transport_ic"}, {[]string{"PAYPAY", "QRコード", "QR決済"}, "qr_payment"}, {[]string{"電子マネー", "IC決済"}, "e_money"}, {[]string{"ポイント"}, "points"}}
	for _, b := range blocks {
		u := strings.ToUpper(normalize(b.Text))
		for _, m := range methods {
			for _, k := range m.keys {
				if strings.Contains(u, strings.ToUpper(k)) {
					v := m.value
					return domain.ExtractedString{Value: &v, Confidence: clamp(.62 + b.Confidence*.28), SourceBlockIDs: []string{b.ID}, Method: "payment_keyword_v1"}
				}
			}
		}
	}
	v := "unknown"
	return domain.ExtractedString{Value: &v, Confidence: 0, SourceBlockIDs: []string{}, Method: "not_found"}
}

func validate(obs domain.ReceiptObservation, imported time.Time) []domain.ValidationWarning {
	var w []domain.ValidationWarning
	missing := func(field, label string, present bool) {
		if !present {
			w = append(w, domain.ValidationWarning{Code: "FIELD_NOT_FOUND", Field: field, Message: label + "を特定できませんでした"})
		}
	}
	missing("merchant", "店舗名", obs.Merchant.Value != nil)
	missing("transaction_date", "取引日", obs.TransactionDate.Value != nil)
	missing("total_amount", "合計金額", obs.TotalAmount.Value != nil)
	if obs.TransactionDate.Value != nil {
		if d, err := time.Parse("2006-01-02", *obs.TransactionDate.Value); err == nil {
			if d.After(imported.AddDate(0, 0, 1)) {
				w = append(w, domain.ValidationWarning{Code: "FUTURE_DATE", Field: "transaction_date", Message: "取引日が取り込み日より未来です"})
			}
			if d.Before(imported.AddDate(-10, 0, 0)) {
				w = append(w, domain.ValidationWarning{Code: "OLD_DATE", Field: "transaction_date", Message: "取引日が10年以上前です"})
			}
		}
	}
	if obs.TotalAmount.Value != nil {
		total := *obs.TotalAmount.Value
		targets := int64(0)
		if obs.TaxableAmount10.Value != nil {
			targets += *obs.TaxableAmount10.Value
		}
		if obs.TaxableAmount8.Value != nil {
			targets += *obs.TaxableAmount8.Value
		}
		if targets > total {
			w = append(w, domain.ValidationWarning{Code: "TAX_TOTAL_MISMATCH", Field: "tax", Message: "税率別対象額の合計が合計金額を超えています"})
		}
	}
	for _, summary := range obs.TaxSummaries {
		if summary.Status == "ambiguous" {
			w = append(w, domain.ValidationWarning{Code: "TAX_CANDIDATE_AMBIGUOUS", Field: "tax", Message: fmt.Sprintf("%d%%の内税・外税または金額候補を安全に確定できませんでした", summary.Rate)})
			continue
		}
		if summary.TaxableAmount.Value == nil || summary.TaxAmount.Value == nil {
			continue
		}
		base, tax := *summary.TaxableAmount.Value, *summary.TaxAmount.Value
		expected := calculatedTax(base, summary.Rate, summary.PriceBasis, summary.RoundingMode)
		if expected > 0 && absInt64(expected-tax) > 1 {
			w = append(w, domain.ValidationWarning{Code: "TAX_CALC_MISMATCH", Field: "tax", Message: fmt.Sprintf("%d%%の対象額と税額が計算上整合しません", summary.Rate)})
		}
	}
	if obs.RegionAmbiguous {
		w = append(w, domain.ValidationWarning{Code: "REGION_MIX_AMBIGUOUS", Field: "tax", Message: "複数の証憑領域を分離できないため，税額と商品明細を未確定にしました"})
	}
	if obs.Accounting.Status == "ambiguous" {
		w = append(w, domain.ValidationWarning{Code: "ACCOUNTING_EQUATION_MISMATCH", Field: "total_amount", Message: "小計・税額・値引または預り金・釣銭と合計が整合しません"})
	}
	if obs.TotalAmount.Value != nil && len(obs.Items) > 0 {
		itemTotal := int64(0)
		for _, item := range obs.Items {
			if item.Amount == nil {
				continue
			}
			amount := *item.Amount
			if item.TaxIncluded != nil && !*item.TaxIncluded && item.TaxRate != nil {
				amount = int64(math.Round(float64(amount) * float64(100+*item.TaxRate) / 100))
			}
			itemTotal += amount
		}
		if itemTotal > 0 && absInt64(itemTotal-*obs.TotalAmount.Value) > 1 {
			w = append(w, domain.ValidationWarning{Code: "ITEM_TOTAL_UNRECONCILED", Field: "items", Message: "抽出できた商品明細の合計がレシート合計と一致しません．未抽出商品・値引・税区分を確認してください"})
		}
	}
	return w
}

func parseMoney(s string) (int64, bool) {
	s = strings.ReplaceAll(normalize(s), ",", "")
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseInt(s, 10, 64)
	return v, err == nil
}
func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range normalize(s) {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
func clamp(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
func unique(v []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, x := range v {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
