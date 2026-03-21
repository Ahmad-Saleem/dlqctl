package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ahmad-saleem/dlqctl/internal/queue"
)

func newContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func newQueueClient(ctx context.Context) (*queue.Client, error) {
	client, err := queue.NewClient(ctx, "eu-west-1")
	if err != nil {
		return nil, fmt.Errorf("failed to create SQS client: %w", err)
	}
	return client, nil
}
