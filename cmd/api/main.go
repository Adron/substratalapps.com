// Command api is the REST API Lambda: every /v1 route plus the
// /.well-known endpoints, behind API Gateway (HTTP API).
package main

import (
	"context"
	"log"

	"github.com/Adron/substratalapps.com/internal/platform/lambdahttp"
	"github.com/Adron/substratalapps.com/internal/platform/wire"
)

func main() {
	ctx := context.Background()
	base, err := wire.NewBase(ctx)
	if err != nil {
		log.Fatal(err)
	}
	notifier, err := base.Notifier(ctx)
	if err != nil {
		log.Fatal(err)
	}
	lambdahttp.Start(base.Server(notifier).Handler())
}
