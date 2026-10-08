// Command jobs runs the scheduled jobs. As a Lambda, EventBridge Scheduler
// invokes it with {"job": "<name>"}. With -job it runs one job locally and
// exits (`make job name=entitlement-sweep`).
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"

	"github.com/CompositeCode/substratalapps.com/internal/jobs"
	"github.com/CompositeCode/substratalapps.com/internal/platform/wire"
)

type event struct {
	Job string `json:"job"`
}

func main() {
	name := flag.String("job", "", "run one job locally and exit")
	list := flag.Bool("list", false, "list the jobs")
	flag.Parse()
	ctx := context.Background()
	base, err := wire.NewBase(ctx)
	if err != nil {
		log.Fatal(err)
	}
	r := jobs.New(base)
	switch {
	case *list:
		for _, n := range r.Names() {
			fmt.Println(n)
		}
	case *name != "":
		if err := r.Run(ctx, *name); err != nil {
			log.Fatal(err)
		}
	case os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "":
		lambda.Start(func(ctx context.Context, ev event) error { return r.Run(ctx, ev.Job) })
	default:
		log.Fatal("jobs: pass -job <name> or -list")
	}
}
