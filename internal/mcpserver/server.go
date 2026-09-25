// Package mcpserver exposes dlqctl's operations as MCP tools so AI agents can
// investigate dead-letter queues during incidents.
//
// Handlers must never write to stdout: with the stdio transport, stdout
// carries the MCP protocol.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/ahmad-saleem/dlqctl/internal/logs"
	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Options struct {
	Version string
	// AllowReplay registers the dlq_replay tool. Off by default so an agent
	// cannot change queue state unless the operator opted in.
	AllowReplay bool
}

type handlers struct {
	queue queue.Queue
	logs  logs.Searcher
}

const instructions = `dlqctl inspects AWS SQS dead-letter queues (DLQs) and correlates their messages with CloudWatch Logs.

Typical incident workflow:
1. dlq_stats: how many messages, and which source queue feeds this DLQ.
2. dlq_peek: sample message bodies; group failures by shape or error.
3. dlq_trace / logs_search: find the consumer's log lines for those messages to identify the root cause.
4. Report findings. Only suggest replaying once the root cause is fixed; replaying into a still-broken consumer just sends messages back to the DLQ.

Reading tools release messages back to the queue immediately after reading them, so they stay available for replay.`

func New(q queue.Queue, l logs.Searcher, opts Options) *mcp.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "dlqctl", Version: opts.Version},
		&mcp.ServerOptions{Instructions: instructions},
	)
	h := &handlers{queue: q, logs: l}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "dlq_stats",
		Description: "Get approximate message counts, visibility timeout, retention, redrive policy, and the source queues that feed a dead-letter queue. Start here.",
		Annotations: readOnly("DLQ stats"),
	}, h.stats)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "dlq_peek",
		Description: fmt.Sprintf("Read up to %d messages from a queue without deleting them, optionally filtered by a regex on the body. Messages are released back to the queue right after reading. Bodies longer than %d bytes are truncated.", maxPeek, maxTextBytes),
		Annotations: readOnly("Peek DLQ messages"),
	}, h.peek)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "dlq_trace",
		Description: fmt.Sprintf("Read up to %d messages and, for each, extract a correlation ID from its JSON body (trace_field, or requestId/correlationId/traceId) and search a CloudWatch Log Group for it. Per-message failures are reported inline. Messages are released back to the queue right after reading.", maxTrace),
		Annotations: readOnly("Trace DLQ messages in logs"),
	}, h.trace)

	mcp.AddTool(s, &mcp.Tool{
		Name:        "logs_search",
		Description: "Search a CloudWatch Log Group with a CloudWatch filter pattern over a relative window (since) or an explicit RFC 3339 start/end range.",
		Annotations: readOnly("Search CloudWatch Logs"),
	}, h.logsSearch)

	if opts.AllowReplay {
		mcp.AddTool(s, &mcp.Tool{
			Name: "dlq_replay",
			Description: "Move messages from a DLQ back to a target queue: send each to the target, then delete it from the source. " +
				"dry_run defaults to true and only previews what would be replayed; set dry_run=false to actually replay. " +
				"Always run a dry run first, confirm the root cause is fixed, and get the user's explicit approval before replaying.",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Replay DLQ messages",
				DestructiveHint: ptr(true),
				OpenWorldHint:   ptr(true),
			},
		}, h.replay)
	}

	s.AddPrompt(&mcp.Prompt{
		Name:        "investigate_dlq",
		Title:       "Investigate a dead-letter queue",
		Description: "Runbook for triaging a DLQ during an incident.",
		Arguments: []*mcp.PromptArgument{
			{Name: "queue_url", Description: "SQS URL of the dead-letter queue", Required: true},
			{Name: "log_group", Description: "CloudWatch Log Group of the consumer (optional)"},
		},
	}, investigatePrompt)

	return s
}

func investigatePrompt(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	queueURL := req.Params.Arguments["queue_url"]
	logGroup := req.Params.Arguments["log_group"]

	logStep := "Ask me for the consumer's CloudWatch Log Group, then use dlq_trace or logs_search to find the errors behind these messages."
	if logGroup != "" {
		logStep = fmt.Sprintf("Use dlq_trace with log_group %q (and logs_search for broader error patterns) to find the errors behind these messages.", logGroup)
	}

	text := fmt.Sprintf(`Investigate the dead-letter queue %s.

1. Call dlq_stats to see how many messages there are and which source queue feeds it.
2. Call dlq_peek to sample messages. Group them by shape, type, or error, and note counts per group.
3. %s
4. Report: what is failing, since roughly when, how many messages are affected, the likely root cause with evidence (log lines, message IDs), and the recommended fix.
5. Do not replay anything. If replay is appropriate once the fix is deployed, say so and propose a dry run of dlq_replay (source = this DLQ, target = the source queue from step 1), with a filter if only some messages should go back.`, queueURL, logStep)

	return &mcp.GetPromptResult{
		Description: "Triage a dead-letter queue",
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: text}},
		},
	}, nil
}

func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, OpenWorldHint: ptr(true)}
}

func ptr[T any](v T) *T { return &v }
