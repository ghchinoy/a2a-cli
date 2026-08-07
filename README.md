# a2a-cli

![status: alpha](https://img.shields.io/badge/status-alpha-orange)
![license: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue)

A command-line client for talking to [A2A protocol](https://a2a-protocol.org) agents from your
terminal: send a message to an agent, wait for its answer, and get the result as readable text or
machine-readable JSON. Built on the official [`a2a-go` v2 SDK](https://github.com/a2aproject/a2a-go).

> **Status: alpha.** This is an early build of a **Tier-1** A2A client. Two commands are merged so
> far — **`send`** and **`discover`**, both over HTTP+JSON. More commands (`get`, `cancel`,
> streaming, auth flows) land as later phases complete. The `--output json` shape and the
> exit-code scheme are a stable contract; everything else may change without notice while the
> [a2a-cli spec](https://github.com/a2aproject/a2a-cli) is still a Draft.

## Contents

- [Installation](#installation)
- [Usage](#usage)
  - [Commands](#commands)
  - [Global flags](#global-flags)
  - [Behavior](#behavior)
  - [Session state](#session-state)
- [Documentation](#documentation)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## Installation

Requires **Go 1.26+**. Build from source:

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

### Commands

| Command | Status | Purpose |
|---|---|---|
| `send` | ✅ available | Send a message to an agent and wait for the result |
| `discover` | ✅ available | Fetch, present, and validate an agent card |
| `get` | ⏳ planned | Retrieve a task's status and artifacts |
| `cancel` | ⏳ planned | Cancel an active task |

`help` and `completion` (shell completion) are provided by the CLI framework.

### Global flags

Flags are global — they attach to `send` and `discover` today, and to the commands still to come.

| Flag | Meaning |
|---|---|
| `-u, --service-url <url>` | Base URL of the A2A agent. Optional if a previous run stored one. |
| `--card-url <url>` | Explicit agent-card URL, overriding the well-known path. |
| `-o, --output <text\|json>` | Output format. Default `text`. |
| `-n, --no-tui` | Shorthand for `--output json`. |
| `--transport <http-json\|jsonrpc\|grpc>` | Transport binding. Default: card-driven, HTTP+JSON. Only HTTP+JSON is functional at Tier 1. |
| `--context-id <id>` | Continue an existing conversation. |
| `--task-id <id>` | Send against an existing task. |
| `--poll-interval <dur>` | Poll interval while waiting for a task. Default `2s`. |
| `--timeout <dur>` | Maximum time to wait for a task. `0` means no timeout. |
| `--a2a-version <ver>` | Protocol version to signal. Default `1.0`. |
| `--bearer <token>` | Bearer token for the `Authorization` header. |
| `--api-key <key>` | API key, sent as `X-API-Key`. |
| `-H, --header <Name: Value>` | Extra request header. Repeatable. |
| `--insecure` | Skip TLS certificate verification (prints a warning). |
| `-v, --verbose` | Verbose diagnostics on stderr. |

Every flag with a default can also be set via an environment variable prefixed `A2A_CLI_`
(for example `A2A_CLI_SERVICE_URL`). Precedence is: explicit flag → environment variable →
stored session → built-in default.

`discover` adds one command-local flag: `--validate`, which checks the card's required-field
structure (a conformance aid, not a security check) and exits non-zero if the card is invalid.

### Behavior

- **Transport** defaults to HTTP+JSON, chosen from the agent card. An explicit `--transport`
  overrides it; otherwise the card's declared preference is honored. gRPC is not yet supported and
  is rejected with a usage error. `discover` surfaces which transport the client would select and
  why (card-declared preference, explicit override, or the HTTP+JSON default).
- **Blocking by default:** `send` waits until the task reaches a terminal state, and is designed to
  stop immediately on an interrupted state (`INPUT_REQUIRED` / `AUTH_REQUIRED`). The always-completing
  sample agent can't exercise the interrupted-stop path yet — see the
  [test plan](docs/test-plan.md#t6--polling-ac-6).
- **Output discipline:** in `-o json`, stdout carries only the envelope; all diagnostics go to
  stderr.
- **Exit codes** (spec §9.5): `0` success · `1` generic failure · `2` usage · `3` unreachable ·
  `4` auth · `5` task failed/rejected · `6` input required · `7` timeout.

### Session state

The most recent conversation (service URL and transport) is captured to
`$XDG_CONFIG_HOME/a2a-cli/session.json` (falling back to `~/.config/a2a-cli/session.json`) so you
can omit `-u` on the next call. Delete that file to clear it. Explicit flags and environment
variables always override stored values.

## Documentation

- [User guide](docs/user-guide.md) — install, quickstart, and worked `send` and `discover` examples.
- [Test plan](docs/test-plan.md) — validation and Tier-1 conformance walkthrough for reviewers and
  the conformance team.

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
with merged behavior — docs describe what the code does today, not planned features.

## License

Apache-2.0. See [LICENSE](./LICENSE).
