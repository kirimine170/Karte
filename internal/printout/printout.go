package printout

import (
	"fmt"
	"regexp"
	"strings"
)

const Infinite = "infinite"

type Spec struct {
	Name     string
	WidthMM  float64
	HeightMM float64
	Infinite bool
}

var paperSizesMM = map[string][2]float64{
	"A0": {841, 1189},
	"A1": {594, 841},
	"A2": {420, 594},
	"A3": {297, 420},
	"A4": {210, 297},
	"A5": {148, 210},
	"A6": {105, 148},
	"B0": {1030, 1456},
	"B1": {728, 1030},
	"B2": {515, 728},
	"B3": {364, 515},
	"B4": {257, 364},
	"B5": {182, 257},
	"B6": {128, 182},
}

func KnownPaperNames() []string {
	return []string{"A0", "A1", "A2", "A3", "A4", "A5", "A6", "B0", "B1", "B2", "B3", "B4", "B5", "B6"}
}

func Normalize(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return Infinite
	}
	if strings.EqualFold(n, Infinite) {
		return Infinite
	}
	up := strings.ToUpper(n)
	if _, ok := paperSizesMM[up]; ok {
		return up
	}
	return Infinite
}

func Resolve(name string) Spec {
	normalized := Normalize(name)
	if normalized == Infinite {
		return Spec{Name: Infinite, Infinite: true}
	}
	dims := paperSizesMM[normalized]
	return Spec{Name: normalized, WidthMM: dims[0], HeightMM: dims[1], Infinite: false}
}

func ParseFromHTML(html string) Spec {
	metaPattern := regexp.MustCompile(`(?i)<meta[^>]+name=["']karte-printout["'][^>]+content=["']([^"']+)["'][^>]*>`)
	if m := metaPattern.FindStringSubmatch(html); len(m) > 1 {
		return Resolve(m[1])
	}
	attrPattern := regexp.MustCompile(`(?i)<html[^>]+data-printout=["']([^"']+)["'][^>]*>`)
	if m := attrPattern.FindStringSubmatch(html); len(m) > 1 {
		return Resolve(m[1])
	}
	return Resolve("")
}

func (s Spec) CSSSizeValue() string {
	if s.Infinite {
		return "auto"
	}
	return fmt.Sprintf("%.3gmm %.3gmm", s.WidthMM, s.HeightMM)
}

func (s Spec) WidthPT() float64 {
	if s.Infinite {
		return 0
	}
	return mmToPt(s.WidthMM)
}

func (s Spec) HeightPT() float64 {
	if s.Infinite {
		return 0
	}
	return mmToPt(s.HeightMM)
}

func mmToPt(mm float64) float64 {
	return mm * 72.0 / 25.4
}
