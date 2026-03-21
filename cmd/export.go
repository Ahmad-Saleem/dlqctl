package cmd

import (
	"fmt"
	"os"

	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/spf13/cobra"
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export messages from the DLQ",
	Long:  "Export messages from the Dead Letter Queue (DLQ) to a file or another destination.",
	RunE:  runExport,
}

func runExport(cmd *cobra.Command, args []string) error {
	queueURL, _ := cmd.Flags().GetString("queue")
	format, _ := cmd.Flags().GetString("format")
	output, _ := cmd.Flags().GetString("output")
	max, _ := cmd.Flags().GetInt("max")

	if format != "json" && format != "csv" {
		return fmt.Errorf("invalid format %q — must be json or csv", format)
	}

	ctx, stop := newContext()
	defer stop()

	client, err := newQueueClient(ctx, cmd)
	if err != nil {
		return err
	}

	messages, err := client.Inspect(ctx, queueURL, max)
	if err != nil {
		return err
	}

	if len(messages) == 0 {
		fmt.Println("no messages found in DLQ")
		return nil
	}

	f, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer f.Close()

	switch format {
	case "json":
		if err := queue.WriteJSON(f, messages); err != nil {
			return fmt.Errorf("failed to write JSON: %w", err)
		}
	case "csv":
		if err := queue.WriteCSV(f, messages); err != nil {
			return fmt.Errorf("failed to write CSV: %w", err)
		}
	}
	fmt.Printf("exported %d messages as %s to %s\n", len(messages), format, output)
	return nil
}

func init() {
	rootCmd.AddCommand(exportCmd)

	exportCmd.Flags().StringP("queue", "q", "", "Name of the queue to export messages from")
	exportCmd.Flags().StringP("format", "f", "json", "Output format for exported messages")
	exportCmd.Flags().StringP("output", "o", "export.json", "Output file for exported messages")
	exportCmd.Flags().IntP("max", "m", 100, "Maximum number of messages to export")

	exportCmd.MarkFlagRequired("queue")
}
