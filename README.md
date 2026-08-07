# a2a-cli

![status: alpha](https://img.shields.io/badge/status-alpha-orange)
![license: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)

A command-line client for talking to [A2A protocol](https://a2a-protocol.org) agents from your
terminal: send a message to an agent, wait for its answer, retrieve or cancel tasks, and get the
result as readable text or machine-readable JSON. Built on the official
[`a2a-go` v2 SDK](https://github.com/a2aproject/a2a-go).

> **Status: alpha.** This is an early build of a **Tier-1** A2A client. The full Tier-1 command
> set is merged — **`send`**, **`discover`**, **`get`**, **`cancel`**, and **`session`** — over the
> **HTTP+JSON** and **JSON-RPC** transports, including caller-supplied authentication and
> card-gated SSE streaming (`send --stream`). A bundled agent skill and self-installable plugin ship
> in the repo ([`skill/SKILL.md`](skill/SKILL.md), [`plugin.json`](plugin.json)). A Tier-1 conformance
> report is published — see [`docs/compliance/tier-1-conformance.md`](docs/compliance/tier-1-conformance.md);
> gRPC is still to come. The `--output json` shape and the exit-code scheme
> are a stable contract; everything else may change without notice while the
> [a2a-cli spec](https://github.com/a2aproject/a2a-cli) is still a Draft.

## Contents

- [Installation](#installation)
- [Usage](#usage)
  - [Retrieving and cancelling tasks](#retrieving-and-cancelling-tasks)
  - [Streaming](#streaming)
  - [Authentication](#authentication)
  - [Commands](#commands)
  - [Global flags](#global-flags)
  - [Behavior](#behavior)
  - [Session state](#session-state)
- [Documentation](#documentation)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Installation

Not published to any package registry yet — during the alpha, build from source. Requires
**Go 1.26+**:

```bash
git clone https://github.com/ghchinoy/a2a-cli.git
cd a2a-cli
go build -o a2a-cli ./cmd/a2a-cli
```

This produces an `a2a-cli` binary in the current directory. Move it onto your `PATH` (e.g.
`sudo mv a2a-cli /usr/local/bin/`) to run it from anywhere. The examples below assume it is on your
`PATH`.

## Usage

Point `a2a-cli` at a running A2A agent with `-u` and send it a message. By default the command
**blocks** until the agent finishes, then prints the result:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001
```

```text
State:     TASK_STATE_COMPLETED
Task ID:   (none)
Context:   (none)
Message:   Hello from REST server!
```

Add `-o json` (or its shorthand `-n`) for machine-readable output on stdout — the normalized
envelope defined by the spec. Diagnostics and warnings always go to stderr, so stdout stays clean
for piping into `jq`:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 -o json
```

```json
{
  "taskId": null,
  "contextId": null,
  "state": "TASK_STATE_COMPLETED",
  "message": {
    "role": "ROLE_AGENT",
    "parts": [
      {
        "text": "Hello from REST server!"
      }
    ]
  }
}
```

`taskId` and `contextId` are `null` here because this agent replied with a direct **message** rather
than opening a stateful **task**. When an agent creates a task, those fields carry the identifiers you
use to continue the conversation.

The most recent service URL is remembered (see [Session state](#session-state)), so after the first
call you can usually drop `-u`:

```bash
a2a-cli send "and again"
```

For the full flag list, run `a2a-cli send --help`.

Before sending, you can inspect an agent with `discover`. It fetches the agent card, presents every
section, and shows which transport the client would select:

```bash
a2a-cli discover -u http://127.0.0.1:9001
```

```text
Name:        REST Hello World Agent
Description: Just a rest hello world agent

Capabilities:
  streaming:         true
  pushNotifications: false
  extendedAgentCard: false

Interfaces:
  - HTTP+JSON http://127.0.0.1:9001 [v1.0]

Security schemes:
  (none — no authentication required)

Skills:
  - hello_world (REST Hello world!)
      Returns a 'Hello from REST server!'
      tags: hello world

Selected transport: http-json -> http://127.0.0.1:9001
  reason: card-declared preference (first supported interface: http-json)
```

Add `--validate` to check the card's required-field structure, or `-o json` for the normalized card
envelope. See the [user guide](docs/user-guide.md#inspecting-an-agent-with-discover) for a full walkthrough.

### Retrieving and cancelling tasks

Once an agent opens a stateful task, `get` retrieves it by id and `cancel` requests its
cancellation. Both take the task id as their single argument, honor `-o json`, and add
`--include-artifacts` / `--history <n>` (on `get`) for richer output:

```bash
a2a-cli get <taskId> -u http://127.0.0.1:9001 --include-artifacts
a2a-cli cancel <taskId> -u http://127.0.0.1:9001
```

`cancel` is idempotent — cancelling an already-terminal task reports its current state instead of
erroring, and a successful cancel exits `0`. A task the server doesn't know surfaces as a
`NOT_FOUND` envelope (`a2aCode: TASK_NOT_FOUND`) and exits `1`. `get --wait`/`--watch` turns the
command into a blocking poll loop. See the [user guide](docs/user-guide.md#retrieving-a-task-with-get).

### Streaming

When the agent card advertises the streaming capability, `send --stream` streams the result over
SSE and emits newline-delimited JSON (NDJSON) in `-o json`. If the card does **not** advertise
streaming, no stream is attempted and `send` falls back to the blocking poll path — behavior never
changes silently:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 --stream
```

### Authentication

Attach caller-supplied credentials with `--bearer`, `--api-key`, or repeatable `-H` headers (or the
`A2A_BEARER` / `A2A_API_KEY` environment variables). Credentials are attached per request to
same-origin targets; they are **withheld** from a cross-origin or downgraded (`https`→`http`) target
unless you opt in with `--allow-cross-origin-credentials`, and the agent-card fetch never carries
credentials:

```bash
a2a-cli send "hello" -u https://agent.example.com --bearer "$TOKEN"
```

### Commands

| Command | Status | Purpose |
|---|---|---|
| `send` | ✅ available | Send a message to an agent and wait for the result (add `--stream` for SSE) |
| `discover` | ✅ available | Fetch, present, and validate an agent card |
| `get` | ✅ available | Retrieve a task by id, with optional artifacts and history |
| `cancel` | ✅ available | Cancel a task by id (idempotent) |
| `session` | ✅ available | Inspect (`session show`) or clear (`session clear`) the local session store |

`help` and `completion` (shell completion) are provided by the CLI framework.

### Global flags

Flags are global — they attach to every command.

| Flag | Meaning |
|---|---|
| `-u, --service-url <url>` | Base URL of the A2A agent. Optional if a previous run stored one. |
| `--card-url <url>` | Explicit agent-card URL, overriding the well-known path. |
| `-o, --output <text\|json>` | Output format. Default `text`. |
| `-n, --no-tui` | Shorthand for `--output json`. |
| `--transport <http-json\|jsonrpc\|grpc>` | Transport binding. Default: card-driven. HTTP+JSON and JSON-RPC are functional; gRPC is not supported (rejected with a usage error). |
| `--context-id <id>` | Continue an existing conversation. |
| `--task-id <id>` | Send against an existing task. |
| `--continue` | Resume the stored conversation (`contextId`) for the next `send`. |
| `--last` | Also resume the stored last task (`latestTaskId`) for the next `send`. |
| `--poll-interval <dur>` | Poll interval while waiting for a task. Default `2s`. |
| `--timeout <dur>` | Maximum time to wait for a task. `0` means no timeout. |
| `--a2a-version <ver>` | Protocol version to signal. Default `1.0`. |
| `--bearer <token>` | Bearer token for the `Authorization` header (env: `A2A_BEARER`). |
| `--api-key <key>` | API key, sent as `X-API-Key` (env: `A2A_API_KEY`). |
| `-H, --header <Name: Value>` | Extra request header. Repeatable. |
| `--allow-cross-origin-credentials` | Forward caller credentials to a cross-origin or downgraded target. Off by default (credentials are withheld). |
| `--insecure` | Skip TLS certificate verification (prints a warning). |
| `-v, --verbose` | Verbose diagnostics on stderr. |

`send` adds `--stream` (SSE, card-gated); `get` adds `--include-artifacts`, `--history <n>`, and
`--wait`/`--watch`.

Every flag with a default can also be set via an environment variable prefixed `A2A_CLI_`
(for example `A2A_CLI_SERVICE_URL`). Precedence is: explicit flag → environment variable →
stored session → built-in default.

`discover` adds one command-local flag: `--validate`, which checks the card's required-field
structure (a conformance aid, not a security check) and exits non-zero if the card is invalid.

### Behavior

- **Transport** is chosen from the agent card. An explicit `--transport` overrides it; otherwise
  the card's declared preference is honored, falling back to HTTP+JSON when the card declares no
  binding the client speaks. HTTP+JSON and JSON-RPC are both functional; gRPC is not supported and
  is rejected with a usage error. `discover` surfaces which transport the client would select and
  why (card-declared preference, explicit override, or the HTTP+JSON default).
- **Blocking by default:** `send` waits until the task reaches a terminal state, and stops
  immediately on an interrupted state (`INPUT_REQUIRED` / `AUTH_REQUIRED`), printing a resume hint.
  The always-completing Go sample agent can't exercise the interrupted-stop path — see the
  [test plan](docs/test-plan.md#t6--polling-ac-6).
- **Streaming is card-gated:** `send --stream` streams over SSE only when the card advertises the
  streaming capability; otherwise no stream is attempted and it falls back to polling.
- **Credentials are per-request** and are withheld from a cross-origin or downgraded target unless
  you opt in with `--allow-cross-origin-credentials`; the agent-card fetch never carries them.
- **Output discipline:** in `-o json`, stdout carries only the envelope (or NDJSON records with
  `--stream`); all diagnostics go to stderr.
- **Exit codes** (spec §9.5): `0` success · `1` generic failure · `2` usage · `3` unreachable ·
  `4` auth · `5` task failed/rejected · `6` input required · `7` timeout.

### Session state

The most recent conversation (service URL, transport, and any `contextId` / latest `taskId`) is
captured to `$XDG_CONFIG_HOME/a2a-cli/session.json` (falling back to
`~/.config/a2a-cli/session.json`, written `0600`) so you can omit `-u` on the next call. Inspect it
with `a2a-cli session show` and remove it with `a2a-cli session clear`. To resume the stored
conversation on the next `send`, pass `--continue` (reuses the `contextId`) or `--last` (also sends
against the stored task); both are opt-in, so a bare `send` never silently attaches to a stale task.
Explicit flags and environment variables always override stored values.

## Documentation

- [User guide](docs/user-guide.md) — install, quickstart, and worked examples for every command.
- [Test plan](docs/test-plan.md) — validation and Tier-1 conformance walkthrough for reviewers and
  the conformance team.
- [Tier-1 conformance report](docs/compliance/tier-1-conformance.md) — the project's own Tier-1
  conformance report for reviewers and the conformance team. The external Python TCK is deferred to
  Tier 2.
- [Changelog](CHANGELOG.md) — notable changes, curated per release.
- [Agent skill](skill/SKILL.md) — a bundled, generic skill that teaches an agent to drive the CLI
  non-interactively, packaged as a self-installable plugin via [`plugin.json`](plugin.json).

## Development

```bash
git clone https://github.com/ghchinoy/a2a-cli.git
cd a2a-cli
go build ./cmd/a2a-cli   # compile
go test ./...            # run the unit tests
```

Requires the Go 1.26+ toolchain; no other services are needed for the unit tests. To exercise the
CLI end to end, run it against a local A2A sample server — see the [test plan](docs/test-plan.md)
for how to stand one up.

The layout follows the design's package boundaries: `cmd/a2a-cli` (entry point) and `internal/`
(`cli`, `client`, `envelope`, `render`, `poll`, `session`, `config`, `clierr`).

## Contributing

This is an early-stage project under active phased development. Issues and pull requests are welcome;
for larger changes, please open an issue first to discuss the approach. Keep documentation aligned
with merged behavior — docs describe what the code does today, not planned features, and notable
changes are curated in the [changelog](CHANGELOG.md). No release has been tagged yet: during the
alpha, build from source (see [Installation](#installation)); tagged releases will appear here once
the client leaves alpha.

## License

Apache-2.0. See [LICENSE](./LICENSE).
