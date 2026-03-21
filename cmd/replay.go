package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/spf13/cobra"
)

var replayCmd = &cobra.Command{
	Use:   "replay",
	Short: "Replay messages from the DLQ",
	Long:  "Replay messages from the Dead Letter Queue (DLQ) to the original queue.",
	RunE:  runReplay,
}

func runReplay(cmd *cobra.Command, args []string) error {

	sourceQueueURL, _ := cmd.Flags().GetString("sourceQueueURL")
	targetQueueURL, _ := cmd.Flags().GetString("targetQueueURL")
	max, _ := cmd.Flags().GetInt("max")
	filter, _ := cmd.Flags().GetString("filter")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := queue.NewClient(ctx, "eu-west-1")
	if err != nil {
		return fmt.Errorf("failed to create SQS client: %w", err)
	}

	messages, err := client.Inspect(ctx, sourceQueueURL, max)
	if err != nil {
		return fmt.Errorf("failed to read from DLQ: %w", err)
	}

	if len(messages) == 0 {
		fmt.Println("no messages found in DLQ")
		return nil
	}

	replayed := 0
	skipped := 0

	for _, m := range messages {
		if filter != "" {
			matched, err := queue.MatchFilter(m.Body, filter)
			if err != nil {
				return fmt.Errorf("invalid filter regex: %w", err)
			}
			if !matched {
				skipped++
				continue
			}
		}

		if err := client.Replay(ctx, targetQueueURL, m.Body); err != nil {
			return fmt.Errorf("failed to replay message %s: %w", m.ID, err)
		}

		if err := client.Delete(ctx, sourceQueueURL, m.ReceiptHandle); err != nil {
			return fmt.Errorf("failed to delete message %s from DLQ: %w", m.ID, err)
		}

		fmt.Printf("replayed: %s\n", m.ID)
		replayed++
	}

	fmt.Printf("\ndone. replayed: %d, skipped: %d\n", replayed, skipped)
	return nil
}

func init() {
	rootCmd.AddCommand(replayCmd)

	replayCmd.Flags().StringP("sourceQueueURL", "S", "", "The URL of the Source SQS queue to replay messages from")
	replayCmd.Flags().StringP("targetQueueURL", "T", "", "The URL of the Target SQS queue to replay messages to")
	replayCmd.Flags().IntP("max", "M", 10, "Maximum number of messages to retrieve")
	replayCmd.Flags().StringP("filter", "F", "", "Regex filter to apply to message bodies")

	replayCmd.MarkFlagRequired("sourceQueueURL")
	replayCmd.MarkFlagRequired("targetQueueURL")
}
