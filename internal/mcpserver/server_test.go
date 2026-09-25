package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ahmad-saleem/dlqctl/internal/logs"
	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type fakeQueue struct {
	messages []queue.Message
	released int
	replayed []queue.Message
	deleted  int
}

func (f *fakeQueue) Inspect(_ context.Context, _ string, max int) ([]queue.Message, error) {
	return f.messages[:min(max, len(f.messages))], nil
}

func (f *fakeQueue) Replay(context.Context, string, string) error { return nil }

func (f *fakeQueue) ReplayWorkerPool(_ context.Context, _, _ string, messages []queue.Message, _ int) []error {
	f.replayed = append(f.replayed, messages...)
	f.deleted += len(messages)
	return nil
}

func (f *fakeQueue) Delete(context.Context, string, string) error {
	f.deleted++
	return nil
}

func (f *fakeQueue) Release(_ context.Context, _ string, messages []queue.Message) error {
	f.released += len(messages)
	return nil
}

func (f *fakeQueue) Stats(context.Context, string) (queue.Stats, error) {
	return queue.Stats{Visible: len(f.messages), VisibilityTimeoutSeconds: 30}, nil
}

func (f *fakeQueue) SourceQueues(context.Context, string) ([]string, error) {
	return nil, errors.New("access denied")
}

type fakeLogs struct{}

func (fakeLogs) Search(_ context.Context, _, pattern string, _, _ time.Time, _ int) ([]logs.LogEvent, error) {
	if pattern == "boom" {
		return nil, errors.New("throttled")
	}
	return []logs.LogEvent{{Timestamp: time.Unix(0, 0), Message: "ERROR for " + pattern + "\n"}}, nil
}

func connect(t *testing.T, q queue.Queue, opts Options) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()

	if _, err := New(q, fakeLogs{}, opts).Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call invokes a tool and decodes its structured output into out.
// It returns the tool error text, if the call ended in a tool error.
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		return res.Content[0].(*mcp.TextContent).Text
	}
	raw, _ := json.Marshal(res.StructuredContent)
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode %s output: %v", name, err)
	}
	return ""
}

func toolNames(t *testing.T, cs *mcp.ClientSession) []string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestReplayToolOnlyWhenAllowed(t *testing.T) {
	readOnly := []string{"dlq_peek", "dlq_stats", "dlq_trace", "logs_search"}

	if got := toolNames(t, connect(t, &fakeQueue{}, Options{})); !slices.Equal(got, readOnly) {
		t.Errorf("default tools = %v, want %v", got, readOnly)
	}

	got := toolNames(t, connect(t, &fakeQueue{}, Options{AllowReplay: true}))
	if !slices.Contains(got, "dlq_replay") {
		t.Errorf("tools with AllowReplay = %v, want dlq_replay included", got)
	}
}

func TestStatsSurvivesSourceQueuesError(t *testing.T) {
	cs := connect(t, &fakeQueue{messages: make([]queue.Message, 3)}, Options{})

	var out StatsOutput
	if msg := call(t, cs, "dlq_stats", map[string]any{"queue_url": "q"}, &out); msg != "" {
		t.Fatalf("unexpected tool error: %s", msg)
	}
	if out.Visible != 3 || out.SourceQueuesError == "" {
		t.Errorf("got %+v, want visible=3 and a source_queues_error", out)
	}
}

func TestPeekFiltersTruncatesAndReleases(t *testing.T) {
	q := &fakeQueue{messages: []queue.Message{
		{ID: "1", Body: `{"type":"order"}`, ReceiptHandle: "secret-1"},
		{ID: "2", Body: `{"type":"refund"}`, ReceiptHandle: "secret-2"},
		{ID: "3", Body: `{"type":"order","blob":"` + strings.Repeat("x", 5000) + `"}`, ReceiptHandle: "secret-3"},
	}}
	cs := connect(t, q, Options{})

	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "dlq_peek", Arguments: map[string]any{"queue_url": "q", "filter": "order"},
	})
	if err != nil || res.IsError {
		t.Fatalf("dlq_peek failed: %v %+v", err, res)
	}
	raw, _ := json.Marshal(res)
	if strings.Contains(string(raw), "secret-") {
		t.Error("receipt handles must not be exposed to the agent")
	}

	var out PeekOutput
	call(t, cs, "dlq_peek", map[string]any{"queue_url": "q", "filter": "order"}, &out)
	if out.Received != 3 || len(out.Messages) != 2 {
		t.Fatalf("received=%d matched=%d, want 3 and 2", out.Received, len(out.Messages))
	}
	if m := out.Messages[1]; !m.Truncated || len(m.Body) != maxTextBytes {
		t.Errorf("long body: truncated=%v len=%d, want true and %d", m.Truncated, len(m.Body), maxTextBytes)
	}
	if q.released != 6 {
		t.Errorf("released %d messages over two calls, want 6", q.released)
	}
}

func TestPeekRejectsBadInput(t *testing.T) {
	cs := connect(t, &fakeQueue{}, Options{})

	tests := map[string]map[string]any{
		"max too large": {"queue_url": "q", "max": maxPeek + 1},
		"negative max":  {"queue_url": "q", "max": -1},
		"bad regex":     {"queue_url": "q", "filter": "[invalid"},
		"missing url":   {},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			if msg := call(t, cs, "dlq_peek", args, &PeekOutput{}); msg == "" {
				t.Error("expected a tool error")
			}
		})
	}
}

func TestTraceReportsPerMessageErrors(t *testing.T) {
	q := &fakeQueue{messages: []queue.Message{
		{ID: "ok", Body: `{"requestId":"r-1"}`},
		{ID: "not-json", Body: `plain text`},
		{ID: "search-fails", Body: `{"requestId":"boom"}`},
	}}
	cs := connect(t, q, Options{})

	var out TraceOutput
	if msg := call(t, cs, "dlq_trace", map[string]any{"queue_url": "q", "log_group": "g"}, &out); msg != "" {
		t.Fatalf("unexpected tool error: %s", msg)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("got %d messages, want 3", len(out.Messages))
	}

	ok, notJSON, fails := out.Messages[0], out.Messages[1], out.Messages[2]
	if ok.TraceValue != "r-1" || len(ok.LogEvents) != 1 || ok.LogEvents[0].Message != "ERROR for r-1" {
		t.Errorf("ok message: %+v", ok)
	}
	if notJSON.Error == "" {
		t.Error("non-JSON body should report an error")
	}
	if fails.Error == "" || fails.TraceValue != "boom" {
		t.Errorf("failed search should report an error: %+v", fails)
	}
}

func TestReplayDryRunByDefault(t *testing.T) {
	q := &fakeQueue{messages: []queue.Message{{ID: "1", Body: "a"}, {ID: "2", Body: "b"}}}
	cs := connect(t, q, Options{AllowReplay: true})

	var out ReplayOutput
	if msg := call(t, cs, "dlq_replay", map[string]any{"source_queue_url": "dlq", "target_queue_url": "main"}, &out); msg != "" {
		t.Fatalf("unexpected tool error: %s", msg)
	}
	if !out.DryRun || len(out.Messages) != 2 {
		t.Errorf("got %+v, want a dry run listing 2 messages", out)
	}
	if len(q.replayed) != 0 || q.deleted != 0 {
		t.Errorf("dry run sent %d and deleted %d messages, want none", len(q.replayed), q.deleted)
	}
	if q.released != 2 {
		t.Errorf("dry run released %d messages, want 2", q.released)
	}
}

func TestReplayRunsWhenDryRunFalse(t *testing.T) {
	q := &fakeQueue{messages: []queue.Message{{ID: "1", Body: "keep"}, {ID: "2", Body: "skip"}}}
	cs := connect(t, q, Options{AllowReplay: true})

	var out ReplayOutput
	msg := call(t, cs, "dlq_replay", map[string]any{
		"source_queue_url": "dlq", "target_queue_url": "main", "filter": "keep", "dry_run": false,
	}, &out)
	if msg != "" {
		t.Fatalf("unexpected tool error: %s", msg)
	}
	if out.DryRun || out.Replayed != 1 || len(q.replayed) != 1 || q.replayed[0].ID != "1" {
		t.Errorf("got %+v, replayed %v; want only message 1 replayed", out, q.replayed)
	}
	if q.released != 1 {
		t.Errorf("released %d skipped messages, want 1", q.released)
	}
}

func TestReplayRejectsSameQueue(t *testing.T) {
	cs := connect(t, &fakeQueue{}, Options{AllowReplay: true})

	msg := call(t, cs, "dlq_replay", map[string]any{"source_queue_url": "q", "target_queue_url": "q"}, &ReplayOutput{})
	if msg == "" {
		t.Error("expected a tool error when source equals target")
	}
}

func TestInvestigatePrompt(t *testing.T) {
	cs := connect(t, &fakeQueue{}, Options{})

	res, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{
		Name: "investigate_dlq", Arguments: map[string]string{"queue_url": "https://sqs/q", "log_group": "/aws/lambda/c"},
	})
	if err != nil {
		t.Fatalf("GetPrompt: %v", err)
	}
	text := res.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(text, "https://sqs/q") || !strings.Contains(text, "/aws/lambda/c") {
		t.Errorf("prompt missing arguments:\n%s", text)
	}
}
