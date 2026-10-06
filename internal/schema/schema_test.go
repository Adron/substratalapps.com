package schema

import (
	"encoding/json"
	"testing"
)

const timetrack = `{"type":"object","properties":{
	"default_billable":{"type":"boolean","default":true},
	"week_start":{"type":"string","enum":["sunday","monday"],"default":"sunday"},
	"invoice_footer":{"type":"string","maxLength":5,"x-pii":true},
	"rate":{"type":"number","minimum":0,"maximum":100},
	"due":{"type":"string","format":"date-time"},
	"tags":{"type":"array","items":{"$ref":"#/$defs/tag"}}
},"$defs":{"tag":{"type":"string","maxLength":3}}}`

func TestParseValid(t *testing.T) {
	s, issues, reserved := Parse(json.RawMessage(timetrack))
	if len(issues) != 0 || len(reserved) != 0 {
		t.Fatalf("issues=%v reserved=%v", issues, reserved)
	}
	if len(s.Props) != 6 || !s.Props["invoice_footer"].PII {
		t.Fatalf("props = %v", s.Order)
	}
	if got := s.PIIKeys(); len(got) != 1 || got[0] != "invoice_footer" {
		t.Fatalf("pii = %v", got)
	}
}

func TestParseRules(t *testing.T) {
	cases := map[string]string{
		"root type":    `{"type":"array","properties":{}}`,
		"bad name":     `{"type":"object","properties":{"Bad-Name":{"type":"string"}}}`,
		"bad type":     `{"type":"object","properties":{"x":{"type":"date"}}}`,
		"remote ref":   `{"type":"object","properties":{"x":{"$ref":"https://example.com/s.json"}}}`,
		"bad default":  `{"type":"object","properties":{"x":{"type":"integer","default":"one"}}}`,
		"bad format":   `{"type":"object","properties":{"x":{"type":"string","format":"email"}}}`,
		"dangling ref": `{"type":"object","properties":{"x":{"$ref":"#/$defs/missing"}}}`,
	}
	for name, raw := range cases {
		if _, issues, _ := Parse(json.RawMessage(raw)); len(issues) == 0 {
			t.Errorf("%s: expected an issue", name)
		}
	}
	if _, _, reserved := Parse(json.RawMessage(`{"type":"object","properties":{"theme":{"type":"string"}}}`)); len(reserved) != 1 {
		t.Error("reserved key not reported")
	}
}

func TestValidateValues(t *testing.T) {
	s, _, _ := Parse(json.RawMessage(timetrack))
	ok := map[string]string{"default_billable": `false`, "week_start": `"monday"`, "rate": `50`,
		"due": `"2026-10-05T12:00:00Z"`, "tags": `["a","bc"]`, "invoice_footer": `"hi"`}
	for k, v := range ok {
		if is := s.Props[k].Validate(json.RawMessage(v), k); len(is) != 0 {
			t.Errorf("%s=%s: %v", k, v, is)
		}
	}
	bad := map[string]string{"default_billable": `"yes"`, "week_start": `"friday"`, "rate": `101`,
		"due": `"tomorrow"`, "tags": `["toolong"]`, "invoice_footer": `"too long"`}
	codes := map[string]string{"default_billable": "invalid_format", "week_start": "enum_mismatch", "rate": "out_of_range",
		"due": "invalid_format", "tags": "too_long", "invoice_footer": "too_long"}
	for k, v := range bad {
		is := s.Props[k].Validate(json.RawMessage(v), k)
		if len(is) == 0 || is[0].Code != codes[k] {
			t.Errorf("%s=%s: got %v, want %s", k, v, is, codes[k])
		}
	}
}
