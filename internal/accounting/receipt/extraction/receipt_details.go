package extraction

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"karte/internal/accounting/receipt/domain"
)

type monetaryToken struct {
	amount      int64
	start       int
	end         int
	explicitYen bool
	marker      string
}

type taxValueCandidate struct {
	value int64
	score float64
	key   string
	ids   []string
}

type taxLabel struct {
	block domain.TextBlock
	role  string
}

var (
	moneyTokenRE = regexp.MustCompile(`(?:[¥￥]\s*)?([0-9０-９][0-9０-９,，]*)\s*(円|[内外※*])?`)
	quantityRE   = regexp.MustCompile(`(?i)(?:[@＠]\s*([0-9０-９,，]+)\s*)?[×xX]\s*([0-9０-９]+)\s*(?:個|点|本|枚)`)
	leadingQtyRE = regexp.MustCompile(`^\s*([0-9０-９]+)\s*[.．]\s*`)
)

func compactText(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || r == '　' {
			return -1
		}
		return r
	}, normalize(s))
}

func containsCompact(text, keyword string) bool {
	return strings.Contains(compactText(text), compactText(keyword))
}

// monetaryTokens returns monetary-looking numbers while excluding percentages.
// This is the key guard against reading the "10" in "10%税額" as ten yen.
func monetaryTokens(text string) []monetaryToken {
	normalized := normalize(text)
	indices := moneyTokenRE.FindAllStringSubmatchIndex(normalized, -1)
	result := make([]monetaryToken, 0, len(indices))
	for _, index := range indices {
		if len(index) < 6 {
			continue
		}
		tail := strings.TrimSpace(normalized[index[1]:])
		if strings.HasPrefix(tail, "%") || strings.HasPrefix(tail, "％") {
			continue
		}
		amount, ok := parseMoney(normalized[index[2]:index[3]])
		if !ok {
			continue
		}
		marker := ""
		if index[4] >= 0 {
			marker = normalized[index[4]:index[5]]
		}
		whole := normalized[index[0]:index[1]]
		result = append(result, monetaryToken{
			amount: amount, start: index[0], end: index[1],
			explicitYen: strings.ContainsAny(whole, "¥￥") || marker == "円",
			marker:      marker,
		})
	}
	return result
}

type taxHypothesis struct {
	target, tax     taxValueCandidate
	basis, rounding string
	normalized      int64
	difference      int64
	score           float64
	components      map[string]float64
}

func extractTaxSummaries(blocks []domain.TextBlock, total domain.ExtractedMoney, cfg ScoringConfig) []domain.TaxSummary {
	result := make([]domain.TaxSummary, 0, 2)
	accountingTotal := total
	if len(taxLabelsForRate(blocks, 8)) > 0 && len(taxLabelsForRate(blocks, 10)) > 0 {
		accountingTotal = emptyMoney()
	}
	for _, rate := range []int{10, 8} {
		labels := taxLabelsForRate(blocks, rate)
		if len(labels) == 0 {
			continue
		}
		var targetCandidates, taxCandidates []taxValueCandidate
		basis := "unknown"
		var rateIDs []string
		for _, label := range labels {
			rateIDs = append(rateIDs, label.block.ID)
			line := compactText(label.block.Text)
			if strings.Contains(line, "税抜") || strings.Contains(line, "外税") {
				basis = "tax_excluded"
			} else if basis == "unknown" && (strings.Contains(line, "税込") || strings.Contains(line, "内税")) {
				basis = "tax_included"
			}
			candidates := candidatesForTaxLabel(blocks, label)
			if label.role == "target" {
				targetCandidates = append(targetCandidates, candidates...)
			} else {
				taxCandidates = append(taxCandidates, candidates...)
			}
		}
		hypotheses := rankTaxHypotheses(blocks, rate, targetCandidates, taxCandidates, basis, accountingTotal, cfg)
		if len(hypotheses) == 0 {
			continue
		}
		best := hypotheses[0]
		viable := make([]taxHypothesis, 0, len(hypotheses))
		for _, hypothesis := range hypotheses {
			if hypothesis.difference <= cfg.CalculationToleranceYen && hypothesis.components["accounting"] >= .75 {
				viable = append(viable, hypothesis)
			}
		}
		margin := 0.0
		if len(viable) > 0 {
			best = viable[0]
			margin = best.score
			if len(viable) > 1 {
				margin -= viable[1].score
			}
		}
		alternatives := make([]domain.MoneyCandidate, 0, minInt(5, len(hypotheses)))
		for _, h := range hypotheses[:minInt(5, len(hypotheses))] {
			alternatives = append(alternatives, domain.MoneyCandidate{Value: h.target.value, Score: h.score, PriceBasis: h.basis, RoundingMode: h.rounding, SourceBlockIDs: unique(append(h.target.ids, h.tax.ids...)), Components: h.components})
		}
		summary := domain.TaxSummary{Rate: rate, TaxableAmount: emptyMoney(), NormalizedNetAmount: emptyMoney(), TaxAmount: emptyMoney(), PriceBasis: domain.TaxBasisUnknown, RoundingMode: domain.RoundingUnknown, Status: "ambiguous", Confidence: clamp(best.score), SourceBlockIDs: unique(append(rateIDs, append(best.target.ids, best.tax.ids...)...)), Candidates: alternatives}
		if len(viable) > 0 && best.score >= cfg.AdoptionThreshold && margin >= cfg.MinimumMargin {
			targetValue, taxValue, normalized := best.target.value, best.tax.value, best.normalized
			summary.TaxableAmount = domain.ExtractedMoney{Value: &targetValue, Currency: "JPY", Confidence: clamp(best.score), SourceBlockIDs: unique(best.target.ids), Method: "candidate_reranking_v3"}
			summary.TaxAmount = domain.ExtractedMoney{Value: &taxValue, Currency: "JPY", Confidence: clamp(best.score), SourceBlockIDs: unique(best.tax.ids), Method: "candidate_reranking_v3"}
			summary.NormalizedNetAmount = domain.ExtractedMoney{Value: &normalized, Currency: "JPY", Confidence: clamp(best.score), SourceBlockIDs: summary.SourceBlockIDs, Method: "tax_normalization_v1"}
			summary.PriceBasis, summary.RoundingMode, summary.CalculationDifference, summary.Status = best.basis, best.rounding, best.difference, "confirmed"
		}
		result = append(result, summary)
	}
	return result
}

func rankTaxHypotheses(blocks []domain.TextBlock, rate int, targets, taxes []taxValueCandidate, labelBasis string, total domain.ExtractedMoney, cfg ScoringConfig) []taxHypothesis {
	if len(targets) == 0 || len(taxes) == 0 {
		return nil
	}
	byID := map[string]domain.TextBlock{}
	for _, block := range blocks {
		byID[block.ID] = block
	}
	seen := map[string]bool{}
	hasExplicitExternalEquation := false
	if labelBasis == domain.TaxBasisUnknown && total.Value != nil {
		for _, target := range targets {
			for _, tax := range taxes {
				if target.value+tax.value == *total.Value && absInt64(calculatedTax(target.value, rate, domain.TaxBasisExcluded, domain.RoundingFloor)-tax.value) <= cfg.CalculationToleranceYen {
					hasExplicitExternalEquation = true
				}
			}
		}
	}
	var result []taxHypothesis
	for _, target := range targets {
		for _, tax := range taxes {
			if target.key == tax.key || target.value <= 0 || tax.value <= 0 || tax.value >= target.value {
				continue
			}
			for _, basis := range []string{domain.TaxBasisIncluded, domain.TaxBasisExcluded} {
				var bestForBasis *taxHypothesis
				for _, rounding := range []string{domain.RoundingFloor, domain.RoundingNearest, domain.RoundingCeil} {
					expected := calculatedTax(target.value, rate, basis, rounding)
					difference := absInt64(expected - tax.value)
					semantic := clamp((target.score + tax.score) / 2)
					if labelBasis == basis {
						semantic = clamp(semantic + .40)
					} else if labelBasis != domain.TaxBasisUnknown {
						semantic = clamp(semantic - .65)
					}
					if labelBasis == domain.TaxBasisUnknown && total.Value != nil && sharesID(target.ids, total.SourceBlockIDs) {
						semantic = clamp(semantic - .50)
					}
					if hasExplicitExternalEquation && basis == domain.TaxBasisIncluded && total.Value != nil && target.value == *total.Value {
						semantic = clamp(semantic - .50)
					}
					position := pairPositionScore(byID, target.ids, tax.ids)
					ocrScore := averageOCRConfidence(byID, append(target.ids, tax.ids...))
					accounting := accountingScore(target.value, tax.value, rate, basis, rounding, total, cfg.CalculationToleranceYen)
					expectedTotal := target.value
					if basis == domain.TaxBasisExcluded {
						expectedTotal += tax.value
					}
					if total.Value != nil && absInt64(expectedTotal-*total.Value) > cfg.CalculationToleranceYen && hasLabeledAmountCandidate(blocks, expectedTotal) {
						accounting = math.Max(accounting, .90)
					}
					components := map[string]float64{"semantic": semantic, "position": position, "ocr": ocrScore, "accounting": accounting, "correction": 0}
					score := cfg.SemanticWeight*semantic + cfg.PositionWeight*position + cfg.OCRWeight*ocrScore + cfg.AccountingWeight*accounting
					// A mathematically contradictory pair must not remain a close second
					// solely because its boxes and labels are visually convincing.
					if accounting < .5 {
						score *= .75
					}
					normalized := target.value
					if basis == domain.TaxBasisIncluded {
						normalized = target.value - tax.value
					}
					h := taxHypothesis{target: target, tax: tax, basis: basis, rounding: rounding, normalized: normalized, difference: difference, score: clamp(score), components: components}
					if bestForBasis == nil || h.score > bestForBasis.score || (h.score == bestForBasis.score && h.difference < bestForBasis.difference) {
						copy := h
						bestForBasis = &copy
					}
				}
				key := fmt.Sprintf("%d/%d/%s", target.value, tax.value, basis)
				if bestForBasis != nil && !seen[key] {
					seen[key] = true
					result = append(result, *bestForBasis)
				}
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].score == result[j].score {
			return result[i].difference < result[j].difference
		}
		return result[i].score > result[j].score
	})
	return result
}

func sharesID(left, right []string) bool {
	values := map[string]bool{}
	for _, id := range left {
		values[id] = true
	}
	for _, id := range right {
		if values[id] {
			return true
		}
	}
	return false
}

func calculatedTax(amount int64, rate int, basis, rounding string) int64 {
	denominator := int64(100)
	if basis == domain.TaxBasisIncluded {
		denominator += int64(rate)
	}
	numerator := float64(amount*int64(rate)) / float64(denominator)
	switch rounding {
	case domain.RoundingFloor:
		return int64(math.Floor(numerator))
	case domain.RoundingCeil:
		return int64(math.Ceil(numerator))
	default:
		return int64(math.Round(numerator))
	}
}

func accountingScore(target, tax int64, rate int, basis, rounding string, total domain.ExtractedMoney, tolerance int64) float64 {
	difference := absInt64(calculatedTax(target, rate, basis, rounding) - tax)
	score := math.Max(0, 1-float64(difference)/math.Max(3, float64(tax)*.12))
	if difference <= tolerance {
		score = 1
	}
	if total.Value != nil {
		expectedTotal := target
		if basis == domain.TaxBasisExcluded {
			expectedTotal += tax
		}
		totalDifference := absInt64(expectedTotal - *total.Value)
		if totalDifference <= tolerance {
			score = math.Min(1, score+.18)
		} else if totalDifference > maxInt64(3, *total.Value/100) {
			score *= .40
		}
	}
	return clamp(score)
}

func hasLabeledAmountCandidate(blocks []domain.TextBlock, expected int64) bool {
	var anchors []domain.TextBlock
	for _, block := range blocks {
		if containsCompact(block.Text, "合計") && !containsCompact(block.Text, "小計") {
			anchors = append(anchors, block)
		}
	}
	for _, block := range blocks {
		matches := false
		for _, token := range monetaryTokens(block.Text) {
			if token.amount == expected {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		for _, anchor := range anchors {
			_, ay := boxCenter(anchor.Box)
			_, by := boxCenter(block.Box)
			if math.Abs(ay-by) <= .05 {
				return true
			}
		}
	}
	return false
}

func averageOCRConfidence(blocks map[string]domain.TextBlock, ids []string) float64 {
	total, count := 0.0, 0.0
	for _, id := range unique(ids) {
		if b, ok := blocks[id]; ok {
			total += clamp(b.Confidence)
			count++
		}
	}
	if count == 0 {
		return .5
	}
	return total / count
}

func pairPositionScore(blocks map[string]domain.TextBlock, targets, taxes []string) float64 {
	best := .55
	for _, a := range targets {
		for _, b := range taxes {
			left, okA := blocks[a]
			right, okB := blocks[b]
			if !okA || !okB {
				continue
			}
			_, ay := boxCenter(left.Box)
			_, by := boxCenter(right.Box)
			best = math.Max(best, 1-math.Min(.45, math.Abs(ay-by)*2.5+float64(absInt(left.Order-right.Order))*.025))
		}
	}
	return clamp(best)
}

func taxLabelsForRate(blocks []domain.TextBlock, rate int) []taxLabel {
	rateText := strconv.Itoa(rate)
	var labels []taxLabel
	var rateAnchors []domain.TextBlock
	for _, block := range blocks {
		line := compactText(block.Text)
		hasRate := strings.Contains(line, rateText+"%") || strings.Contains(line, "税率"+rateText)
		if !hasRate {
			continue
		}
		rateAnchors = append(rateAnchors, block)
		if strings.Contains(line, "対象") || strings.Contains(line, "課税") || strings.Contains(line, "税抜") || strings.Contains(line, "税込") {
			labels = append(labels, taxLabel{block: block, role: "target"})
		}
		if strings.Contains(line, "消費税") || strings.Contains(line, "税額") || strings.Contains(line, "内税") || strings.Contains(line, "外税") {
			labels = append(labels, taxLabel{block: block, role: "tax"})
		}
	}
	// Some formats print "課税対象" and "消費税 10%" in separate blocks.
	for _, block := range blocks {
		line := compactText(block.Text)
		role := ""
		if strings.Contains(line, "対象") || strings.Contains(line, "課税") {
			role = "target"
		} else if strings.Contains(line, "消費税") || strings.Contains(line, "税額") || strings.Contains(line, "内税") || strings.Contains(line, "外税") {
			role = "tax"
		}
		if role == "" || strings.Contains(line, rateText+"%") {
			continue
		}
		for _, anchor := range rateAnchors {
			_, ay := boxCenter(anchor.Box)
			_, by := boxCenter(block.Box)
			if absInt(anchor.Order-block.Order) <= 4 || math.Abs(ay-by) <= .075 {
				labels = append(labels, taxLabel{block: block, role: role})
				break
			}
		}
	}
	return labels
}

func candidatesForTaxLabel(blocks []domain.TextBlock, label taxLabel) []taxValueCandidate {
	var result []taxValueCandidate
	line := normalize(label.block.Text)
	tokens := monetaryTokens(line)
	semantic := semanticIndex(line, label.role)
	for i, token := range tokens {
		score := .76
		if token.explicitYen {
			score += .08
		}
		distance := absInt(token.start - semantic)
		if semantic >= 0 {
			score += math.Max(0, .12-float64(distance)*.008)
		}
		if token.start >= semantic && semantic >= 0 {
			score += .05
		}
		// "4,600 消費税 10%" describes the taxable base; a tax amount is
		// normally after the tax wording. Parenthesized "460 の消費税" is the
		// common exception.
		if label.role == "tax" && semantic >= 0 && token.end <= semantic && !strings.Contains(compactText(line), "の消費税") {
			score -= .30
		}
		result = append(result, taxValueCandidate{value: token.amount, score: clamp(score), key: label.block.ID + ":" + strconv.Itoa(i), ids: []string{label.block.ID}})
	}
	lcx, lcy := boxCenter(label.block.Box)
	for _, block := range blocks {
		if block.ID == label.block.ID || !isAmountFragment(block.Text) {
			continue
		}
		bcx, bcy := boxCenter(block.Box)
		dy := math.Abs(bcy - lcy)
		orderDistance := absInt(block.Order - label.block.Order)
		if dy > .075 && orderDistance > 4 {
			continue
		}
		for i, token := range monetaryTokens(block.Text) {
			if token.amount <= 0 {
				continue
			}
			score := .72 - math.Min(.24, dy*3.2) - math.Min(.16, float64(orderDistance)*.035)
			if bcx > lcx-.03 {
				score += .09
			}
			if token.explicitYen {
				score += .07
			}
			if orderDistance == 1 {
				score += .07
			}
			if score >= .48 {
				result = append(result, taxValueCandidate{value: token.amount, score: clamp(score), key: block.ID + ":" + strconv.Itoa(i), ids: []string{label.block.ID, block.ID}})
			}
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].score > result[j].score })
	if len(result) > 12 {
		result = result[:12]
	}
	return result
}

func semanticIndex(line, role string) int {
	terms := []string{"対象", "課税", "税抜", "税込"}
	if role == "tax" {
		terms = []string{"内消費税", "消費税", "税額", "内税", "外税"}
	}
	best := -1
	for _, term := range terms {
		if index := strings.LastIndex(line, term); index > best {
			best = index
		}
	}
	return best
}

func bestTaxPair(targets, taxes []taxValueCandidate) (taxValueCandidate, taxValueCandidate) {
	var bestTarget, bestTax taxValueCandidate
	bestScore := 0.0
	if len(targets) == 0 {
		targets = []taxValueCandidate{{}}
	}
	if len(taxes) == 0 {
		taxes = []taxValueCandidate{{}}
	}
	for _, target := range targets {
		for _, tax := range taxes {
			if target.key != "" && target.key == tax.key {
				continue
			}
			score := target.score + tax.score
			if target.value > 0 && tax.value > 0 {
				if tax.value < target.value {
					score += .08
				} else {
					score -= .25
				}
			}
			if score > bestScore {
				bestScore, bestTarget, bestTax = score, target, tax
			}
		}
	}
	return bestTarget, bestTax
}

func reconcileTotalWithTax(blocks []domain.TextBlock, current domain.ExtractedMoney, summaries []domain.TaxSummary, cfg ScoringConfig) domain.ExtractedMoney {
	if current.Value == nil || len(summaries) == 0 {
		return current
	}
	expected := int64(0)
	for _, summary := range summaries {
		if summary.TaxableAmount.Value == nil {
			return current
		}
		part := *summary.TaxableAmount.Value
		if summary.PriceBasis == "tax_excluded" {
			if summary.TaxAmount.Value == nil {
				return current
			}
			part += *summary.TaxAmount.Value
		}
		expected += part
	}
	if expected <= 0 || expected == *current.Value {
		return current
	}
	anchorIDs := map[string]bool{}
	for _, id := range current.SourceBlockIDs {
		anchorIDs[id] = true
	}
	var anchors []domain.TextBlock
	for _, block := range blocks {
		if anchorIDs[block.ID] || containsCompact(block.Text, "合計") {
			anchors = append(anchors, block)
		}
	}
	bestScore := 0.0
	bestID := ""
	for _, block := range blocks {
		for _, token := range monetaryTokens(block.Text) {
			if token.amount != expected {
				continue
			}
			for _, anchor := range anchors {
				acx, acy := boxCenter(anchor.Box)
				bcx, bcy := boxCenter(block.Box)
				distance := math.Abs(acy-bcy)*3 + math.Abs(acx-bcx)*.18 + float64(absInt(anchor.Order-block.Order))*.025
				score := .97 - math.Min(.35, distance)
				if token.explicitYen {
					score += .04
				}
				if score > bestScore {
					bestScore, bestID = score, block.ID
				}
			}
		}
	}
	if bestScore < cfg.AdoptionThreshold || bestID == "" {
		// The current value conflicts with the tax equation and the matching OCR
		// value is not strong enough. Do not auto-confirm a known-inconsistent sum.
		current.Value = nil
		current.Confidence = 0
		current.Method = "ambiguous_accounting_total"
		return current
	}
	value := expected
	ids := append([]string{}, current.SourceBlockIDs...)
	ids = append(ids, bestID)
	return domain.ExtractedMoney{Value: &value, Currency: "JPY", Confidence: clamp(bestScore), SourceBlockIDs: unique(ids), Method: "tax_reconciled_total_v1"}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func isAmountFragment(text string) bool {
	line := strings.TrimSpace(normalize(text))
	if line == "" || strings.ContainsAny(line, "×xX") {
		return false
	}
	tokens := monetaryTokens(line)
	if len(tokens) == 0 {
		return false
	}
	if len([]rune(line)) <= 18 {
		return true
	}
	return tokens[0].explicitYen && len([]rune(line)) <= 28
}

func extractItems(blocks []domain.TextBlock, obs domain.ReceiptObservation) []domain.ReceiptItem {
	excluded := map[string]bool{}
	addMoneyIDs := func(value domain.ExtractedMoney) {
		for _, id := range value.SourceBlockIDs {
			excluded[id] = true
		}
	}
	addMoneyIDs(obs.TotalAmount)
	for _, summary := range obs.TaxSummaries {
		addMoneyIDs(summary.TaxableAmount)
		addMoneyIDs(summary.TaxAmount)
	}

	type priceBlock struct {
		block domain.TextBlock
		token monetaryToken
	}
	var prices []priceBlock
	for _, block := range blocks {
		if excluded[block.ID] || !isLinePriceBlock(block.Text) {
			continue
		}
		tokens := monetaryTokens(block.Text)
		if len(tokens) == 0 {
			continue
		}
		token := tokens[len(tokens)-1]
		if token.amount <= 0 || token.amount > 100000000 {
			continue
		}
		if obs.TotalAmount.Value != nil && token.amount > *obs.TotalAmount.Value {
			continue
		}
		prices = append(prices, priceBlock{block: block, token: token})
	}

	usedNames := map[string]bool{}
	var items []domain.ReceiptItem
	for _, price := range prices {
		pcx, pcy := boxCenter(price.block.Box)
		bestScore := 0.0
		var best domain.TextBlock
		for _, block := range blocks {
			if block.ID == price.block.ID || usedNames[block.ID] || !isItemName(block.Text) {
				continue
			}
			bcx, bcy := boxCenter(block.Box)
			dy := math.Abs(bcy - pcy)
			if dy > math.Max(.045, math.Max(block.Box.Height, price.block.Box.Height)*1.35) || bcx >= pcx-.015 {
				continue
			}
			dx := pcx - bcx
			if dx > .58 {
				continue
			}
			score := .72 - dy*5 - math.Max(0, dx-.34)*.35 + clamp(block.Confidence)*.12
			if absInt(block.Order-price.block.Order) <= 3 {
				score += .08
			}
			if score > bestScore {
				bestScore, best = score, block
			}
		}
		if bestScore < .58 {
			continue
		}
		usedNames[best.ID] = true
		quantity, unitPrice, quantityIDs := itemQuantity(blocks, best, price.block, price.token.amount)
		amount := price.token.amount
		rate := singleTaxRate(obs.TaxSummaries)
		included := taxIncludedMarker(price.block.Text)
		if strings.ContainsAny(price.block.Text, "※*") && hasReducedTaxLegend(blocks) {
			reduced := 8
			rate = &reduced
			value := true
			included = &value
		}
		ids := unique(append([]string{best.ID, price.block.ID}, quantityIDs...))
		items = append(items, domain.ReceiptItem{
			ID: domain.NewID("item"), Name: strings.TrimSpace(normalize(best.Text)), Quantity: quantity,
			UnitPrice: unitPrice, Amount: &amount, TaxRate: rate, TaxIncluded: included,
			Confidence: clamp((bestScore + clamp(price.block.Confidence)) / 2), SourceBlockIDs: ids,
			Method: "item_row_geometry_v1", UsageCategory: "unassigned",
		})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return firstBlockOrder(blocks, items[i].SourceBlockIDs) < firstBlockOrder(blocks, items[j].SourceBlockIDs)
	})
	return items
}

func extractAccountingBreakdown(receiptBlocks []domain.TextBlock, obs domain.ReceiptObservation) domain.AccountingBreakdown {
	result := domain.AccountingBreakdown{
		NonTaxableAmount: labeledMoney(receiptBlocks, []string{"非課税対象", "非課税", "免税対象"}, []string{"消費税"}),
		DiscountAmount:   labeledMoney(receiptBlocks, []string{"値引", "割引", "クーポン"}, []string{"値引後"}),
		TenderedAmount:   labeledMoney(receiptBlocks, []string{"お預り", "預り", "預かり"}, []string{"釣銭", "おつり"}),
		ChangeAmount:     labeledMoney(receiptBlocks, []string{"釣銭", "おつり", "お返し"}, nil),
		Status:           "unknown",
	}
	var candidates []int64
	if obs.SubtotalAmount.Value != nil {
		base := *obs.SubtotalAmount.Value
		externalTax := int64(0)
		for _, summary := range obs.TaxSummaries {
			if summary.PriceBasis == domain.TaxBasisExcluded && summary.TaxAmount.Value != nil {
				externalTax += *summary.TaxAmount.Value
			}
		}
		candidates = append(candidates, base+externalTax)
		if result.DiscountAmount.Value != nil {
			candidates = append(candidates, base-*result.DiscountAmount.Value+externalTax)
		}
	}
	if result.TenderedAmount.Value != nil && result.ChangeAmount.Value != nil {
		candidates = append(candidates, *result.TenderedAmount.Value-*result.ChangeAmount.Value)
	}
	if obs.TotalAmount.Value == nil || len(candidates) == 0 {
		return result
	}
	best, difference := candidates[0], absInt64(candidates[0]-*obs.TotalAmount.Value)
	for _, candidate := range candidates[1:] {
		if d := absInt64(candidate - *obs.TotalAmount.Value); d < difference {
			best, difference = candidate, d
		}
	}
	result.CalculatedTotal, result.Difference = &best, difference
	if difference <= 1 {
		result.Status = "confirmed"
	} else {
		result.Status = "ambiguous"
	}
	return result
}

func isLinePriceBlock(text string) bool {
	line := strings.TrimSpace(normalize(text))
	if line == "" || strings.ContainsAny(line, "×xX") || containsCompact(line, "%") {
		return false
	}
	if standaloneMoneyRE.MatchString(line) {
		return true
	}
	if strings.ContainsAny(line, "¥￥") && len([]rune(line)) <= 18 {
		return true
	}
	return leadingQtyRE.MatchString(line) && len(monetaryTokens(line)) >= 2
}

func isItemName(text string) bool {
	line := strings.TrimSpace(normalize(text))
	runeCount := len([]rune(line))
	if runeCount < 4 || runeCount > 48 || moneyRE.MatchString(line) || dateYMDRE.MatchString(line) || timeRE.MatchString(line) {
		return false
	}
	upper := strings.ToUpper(line)
	excluded := []string{"合計", "小計", "対象", "消費税", "税額", "お預", "お釣", "支払", "クレジット", "カード", "領収", "登録番号", "レジ", "担当", "取引", "TEL", "電話", "住所", "〒", "株式会社", "ポイント", "会員", "返品", "保管", "お願い", "願い", "ありがとうございました", "印紙", "但し", "店舗", "端末", "伝票", "承認番号", "有効期限", "お客様控", "ご利用日", "お買上点数", "金額", "処理通番", "明細", "上記", "店", "税率適用", "今回", "自由です", "見せください", "再入場", "JCB", "VISA", "ビザ", "マスター", "IC", "QMO", "イートイン", "値引"}
	for _, word := range excluded {
		if strings.Contains(upper, strings.ToUpper(word)) {
			return false
		}
	}
	if strings.ContainsAny(line, ":：") {
		return false
	}
	for _, r := range line {
		if unicode.IsLetter(r) || unicode.In(r, unicode.Hiragana, unicode.Katakana, unicode.Han) {
			return true
		}
	}
	return false
}

func itemQuantity(blocks []domain.TextBlock, name, price domain.TextBlock, amount int64) (float64, *int64, []string) {
	quantity := 1.0
	unit := amount
	for _, block := range blocks {
		if absInt(block.Order-name.Order) > 4 && absInt(block.Order-price.Order) > 4 {
			continue
		}
		match := quantityRE.FindStringSubmatch(normalize(block.Text))
		if match == nil {
			continue
		}
		q, err := strconv.Atoi(normalize(match[2]))
		if err != nil || q <= 0 || q > 1000 {
			continue
		}
		quantity = float64(q)
		if match[1] != "" {
			if parsed, ok := parseMoney(match[1]); ok {
				unit = parsed
			}
		} else if amount%int64(q) == 0 {
			unit = amount / int64(q)
		}
		return quantity, &unit, []string{block.ID}
	}
	return quantity, &unit, nil
}

func singleTaxRate(summaries []domain.TaxSummary) *int {
	if len(summaries) != 1 {
		return nil
	}
	rate := summaries[0].Rate
	return &rate
}

func taxIncludedMarker(text string) *bool {
	line := compactText(text)
	if strings.Contains(line, "外") {
		value := false
		return &value
	}
	if strings.Contains(line, "内") || strings.ContainsAny(line, "※*") {
		value := true
		return &value
	}
	return nil
}

func hasReducedTaxLegend(blocks []domain.TextBlock) bool {
	for _, block := range blocks {
		line := compactText(block.Text)
		if strings.Contains(line, "軽減税率") && strings.Contains(line, "8%") {
			return true
		}
	}
	return false
}

func firstBlockOrder(blocks []domain.TextBlock, ids []string) int {
	best := int(^uint(0) >> 1)
	for _, block := range blocks {
		for _, id := range ids {
			if block.ID == id && block.Order < best {
				best = block.Order
			}
		}
	}
	return best
}

func receiptRegion(blocks []domain.TextBlock, anchor domain.ExtractedMoney, yRadius, xRadius float64) []domain.TextBlock {
	if anchor.Value == nil || len(anchor.SourceBlockIDs) == 0 {
		return blocks
	}
	ids := map[string]bool{}
	for _, id := range anchor.SourceBlockIDs {
		ids[id] = true
	}
	var x, y float64
	count := 0.0
	for _, block := range blocks {
		if !ids[block.ID] {
			continue
		}
		cx, cy := boxCenter(block.Box)
		x, y, count = x+cx, y+cy, count+1
	}
	if count == 0 {
		return blocks
	}
	x, y = x/count, y/count
	result := make([]domain.TextBlock, 0, len(blocks))
	for _, block := range blocks {
		cx, cy := boxCenter(block.Box)
		if math.Abs(cy-y) <= yRadius && math.Abs(cx-x) <= xRadius {
			result = append(result, block)
		}
	}
	return result
}

func isolateReceiptRegion(blocks []domain.TextBlock, selected domain.ExtractedMoney) (*domain.ReceiptRegion, bool) {
	type anchor struct {
		block domain.TextBlock
		x, y  float64
	}
	var anchors []anchor
	for _, block := range blocks {
		line := compactText(block.Text)
		if !strings.Contains(line, "合計") || strings.Contains(line, "小計") || strings.Contains(line, "合計点数") {
			continue
		}
		x, y := boxCenter(block.Box)
		duplicate := false
		for _, existing := range anchors {
			if math.Abs(existing.x-x) < .08 && math.Abs(existing.y-y) < .055 {
				duplicate = true
				break
			}
		}
		if !duplicate {
			anchors = append(anchors, anchor{block: block, x: x, y: y})
		}
	}
	selectedIDs := map[string]bool{}
	for _, id := range selected.SourceBlockIDs {
		selectedIDs[id] = true
	}
	var sx, sy float64
	found := false
	for _, block := range blocks {
		if selectedIDs[block.ID] {
			sx, sy = boxCenter(block.Box)
			found = true
			break
		}
	}
	if !found && len(anchors) == 1 {
		sx, sy, found = anchors[0].x, anchors[0].y, true
	}
	if !found {
		return nil, len(anchors) > 1
	}
	chosenDistance, secondDistance := math.MaxFloat64, math.MaxFloat64
	chosenAnchorID := ""
	for _, a := range anchors {
		d := math.Abs(a.y-sy)*2 + math.Abs(a.x-sx)
		if d < chosenDistance {
			secondDistance, chosenDistance = chosenDistance, d
			chosenAnchorID = a.block.ID
		} else if d < secondDistance {
			secondDistance = d
		}
	}
	if len(anchors) > 1 && secondDistance-chosenDistance < .06 {
		return nil, true
	}
	left, right, top, bottom := 0.0, 1.0, 0.0, 1.0
	for _, a := range anchors {
		if a.block.ID == chosenAnchorID {
			continue
		}
		if math.Abs(a.x-sx) < .25 {
			mid := (a.y + sy) / 2
			if a.y < sy && mid > top {
				top = mid
			}
			if a.y > sy && mid < bottom {
				bottom = mid
			}
		} else if math.Abs(a.y-sy) < .25 {
			mid := (a.x + sx) / 2
			if a.x < sx && mid > left {
				left = mid
			}
			if a.x > sx && mid < right {
				right = mid
			}
		}
	}
	// A receipt total is usually in the lower half. Keep enough space above it
	// for merchant and items, and a small payment section below it.
	top = math.Max(top, sy-.72)
	bottom = math.Min(bottom, sy+.22)
	box := domain.BoundingBox{X: left, Y: top, Width: right - left, Height: bottom - top}
	var ids []string
	for _, block := range blocksInBox(blocks, box) {
		ids = append(ids, block.ID)
	}
	confidence := .88
	if len(anchors) > 1 {
		confidence = .76
	}
	return &domain.ReceiptRegion{ID: domain.NewID("region"), Box: box, Confidence: confidence, SourceBlockIDs: ids}, false
}

func blocksInBox(blocks []domain.TextBlock, box domain.BoundingBox) []domain.TextBlock {
	var result []domain.TextBlock
	for _, block := range blocks {
		x, y := boxCenter(block.Box)
		if x >= box.X && x <= box.X+box.Width && y >= box.Y && y <= box.Y+box.Height {
			result = append(result, block)
		}
	}
	return result
}

func suspectedMultipleReceipts(blocks []domain.TextBlock) bool {
	var totals []float64
	registrations := map[string]bool{}
	for _, block := range blocks {
		line := compactText(block.Text)
		if strings.Contains(line, "合計") && !strings.Contains(line, "小計") {
			_, y := boxCenter(block.Box)
			totals = append(totals, y)
		}
		if match := registrationRE.FindStringSubmatch(normalize(block.Text)); match != nil {
			digits := onlyDigits(match[1])
			if len(digits) == 13 {
				registrations[digits] = true
			}
		}
	}
	if len(registrations) >= 2 {
		return true
	}
	for i := range totals {
		for j := i + 1; j < len(totals); j++ {
			if math.Abs(totals[i]-totals[j]) >= .12 {
				return true
			}
		}
	}
	return false
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func absInt64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}
