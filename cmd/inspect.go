package cmd

import (
	"context"
	"fmt"

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
	// follow, _ := cmd.Flags().GetBool("follow")

	// fmt.Println("Inspect command executed with args:", args)
	// fmt.Println("Queue:", queue)
	// fmt.Println("Max messages:", max)
	// fmt.Println("Follow:", follow)
	ctx := context.Background()
	client, err := queue.NewClient(ctx, "eu-west-1")
	if err != nil {
		return err
	}

	messages, err := client.Inspect(ctx, queueURL, max)
	if err != nil {
		return err
	}

	for _, msg := range messages {
		fmt.Printf("Message ID: %s, Body: %s\n", msg.ID, msg.Body)
	}

	return nil
}

func init() {
	rootCmd.AddCommand(inspectCmd)

	inspectCmd.Flags().String("queue", "", "SQS Queue URL")
	inspectCmd.Flags().Int("max", 10, "Number of messages to fetch")
	inspectCmd.Flags().Bool("follow", false, "Keep polling after draining")

	inspectCmd.MarkFlagRequired("queue")

}
