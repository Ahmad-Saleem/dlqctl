package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ahmad-saleem/dlqctl/internal/logs"
	"github.com/ahmad-saleem/dlqctl/internal/mcpserver"
	"github.com/ahmad-saleem/dlqctl/internal/queue"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type fakeQueue struct {
	messages []queue.Message
	written  int
}

func (f *fakeQueue) Inspect(_ context.Context, _ string, max int) ([]queue.Message, error) {
	return f.messages[:min(max, len(f.messages))], nil
}

func (f *fakeQueue) Replay(context.Context, string, string) error {
	f.written++
	return nil
}

func (f *fakeQueue) ReplayWorkerPool(_ context.Context, _, _ string, messages []queue.Message, _ int) []error {
	f.written += len(messages)
	return nil
}

func (f *fakeQueue) Delete(context.Context, string, string) error {
	f.written++
	return nil
}

func (f *fakeQueue) Release(context.Context, string, []queue.Message) error { return nil }

func (f *fakeQueue) Stats(context.Context, string) (queue.Stats, error) {
	return queue.Stats{Visible: len(f.messages)}, nil
}

func (f *fakeQueue) SourceQueues(context.Context, string) ([]string, error) {
	return []string{"https://sqs/orders"}, nil
}

type fakeLogs struct{}

func (fakeLogs) Search(_ context.Context, _, pattern string, _, _ time.Time, _ int) ([]logs.LogEvent, error) {
	return []logs.LogEvent{{Timestamp: time.Unix(0, 0), Message: "ERROR for " + pattern}}, nil
}

// fakeLLM returns scripted responses and records every request.
type fakeLLM struct {
	t         *testing.T
	responses []string
	requests  []anthropic.BetaMessageNewParams
}

func (f *fakeLLM) New(_ context.Context, params anthropic.BetaMessageNewParams, _ ...option.RequestOption) (*anthropic.BetaMessage, error) {
	f.t.Helper()
	f.requests = append(f.requests, params)
	if len(f.responses) == 0 {
		f.t.Fatalf("unexpected request %d", len(f.requests))
	}
	var msg anthropic.BetaMessage
	if err := json.Unmarshal([]byte(f.responses[0]), &msg); err != nil {
		f.t.Fatalf("decode scripted response: %v", err)
	}
	f.responses = f.responses[1:]
	return &msg, nil
}

func message(stopReason string, blocks ...string) string {
	return fmt.Sprintf(`{"id":"msg","type":"message","role":"assistant","model":"test","stop_reason":%q,"content":[%s]}`,
		stopReason, strings.Join(blocks, ","))
}

func toolUse(id, name, input string) string {
	return fmt.Sprintf(`{"type":"tool_use","id":%q,"name":%q,"input":%s}`, id, name, input)
}

func text(s string) string {
	raw, _ := json.Marshal(s)
	return fmt.Sprintf(`{"type":"text","text":%s}`, raw)
}

func run(t *testing.T, q queue.Queue, opts mcpserver.Options, cfg Config, responses ...string) (*fakeLLM, string, error) {
	t.Helper()
	llm := &fakeLLM{t: t, responses: responses}
	if cfg.MaxTurns == 0 {
		cfg.MaxTurns = 5
	}
	cfg.QueueURL, cfg.Model = "https://sqs/dlq", "test"
	report, err := Run(context.Background(), llm, mcpserver.New(q, fakeLogs{}, opts), cfg)
	return llm, report, err
}

func encode(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return string(raw)
}

func TestRunToolRoundTrip(t *testing.T) {
	var progress bytes.Buffer
	q := &fakeQueue{messages: make([]queue.Message, 3)}

	llm, report, err := run(t, q, mcpserver.Options{}, Config{LogGroup: "/ecs/consumer", Progress: &progress},
		message("tool_use", text("Checking the queue."), toolUse("t1", "dlq_stats", `{"queue_url":"https://sqs/dlq"}`)),
		message("end_turn", text("## Root cause\nbad deploy")),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report != "## Root cause\nbad deploy" {
		t.Errorf("report = %q", report)
	}
	if len(llm.requests) != 2 {
		t.Fatalf("got %d requests, want 2", len(llm.requests))
	}

	first := llm.requests[0]
	if system := first.System[0].Text; !strings.Contains(system, "non-interactively") || !strings.Contains(system, "Typical incident workflow") {
		t.Errorf("system prompt should combine the agent role and server instructions:\n%s", system)
	}
	if prompt := encode(t, first.Messages); !strings.Contains(prompt, "https://sqs/dlq") || !strings.Contains(prompt, "/ecs/consumer") {
		t.Errorf("first message should be the investigate_dlq prompt for the queue and log group:\n%s", prompt)
	}

	second := encode(t, llm.requests[1].Messages)
	if !strings.Contains(second, `"tool_use_id":"t1"`) || !strings.Contains(second, `\"visible\":3`) || strings.Contains(second, `"is_error":true`) {
		t.Errorf("second request should carry the dlq_stats result:\n%s", second)
	}
	if !strings.Contains(progress.String(), "→ dlq_stats") {
		t.Errorf("progress = %q, want a dlq_stats line", progress.String())
	}
}

func TestRunOffersOnlyReadOnlyTools(t *testing.T) {
	llm, _, err := run(t, &fakeQueue{}, mcpserver.Options{AllowReplay: true}, Config{},
		message("end_turn", text("done")),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	var names []string
	for _, tool := range llm.requests[0].Tools {
		names = append(names, tool.OfTool.Name)
	}
	slices.Sort(names)
	if want := []string{"dlq_peek", "dlq_stats", "dlq_trace", "logs_search"}; !slices.Equal(names, want) {
		t.Errorf("tools = %v, want %v", names, want)
	}
}

func TestRunReturnsToolErrorsToModel(t *testing.T) {
	q := &fakeQueue{messages: []queue.Message{{ID: "1", Body: "a"}}}

	llm, report, err := run(t, q, mcpserver.Options{AllowReplay: true}, Config{},
		message("tool_use",
			toolUse("t1", "dlq_replay", `{"source_queue_url":"https://sqs/dlq","target_queue_url":"https://sqs/orders","dry_run":false}`),
			toolUse("t2", "dlq_peek", `{"queue_url":"https://sqs/dlq","max":1000}`),
		),
		message("end_turn", text("done")),
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report != "done" {
		t.Errorf("report = %q, want done", report)
	}
	if q.written != 0 {
		t.Errorf("agent wrote %d messages, want none", q.written)
	}

	msgs := llm.requests[1].Messages
	results := encode(t, msgs[len(msgs)-1])
	if n := strings.Count(results, `"is_error":true`); n != 2 {
		t.Errorf("got %d error results in the last message, want 2:\n%s", n, results)
	}
	if !strings.Contains(results, "unknown tool") || !strings.Contains(results, "between 1 and 100") {
		t.Errorf("results should explain both errors:\n%s", results)
	}
}

func TestRunStopsAtMaxTurns(t *testing.T) {
	stats := message("tool_use", toolUse("t", "dlq_stats", `{"queue_url":"https://sqs/dlq"}`))

	llm, _, err := run(t, &fakeQueue{}, mcpserver.Options{}, Config{MaxTurns: 2}, stats, stats)
	if err == nil || !strings.Contains(err.Error(), "no report after 2 turns") {
		t.Errorf("err = %v, want a max-turns error", err)
	}
	if len(llm.requests) != 2 {
		t.Errorf("got %d requests, want 2", len(llm.requests))
	}
}

func TestRunFailsOnUnusableResponses(t *testing.T) {
	tests := map[string]struct {
		response string
		want     string
	}{
		"refusal": {
			response: `{"id":"msg","type":"message","role":"assistant","model":"test","stop_reason":"refusal","content":[],` +
				`"stop_details":{"type":"refusal","category":"cyber","explanation":"declined"}}`,
			want: `category "cyber"`,
		},
		"max tokens":            {response: message("max_tokens", text("partial")), want: "output limit"},
		"empty report":          {response: message("end_turn"), want: "without writing a report"},
		"unknown stop reason":   {response: message("pause_turn"), want: "unexpected stop reason"},
		"tool use with no call": {response: message("tool_use", text("hmm")), want: "without calling a tool"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := run(t, &fakeQueue{}, mcpserver.Options{}, Config{}, tt.response)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
