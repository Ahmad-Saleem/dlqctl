package cmd

import (
	"fmt"
	"os"

	"github.com/ahmad-saleem/dlqctl/internal/agent"
	"github.com/ahmad-saleem/dlqctl/internal/logs"
	"github.com/ahmad-saleem/dlqctl/internal/mcpserver"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/spf13/cobra"
)

var investigateCmd = &cobra.Command{
	Use:   "investigate",
	Short: "Let Claude find the root cause of a DLQ's failures",
	Long: `Run an AI agent that investigates a dead-letter queue and prints a
Markdown root-cause report. It samples messages, traces them in the
consumer's CloudWatch logs, and recommends a fix and replay commands.

The agent is read-only: it releases every message it reads and never
replays or deletes. It calls the Claude API, so it needs ANTHROPIC_API_KEY
(or an "ant auth login" profile) and costs API usage.

Progress goes to stderr and the report to stdout.

Example:
  dlqctl investigate -q https://sqs.eu-west-1.amazonaws.com/123456789012/orders-dlq \
    --log-group /ecs/orders-consumer > report.md`,
	RunE: runInvestigate,
}

type investigateOptions struct {
	queueURL string
	logGroup string
	model    string
	maxTurns int
}

const maxInvestigateTurns = 100

func (o investigateOptions) validate() error {
	if o.model == "" {
		return fmt.Errorf("--model must not be empty")
	}
	if o.maxTurns < 1 || o.maxTurns > maxInvestigateTurns {
		return fmt.Errorf("--max-turns must be between 1 and %d, got %d", maxInvestigateTurns, o.maxTurns)
	}
	return nil
}

func runInvestigate(cmd *cobra.Command, args []string) error {
	var opts investigateOptions
	opts.queueURL, _ = cmd.Flags().GetString("queue")
	opts.logGroup, _ = cmd.Flags().GetString("log-group")
	opts.model, _ = cmd.Flags().GetString("model")
	opts.maxTurns, _ = cmd.Flags().GetInt("max-turns")

	if err := opts.validate(); err != nil {
		return err
	}

	ctx, stop := newContext()
	defer stop()

	queueClient, err := newQueueClient(ctx, cmd)
	if err != nil {
		return err
	}

	region, _ := cmd.Flags().GetString("region")
	logsClient, err := logs.NewClient(ctx, region)
	if err != nil {
		return err
	}

	// No AllowReplay: the agent only gets read-only tools.
	server := mcpserver.New(queueClient, logsClient, mcpserver.Options{Version: rootCmd.Version})
	client := anthropic.NewClient()

	report, err := agent.Run(ctx, &client.Beta.Messages, server, agent.Config{
		QueueURL: opts.queueURL,
		LogGroup: opts.logGroup,
		Model:    opts.model,
		MaxTurns: opts.maxTurns,
		Progress: os.Stderr,
	})
	if err != nil {
		return err
	}

	fmt.Println(report)
	return nil
}

func init() {
	rootCmd.AddCommand(investigateCmd)

	investigateCmd.Flags().StringP("queue", "q", "", "SQS URL of the dead-letter queue to investigate")
	investigateCmd.Flags().String("log-group", "", "CloudWatch Log Group of the consumer (optional, but makes the root cause much easier to find)")
	investigateCmd.Flags().String("model", "claude-opus-5", "Claude model to use (must support adaptive thinking)")
	investigateCmd.Flags().Int("max-turns", 20, "Maximum model turns before giving up; bounds API cost")

	investigateCmd.MarkFlagRequired("queue")
}
