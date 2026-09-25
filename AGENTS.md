# AGENTS.md

Guidance for AI coding agents working in this repository. User-facing docs (install, full flag tables) live in [README.md](README.md); this file covers what you need to change the code safely.

## Project

`dlqctl` is a Go CLI for operating on AWS SQS dead-letter queues: inspect stuck messages, correlate them with CloudWatch Logs, replay them to a target queue, and export them to JSON/CSV. `dlqctl mcp` also runs it as an MCP server so AI agents can investigate DLQs during incidents. Kafka support is planned but **not implemented** — don't assume any Kafka code exists.

- Module: `github.com/ahmad-saleem/dlqctl`
- Go: `1.26.1` (from `go.mod`; CI reads it via `go-version-file`)
- Dependencies: `spf13/cobra` (CLI), `aws-sdk-go-v2` (`config`, `service/sqs`, `service/cloudwatchlogs`), `modelcontextprotocol/go-sdk` (MCP server). No viper, no logging library, no test framework beyond `testing`.
- AWS auth: standard default credential chain via `config.LoadDefaultConfig`. Region comes only from the global `--region/-R` flag (default `eu-west-1`).

## Commands

```sh
go build ./...          # build
go vet ./...            # static checks (CI runs this)
go test ./...           # unit tests — no AWS credentials or network needed
go run . <subcommand>   # run locally, e.g. go run . inspect --help
goreleaser check        # validate .goreleaser.yaml after editing it
```

Before finishing any change, run `go build ./... && go vet ./... && go test ./...` — this is exactly what CI (`.github/workflows/ci.yml`) runs. Run `gofmt -l .` and fix anything it lists.

## Layout

```
main.go                     entry point → cmd.Execute(version), exits 1 on error; version set by GoReleaser ldflags
cmd/
  root.go                   rootCmd + persistent --region/-R flag
  helpers.go                newContext() (Ctrl+C-cancellable ctx), newQueueClient()
  inspect.go                inspect: receive + print, --filter, --follow, --trace (CloudWatch correlation)
  replay.go                 replay: receive from source, filter, send to target, delete from source
  export.go                 export: receive + write JSON/CSV file
  logs.go                   logs: CloudWatch FilterLogEvents search
  mcp.go                    mcp: stdio MCP server; --allow-replay exposes dlq_replay
  validate_test.go          unit tests for flag validation
internal/
  queue/queue.go            Queue interface (what cmd/ depends on)
  queue/sqs.go              *Client (SQS impl), Message, Release, Stats, SourceQueues, MatchFilter, WriteJSON, WriteCSV
  logs/client.go            Searcher interface; *Client for CloudWatch Logs, Search() with pagination
  mcpserver/server.go       New(): registers MCP tools, server instructions, investigate_dlq prompt
  mcpserver/tools.go        tool handlers, typed input/output structs, size limits
  extract/extract.go        Field(): pull a correlation value out of a JSON body
  timeparse/timeparse.go    ParseSince(): Go durations + "d" suffix; Resolve(): since or start/end range
```

Data flow: `cmd/*` parses flags → builds a client with the region flag → calls `internal/*` → prints to stdout. The `internal/` packages do not print; all user output happens in `cmd/`. In MCP mode, `internal/mcpserver` returns structured results instead, and **nothing may write to stdout**, because stdout carries the MCP protocol.

## Code conventions

- **Adding a command**: create `cmd/<name>.go` with a `var <name>Cmd = &cobra.Command{..., RunE: run<Name>}`, a `run<Name>(cmd, args) error` function, and an `init()` that calls `rootCmd.AddCommand` and registers flags. Use `RunE` and return errors; don't call `os.Exit` in commands.
- **Flags**: read them with `cmd.Flags().GetX("name")` inside `RunE` (errors are ignored with `_`, the existing style). Don't bind flags to package-level variables. Mark required flags with `MarkFlagRequired`. Use kebab-case for new flag names (`--log-group`). `replay`'s camelCase `--sourceQueueURL`/`--targetQueueURL` is legacy — don't copy it, and don't rename it without asking, because that would break users.
- **Region**: read `cmd.Flags().GetString("region")`; it's inherited from root. Don't add per-command region flags.
- **Context**: every command does `ctx, stop := newContext(); defer stop()` so Ctrl+C cancels AWS calls. Pass `ctx` all the way through.
- **Validation**: validate every flag value *before* creating AWS clients or receiving messages (see `inspectOptions.validate()`, `validateReplay()` and `timeparse.Resolve()`, tested in `cmd/validate_test.go`). A late failure leaves already-received messages invisible until the visibility timeout expires. Put validation logic in pure functions so it can be unit-tested.
- **Exit codes**: `main.go` exits `1` when `cmd.Execute()` returns an error (cobra prints the error itself). Commands signal failure by returning an error from `RunE`.
- **Errors**: wrap with `fmt.Errorf("...: %w", err)`. Messages are lowercase with no trailing punctuation. Per-item failures that shouldn't abort the run (e.g. `--trace` per message) are printed inline and skipped.
- **Output**: plain `fmt.Printf` to stdout; failure details go to stderr (`fmt.Fprintf(os.Stderr, ...)`). Timestamps are formatted `time.RFC3339`. Trim CloudWatch messages with `strings.TrimRight(msg, "\n")` before printing, because some have trailing newlines and some don't.
- **Interfaces**: `cmd/` depends on `queue.Queue`, not `*queue.Client`. If you change a `Client` method signature, update the interface too (the compiler catches this at `newQueueClient`'s return). `internal/mcpserver` depends on `queue.Queue` and `logs.Searcher`, and its tests use fakes of both.
- **Tests**: live next to the code (`*_test.go`, same package). Use table-driven tests (see `internal/queue/sqs_test.go`). Tests must not touch AWS or the network. Test pure logic, and put interfaces or fakes in front of anything that talks to AWS.

## MCP server (`internal/mcpserver`)

- Tools: `dlq_stats`, `dlq_peek`, `dlq_trace`, `logs_search` (all `readOnlyHint`), and `dlq_replay` (`destructiveHint`, registered **only** when `Options.AllowReplay` is set). `dlq_replay` defaults to `dry_run: true`. Keep all three safeguards, because they are how a human stays in the loop.
- Tool inputs and outputs are typed structs. The SDK generates JSON schemas from them: fields without `omitempty` are required, and `jsonschema:"..."` tags become the descriptions the agent reads, so write them for a model. A handler error becomes a tool error the agent can see and fix. Use it for bad input, and report per-item failures inside the output instead (see `dlq_trace`).
- Reading tools call `queue.Release` after receiving, so peeking doesn't hide messages from a later replay. Receipt handles are never returned to the agent.
- Keep results small: `max` is capped per tool, and bodies and log lines are truncated at `maxTextBytes`. Initialize output slices to empty (not nil), so they encode as `[]` rather than `null`.
- When you add a CLI capability, decide whether agents need it too. If they do, add a tool with a test in `server_test.go` (in-memory transport + fakes).

## AWS/SQS facts that matter

- `ReceiveMessage` rejects `MaxNumberOfMessages > 10`. `queue.Client.Inspect` batches in 10s: it long-polls 20s on the first call, then short-polls 1s, and stops on the first empty response. Any new receive path must batch the same way.
- Receiving is **not** peeking: received messages become invisible until the queue's visibility timeout expires. `inspect` and `export` never delete. `replay` deletes from the source **only after** a successful send to the target (`replayAndDelete`). Keep that ordering — reversing it loses messages.
- `queue.MatchFilter("", body)` returns `true` (empty filter = match all). `inspect` relies on this by calling it unconditionally.
- `FilterLogEvents` accepts `Limit` 1–10000 per page. `logs.Client.Search` paginates via `NextToken` until it has `max` events.
- `extract.Field` with an empty field name falls back to `requestId`, then `correlationId`, then `traceId`. The body must be a JSON object.
- IAM needed: `sqs:ReceiveMessage`, `sqs:SendMessage`, `sqs:DeleteMessage`, `logs:FilterLogEvents`; the MCP server also needs `sqs:GetQueueAttributes`, `sqs:ListDeadLetterSourceQueues` and `sqs:ChangeMessageVisibility` (for `Release`).

## Known limitations

- **Long replays can outlive the visibility timeout.** `replay` receives all messages up front, then sends and deletes them. If that takes longer than the source queue's visibility timeout, messages can be redelivered and their receipt handles go stale, so deletes fail. A proper fix would receive, replay and delete in batches.
- `internal/logs` and the AWS-calling paths in `cmd/` have no tests. `cmd/validate_test.go` covers only flag validation.

## Git & commits

- Conventional commits: `feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `build:`, `ci:`, `chore:`, with an optional scope, e.g. `feat(inspect): ...`. The subject is imperative with no trailing period.
- The release changelog excludes `docs:`, `test:` and `chore:`, so pick the type that reflects user impact.
- Never commit build outputs: `dlqctl` (binary), `dist/` and `tmp/` are gitignored. Never commit `.env` files or AWS credentials.

## Release & distribution — handle with care

- Pushing a `v*` tag triggers `.github/workflows/release.yml` → GoReleaser **v2** (`.goreleaser.yaml`). Don't create or push tags unless explicitly asked.
- GoReleaser builds `CGO_ENABLED=0` binaries for linux/darwin/windows × amd64/arm64, tar.gz archives (zip on Windows), `.deb`/`.rpm` via `nfpms`, and `checksums.txt`, then publishes a Homebrew **cask** to `ahmad-saleem/homebrew-dlqctl` using the `HOMEBREW_TAP_TOKEN` secret.
- Keep these as they are unless the task is specifically about them:
  - Use v2 syntax (`version: 2`, `archives.formats: [...]`, `homebrew_casks`), not the deprecated `brews`/`format`.
  - nfpm `file_name_template` has **no version** on purpose, so the README's `releases/latest/download/dlqctl_linux_amd64.deb` URLs keep working.
  - The cask's `post.install` hook strips `com.apple.quarantine` because binaries aren't notarized. Removing it makes Gatekeeper block the binary.
- If you change flags, defaults, install steps or behavior, update the README tables and the matching part of this file.
