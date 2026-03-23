package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/spf13/cobra"
)

func newContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt)
}

func newQueueClient(ctx context.Context, cmd *cobra.Command) (queue.Queue, error) {

	region, _ := cmd.Flags().GetString("region")

	client, err := queue.NewClient(ctx, region)
	if err != nil {
		return nil, fmt.Errorf("failed to create SQS client: %w", err)
	}
	return client, nil
}
