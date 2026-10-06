// Package mcp is the MCP server at /mcp (docs/mcp-server.md): Streamable
// HTTP, JSON-RPC 2.0, one buffered JSON response per request, no server
// push. It's a protocol gateway, not a new capability: every tool call is
// forwarded, with the caller's own Bearer credential unchanged, to the
// matching /v1 REST call, so Access Control, restrict_destructive, rate
// limits, and the audit trail all apply exactly as they do to curl.
//
// The session is stateless: Mcp-Session-Id is a signed, self-contained
// token carrying the negotiated protocol version, so no table, no cache,
// and any Lambda instance can serve any call.
package mcp

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

//go:embed tools.json
var catalogJSON []byte

type tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	Annotations  map[string]any `json:"annotations"`
	OperationID  string         `json:"x_operation_id"`
	Method       string         `json:"x_method"`
	Path         string         `json:"x_path"`
	PathParams   []string       `json:"x_path_params"`
	QueryParams  []string       `json:"x_query_params"`
	HasBody      bool           `json:"x_has_body"`
	NeedsIdemKey bool           `json:"x_needs_idempotency_key"`
}

type resource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description"`
	MimeType    string `json:"mimeType"`
	Text        string `json:"text"`
}

var catalog struct {
	Tools     []tool     `json:"tools"`
	Resources []resource `json:"resources"`
}

func init() {
	if err := json.Unmarshal(catalogJSON, &catalog); err != nil {
		panic("mcp: bad tools.json: " + err.Error())
	}
}

// Tools returns the generated tool names → operationIds (tests).
func Tools() map[string]string {
	out := map[string]string{}
	for _, t := range catalog.Tools {
		out[t.Name] = t.OperationID
	}
	return out
}

// Annotations returns a tool's annotations (tests).
func Annotations(name string) map[string]any {
	for _, t := range catalog.Tools {
		if t.Name == name {
			return t.Annotations
		}
	}
	return nil
}

// SupportedVersions are the MCP protocol versions this server speaks,
// newest first.
var SupportedVersions = []string{"2025-06-18", "2025-03-26"}

// Server handles POST /mcp.
type Server struct {
	// API is where tool calls go: an HTTP client to the API's own /v1 (API
	// Gateway in AWS), or an in-process transport in the devserver.
	API     *http.Client
	BaseURL string // e.g. https://api.substratalapps.com
	Key     []byte // HMAC key for session tokens
	Now     func() time.Time
}

const sessionTTL = 24 * time.Hour

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet:
		// No server-initiated stream at Tier 0.
		w.Header().Set("Allow", "POST, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	case http.MethodDelete:
		w.WriteHeader(http.StatusNoContent) // stateless: nothing to tear down
		return
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !strings.HasPrefix(strings.TrimSpace(r.Header.Get("Authorization")), "Bearer ") {
		w.Header().Set("WWW-Authenticate", `Bearer realm="substratal"`)
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{
			"code": "unauthenticated", "message": "The MCP endpoint takes the same Bearer credential as the REST API."}})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		writeJSON(w, http.StatusBadRequest, rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"),
			Error: &rpcError{Code: -32700, Message: "parse error: expected one JSON-RPC 2.0 request"}})
		return
	}
	if req.Method != "initialize" {
		version, ok := s.checkSession(r.Header.Get("Mcp-Session-Id"))
		if !ok {
			// 404 tells a conforming client to start a new session.
			writeJSON(w, http.StatusNotFound, rpcResponse{JSONRPC: "2.0", ID: idOrNull(req.ID),
				Error: &rpcError{Code: -32001, Message: "session expired or missing; initialize again"}})
			return
		}
		w.Header().Set("MCP-Protocol-Version", version)
	}
	if len(req.ID) == 0 {
		// A notification (e.g. notifications/initialized): accepted, no body.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	result, rerr := s.dispatch(r, req)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	if req.Method == "initialize" && rerr == nil {
		v := result.(map[string]any)["protocolVersion"].(string)
		w.Header().Set("Mcp-Session-Id", s.newSession(v))
	}
	writeJSON(w, http.StatusOK, resp)
}

func idOrNull(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage("null")
	}
	return id
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) dispatch(r *http.Request, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := SupportedVersions[0]
		for _, v := range SupportedVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}, "resources": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "substratal-apps", "title": "Substratal Apps Platform API", "version": "1.0.0"},
			"instructions": "Tools map 1:1 to the Substratal Apps REST API (operationId → substratal_<operationId>). " +
				"Your Bearer credential is forwarded unchanged; use an intended_use: agent API Key, whose restrict_destructive " +
				"default blocks destructive operations server-side. Read the llms.txt resource first.",
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		out := make([]map[string]any, 0, len(catalog.Tools))
		for _, t := range catalog.Tools {
			out = append(out, map[string]any{"name": t.Name, "title": t.Title, "description": t.Description,
				"inputSchema": t.InputSchema, "annotations": t.Annotations})
		}
		return map[string]any{"tools": out}, nil
	case "tools/call":
		var p struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		for _, t := range catalog.Tools {
			if t.Name == p.Name {
				return s.call(r, t, p.Arguments), nil
			}
		}
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	case "resources/list":
		out := make([]map[string]any, 0, len(catalog.Resources))
		for _, res := range catalog.Resources {
			out = append(out, map[string]any{"uri": res.URI, "name": res.Name, "description": res.Description, "mimeType": res.MimeType})
		}
		return map[string]any{"resources": out}, nil
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(req.Params, &p)
		for _, res := range catalog.Resources {
			if res.URI == p.URI {
				return map[string]any{"contents": []map[string]any{{"uri": res.URI, "mimeType": res.MimeType, "text": res.Text}}}, nil
			}
		}
		return nil, &rpcError{Code: -32002, Message: "resource not found"}
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
}

// call translates one tool call into its REST request, forwarding the
// caller's Authorization header unchanged, and returns the MCP result. A
// REST error is a tool result with isError, never a protocol error.
func (s *Server) call(r *http.Request, t tool, args map[string]json.RawMessage) map[string]any {
	path := t.Path
	for _, name := range t.PathParams {
		var v string
		if err := json.Unmarshal(args[name], &v); err != nil || v == "" {
			return toolError(fmt.Sprintf("missing required argument %q", name))
		}
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(v))
	}
	q := url.Values{}
	for _, name := range t.QueryParams {
		raw, ok := args[name]
		if !ok {
			continue
		}
		var list []any
		if json.Unmarshal(raw, &list) == nil {
			for _, x := range list {
				q.Add(name, fmt.Sprint(x))
			}
			continue
		}
		var x any
		_ = json.Unmarshal(raw, &x)
		q.Set(name, fmt.Sprint(x))
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var body io.Reader
	if t.HasBody {
		b := args["body"]
		if len(b) == 0 {
			b = json.RawMessage("{}")
		}
		body = bytes.NewReader(b)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, t.Method, s.BaseURL+path, body)
	if err != nil {
		return toolError(err.Error())
	}
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Content-Type", "application/json")
	if rid := r.Header.Get("X-Request-Id"); rid != "" {
		req.Header.Set("X-Request-Id", rid)
	}
	if raw, ok := args["if_match"]; ok {
		var v string
		_ = json.Unmarshal(raw, &v)
		req.Header.Set("If-Match", v)
	}
	if t.NeedsIdemKey {
		// Derived from the arguments, so a retried tool call can't double-apply.
		canon, _ := json.Marshal(args)
		sum := sha256.Sum256(append([]byte(t.Name+"\n"), canon...))
		req.Header.Set("Idempotency-Key", "mcp-"+hex.EncodeToString(sum[:16]))
	}
	resp, err := s.API.Do(req)
	if err != nil {
		return toolError("the API is unreachable: " + err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	text := string(out)
	if text == "" {
		text = fmt.Sprintf(`{"status": %d}`, resp.StatusCode)
	}
	result := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	var structured map[string]any
	if json.Unmarshal(out, &structured) == nil {
		result["structuredContent"] = structured
	}
	if resp.StatusCode >= 400 {
		result["isError"] = true
	}
	return result
}

func toolError(msg string) map[string]any {
	return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
}

// ── Stateless session tokens ───────────────────────────────────────────

type session struct {
	V   string `json:"v"`
	Exp int64  `json:"e"`
}

func (s *Server) newSession(version string) string {
	b, _ := json.Marshal(session{V: version, Exp: s.now().Add(sessionTTL).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(b)
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte("mcp-session." + payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

func (s *Server) checkSession(tok string) (string, bool) {
	payload, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return "", false
	}
	mac := hmac.New(sha256.New, s.Key)
	mac.Write([]byte("mcp-session." + payload))
	if !hmac.Equal([]byte(sig), []byte(base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16]))) {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", false
	}
	var ss session
	if json.Unmarshal(raw, &ss) != nil || s.now().Unix() > ss.Exp {
		return "", false
	}
	return ss.V, true
}

// InProcess is an http.Client that calls handler directly, for the
// devserver and tests: tool calls still go through the full API stack.
func InProcess(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripper{handler}}
}

type roundTripper struct{ h http.Handler }

func (rt roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := &recorder{header: http.Header{}, status: 200}
	rt.h.ServeHTTP(rec, req)
	return &http.Response{StatusCode: rec.status, Header: rec.header, Body: io.NopCloser(&rec.body), Request: req}, nil
}

type recorder struct {
	header http.Header
	status int
	body   bytes.Buffer
	wrote  bool
}

func (r *recorder) Header() http.Header { return r.header }
func (r *recorder) WriteHeader(s int) {
	if !r.wrote {
		r.status, r.wrote = s, true
	}
}
func (r *recorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.body.Write(b)
}
