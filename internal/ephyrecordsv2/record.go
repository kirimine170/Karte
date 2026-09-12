package ephyrecordsv2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"html"
	"karte/internal/canonical"
	"strings"
)

type Record struct {
	DocID           string         `json:"doc_id"`
	Project         string         `json:"project"`
	Kind            string         `json:"kind"`
	Sensitivity     string         `json:"sensitivity"`
	Tags            []string       `json:"tags"`
	ProvenanceTypes []string       `json:"provenance_types"`
	Authorship      string         `json:"authorship"`
	Meta            RecordMeta     `json:"runtime_record"`
	Events          []Event        `json:"events,omitempty"`
	Derivation      *Derivation    `json:"derivation,omitempty"`
	Extra           map[string]any `json:"-"`
}
type RecordMeta struct {
	SchemaVersion string      `json:"schema_version"`
	ScopeID       string      `json:"scope_id"`
	ProducerID    string      `json:"producer_instance_id"`
	LogicalKey    string      `json:"logical_record_key"`
	Revision      int64       `json:"revision"`
	Spec          RecordSpec  `json:"record"`
	Adoption      Adoption    `json:"adoption"`
	HumanEdited   bool        `json:"human_edited"`
	Derivation    *Derivation `json:"derivation,omitempty"`
}

func itemMetadata(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	var m map[string]json.RawMessage
	if e = json.Unmarshal(b, &m); e != nil {
		return nil, e
	}
	delete(m, "text")
	return json.Marshal(m)
}
func Render(r Record) ([]byte, error) {
	header := r
	header.Events = nil
	header.Derivation = nil
	header.Meta.Derivation = nil
	if r.Derivation != nil {
		copy := *r.Derivation
		copy.Claims = nil
		header.Meta.Derivation = &copy
	}
	raw, e := json.Marshal(header)
	if e != nil {
		return nil, e
	}
	var fields map[string]any
	if e = json.Unmarshal(raw, &fields); e != nil {
		return nil, e
	}
	fields["title"] = r.Meta.Spec.Title
	for k, v := range r.Extra {
		if _, reserved := fields[k]; reserved || oneOf(k, "events", "derivation") {
			return nil, fmt.Errorf("reserved_metadata")
		}
		fields[k] = v
	}
	y, e := yaml.Marshal(fields)
	if e != nil {
		return nil, e
	}
	var b bytes.Buffer
	b.WriteString("---\n")
	b.Write(y)
	b.WriteString("---\n\n")
	write := func(label string, v any, text string) error {
		meta, e := itemMetadata(v)
		if e != nil {
			return e
		}
		b.WriteString("## " + label + "\n<!-- karte-v2:item ")
		b.Write(meta)
		b.WriteString(" -->\n")
		for _, line := range strings.Split(text, "\n") {
			b.WriteString("> " + html.EscapeString(line) + "\n")
		}
		b.WriteString("<!-- karte-v2:end -->\n\n")
		return nil
	}
	for _, v := range r.Events {
		if e := write(v.Type, v, v.Text); e != nil {
			return nil, e
		}
	}
	if r.Derivation != nil {
		for _, v := range r.Derivation.Claims {
			if e := write(v.Class, v, v.Text); e != nil {
				return nil, e
			}
		}
	}
	if b.Len() > MaxRecordBytes {
		return nil, fmt.Errorf("record_capacity")
	}
	return b.Bytes(), nil
}
func Parse(raw []byte) (Record, error) {
	var r Record
	if len(raw) > MaxRecordBytes || !bytes.HasPrefix(raw, []byte("---\n")) {
		return r, fmt.Errorf("invalid_record")
	}
	parts := bytes.SplitN(raw[4:], []byte("\n---\n\n"), 2)
	if len(parts) != 2 {
		return r, fmt.Errorf("invalid_metadata")
	}
	var fields map[string]any
	if e := yaml.Unmarshal(parts[0], &fields); e != nil {
		return r, e
	}
	r.Extra = map[string]any{}
	for k, v := range fields {
		if !oneOf(k, "doc_id", "project", "kind", "sensitivity", "tags", "provenance_types", "authorship", "runtime_record", "title") {
			r.Extra[k] = v
			delete(fields, k)
		}
	}
	title, ok := fields["title"].(string)
	if !ok {
		return r, fmt.Errorf("invalid_title")
	}
	delete(fields, "title")
	j, e := json.Marshal(fields)
	if e != nil {
		return r, e
	}
	extra := r.Extra
	if e = strict(j, &r); e != nil {
		return r, e
	}
	r.Extra = extra
	if r.Meta.SchemaVersion != Version || !validID(r.DocID) || !validID(r.Meta.ScopeID) || !validID(r.Meta.ProducerID) || r.Meta.Revision < 1 || r.Meta.Spec.Title != title {
		return r, fmt.Errorf("invalid_metadata")
	}
	if r.Meta.Derivation != nil {
		copy := *r.Meta.Derivation
		r.Derivation = &copy
		r.Meta.Derivation = nil
	}
	lines := strings.Split(string(parts[1]), "\n")
	for i := 0; i < len(lines); {
		if i == len(lines)-1 && lines[i] == "" {
			break
		}
		if !strings.HasPrefix(lines[i], "## ") || i+2 >= len(lines) {
			return r, fmt.Errorf("invalid_item")
		}
		label := strings.TrimPrefix(lines[i], "## ")
		i++
		const prefix = "<!-- karte-v2:item "
		const suffix = " -->"
		line := lines[i]
		if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
			return r, fmt.Errorf("invalid_item")
		}
		metadata := strings.TrimSuffix(strings.TrimPrefix(line, prefix), suffix)
		i++
		textLines := []string{}
		for i < len(lines) && strings.HasPrefix(lines[i], "> ") {
			textLines = append(textLines, html.UnescapeString(strings.TrimPrefix(lines[i], "> ")))
			i++
		}
		if len(textLines) == 0 || i+1 >= len(lines) || lines[i] != "<!-- karte-v2:end -->" || lines[i+1] != "" {
			return r, fmt.Errorf("invalid_item")
		}
		i += 2
		text := strings.Join(textLines, "\n")
		if len(text) > MaxEventBytes {
			return r, fmt.Errorf("event_capacity")
		}
		if r.Meta.Spec.Type == "conversation" {
			var v Event
			if e := strict([]byte(metadata), &v); e != nil {
				return r, e
			}
			if v.Text != "" || v.Type != label {
				return r, fmt.Errorf("invalid_item")
			}
			v.Text = text
			r.Events = append(r.Events, v)
		} else {
			if r.Derivation == nil {
				return r, fmt.Errorf("invalid_derivation")
			}
			var c Claim
			if e := strict([]byte(metadata), &c); e != nil {
				return r, e
			}
			if c.Text != "" || c.Class != label {
				return r, fmt.Errorf("invalid_item")
			}
			c.Text = text
			r.Derivation.Claims = append(r.Derivation.Claims, c)
		}
	}
	if len(r.Events) > 256 {
		return r, fmt.Errorf("record_capacity")
	}
	return r, nil
}
func (r Record) Target(raw []byte) Target {
	return Target{DocID: r.DocID, Revision: r.Meta.Revision, SHA256: canonical.Hash(raw)}
}
func (r Record) Resource() Classification {
	return Classification{Kind: r.Kind, Sensitivity: r.Sensitivity, Tags: r.Tags, ProvenanceTypes: r.ProvenanceTypes}
}
