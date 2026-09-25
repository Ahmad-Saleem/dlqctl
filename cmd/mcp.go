package cmd

import (
	"fmt"

	"github.com/ahmad-saleem/dlqctl/internal/logs"
	"github.com/ahmad-saleem/dlqctl/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run an MCP server over stdio for AI agents",
	Long: `Run a Model Context Protocol (MCP) server over stdio so AI agents
(Claude Code, Claude Desktop, Cursor, ...) can investigate dead-letter queues.

Read-only tools are always available. The dlq_replay tool is only exposed
with --allow-replay.

Example:
  claude mcp add dlqctl -- dlqctl mcp --region eu-west-1`,
	RunE: runMCP,
}

func runMCP(cmd *cobra.Command, args []string) error {
	allowReplay, _ := cmd.Flags().GetBool("allow-replay")
	region, _ := cmd.Flags().GetString("region")

	ctx, stop := newContext()
	defer stop()

	queueClient, err := newQueueClient(ctx, cmd)
	if err != nil {
		return err
	}

	logsClient, err := logs.NewClient(ctx, region)
	if err != nil {
		return err
	}

	server := mcpserver.New(queueClient, logsClient, mcpserver.Options{
		Version:     rootCmd.Version,
		AllowReplay: allowReplay,
	})

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil && ctx.Err() == nil {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

func init() {
	rootCmd.AddCommand(mcpCmd)

	mcpCmd.Flags().Bool("allow-replay", false, "Expose the dlq_replay tool (sends to target and deletes from source)")
}
