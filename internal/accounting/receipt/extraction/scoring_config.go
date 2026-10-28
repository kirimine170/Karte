package extraction

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type ScoringConfig struct {
	SemanticWeight          float64
	PositionWeight          float64
	OCRWeight               float64
	AccountingWeight        float64
	CorrectionWeight        float64
	AdoptionThreshold       float64
	MinimumMargin           float64
	CalculationToleranceYen int64
}

func DefaultScoringConfig() ScoringConfig {
	return ScoringConfig{SemanticWeight: .30, PositionWeight: .25, OCRWeight: .20, AccountingWeight: .20, CorrectionWeight: .05, AdoptionThreshold: .72, MinimumMargin: .12, CalculationToleranceYen: 1}
}

// LoadScoringConfig reads the intentionally small key/value YAML file without
// adding a runtime YAML dependency to the bundled application.
func LoadScoringConfig(path string) (ScoringConfig, error) {
	cfg := DefaultScoringConfig()
	f, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	values := map[string]*float64{
		"semantic_weight": &cfg.SemanticWeight, "position_weight": &cfg.PositionWeight,
		"ocr_weight": &cfg.OCRWeight, "accounting_weight": &cfg.AccountingWeight,
		"correction_weight": &cfg.CorrectionWeight, "adoption_threshold": &cfg.AdoptionThreshold,
		"minimum_margin": &cfg.MinimumMargin,
	}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.SplitN(scanner.Text(), "#", 2)[0])
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			return cfg, fmt.Errorf("invalid scoring config line: %q", line)
		}
		key, raw := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if key == "calculation_tolerance_yen" {
			value, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil {
				return cfg, parseErr
			}
			cfg.CalculationToleranceYen = value
			continue
		}
		target, ok := values[key]
		if !ok {
			continue
		}
		value, parseErr := strconv.ParseFloat(raw, 64)
		if parseErr != nil {
			return cfg, parseErr
		}
		*target = value
	}
	if err := scanner.Err(); err != nil {
		return cfg, err
	}
	if cfg.AdoptionThreshold <= 0 || cfg.AdoptionThreshold > 1 || cfg.MinimumMargin < 0 {
		return cfg, fmt.Errorf("invalid scoring thresholds")
	}
	return cfg, nil
}

func (c ScoringConfig) normalized() ScoringConfig {
	if c.AdoptionThreshold == 0 {
		return DefaultScoringConfig()
	}
	return c
}
