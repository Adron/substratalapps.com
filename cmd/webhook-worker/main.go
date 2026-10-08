// Command webhook-worker is the SQS-triggered Lambda that delivers
// outbound webhooks. A message only nudges it; it always drains the
// outbox and every due retry, so a lost or duplicate message is harmless.
package main

import (
	"context"
	"log"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/CompositeCode/substratalapps.com/internal/platform/wire"
)

func main() {
	ctx := context.Background()
	base, err := wire.NewBase(ctx)
	if err != nil {
		log.Fatal(err)
	}
	d := base.Deliverer()
	lambda.Start(func(ctx context.Context, ev events.SQSEvent) error {
		return d.Run(ctx)
	})
}
