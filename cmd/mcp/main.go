// Command mcp is the MCP server Lambda at /mcp. It holds no data-plane
// permissions at all: every tool call is an HTTPS call to the API's own
// /v1 routes on the same API Gateway, with the caller's credential
// forwarded unchanged (DEPLOYMENT.md → MCP server).
package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/Adron/substratalapps.com/internal/mcp"
	"github.com/Adron/substratalapps.com/internal/platform/lambdahttp"
)

func main() {
	base := os.Getenv("PUBLIC_BASE_URL")
	key := os.Getenv("MCP_SESSION_KEY")
	if base == "" || key == "" {
		log.Fatal("mcp: PUBLIC_BASE_URL and MCP_SESSION_KEY are required")
	}
	lambdahttp.Start(&mcp.Server{
		API:     &http.Client{Timeout: 28 * time.Second},
		BaseURL: base,
		Key:     []byte(key),
	})
}
