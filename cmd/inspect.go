package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect the DLQ",
	Long:  "Inspect the Dead Letter Queue (DLQ) for messages that failed to process.",
	RunE:  runInspect,
}

func runInspect(cmd *cobra.Command, args []string) error {
	queueURL, _ := cmd.Flags().GetString("queue")
	max, _ := cmd.Flags().GetInt("max")
	follow, _ := cmd.Flags().GetBool("follow")
	filter, _ := cmd.Flags().GetString("filter")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	client, err := queue.NewClient(ctx, "eu-west-1")
	if err != nil {
		return err
	}

	for {
		messages, err := client.Inspect(ctx, queueURL, max)
		if err != nil {
			return err
		}

		for _, msg := range messages {

			matched, err := queue.MatchFilter(msg.Body, filter)
			if err != nil {
				return err
			}

			if !matched {
				continue
			}
			fmt.Printf("Message ID: %s, Body: %s\n", msg.ID, msg.Body)
		}

		if !follow {
			break
		}

		select {
		case <-ctx.Done():
			fmt.Println("Exiting...")
			return nil
		default:
		}
	}

	return nil
}

func init() {
	rootCmd.AddCommand(inspectCmd)

	inspectCmd.Flags().String("queue", "", "SQS Queue URL")
	inspectCmd.Flags().Int("max", 10, "Number of messages to fetch")
	inspectCmd.Flags().Bool("follow", false, "Keep polling after draining")
	inspectCmd.Flags().String("filter", "", "Regex filter for message bodies")

	inspectCmd.MarkFlagRequired("queue")

}
