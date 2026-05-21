package cmd

import (
	"fmt"
	"time"

	"github.com/ahmad-saleem/dlqctl/internal/extract"
	"github.com/ahmad-saleem/dlqctl/internal/logs"
	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/ahmad-saleem/dlqctl/internal/timeparse"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
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
	trace, _ := cmd.Flags().GetBool("trace")
	traceField, _ := cmd.Flags().GetString("trace-field")
	logGroup, _ := cmd.Flags().GetString("log-group")

	if trace && logGroup == "" {
		return fmt.Errorf("--log-group is required when using --trace")
	}

	ctx, stop := newContext()
	defer stop()

	client, err := newQueueClient(ctx, cmd)
	if err != nil {
		return err
	}

	var logsClient *logs.Client
	if trace {
		region := viper.GetString("aws.region")
		logsClient, err = logs.NewClient(ctx, region)
		if err != nil {
			return err
		}
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

			if trace {
				val, err := extract.Field(msg.Body, traceField)
				if err != nil {
					fmt.Printf("  [trace] %v\n", err)
					continue
				}

				since, _ := cmd.Flags().GetString("since")
				if since == "" {
					since = "1h"
				}
				start, end, _ := timeparse.ParseSince(since)

				events, err := logsClient.Search(ctx, logGroup, val, start, end, 10)
				if err != nil {
					fmt.Printf("  [trace] log search failed: %v\n", err)
					continue
				}

				if len(events) == 0 {
					fmt.Println("  [trace] no matching logs found")
				}

				for _, e := range events {
					fmt.Printf("  [log] %s  %s", e.Timestamp.Format(time.RFC3339), e.Message)
				}
			}
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

	inspectCmd.Flags().Bool("trace", false, "Search CloudWatch logs for each DLQ message")
	inspectCmd.Flags().String("trace-field", "", "JSON field to extract as search pattern")
	inspectCmd.Flags().String("log-group", "", "CloudWatch Log Group to search")
	inspectCmd.Flags().String("since", "1h", "Time range for log search (e.g. 30m, 2h, 1d)")

	inspectCmd.MarkFlagRequired("queue")

}
