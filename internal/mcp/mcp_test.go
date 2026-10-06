package mcp_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Adron/substratalapps.com/internal/core"
	"github.com/Adron/substratalapps.com/internal/mcp"
	"github.com/Adron/substratalapps.com/internal/testenv"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testenv.Drop()
	os.Exit(code)
}

// The MCP destructiveHint and the server's restrict_destructive gate read
// the same classification; they must never disagree.
func TestDestructiveClassificationMatchesSpec(t *testing.T) {
	for name, op := range mcp.Tools() {
		_, server := core.DestructiveOps[op]
		hint := mcp.Annotations(name)["destructiveHint"] == true
		if server != hint {
			t.Errorf("%s (%s): server classification %v, spec x-substratal-destructive %v", name, op, server, hint)
		}
	}
	if len(mcp.Tools()) < 90 {
		t.Fatalf("only %d tools generated", len(mcp.Tools()))
	}
}

type client struct {
	t       *testing.T
	url     string
	token   string
	session string
	id      int
}

func (c *client) rpc(method string, params any) (map[string]any, int) {
	c.id++
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	req, _ := http.NewRequest("POST", c.url, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" {
		c.session = s
	}
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, resp.StatusCode
}

func TestMCPEndToEnd(t *testing.T) {
	e := testenv.New(t)
	srv := httptest.NewServer(&mcp.Server{API: mcp.InProcess(e.Core.Handler()), BaseURL: "http://api.internal", Key: []byte("k")})
	defer srv.Close()
	agentKey := e.Post("/v1/api-keys", e.Admin, map[string]any{"name": "agent", "scope": "platform", "intended_use": "agent",
		"permissions": []string{"users.list", "entitlements.manage", "audit.view"}}).Expect(201).Str("secret")
	c := &client{t: t, url: srv.URL, token: agentKey}

	if _, status := c.rpc("tools/list", nil); status != 404 {
		t.Fatalf("tools/list before initialize = %d", status)
	}
	init, _ := c.rpc("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{},
		"clientInfo": map[string]any{"name": "test", "version": "1"}})
	if init["result"].(map[string]any)["protocolVersion"] != "2025-06-18" || c.session == "" {
		t.Fatalf("initialize = %v", init)
	}
	tools, _ := c.rpc("tools/list", nil)
	if n := len(tools["result"].(map[string]any)["tools"].([]any)); n < 90 {
		t.Fatalf("tools = %d", n)
	}
	res, _ := c.rpc("resources/read", map[string]any{"uri": "https://adron.github.io/substratalapps.com/llms.txt"})
	if !strings.Contains(res["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)["text"].(string), "Substratal") {
		t.Fatal("llms.txt resource")
	}

	app := e.App("usr_01JAG0SYSTEM00000000000000", nil)
	u := e.Signup()
	// A non-destructive tool call works, through the full REST stack.
	grant := map[string]any{"name": "substratal_entitlements_grant", "arguments": map[string]any{"id": u.ID,
		"body": map[string]any{"application_id": app, "source": "admin_grant"}}}
	r1, _ := c.rpc("tools/call", grant)
	result := r1["result"].(map[string]any)
	if result["isError"] == true {
		t.Fatalf("grant failed: %v", result)
	}
	entID := result["structuredContent"].(map[string]any)["id"].(string)
	// A retried tool call replays instead of double-granting.
	r2, _ := c.rpc("tools/call", grant)
	if r2["result"].(map[string]any)["structuredContent"].(map[string]any)["id"] != entID {
		t.Fatalf("retry = %v", r2)
	}
	// A destructive call is refused server-side for the agent key.
	off, _ := c.rpc("tools/call", map[string]any{"name": "substratal_entitlements_update", "arguments": map[string]any{
		"id": entID, "body": map[string]any{"status": "disabled", "disabled_reason": "agent"}}})
	or := off["result"].(map[string]any)
	if or["isError"] != true || or["structuredContent"].(map[string]any)["error"].(map[string]any)["code"] != "destructive_operation_restricted" {
		t.Fatalf("destructive call = %v", or)
	}
	// A tampered session is rejected.
	c.session = c.session + "x"
	if _, status := c.rpc("ping", nil); status != 404 {
		t.Fatalf("tampered session = %d", status)
	}
}
