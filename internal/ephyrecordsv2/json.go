package ephyrecordsv2

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"
)

// CanonicalJSON is shared by MAC，event identity and cross-language fixtures．
// Object keys sort lexically；array order and text bytes are preserved．
func CanonicalJSON(raw []byte) ([]byte, error) {
	if len(raw) > 2<<20 || !utf8.Valid(raw) || !validEscapedUnicode(raw) {
		return nil, fmt.Errorf("invalid_json")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, e := readValue(d, 0)
	if e != nil {
		return nil, e
	}
	if _, e = d.Token(); e != io.EOF {
		return nil, fmt.Errorf("trailing_json")
	}
	var out bytes.Buffer
	encodeValue(&out, v)
	return out.Bytes(), nil
}
func readValue(d *json.Decoder, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("json_depth")
	}
	t, e := d.Token()
	if e != nil {
		return nil, e
	}
	switch x := t.(type) {
	case json.Delim:
		if x == '{' {
			m := map[string]any{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return nil, e
				}
				s, ok := k.(string)
				if !ok {
					return nil, fmt.Errorf("invalid_key")
				}
				if _, ok = m[s]; ok {
					return nil, fmt.Errorf("duplicate_key")
				}
				v, e := readValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				m[s] = v
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("invalid_object")
			}
			return m, nil
		}
		if x == '[' {
			a := []any{}
			for d.More() {
				v, e := readValue(d, depth+1)
				if e != nil {
					return nil, e
				}
				a = append(a, v)
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("invalid_array")
			}
			return a, nil
		}
		return nil, fmt.Errorf("invalid_delimiter")
	case json.Number:
		n, e := strconv.ParseInt(string(x), 10, 64)
		if e != nil || n > 9007199254740991 || n < -9007199254740991 || string(x) == "-0" {
			return nil, fmt.Errorf("integer_required")
		}
		return n, nil
	default:
		return x, nil
	}
}
func encodeString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}
func encodeValue(b *bytes.Buffer, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeString(b, k)
			b.WriteByte(':')
			encodeValue(b, x[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, v := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			encodeValue(b, v)
		}
		b.WriteByte(']')
	case string:
		encodeString(b, x)
	case int64:
		fmt.Fprint(b, x)
	case bool:
		fmt.Fprint(b, x)
	case nil:
		b.WriteString("null")
	}
}
func encodeCanonical(v any) ([]byte, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return CanonicalJSON(b)
}
func strict(raw []byte, v any) error {
	if _, e := CanonicalJSON(raw); e != nil {
		return e
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

// encoding/json replaces lone UTF-16 surrogates．Reject them before decoding
// so canonicalization never silently changes producer text or identity．
func validEscapedUnicode(raw []byte) bool {
	inside := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inside = !inside
			continue
		}
		if !inside || raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		value, e := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if e != nil {
			return false
		}
		i += 4
		if value >= 0xdc00 && value <= 0xdfff {
			return false
		}
		if value >= 0xd800 && value <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, e := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if e != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 6
		}
	}
	return true
}

// Envelope-level required keys include fields whose zero value is meaningful
// (for example explicit target:null on create and storage-only permissions)．
func requireWireFields(raw []byte, required []string, nullable ...string) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	for _, key := range required {
		if _, ok := root[key]; !ok {
			return fmt.Errorf("missing_required_field")
		}
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var walk func(any, string) error
	walk = func(value any, path string) error {
		switch item := value.(type) {
		case nil:
			allowed := false
			for _, p := range nullable {
				if path == p {
					allowed = true
				}
			}
			if !allowed {
				return fmt.Errorf("invalid_null")
			}
		case map[string]any:
			for k, v := range item {
				if err := walk(v, path+"/"+k); err != nil {
					return err
				}
			}
		case []any:
			for i, v := range item {
				if err := walk(v, path+"/"+strconv.Itoa(i)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(value, "")
}

func checkVersion(raw []byte, key string) error {
	if _, e := CanonicalJSON(raw); e != nil {
		return e
	}
	var fields map[string]json.RawMessage
	if e := json.Unmarshal(raw, &fields); e != nil {
		return e
	}
	var version string
	if e := json.Unmarshal(fields[key], &version); e != nil {
		return fmt.Errorf("unsupported_protocol")
	}
	if version != Version {
		return fmt.Errorf("unsupported_protocol")
	}
	return nil
}
