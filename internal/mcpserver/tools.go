package mcpserver

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ahmad-saleem/dlqctl/internal/extract"
	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/ahmad-saleem/dlqctl/internal/timeparse"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Limits that keep tool results small enough for a model's context.
const (
	maxTextBytes     = 4096
	maxPeek          = 100
	maxTrace         = 20
	maxLogEvents     = 500
	maxReplay        = 1000
	maxReplayWorkers = 20
	traceEventsPer   = 10
)

type Message struct {
	ID        string `json:"id"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated,omitempty"`
}

type LogEvent struct {
	Timestamp string `json:"timestamp" jsonschema:"RFC 3339 timestamp"`
	Message   string `json:"message"`
	Truncated bool   `json:"truncated,omitempty"`
}

// --- dlq_stats ---

type StatsInput struct {
	QueueURL string `json:"queue_url" jsonschema:"SQS URL of the dead-letter queue"`
}

type StatsOutput struct {
	QueueURL                 string   `json:"queue_url"`
	Visible                  int      `json:"visible" jsonschema:"approximate number of messages available to receive"`
	InFlight                 int      `json:"in_flight" jsonschema:"approximate number of received but not yet deleted messages"`
	Delayed                  int      `json:"delayed"`
	VisibilityTimeoutSeconds int      `json:"visibility_timeout_seconds"`
	RetentionSeconds         int      `json:"retention_seconds" jsonschema:"messages older than this are deleted by SQS"`
	RedrivePolicy            string   `json:"redrive_policy,omitempty" jsonschema:"this queue's own redrive policy, if any"`
	SourceQueues             []string `json:"source_queues" jsonschema:"queues whose redrive policy sends failed messages to this DLQ; usually the replay target"`
	SourceQueuesError        string   `json:"source_queues_error,omitempty"`
}

func (h *handlers) stats(ctx context.Context, _ *mcp.CallToolRequest, in StatsInput) (*mcp.CallToolResult, StatsOutput, error) {
	st, err := h.queue.Stats(ctx, in.QueueURL)
	if err != nil {
		return nil, StatsOutput{}, fmt.Errorf("failed to get queue attributes: %w", err)
	}

	out := StatsOutput{
		QueueURL:                 in.QueueURL,
		Visible:                  st.Visible,
		InFlight:                 st.InFlight,
		Delayed:                  st.Delayed,
		VisibilityTimeoutSeconds: st.VisibilityTimeoutSeconds,
		RetentionSeconds:         st.RetentionSeconds,
		RedrivePolicy:            st.RedrivePolicy,
		SourceQueues:             []string{},
	}

	// Missing IAM permission for this call shouldn't hide the counts.
	sources, err := h.queue.SourceQueues(ctx, in.QueueURL)
	if err != nil {
		out.SourceQueuesError = err.Error()
	} else if sources != nil {
		out.SourceQueues = sources
	}

	return nil, out, nil
}

// --- dlq_peek ---

type PeekInput struct {
	QueueURL string `json:"queue_url" jsonschema:"SQS queue URL"`
	Max      int    `json:"max,omitempty" jsonschema:"messages to read, 1-100 (default 10)"`
	Filter   string `json:"filter,omitempty" jsonschema:"Go regex; only matching bodies are returned"`
}

type PeekOutput struct {
	Received     int       `json:"received" jsonschema:"messages read from the queue before filtering"`
	Messages     []Message `json:"messages"`
	ReleaseError string    `json:"release_error,omitempty" jsonschema:"set if messages could not be released and stay hidden until their visibility timeout"`
}

func (h *handlers) peek(ctx context.Context, _ *mcp.CallToolRequest, in PeekInput) (*mcp.CallToolResult, PeekOutput, error) {
	max, err := limit("max", in.Max, 10, maxPeek)
	if err != nil {
		return nil, PeekOutput{}, err
	}
	re, err := compileFilter(in.Filter)
	if err != nil {
		return nil, PeekOutput{}, err
	}

	received, err := h.queue.Inspect(ctx, in.QueueURL, max)
	if err != nil {
		return nil, PeekOutput{}, fmt.Errorf("failed to receive messages: %w", err)
	}

	out := PeekOutput{Received: len(received), Messages: []Message{}}
	for _, m := range received {
		if re == nil || re.MatchString(m.Body) {
			out.Messages = append(out.Messages, toMessage(m))
		}
	}
	out.ReleaseError = h.release(ctx, in.QueueURL, received)

	return nil, out, nil
}

// --- dlq_trace ---

type TraceInput struct {
	QueueURL   string `json:"queue_url" jsonschema:"SQS queue URL"`
	LogGroup   string `json:"log_group" jsonschema:"CloudWatch Log Group of the consumer"`
	TraceField string `json:"trace_field,omitempty" jsonschema:"JSON field holding the correlation ID; defaults to the first of requestId, correlationId, traceId"`
	Since      string `json:"since,omitempty" jsonschema:"log search window, e.g. 30m, 2h, 1d (default 1h)"`
	Max        int    `json:"max,omitempty" jsonschema:"messages to trace, 1-20 (default 5)"`
	Filter     string `json:"filter,omitempty" jsonschema:"Go regex; only matching bodies are traced"`
}

type TracedMessage struct {
	Message
	TraceValue string     `json:"trace_value,omitempty"`
	Error      string     `json:"error,omitempty"`
	LogEvents  []LogEvent `json:"log_events"`
}

type TraceOutput struct {
	Messages     []TracedMessage `json:"messages"`
	ReleaseError string          `json:"release_error,omitempty"`
}

func (h *handlers) trace(ctx context.Context, _ *mcp.CallToolRequest, in TraceInput) (*mcp.CallToolResult, TraceOutput, error) {
	max, err := limit("max", in.Max, 5, maxTrace)
	if err != nil {
		return nil, TraceOutput{}, err
	}
	re, err := compileFilter(in.Filter)
	if err != nil {
		return nil, TraceOutput{}, err
	}
	since := in.Since
	if since == "" {
		since = "1h"
	}
	start, end, err := timeparse.ParseSince(since)
	if err != nil {
		return nil, TraceOutput{}, fmt.Errorf("invalid since: %w", err)
	}

	received, err := h.queue.Inspect(ctx, in.QueueURL, max)
	if err != nil {
		return nil, TraceOutput{}, fmt.Errorf("failed to receive messages: %w", err)
	}
	// Release before the log searches, which can take a while.
	releaseErr := h.release(ctx, in.QueueURL, received)

	out := TraceOutput{Messages: []TracedMessage{}, ReleaseError: releaseErr}
	for _, m := range received {
		if re != nil && !re.MatchString(m.Body) {
			continue
		}

		tm := TracedMessage{Message: toMessage(m), LogEvents: []LogEvent{}}
		val, err := extract.Field(m.Body, in.TraceField)
		if err != nil {
			tm.Error = err.Error()
			out.Messages = append(out.Messages, tm)
			continue
		}
		tm.TraceValue = val

		events, err := h.logs.Search(ctx, in.LogGroup, val, start, end, traceEventsPer)
		if err != nil {
			tm.Error = fmt.Sprintf("log search failed: %v", err)
		}
		for _, e := range events {
			tm.LogEvents = append(tm.LogEvents, toLogEvent(e.Timestamp, e.Message))
		}
		out.Messages = append(out.Messages, tm)
	}

	return nil, out, nil
}

// --- logs_search ---

type LogsSearchInput struct {
	LogGroup string `json:"log_group" jsonschema:"CloudWatch Log Group name"`
	Pattern  string `json:"pattern" jsonschema:"CloudWatch Logs filter pattern, e.g. ERROR or \"order 123\""`
	Since    string `json:"since,omitempty" jsonschema:"relative window, e.g. 30m, 2h, 1d (default 1h); ignored when start/end are set"`
	Start    string `json:"start,omitempty" jsonschema:"RFC 3339 start time; requires end"`
	End      string `json:"end,omitempty" jsonschema:"RFC 3339 end time; requires start"`
	Max      int    `json:"max,omitempty" jsonschema:"events to return, 1-500 (default 50)"`
}

type LogsSearchOutput struct {
	Events []LogEvent `json:"events"`
}

func (h *handlers) logsSearch(ctx context.Context, _ *mcp.CallToolRequest, in LogsSearchInput) (*mcp.CallToolResult, LogsSearchOutput, error) {
	max, err := limit("max", in.Max, 50, maxLogEvents)
	if err != nil {
		return nil, LogsSearchOutput{}, err
	}
	since := in.Since
	if since == "" {
		since = "1h"
	}
	start, end, err := timeparse.Resolve(since, in.Start, in.End)
	if err != nil {
		return nil, LogsSearchOutput{}, err
	}

	events, err := h.logs.Search(ctx, in.LogGroup, in.Pattern, start, end, max)
	if err != nil {
		return nil, LogsSearchOutput{}, err
	}

	out := LogsSearchOutput{Events: make([]LogEvent, 0, len(events))}
	for _, e := range events {
		out.Events = append(out.Events, toLogEvent(e.Timestamp, e.Message))
	}
	return nil, out, nil
}

// --- dlq_replay ---

type ReplayInput struct {
	SourceQueueURL string `json:"source_queue_url" jsonschema:"SQS URL of the dead-letter queue to replay from"`
	TargetQueueURL string `json:"target_queue_url" jsonschema:"SQS URL to send messages to, usually a source queue from dlq_stats"`
	Max            int    `json:"max,omitempty" jsonschema:"messages to read, 1-1000 (default 10)"`
	Filter         string `json:"filter,omitempty" jsonschema:"Go regex; only matching bodies are replayed"`
	Workers        int    `json:"workers,omitempty" jsonschema:"concurrent senders, 1-20 (default 1)"`
	DryRun         *bool  `json:"dry_run,omitempty" jsonschema:"preview only, nothing is sent or deleted (default true)"`
}

type ReplayOutput struct {
	DryRun   bool      `json:"dry_run"`
	Received int       `json:"received"`
	Matched  int       `json:"matched" jsonschema:"messages that matched the filter (all received messages when no filter)"`
	Replayed int       `json:"replayed"`
	Failed   int       `json:"failed" jsonschema:"messages that could not be sent or deleted; they stay in the source queue"`
	Errors   []string  `json:"errors"`
	Messages []Message `json:"messages,omitempty" jsonschema:"dry run only: the messages that would be replayed"`
	Note     string    `json:"note,omitempty"`
}

func (h *handlers) replay(ctx context.Context, _ *mcp.CallToolRequest, in ReplayInput) (*mcp.CallToolResult, ReplayOutput, error) {
	max, err := limit("max", in.Max, 10, maxReplay)
	if err != nil {
		return nil, ReplayOutput{}, err
	}
	workers, err := limit("workers", in.Workers, 1, maxReplayWorkers)
	if err != nil {
		return nil, ReplayOutput{}, err
	}
	re, err := compileFilter(in.Filter)
	if err != nil {
		return nil, ReplayOutput{}, err
	}
	if in.SourceQueueURL == in.TargetQueueURL {
		return nil, ReplayOutput{}, fmt.Errorf("source and target queue must differ")
	}
	dryRun := in.DryRun == nil || *in.DryRun

	received, err := h.queue.Inspect(ctx, in.SourceQueueURL, max)
	if err != nil {
		return nil, ReplayOutput{}, fmt.Errorf("failed to receive messages: %w", err)
	}

	var matched, skipped []queue.Message
	for _, m := range received {
		if re == nil || re.MatchString(m.Body) {
			matched = append(matched, m)
		} else {
			skipped = append(skipped, m)
		}
	}

	out := ReplayOutput{DryRun: dryRun, Received: len(received), Matched: len(matched), Errors: []string{}}

	if dryRun {
		out.Messages = make([]Message, 0, len(matched))
		for _, m := range matched {
			out.Messages = append(out.Messages, toMessage(m))
		}
		if msg := h.release(ctx, in.SourceQueueURL, received); msg != "" {
			out.Errors = append(out.Errors, msg)
		}
		out.Note = "dry run: nothing was sent or deleted. Call again with dry_run=false, after the user approves, to replay."
		return nil, out, nil
	}

	if msg := h.release(ctx, in.SourceQueueURL, skipped); msg != "" {
		out.Errors = append(out.Errors, msg)
	}

	errs := h.queue.ReplayWorkerPool(ctx, in.SourceQueueURL, in.TargetQueueURL, matched, workers)
	out.Failed = len(errs)
	out.Replayed = len(matched) - len(errs)
	for _, e := range errs {
		out.Errors = append(out.Errors, e.Error())
	}

	return nil, out, nil
}

// --- helpers ---

// limit applies a default for zero and rejects values outside 1..max.
func limit(name string, v, def, max int) (int, error) {
	if v == 0 {
		return def, nil
	}
	if v < 1 || v > max {
		return 0, fmt.Errorf("%s must be between 1 and %d, got %d", name, max, v)
	}
	return v, nil
}

// compileFilter returns nil for an empty filter (match all).
func compileFilter(filter string) (*regexp.Regexp, error) {
	if filter == "" {
		return nil, nil
	}
	re, err := regexp.Compile(filter)
	if err != nil {
		return nil, fmt.Errorf("invalid filter regex: %w", err)
	}
	return re, nil
}

// release makes messages visible again and returns a non-empty string on failure.
func (h *handlers) release(ctx context.Context, queueURL string, messages []queue.Message) string {
	if len(messages) == 0 {
		return ""
	}
	if err := h.queue.Release(ctx, queueURL, messages); err != nil {
		return fmt.Sprintf("could not release messages; they stay hidden until the queue's visibility timeout: %v", err)
	}
	return ""
}

func toMessage(m queue.Message) Message {
	body, truncated := truncate(m.Body)
	return Message{ID: m.ID, Body: body, Truncated: truncated}
}

func toLogEvent(ts time.Time, msg string) LogEvent {
	text, truncated := truncate(strings.TrimRight(msg, "\n"))
	return LogEvent{Timestamp: ts.Format(time.RFC3339), Message: text, Truncated: truncated}
}

func truncate(s string) (string, bool) {
	if len(s) <= maxTextBytes {
		return s, false
	}
	i := maxTextBytes
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i], true
}
