package decisionlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"karte/internal/canonical"
)

const (
	SettingsPath = ".mdsys/decision-log.json"
	Reviewer     = "雫さん"
)

var requiredPrivatePair = [2]string{"hsap", "essen"}

func defaultBusinesses() map[string]string {
	return map[string]string{
		"overall": "全体", "karte": "Karte", "hacksick": "HackSick", "essen": "ESSEN",
		"mirai-labo": "MIRAIラボ", "hsap": "HSAP", "ephy": "ephy", "far": "FAR",
	}
}

// InitializeSettings persists defaults on the first import. Review requires
// an on-disk reviewer so that missing configuration fails closed.
func InitializeSettings(w *canonical.Writer) error {
	if _, err := w.ReadLimit(SettingsPath, 64*1024); !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return writeSettings(w, Settings{
		Businesses:      defaultBusinesses(),
		PrivateTogether: [][]string{{requiredPrivatePair[0], requiredPrivatePair[1]}},
		Decider:         Reviewer,
	})
}

func writeSettings(w *canonical.Writer, settings Settings) error {
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return w.Write(SettingsPath, raw, 0600)
}

func requireReviewer(settings Settings, decider string) error {
	if settings.Decider != Reviewer || decider != Reviewer {
		return fmt.Errorf("decision review requires the configured reviewer %s", Reviewer)
	}
	return nil
}

type Settings struct {
	Businesses      map[string]string `json:"businesses"`
	PrivateTogether [][]string        `json:"private_together"`
	Decider         string            `json:"decider"`
}

func LoadSettings(w *canonical.Writer) (Settings, error) {
	settings := Settings{}
	raw, err := w.ReadLimit(SettingsPath, 64*1024)
	if errors.Is(err, os.ErrNotExist) {
		return settings, nil
	}
	if err != nil {
		return Settings{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&settings); err != nil {
		return Settings{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Settings{}, fmt.Errorf("invalid decision log settings JSON")
	}
	if err := settings.validate(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (settings Settings) validate() error {
	for id, label := range settings.Businesses {
		if !businessPattern.MatchString(id) || !validLabel(label) {
			return fmt.Errorf("invalid decision log business")
		}
	}
	owners := map[string]string{}
	for id, label := range settings.businessLabels() {
		for _, alias := range []string{normalizeName(id), normalizeName(label)} {
			if owner, exists := owners[alias]; exists && owner != id {
				return fmt.Errorf("duplicate decision log business label")
			}
			owners[alias] = id
		}
	}
	for _, pair := range settings.PrivateTogether {
		if len(pair) != 2 || !validLabel(pair[0]) || !validLabel(pair[1]) || normalizeName(pair[0]) == normalizeName(pair[1]) {
			return fmt.Errorf("invalid decision log publication rule")
		}
	}
	if settings.Decider != "" && !validLabel(settings.Decider) {
		return fmt.Errorf("invalid decision log reviewer")
	}
	return nil
}

func validLabel(label string) bool {
	return label != "" && strings.TrimSpace(label) == label && utf8.RuneCountInString(label) <= 128 &&
		strings.IndexFunc(label, unicode.IsControl) < 0
}

func normalizeName(name string) string { return strings.ToLower(norm.NFKC.String(name)) }

func (s Settings) businessLabels() map[string]string {
	labels := defaultBusinesses()
	for id, label := range s.Businesses {
		labels[id] = label
	}
	return labels
}

func (s Settings) requiresPrivate(record Record) bool {
	if record.PublicationRestriction != "" {
		return true
	}
	for _, pair := range s.publicationPairs() {
		if mentionsBusiness(record, pair[0]) && mentionsBusiness(record, pair[1]) {
			return true
		}
	}
	return false
}

func (s Settings) publicationPairs() [][2]string {
	pairs := [][2]string{requiredPrivatePair}
	for _, pair := range s.PrivateTogether {
		if len(pair) == 2 && (normalizeName(pair[0]) != requiredPrivatePair[0] || normalizeName(pair[1]) != requiredPrivatePair[1]) {
			pairs = append(pairs, [2]string{pair[0], pair[1]})
		}
	}
	return pairs
}

func mentionsBusiness(record Record, name string) bool {
	text := normalizeName(record.Business + " " + record.BusinessLabel + " " + record.Content)
	return strings.Contains(text, normalizeName(name))
}
