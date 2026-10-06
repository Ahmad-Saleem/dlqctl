<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark.svg">
    <img src="assets/logo.svg" alt="dlqctl" width="380">
  </picture>
</p>

A CLI for operating on AWS SQS dead-letter queues: inspect stuck messages, correlate them with CloudWatch Logs, replay them back to their source queue, export them to files, and let Claude investigate why they failed.

> Currently supports **SQS** (Kafka support is planned). All commands use the standard AWS credential chain (environment variables, shared config/credentials files, SSO, instance roles).

## Install

```sh
# Homebrew (macOS)
brew install --cask ahmad-saleem/dlqctl/dlqctl

# Debian/Ubuntu (also available: arm64, .rpm for Fedora/RHEL)
curl -LO https://github.com/ahmad-saleem/dlqctl/releases/latest/download/dlqctl_linux_amd64.deb
sudo apt install ./dlqctl_linux_amd64.deb

# Fedora/RHEL
sudo dnf install https://github.com/ahmad-saleem/dlqctl/releases/latest/download/dlqctl_linux_amd64.rpm

# From source (any platform, requires Go >= 1.26)
go install github.com/ahmad-saleem/dlqctl@latest
```

Prebuilt binaries and packages for Linux, macOS, and Windows (amd64/arm64) are on the [releases page](https://github.com/ahmad-saleem/dlqctl/releases).

## Quick start

```sh
# Peek at messages in a DLQ
dlqctl inspect --queue https://sqs.eu-west-1.amazonaws.com/123456789012/orders-dlq

# Inspect and pull matching CloudWatch logs for each message
dlqctl inspect --queue <dlq-url> --trace --log-group /aws/lambda/orders-consumer --since 2h

# Replay messages from the DLQ back to the main queue with 5 workers
dlqctl replay -S <dlq-url> -T <main-queue-url> -M 50 -W 5

# Export up to 100 messages to a JSON file
dlqctl export -q <dlq-url> -o dlq-dump.json

# Search a CloudWatch Log Group directly
dlqctl logs --log-group /aws/lambda/orders-consumer --pattern "ERROR" --since 30m

# Let Claude find the root cause and write a report (needs ANTHROPIC_API_KEY)
dlqctl investigate -q <dlq-url> --log-group /aws/lambda/orders-consumer > report.md
```

## Commands

### Global flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--region` | `-R` | `eu-west-1` | AWS region for all API calls |

### `dlqctl inspect`

Reads messages from a queue and prints ID and body. Messages are **not deleted** — they return to the queue after the visibility timeout expires.

| Flag | Required | Default | Description |
|---|---|---|---|
| `--queue` | yes | — | SQS queue URL |
| `--max` | no | `10` | Max messages to fetch (batched; may exceed 10) |
| `--follow` | no | `false` | Keep polling after draining (Ctrl+C to stop) |
| `--filter` | no | — | Regex applied to message bodies; non-matching messages are skipped |
| `--trace` | no | `false` | For each message, search CloudWatch Logs for a correlation value |
| `--log-group` | with `--trace` | — | CloudWatch Log Group to search |
| `--trace-field` | no | auto | JSON field in the body to use as the search value. Without it, tries `requestId`, `correlationId`, `traceId` in order |
| `--since` | no | `1h` | Log search window, e.g. `30m`, `2h`, `1d` |

Trace requires message bodies to be JSON objects. Per-message trace failures (non-JSON body, missing field, log search error) are printed and skipped; they do not abort the run.

### `dlqctl replay`

Receives messages from a source queue (the DLQ), optionally filters them, sends them to a target queue, and **deletes each message from the source only after a successful send**. Failed messages stay in the source queue and are reported on stderr; the command exits non-zero if any message fails.

| Flag | Short | Required | Default | Description |
|---|---|---|---|---|
| `--sourceQueueURL` | `-S` | yes | — | Queue to read from (DLQ) |
| `--targetQueueURL` | `-T` | yes | — | Queue to send to |
| `--max` | `-M` | no | `10` | Max messages to replay |
| `--filter` | `-F` | no | — | Regex; only matching bodies are replayed |
| `--workers` | `-W` | no | `1` | Concurrent workers (must be ≥ 1) |

### `dlqctl export`

Fetches messages and writes them to a file. Like `inspect`, it does not delete messages.

| Flag | Short | Required | Default | Description |
|---|---|---|---|---|
| `--queue` | `-q` | yes | — | SQS queue URL |
| `--format` | `-f` | no | `json` | `json` or `csv` |
| `--output` | `-o` | no | `export.json` | Output file path |
| `--max` | `-m` | no | `100` | Max messages to export |

JSON output is an indented array of `{ID, Body, ReceiptHandle}` objects. CSV has header `id,body`.

### `dlqctl logs`

Searches a CloudWatch Log Group with a [filter pattern](https://docs.aws.amazon.com/AmazonCloudWatch/latest/logs/FilterAndPatternSyntax.html) and prints matching events with RFC 3339 timestamps.

| Flag | Required | Default | Description |
|---|---|---|---|
| `--log-group` | yes | — | Log Group name |
| `--pattern` | yes | — | CloudWatch filter pattern |
| `--since` | no | `1h` | Relative window, e.g. `30m`, `2h`, `1d`. Ignored when `--start`/`--end` are set |
| `--start` / `--end` | together | — | Absolute range in ISO 8601 (e.g. `2026-07-01T00:00:00Z`); `--end` must be after `--start` |
| `--max` | no | `50` | Max events to return |

### `dlqctl investigate`

Runs an AI agent that investigates a DLQ by itself and prints a Markdown root-cause report: what is failing, since when, how many messages are affected, the evidence (log lines, message IDs), the recommended fix, and the `dlqctl` commands to replay once it's fixed. It uses the Claude API and the same tools as [`dlqctl mcp`](#use-with-ai-agents-mcp), so you don't need an MCP client.

| Flag | Short | Required | Default | Description |
|---|---|---|---|---|
| `--queue` | `-q` | yes | — | SQS URL of the dead-letter queue |
| `--log-group` | | no | — | CloudWatch Log Group of the consumer. Optional, but without it the agent can only reason from message bodies |
| `--model` | | no | `claude-opus-5` | Claude model; must support adaptive thinking |
| `--max-turns` | | no | `20` | Max model turns before giving up; bounds API cost |

- **Read-only.** The agent gets only the read-only tools. It releases every message right after reading it and never replays or deletes, so it needs only the [read-only IAM policy](#use-with-ai-agents-mcp).
- **Claude API access.** It needs `ANTHROPIC_API_KEY` (or a profile from `ant auth login`), and each run costs API usage.
- **Output.** Each tool call is printed to stderr as it runs; the report goes to stdout, so `> report.md` saves just the report.

### `dlqctl mcp`

Runs an [MCP](https://modelcontextprotocol.io) server over stdio so AI agents can investigate DLQs during incidents. See [Use with AI agents](#use-with-ai-agents-mcp).

| Flag | Required | Default | Description |
|---|---|---|---|
| `--allow-replay` | no | `false` | Expose the `dlq_replay` tool |

## Use with AI agents (MCP)

`dlqctl mcp` lets an AI agent (Claude Code, Claude Desktop, Cursor, or any MCP client) triage a dead-letter queue: check its depth, sample messages, pull matching CloudWatch logs, and report a likely root cause. By default it can only read. No MCP client at hand? [`dlqctl investigate`](#dlqctl-investigate) runs the same investigation from the command line.

**1. Give it AWS credentials.** It uses the same credential chain as the CLI (`AWS_PROFILE`, SSO, and so on). Use a role limited to what the agent needs:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "DlqctlRead",
      "Effect": "Allow",
      "Action": [
        "sqs:GetQueueAttributes",
        "sqs:ListDeadLetterSourceQueues",
        "sqs:ReceiveMessage",
        "sqs:ChangeMessageVisibility",
        "logs:FilterLogEvents"
      ],
      "Resource": "*"
    }
  ]
}
```

Only if you enable replay, also allow `sqs:SendMessage` on the target queues and `sqs:DeleteMessage` on the DLQs. Scope `Resource` to your queue and log group ARNs where you can.

**2. Register the server with your agent.**

```sh
# Claude Code
claude mcp add dlqctl --env AWS_PROFILE=incident-readonly -- dlqctl mcp --region eu-west-1
```

Claude Desktop (`claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "dlqctl": {
      "command": "dlqctl",
      "args": ["mcp", "--region", "eu-west-1"],
      "env": { "AWS_PROFILE": "incident-readonly" }
    }
  }
}
```

**3. Ask it to investigate**, e.g. *"orders-dlq is filling up, find out why"*, or use the built-in `investigate_dlq` prompt (in Claude Code: `/mcp__dlqctl__investigate_dlq`).

| Tool | What it does |
|---|---|
| `dlq_stats` | Message counts, visibility timeout, retention, and the source queues that feed the DLQ |
| `dlq_peek` | Read up to 100 messages (optional regex filter) |
| `dlq_trace` | For each message, extract its correlation ID and search a Log Group for it |
| `logs_search` | CloudWatch filter-pattern search over a relative or absolute time range |
| `dlq_replay` | Only with `--allow-replay`. Send messages to a target queue, then delete them from the DLQ. Defaults to a dry run |

Reading tools make messages visible again right after reading them, so an investigation never hides messages from a later replay. Message bodies and log lines are truncated to 4 KB to keep the agent's context small, and receipt handles are never exposed.

**Replay safeguards:** `dlq_replay` only exists if the server was started with `--allow-replay`. It's marked destructive, so MCP clients ask you to approve every call. It previews by default (`dry_run: true`), so an actual replay is always a separate call with `dry_run: false` that you approve.

## Behavior notes

- **Time windows**: `--since` accepts Go durations (`30m`, `2h`, `90s`) plus a `d` suffix for days (`1d` = 24h).
- **Message fetching**: SQS caps a single receive at 10 messages; dlqctl batches receives until `--max` is reached or the queue stops returning messages. The first receive long-polls for up to 20s.
- **Visibility**: `inspect` and `export` leave messages in flight until the queue's visibility timeout expires; re-running immediately may return fewer messages.
- **Exit codes**: `0` on success (including "no messages found"); non-zero on invalid flags, AWS errors, or any failed replay.
- **Required AWS permissions**: `sqs:ReceiveMessage` (inspect/export/replay), `sqs:SendMessage` + `sqs:DeleteMessage` (replay), `logs:FilterLogEvents` (logs, inspect --trace). For `mcp` and `investigate`, see the [policy above](#use-with-ai-agents-mcp).

## Development

```sh
go build ./...   # build
go vet ./...     # static checks
go test ./...    # unit tests (no AWS credentials needed)
```

### Repository layout

```
main.go                    # entry point; delegates to cmd.Execute()
cmd/                       # cobra commands: root, inspect, replay, export, logs, investigate, mcp, helpers
internal/queue/            # SQS client (receive/send/delete, worker pool), filtering, JSON/CSV writers
internal/logs/             # CloudWatch Logs FilterLogEvents client with pagination
internal/extract/          # JSON field extraction for --trace
internal/timeparse/        # --since parsing (Go durations + "d" suffix) and start/end ranges
internal/mcpserver/        # MCP tools, prompt, and server instructions for `dlqctl mcp`
internal/agent/            # Claude tool loop behind `dlqctl investigate`; drives mcpserver in process
```

### CI & releases

- **CI** (`.github/workflows/ci.yml`): runs `go vet`, `go test`, `go build` on pushes to `main` and on pull requests. Go version comes from `go.mod` (`go-version-file`).
- **Releases** (`.github/workflows/release.yml`): pushing a tag matching `v*` runs GoReleaser v2 (`.goreleaser.yaml`), which builds all platform binaries, `.deb`/`.rpm` packages (`nfpms`, version-less filenames so `latest/download` URLs stay stable), creates a GitHub release with checksums, and publishes a Homebrew cask to the tap `ahmad-saleem/homebrew-dlqctl`. Requires the `HOMEBREW_TAP_TOKEN` repository secret.
- **Commit convention**: conventional commits (`feat:`, `fix:`, `chore:`, `ci:`, `build:`, `docs:`, `test:`). The release changelog excludes `docs:`, `test:`, and `chore:` commits.

## Notes for AI agents

Facts that are easy to get wrong when modifying this codebase:

- Module path is `github.com/ahmad-saleem/dlqctl`. Cobra commands register themselves in `init()` via `rootCmd.AddCommand`; adding a command means adding a file in `cmd/` with its own `init()`.
- The `--region` flag is defined once as a persistent flag on the root command ([cmd/root.go](cmd/root.go)) and read by subcommands via `cmd.Flags().GetString("region")`. Do not add per-command region flags, and do not read configuration through viper — it is not used.
- `queue.Queue` ([internal/queue/queue.go](internal/queue/queue.go)) is the interface commands depend on; keep `*queue.Client` conforming to it when changing signatures.
- `queue.MatchFilter` treats an empty filter as match-all. Preserve this — `inspect` calls it unconditionally.
- SQS `ReceiveMessage` rejects `MaxNumberOfMessages > 10`; any new fetch path must batch (see `Client.Inspect`).
- CloudWatch log messages may lack trailing newlines; output paths trim and add `\n` explicitly.
- Verification before committing: `go build ./... && go vet ./... && go test ./...`. Tests are pure unit tests and must not require AWS credentials or network access.
- `.goreleaser.yaml` is GoReleaser **v2** syntax (`version: 2`, `archives.formats`). Validate config changes with `goreleaser check`. Homebrew distribution uses `homebrew_casks` (casks are macOS-only; Linux users install via `go install` or release binaries). The cask has a post-install hook that strips the macOS quarantine attribute because release binaries are not notarized — keep it when editing the cask config.
- Do not commit binaries: `dlqctl`, `tmp/`, and `dist/` are gitignored.

## License

Open source under the [MIT License](LICENSE) — free for any kind of use, personal or commercial.
