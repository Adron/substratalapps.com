// Command mcpgen generates the MCP tool surface from docs/openapi.yaml
// (MCP Server → Tool surface is generated, not hand-authored): one tool
// per operationId, named substratal_<operationId with dots → underscores>,
// with its parameters and request body as the input schema and its
// summary/description as the tool description. It also captures the two
// MCP Resources (llms.txt and the Glossary).
//
// Run by `make gen`; CI fails if internal/mcp/tools.json differs from
// what this produces, so the tool list can't drift from the spec.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tool is one generated MCP tool plus what the server needs to route it.
type Tool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`

	OperationID   string   `json:"x_operation_id"`
	Method        string   `json:"x_method"`
	Path          string   `json:"x_path"`
	PathParams    []string `json:"x_path_params"`
	QueryParams   []string `json:"x_query_params"`
	HasBody       bool     `json:"x_has_body"`
	NeedsIdemKey  bool     `json:"x_needs_idempotency_key"`
	AcceptsIfMatc bool     `json:"x_accepts_if_match"`
}

// Resource is one static MCP resource.
type Resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
	Text        string `json:"text"`
}

type output struct {
	Generated string     `json:"_generated"`
	Tools     []Tool     `json:"tools"`
	Resources []Resource `json:"resources"`
}

var doc map[string]any

func main() {
	raw, err := os.ReadFile("docs/openapi.yaml")
	if err != nil {
		log.Fatal(err)
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		log.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	var tools []Tool
	for _, p := range sortedKeys(paths) {
		ops := paths[p].(map[string]any)
		shared := asList(ops["parameters"])
		for _, method := range []string{"get", "post", "patch", "put", "delete"} {
			op, ok := ops[method].(map[string]any)
			if !ok {
				continue
			}
			tools = append(tools, tool(p, strings.ToUpper(method), op, shared))
		}
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	out := output{Generated: "by cmd/mcpgen from docs/openapi.yaml; do not edit", Tools: tools, Resources: resources()}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("internal/mcp/tools.json", append(b, '\n'), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("generated %d tools\n", len(tools))
}

func tool(path, method string, op map[string]any, shared []any) Tool {
	id := op["operationId"].(string)
	t := Tool{
		Name:        "substratal_" + strings.ReplaceAll(id, ".", "_"),
		Title:       str(op["summary"]),
		OperationID: id, Method: method, Path: "/v1" + path,
	}
	desc := strings.TrimSpace(str(op["summary"]))
	if d := strings.TrimSpace(str(op["description"])); d != "" {
		desc += "\n\n" + d
	}
	desc += fmt.Sprintf("\n\nREST: %s %s", method, t.Path)
	t.Description = desc

	props := map[string]any{}
	var required []string
	for _, pr := range append(shared, asList(op["parameters"])...) {
		param := resolve(pr).(map[string]any)
		name, in := str(param["name"]), str(param["in"])
		switch in {
		case "path":
			t.PathParams = append(t.PathParams, name)
			required = append(required, name)
		case "query":
			t.QueryParams = append(t.QueryParams, name)
		case "header":
			if name == "Idempotency-Key" {
				t.NeedsIdemKey = param["required"] == true
				continue // derived from the arguments, never asked for
			}
			if name == "If-Match" {
				t.AcceptsIfMatc = true
				props["if_match"] = map[string]any{"type": "string", "description": "ETag from a prior read; a mismatch returns 409 version_conflict."}
				continue
			}
			continue
		}
		sch := inline(param["schema"], 0)
		if s, ok := sch.(map[string]any); ok {
			if d := str(param["description"]); d != "" {
				s["description"] = d
			}
		}
		props[name] = sch
		if in == "query" && param["required"] == true {
			required = append(required, name)
		}
	}
	if rb, ok := op["requestBody"].(map[string]any); ok {
		if content, ok := rb["content"].(map[string]any); ok {
			if js, ok := content["application/json"].(map[string]any); ok {
				t.HasBody = true
				props["body"] = inline(js["schema"], 0)
				if rb["required"] == true {
					required = append(required, "body")
				}
			}
		}
	}
	schema := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	t.InputSchema = schema

	_, destructive := op["x-substratal-destructive"]
	readOnly := method == "GET"
	idempotent := method == "GET" || method == "PATCH" || method == "DELETE" || (method == "POST" && t.NeedsIdemKey)
	t.Annotations = map[string]any{
		"title": t.Title, "readOnlyHint": readOnly, "destructiveHint": destructive, "idempotentHint": idempotent,
		"openWorldHint": false,
	}
	return t
}

var refRe = regexp.MustCompile(`^#/(.+)$`)

// resolve follows a local $ref one level.
func resolve(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	ref, ok := m["$ref"].(string)
	if !ok {
		return v
	}
	parts := strings.Split(refRe.FindStringSubmatch(ref)[1], "/")
	var cur any = doc
	for _, p := range parts {
		cur = cur.(map[string]any)[p]
	}
	return cur
}

// inline returns a copy of a schema with every local $ref expanded, so
// tool input schemas are self-contained.
func inline(v any, depth int) any {
	if depth > 12 {
		return map[string]any{}
	}
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["$ref"]; ok {
			return inline(resolve(x), depth+1)
		}
		out := map[string]any{}
		for k, val := range x {
			if strings.HasPrefix(k, "x-") || k == "example" || k == "readOnly" {
				continue
			}
			out[k] = inline(val, depth+1)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = inline(val, depth+1)
		}
		return out
	default:
		return v
	}
}

func resources() []Resource {
	base := "https://adron.github.io/substratalapps.com"
	llms, err := os.ReadFile("docs/llms.txt")
	if err != nil {
		log.Fatal(err)
	}
	text := string(llms)
	if i := strings.Index(text, "---\n"); i == 0 {
		if j := strings.Index(text[4:], "---\n"); j >= 0 {
			text = text[4+j+4:]
		}
	}
	text = strings.ReplaceAll(text, "{{ site.url }}{{ site.baseurl }}", base)
	glossary, err := os.ReadFile("docs/glossary.md")
	if err != nil {
		log.Fatal(err)
	}
	g := string(glossary)
	if strings.HasPrefix(g, "---\n") {
		if j := strings.Index(g[4:], "---\n"); j >= 0 {
			g = g[4+j+4:]
		}
	}
	return []Resource{
		{URI: base + "/llms.txt", Name: "llms.txt", Description: "Machine-oriented index of every page of the Substratal Apps API specification.",
			MimeType: "text/markdown", Text: strings.TrimSpace(text) + "\n"},
		{URI: base + "/glossary/", Name: "Glossary", Description: "Precise definitions for every term the API uses.",
			MimeType: "text/markdown", Text: strings.TrimSpace(g) + "\n"},
	}
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func asList(v any) []any {
	l, _ := v.([]any)
	return l
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
