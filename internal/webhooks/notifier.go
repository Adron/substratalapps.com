package webhooks

import (
	"context"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sqs"
)

// SQSNotifier nudges the webhook worker Lambda through SQS after a commit.
// The message carries only the event (or delivery) id; the worker reads
// everything else from the outbox, so a lost message only delays delivery
// until the scheduled sweep.
type SQSNotifier struct {
	Client   *sqs.Client
	QueueURL string
	Log      *slog.Logger
}

func (n SQSNotifier) EventCommitted(ctx context.Context, id string) {
	_, err := n.Client.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: &n.QueueURL, MessageBody: &id})
	if err != nil {
		n.Log.Warn("webhook nudge failed; the scheduled sweep will deliver it", "id", id, "err", err)
	}
}

// LocalNotifier runs the Deliverer in-process: every nudge triggers a
// pass, and a ticker sweeps for retries that come due. The devserver uses
// it so webhooks work on a laptop with no queue at all.
type LocalNotifier struct {
	kick chan struct{}
}

// NewLocal starts the in-process loop and returns its notifier.
func NewLocal(ctx context.Context, d *Deliverer, every time.Duration) *LocalNotifier {
	n := &LocalNotifier{kick: make(chan struct{}, 1)}
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-n.kick:
			case <-t.C:
			}
			if err := d.Run(ctx); err != nil && ctx.Err() == nil {
				d.Log.Error("webhook worker pass failed", "err", err)
			}
		}
	}()
	return n
}

func (n *LocalNotifier) EventCommitted(context.Context, string) {
	select {
	case n.kick <- struct{}{}:
	default:
	}
}
