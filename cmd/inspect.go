package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ahmad-saleem/dlqctl/internal/extract"
	"github.com/ahmad-saleem/dlqctl/internal/logs"
	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/ahmad-saleem/dlqctl/internal/timeparse"
	"github.com/spf13/cobra"
)

var inspectCmd = &cobra.Command{
	Use:   "inspect",
	Short: "Inspect the DLQ",
	Long:  "Inspect the Dead Letter Queue (DLQ) for messages that failed to process.",
	RunE:  runInspect,
}

type inspectOptions struct {
	queueURL   string
	max        int
	follow     bool
	filter     string
	trace      bool
	traceField string
	logGroup   string
	since      string
}

func (o inspectOptions) validate() error {
	if o.trace && o.logGroup == "" {
		return fmt.Errorf("--log-group is required when using --trace")
	}

	if _, _, err := timeparse.ParseSince(o.since); err != nil {
		return fmt.Errorf("invalid --since value: %w", err)
	}

	return nil
}

func runInspect(cmd *cobra.Command, args []string) error {
	var opts inspectOptions
	opts.queueURL, _ = cmd.Flags().GetString("queue")
	opts.max, _ = cmd.Flags().GetInt("max")
	opts.follow, _ = cmd.Flags().GetBool("follow")
	opts.filter, _ = cmd.Flags().GetString("filter")
	opts.trace, _ = cmd.Flags().GetBool("trace")
	opts.traceField, _ = cmd.Flags().GetString("trace-field")
	opts.logGroup, _ = cmd.Flags().GetString("log-group")
	opts.since, _ = cmd.Flags().GetString("since")

	if err := opts.validate(); err != nil {
		return err
	}

	ctx, stop := newContext()
	defer stop()

	client, err := newQueueClient(ctx, cmd)
	if err != nil {
		return err
	}

	var logsClient *logs.Client
	if opts.trace {
		region, _ := cmd.Flags().GetString("region")
		logsClient, err = logs.NewClient(ctx, region)
		if err != nil {
			return err
		}
	}

	return pollQueue(ctx, client, logsClient, opts)
}

func pollQueue(ctx context.Context, client queue.Queue, logsClient *logs.Client, opts inspectOptions) error {
	for {
		messages, err := client.Inspect(ctx, opts.queueURL, opts.max)
		if err != nil {
			if ctx.Err() != nil {
				fmt.Println("Exiting...")
				return nil
			}
			return err
		}

		if len(messages) == 0 && !opts.follow {
			fmt.Println("no messages found in the queue")
			return nil
		}

		if err := printMessages(ctx, logsClient, opts, messages); err != nil {
			return err
		}

		if !opts.follow {
			return nil
		}

		select {
		case <-ctx.Done():
			fmt.Println("Exiting...")
			return nil
		default:
		}
	}
}

func printMessages(ctx context.Context, logsClient *logs.Client, opts inspectOptions, messages []queue.Message) error {
	for _, msg := range messages {
		matched, err := queue.MatchFilter(msg.Body, opts.filter)
		if err != nil {
			return err
		}

		if !matched {
			continue
		}

		fmt.Printf("Message ID: %s, Body: %s\n", msg.ID, msg.Body)

		if opts.trace {
			traceMessage(ctx, logsClient, opts.logGroup, opts.traceField, opts.since, msg.Body)
		}
	}
	return nil
}

func traceMessage(ctx context.Context, logsClient *logs.Client, logGroup, traceField, since, body string) {
	val, err := extract.Field(body, traceField)
	if err != nil {
		fmt.Printf("  [trace] %v\n", err)
		return
	}

	start, end, err := timeparse.ParseSince(since)
	if err != nil {
		fmt.Printf("  [trace] %v\n", err)
		return
	}

	events, err := logsClient.Search(ctx, logGroup, val, start, end, 10)
	if err != nil {
		fmt.Printf("  [trace] log search failed: %v\n", err)
		return
	}

	if len(events) == 0 {
		fmt.Println("  [trace] no matching logs found")
		return
	}

	for _, e := range events {
		fmt.Printf("  [log] %s  %s\n", e.Timestamp.Format(time.RFC3339), strings.TrimRight(e.Message, "\n"))
	}
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
