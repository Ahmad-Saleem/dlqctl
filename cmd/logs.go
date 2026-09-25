package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/ahmad-saleem/dlqctl/internal/logs"
	"github.com/ahmad-saleem/dlqctl/internal/timeparse"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Search CloudWatch Log Group for matching log events",
	RunE:  runLogs,
}

func runLogs(cmd *cobra.Command, args []string) error {
	logGroup, _ := cmd.Flags().GetString("log-group")
	pattern, _ := cmd.Flags().GetString("pattern")
	since, _ := cmd.Flags().GetString("since")
	start, _ := cmd.Flags().GetString("start")
	end, _ := cmd.Flags().GetString("end")
	max, _ := cmd.Flags().GetInt("max")

	startTime, endTime, err := timeparse.Resolve(since, start, end)
	if err != nil {
		return err
	}

	ctx, stop := newContext()
	defer stop()

	region, _ := cmd.Flags().GetString("region")

	client, err := logs.NewClient(ctx, region)
	if err != nil {
		return err
	}

	events, err := client.Search(ctx, logGroup, pattern, startTime, endTime, max)
	if err != nil {
		return err
	}

	if len(events) == 0 {
		fmt.Println("no matching log events found")
		return nil
	}

	for _, e := range events {
		fmt.Printf("%s  %s\n", e.Timestamp.Format(time.RFC3339), strings.TrimRight(e.Message, "\n"))
	}

	fmt.Printf("\n%d event(s) found\n", len(events))

	return nil
}

func init() {
	rootCmd.AddCommand(logsCmd)

	logsCmd.Flags().String("log-group", "", "CloudWatch Log Group name")
	logsCmd.Flags().String("pattern", "", "CloudWatch filter pattern")
	logsCmd.Flags().String("since", "1h", "Relative time window (e.g. 30m, 2h, 1d)")
	logsCmd.Flags().String("start", "", "Explicit start time (ISO 8601)")
	logsCmd.Flags().String("end", "", "Explicit end time (ISO 8601)")
	logsCmd.Flags().Int("max", 50, "Max log events to return")

	logsCmd.MarkFlagRequired("log-group")
	logsCmd.MarkFlagRequired("pattern")
}
