// Package schema implements the JSON Schema subset an Application's
// settings_schema may use (Domain Model → Settings → settings_schema
// rules): draft 2020-12, root {"type":"object","properties":{...}},
// additionalProperties always false, ≤200 properties, ≤64 KB, property
// names ^[a-z][a-z0-9_]{0,62}$, types string (enum, format date-time,
// maxLength), boolean, integer, number (minimum/maximum), object and
// array; in-document $ref only; defaults must validate; x-pii marks
// personal data.
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Reserved global settings keys an Application may not declare.
var Reserved = []string{"locale", "timezone", "theme", "notifications"}

// IsReserved reports whether key is a reserved global key.
func IsReserved(key string) bool {
	for _, r := range Reserved {
		if r == key {
			return true
		}
	}
	return false
}

// Issue is one schema-rule or value failure.
type Issue struct {
	Path    string   `json:"field"`
	Code    string   `json:"code"`
	Max     *float64 `json:"max,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Allowed []string `json:"allowed,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Message string   `json:"message,omitempty"`
}

// Prop is one resolved property schema.
type Prop struct {
	Type       string
	Enum       []json.RawMessage
	Format     string
	MaxLength  *int
	Minimum    *float64
	Maximum    *float64
	Default    json.RawMessage
	PII        bool
	Items      *Prop
	Properties map[string]*Prop
}

// Schema is a parsed settings_schema.
type Schema struct {
	Props map[string]*Prop
	Order []string
}

var propName = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

const (
	maxBytes = 64 << 10
	maxProps = 200
	maxDepth = 8
)

// Parse checks raw against the settings_schema rules. A nil raw (absent)
// is the empty schema. Issues are reported per path; reserved holds any
// reserved global keys the schema declares (422 reserved_settings_key).
func Parse(raw json.RawMessage) (s *Schema, issues []Issue, reserved []string) {
	s = &Schema{Props: map[string]*Prop{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return s, nil, nil
	}
	if len(raw) > maxBytes {
		return s, []Issue{{Path: "settings_schema", Code: "too_long", Max: f64(maxBytes), Message: "at most 64 KB serialized"}}, nil
	}
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return s, []Issue{{Path: "settings_schema", Code: "invalid_format", Message: "must be a JSON object"}}, nil
	}
	if t := str(root["type"]); t != "object" {
		issues = append(issues, Issue{Path: "settings_schema.type", Code: "enum_mismatch", Allowed: []string{"object"}})
	}
	var props map[string]json.RawMessage
	if p, ok := root["properties"]; ok {
		if err := json.Unmarshal(p, &props); err != nil {
			issues = append(issues, Issue{Path: "settings_schema.properties", Code: "invalid_format", Message: "must be an object"})
		}
	}
	if len(props) > maxProps {
		issues = append(issues, Issue{Path: "settings_schema.properties", Code: "too_long", Max: f64(maxProps)})
	}
	p := parser{root: raw}
	for _, name := range sortedKeys(props) {
		path := "settings_schema.properties." + name
		if IsReserved(name) {
			reserved = append(reserved, name)
			continue
		}
		if !propName.MatchString(name) {
			issues = append(issues, Issue{Path: path, Code: "invalid_format", Pattern: propName.String()})
			continue
		}
		prop, errs := p.prop(props[name], path, 0)
		issues = append(issues, errs...)
		if prop != nil {
			s.Props[name] = prop
			s.Order = append(s.Order, name)
		}
	}
	return s, issues, reserved
}

type parser struct{ root json.RawMessage }

func (p parser) prop(raw json.RawMessage, path string, depth int) (*Prop, []Issue) {
	if depth > maxDepth {
		return nil, []Issue{{Path: path, Code: "invalid_format", Message: "nested too deeply"}}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, []Issue{{Path: path, Code: "invalid_format", Message: "must be a schema object"}}
	}
	if ref, ok := m["$ref"]; ok {
		target, err := p.resolve(str(ref))
		if err != nil {
			return nil, []Issue{{Path: path + ".$ref", Code: "invalid_format", Message: err.Error()}}
		}
		return p.prop(target, path, depth+1)
	}
	var issues []Issue
	prop := &Prop{Type: str(m["type"]), Format: str(m["format"]), Default: m["default"]}
	switch prop.Type {
	case "string", "boolean", "integer", "number", "object", "array":
	default:
		return nil, []Issue{{Path: path + ".type", Code: "enum_mismatch",
			Allowed: []string{"string", "boolean", "integer", "number", "object", "array"}}}
	}
	if e, ok := m["enum"]; ok {
		if err := json.Unmarshal(e, &prop.Enum); err != nil || len(prop.Enum) == 0 {
			issues = append(issues, Issue{Path: path + ".enum", Code: "invalid_format"})
		}
	}
	if prop.Format != "" && !(prop.Type == "string" && prop.Format == "date-time") {
		issues = append(issues, Issue{Path: path + ".format", Code: "enum_mismatch", Allowed: []string{"date-time"}})
	}
	if v, ok := m["maxLength"]; ok {
		var n int
		if json.Unmarshal(v, &n) != nil || n < 0 || prop.Type != "string" {
			issues = append(issues, Issue{Path: path + ".maxLength", Code: "invalid_format"})
		} else {
			prop.MaxLength = &n
		}
	}
	for _, k := range []string{"minimum", "maximum"} {
		if v, ok := m[k]; ok {
			var n float64
			if json.Unmarshal(v, &n) != nil || (prop.Type != "integer" && prop.Type != "number") {
				issues = append(issues, Issue{Path: path + "." + k, Code: "invalid_format"})
			} else if k == "minimum" {
				prop.Minimum = &n
			} else {
				prop.Maximum = &n
			}
		}
	}
	if v, ok := m["x-pii"]; ok {
		_ = json.Unmarshal(v, &prop.PII)
	}
	if prop.Type == "array" {
		if items, ok := m["items"]; ok {
			it, errs := p.prop(items, path+".items", depth+1)
			issues = append(issues, errs...)
			prop.Items = it
		}
	}
	if prop.Type == "object" {
		if ps, ok := m["properties"]; ok {
			var sub map[string]json.RawMessage
			if json.Unmarshal(ps, &sub) == nil {
				prop.Properties = map[string]*Prop{}
				for _, k := range sortedKeys(sub) {
					sp, errs := p.prop(sub[k], path+".properties."+k, depth+1)
					issues = append(issues, errs...)
					if sp != nil {
						prop.Properties[k] = sp
					}
				}
			}
		}
	}
	if len(issues) == 0 && prop.Default != nil {
		if is := prop.Validate(prop.Default, path+".default"); len(is) > 0 {
			issues = append(issues, Issue{Path: path + ".default", Code: "invalid_format", Message: "default doesn't validate against its property"})
		}
	}
	return prop, issues
}

// resolve follows an in-document JSON pointer ("#/$defs/x"). Remote
// references are rejected.
func (p parser) resolve(ref string) (json.RawMessage, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("only in-document $ref (#/...) is supported")
	}
	cur := p.root
	for _, part := range strings.Split(ref[2:], "/") {
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		var m map[string]json.RawMessage
		if json.Unmarshal(cur, &m) != nil {
			return nil, fmt.Errorf("$ref %s doesn't resolve", ref)
		}
		next, ok := m[part]
		if !ok {
			return nil, fmt.Errorf("$ref %s doesn't resolve", ref)
		}
		cur = next
	}
	return cur, nil
}

// Validate checks one value against the property. Path labels issues.
func (p *Prop) Validate(v json.RawMessage, path string) []Issue {
	var x any
	if err := json.Unmarshal(v, &x); err != nil {
		return []Issue{{Path: path, Code: "invalid_format"}}
	}
	wrongType := []Issue{{Path: path, Code: "invalid_format", Message: "expected " + p.Type}}
	switch p.Type {
	case "string":
		s, ok := x.(string)
		if !ok {
			return wrongType
		}
		if p.MaxLength != nil && utf8.RuneCountInString(s) > *p.MaxLength {
			return []Issue{{Path: path, Code: "too_long", Max: f64(*p.MaxLength)}}
		}
		if p.Format == "date-time" {
			if _, err := time.Parse(time.RFC3339, s); err != nil {
				return []Issue{{Path: path, Code: "invalid_format", Message: "expected an RFC 3339 date-time"}}
			}
		}
	case "boolean":
		if _, ok := x.(bool); !ok {
			return wrongType
		}
	case "integer", "number":
		n, ok := x.(float64)
		if !ok || (p.Type == "integer" && n != math.Trunc(n)) {
			return wrongType
		}
		if (p.Minimum != nil && n < *p.Minimum) || (p.Maximum != nil && n > *p.Maximum) {
			return []Issue{{Path: path, Code: "out_of_range", Min: p.Minimum, Max: p.Maximum}}
		}
	case "object":
		m, ok := x.(map[string]any)
		if !ok {
			return wrongType
		}
		if p.Properties != nil {
			var issues []Issue
			for k, sv := range m {
				sp, ok := p.Properties[k]
				if !ok {
					issues = append(issues, Issue{Path: path + "." + k, Code: "undeclared_key"})
					continue
				}
				b, _ := json.Marshal(sv)
				issues = append(issues, sp.Validate(b, path+"."+k)...)
			}
			return issues
		}
	case "array":
		arr, ok := x.([]any)
		if !ok {
			return wrongType
		}
		if p.Items != nil {
			var issues []Issue
			for i, it := range arr {
				b, _ := json.Marshal(it)
				issues = append(issues, p.Items.Validate(b, fmt.Sprintf("%s[%d]", path, i))...)
			}
			return issues
		}
	}
	if len(p.Enum) > 0 {
		canon, _ := json.Marshal(x)
		for _, e := range p.Enum {
			var ev any
			_ = json.Unmarshal(e, &ev)
			eb, _ := json.Marshal(ev)
			if bytes.Equal(eb, canon) {
				return nil
			}
		}
		allowed := make([]string, len(p.Enum))
		for i, e := range p.Enum {
			allowed[i] = strings.Trim(string(e), `"`)
		}
		return []Issue{{Path: path, Code: "enum_mismatch", Allowed: allowed}}
	}
	return nil
}

// Scalar reports whether the property gets a typed projection (Database
// Schema → Typed fields): object and array never do.
func (p *Prop) Scalar() bool {
	return p.Type == "string" || p.Type == "boolean" || p.Type == "integer" || p.Type == "number"
}

// PIIKeys lists declared properties marked x-pii.
func (s *Schema) PIIKeys() []string {
	var out []string
	for _, k := range s.Order {
		if s.Props[k].PII {
			out = append(out, k)
		}
	}
	return out
}

func str(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

func f64(n int) *float64 { v := float64(n); return &v }

func sortedKeys(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
