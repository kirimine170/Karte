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

const SettingsPath = ".mdsys/decision-log.json"

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
	for id, label := range settings.Businesses {
		if !businessPattern.MatchString(id) || !validLabel(label) {
			return Settings{}, fmt.Errorf("invalid decision log business")
		}
	}
	owners := map[string]string{}
	for id, label := range settings.businessLabels() {
		for _, alias := range []string{normalizeName(id), normalizeName(label)} {
			if owner, exists := owners[alias]; exists && owner != id {
				return Settings{}, fmt.Errorf("duplicate decision log business label")
			}
			owners[alias] = id
		}
	}
	for _, pair := range settings.PrivateTogether {
		if len(pair) != 2 || !validLabel(pair[0]) || !validLabel(pair[1]) || normalizeName(pair[0]) == normalizeName(pair[1]) {
			return Settings{}, fmt.Errorf("invalid decision log publication rule")
		}
	}
	if settings.Decider != "" && !validLabel(settings.Decider) {
		return Settings{}, fmt.Errorf("invalid decision log reviewer")
	}
	return settings, nil
}

func validLabel(label string) bool {
	return label != "" && strings.TrimSpace(label) == label && utf8.RuneCountInString(label) <= 128 &&
		strings.IndexFunc(label, unicode.IsControl) < 0
}

func normalizeName(name string) string { return strings.ToLower(norm.NFKC.String(name)) }

func (s Settings) businessLabels() map[string]string {
	labels := map[string]string{"overall": "全体", "karte": "Karte"}
	for id, label := range s.Businesses {
		labels[id] = label
	}
	return labels
}

func (s Settings) requiresPrivate(record Record) bool {
	if record.PublicationRestriction != "" {
		return true
	}
	text := normalizeName(record.Business + " " + record.BusinessLabel + " " + record.Content)
	for _, pair := range s.PrivateTogether {
		if strings.Contains(text, normalizeName(pair[0])) && strings.Contains(text, normalizeName(pair[1])) {
			return true
		}
	}
	return false
}
