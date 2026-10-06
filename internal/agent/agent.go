// Package agent runs an autonomous, read-only DLQ investigation with Claude.
//
// It drives the dlqctl MCP server in process: the server's read-only tools
// become Claude tools, its instructions go into the system prompt, and its
// investigate_dlq prompt starts the conversation. The CLI and MCP modes
// therefore share one implementation of every tool.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Messages is the part of the Anthropic client the agent uses.
// *anthropic.BetaMessageService satisfies it; tests use a fake.
type Messages interface {
	New(ctx context.Context, params anthropic.BetaMessageNewParams, opts ...option.RequestOption) (*anthropic.BetaMessage, error)
}

type Config struct {
	QueueURL string
	LogGroup string
	Model    string
	MaxTurns int
	// Progress receives one line per tool call.
	Progress io.Writer
}

const maxTokens = 16000

const role = `You are dlqctl's incident investigator. You find out why messages are landing in an AWS SQS dead-letter queue and write a root-cause report for the on-call engineer.

You run non-interactively from the command line, so nobody can answer questions during the run. If something is missing, such as the consumer's log group, investigate with what you have and say in the report what would narrow it down.

Your tools are read-only: you cannot replay or delete messages. Where the runbook says to propose a dlq_replay dry run, recommend CLI commands instead: "dlqctl inspect --queue <dlq> --filter <regex>" to preview, then "dlqctl replay --sourceQueueURL <dlq> --targetQueueURL <source queue> --max <count> [--filter <regex>]". replay's --max defaults to 10, so set it to the number of messages to move.

Your final message is printed as the report, in Markdown. Lead with the conclusion.`

// Run investigates cfg.QueueURL using the tools of server and returns the
// final report.
func Run(ctx context.Context, llm Messages, server *mcp.Server, cfg Config) (string, error) {
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverT, nil); err != nil {
		return "", fmt.Errorf("start tool server: %w", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "dlqctl-investigate"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		return "", fmt.Errorf("connect to tool server: %w", err)
	}
	defer cs.Close()

	tools, allowed, err := readOnlyTools(ctx, cs)
	if err != nil {
		return "", err
	}
	prompt, err := investigatePrompt(ctx, cs, cfg)
	if err != nil {
		return "", err
	}

	params := anthropic.BetaMessageNewParams{
		Model:        cfg.Model,
		MaxTokens:    maxTokens,
		System:       []anthropic.BetaTextBlockParam{{Text: role + "\n\n" + cs.InitializeResult().Instructions}},
		Tools:        tools,
		Thinking:     anthropic.BetaThinkingConfigParamUnion{OfAdaptive: &anthropic.BetaThinkingConfigAdaptiveParam{}},
		CacheControl: anthropic.NewBetaCacheControlEphemeralParam(),
		// Re-serve a policy refusal with a fallback model instead of failing the run.
		Fallbacks: anthropic.BetaFallbacksParamOfDefault(),
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
	}

	for range cfg.MaxTurns {
		resp, err := llm.New(ctx, params)
		if err != nil {
			return "", apiError(err)
		}
		params.Messages = append(params.Messages, resp.ToParam())

		switch resp.StopReason {
		case anthropic.BetaStopReasonEndTurn:
			return report(resp)
		case anthropic.BetaStopReasonToolUse:
			results := runTools(ctx, cs, allowed, resp, cfg.Progress)
			if len(results) == 0 {
				return "", errors.New("model stopped for tool use without calling a tool")
			}
			params.Messages = append(params.Messages, anthropic.NewBetaUserMessage(results...))
		case anthropic.BetaStopReasonRefusal:
			return "", fmt.Errorf("model declined to continue (category %q): %s", resp.StopDetails.Category, resp.StopDetails.Explanation)
		case anthropic.BetaStopReasonMaxTokens:
			return "", fmt.Errorf("model hit the %d-token output limit before finishing the report", maxTokens)
		default:
			return "", fmt.Errorf("unexpected stop reason %q", resp.StopReason)
		}
	}
	return "", fmt.Errorf("no report after %d turns; raise --max-turns", cfg.MaxTurns)
}

// readOnlyTools converts the server's read-only tools to Claude tools. The
// returned set is checked before every call, so the agent can't reach a
// destructive tool even if the server was started with one.
func readOnlyTools(ctx context.Context, cs *mcp.ClientSession) ([]anthropic.BetaToolUnionParam, map[string]bool, error) {
	var tools []anthropic.BetaToolUnionParam
	allowed := map[string]bool{}
	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return nil, nil, fmt.Errorf("list tools: %w", err)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			continue
		}
		schema, err := inputSchema(tool.InputSchema)
		if err != nil {
			return nil, nil, fmt.Errorf("tool %s: %w", tool.Name, err)
		}
		tools = append(tools, anthropic.BetaToolUnionParam{OfTool: &anthropic.BetaToolParam{
			Name:        tool.Name,
			Description: anthropic.String(tool.Description),
			InputSchema: schema,
		}})
		allowed[tool.Name] = true
	}
	if len(tools) == 0 {
		return nil, nil, errors.New("tool server offers no read-only tools")
	}
	return tools, allowed, nil
}

func inputSchema(s any) (anthropic.BetaToolInputSchemaParam, error) {
	raw, err := json.Marshal(s)
	if err != nil {
		return anthropic.BetaToolInputSchemaParam{}, fmt.Errorf("encode input schema: %w", err)
	}
	var parsed struct {
		Properties map[string]any `json:"properties"`
		Required   []string       `json:"required"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return anthropic.BetaToolInputSchemaParam{}, fmt.Errorf("decode input schema: %w", err)
	}
	return anthropic.BetaToolInputSchemaParam{Properties: parsed.Properties, Required: parsed.Required}, nil
}

func investigatePrompt(ctx context.Context, cs *mcp.ClientSession, cfg Config) (string, error) {
	args := map[string]string{"queue_url": cfg.QueueURL}
	if cfg.LogGroup != "" {
		args["log_group"] = cfg.LogGroup
	}
	res, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "investigate_dlq", Arguments: args})
	if err != nil {
		return "", fmt.Errorf("get investigate_dlq prompt: %w", err)
	}
	var parts []string
	for _, m := range res.Messages {
		if t, ok := m.Content.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	if len(parts) == 0 {
		return "", errors.New("investigate_dlq prompt is empty")
	}
	return strings.Join(parts, "\n\n"), nil
}

// runTools executes the turn's tool calls one at a time: concurrent receives
// on the same queue would hide messages from each other.
func runTools(ctx context.Context, cs *mcp.ClientSession, allowed map[string]bool, resp *anthropic.BetaMessage, progress io.Writer) []anthropic.BetaContentBlockParamUnion {
	var results []anthropic.BetaContentBlockParamUnion
	for _, block := range resp.Content {
		use, ok := block.AsAny().(anthropic.BetaToolUseBlock)
		if !ok {
			continue
		}
		input := use.JSON.Input.Raw()
		if input == "" {
			input = "{}"
		}
		if progress != nil {
			fmt.Fprintf(progress, "→ %s %s\n", use.Name, input)
		}

		if !allowed[use.Name] {
			results = append(results, anthropic.NewBetaToolResultBlock(use.ID, fmt.Sprintf("unknown tool %q", use.Name), true))
			continue
		}
		text, isError := callTool(ctx, cs, use.Name, input)
		results = append(results, anthropic.NewBetaToolResultBlock(use.ID, text, isError))
	}
	return results
}

func callTool(ctx context.Context, cs *mcp.ClientSession, name, input string) (string, bool) {
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: json.RawMessage(input)})
	if err != nil {
		return err.Error(), true
	}
	if !res.IsError && res.StructuredContent != nil {
		if raw, err := json.Marshal(res.StructuredContent); err == nil {
			return string(raw), false
		}
	}
	var parts []string
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, t.Text)
		}
	}
	return strings.Join(parts, "\n"), res.IsError
}

func report(resp *anthropic.BetaMessage) (string, error) {
	var parts []string
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.BetaTextBlock); ok && strings.TrimSpace(t.Text) != "" {
			parts = append(parts, strings.TrimSpace(t.Text))
		}
	}
	if len(parts) == 0 {
		return "", errors.New("model finished without writing a report")
	}
	return strings.Join(parts, "\n\n"), nil
}

func apiError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("claude api authentication failed; set ANTHROPIC_API_KEY or run ant auth login: %w", err)
	}
	return fmt.Errorf("claude api: %w", err)
}
