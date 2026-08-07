# a2a-cli User Guide

This guide gets a new user from nothing to a working round-trip against an A2A agent, then walks
through the options that matter day to day.

> **What works today.** `a2a-cli` is in **alpha**, but the full Tier-1 command set is merged:
> **`send`** (with `--stream`), **`discover`**, **`get`**, **`cancel`**, and **`session`**, over the
> **HTTP+JSON** and **JSON-RPC** transports, plus caller-supplied authentication. gRPC, a bundled
> agent skill, and interactive OAuth login are **not** available yet. Every runnable example below
> was captured from the built binary run against a live Go sample server. A few behaviors that need
> a richer fixture — auth enforcement, protocol-version negotiation, cross-origin credential
> handling, and exit codes 4–7 — are described from the spec and implementation (unit-tested) and
> are tracked in the [test plan](test-plan.md).

## Contents

- [Prerequisites](#prerequisites)
- [Install](#install)
- [Quickstart](#quickstart)
- [Understanding the output](#understanding-the-output)
- [Text vs JSON](#text-vs-json)
- [Inspecting an agent with `discover`](#inspecting-an-agent-with-discover)
- [Retrieving a task with `get`](#retrieving-a-task-with-get)
- [Cancelling a task with `cancel`](#cancelling-a-task-with-cancel)
- [Streaming with `--stream`](#streaming-with---stream)
- [Choosing the target agent](#choosing-the-target-agent)
- [Session state and omitting `-u`](#session-state-and-omitting--u)
- [Continuing a conversation](#continuing-a-conversation)
- [Managing the session store](#managing-the-session-store)
- [Credentials](#credentials)
- [Transport and protocol version](#transport-and-protocol-version)
- [Exit codes](#exit-codes)
- [Troubleshooting](#troubleshooting)
- [What's coming next](#whats-coming-next)

## Prerequisites

- **Go 1.26 or newer** to build the binary (`go version` to check).
- A running **A2A agent** to talk to. If you don't have one, the
  [test plan](test-plan.md#standing-up-a-sample-server) shows how to start the Go hello-world REST
  sample server in a minute — the same server used for the examples below (listening on
  `http://127.0.0.1:9001`).

## Install

Build from source:

```bash
git clone https://github.com/ghchinoy/a2a-cli.git
cd a2a-cli
go build -o a2a-cli ./cmd/a2a-cli
```

Move the resulting `a2a-cli` binary onto your `PATH` (e.g. `sudo mv a2a-cli /usr/local/bin/`) so you
can run it from anywhere. The rest of this guide assumes it is on your `PATH`.

## Quickstart

Send a message and wait for the reply:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001
```

```text
State:     TASK_STATE_COMPLETED
Task ID:   (none)
Context:   (none)
Message:   Hello from REST server!
```

That's the whole round-trip: `a2a-cli` fetched the agent's card, chose the HTTP+JSON transport, sent
your message, waited for the agent to finish, and printed the result.

## Understanding the output

Every `send` reports four things:

- **State** — the task state in canonical wire form (`TASK_STATE_COMPLETED`, `TASK_STATE_WORKING`,
  and so on). This is how you know whether the work succeeded.
- **Task ID** and **Context ID** — the server-assigned identifiers you replay to continue a
  conversation. They show `(none)` when the agent answered with a direct **message** instead of
  opening a stateful **task** — which is exactly what the hello-world agent does.
- **Message** — the agent's direct textual reply, when it sent one.

`a2a-cli` never invents these identifiers; it captures them from the server's response and reports
them back so your next invocation can reuse them.

## Text vs JSON

The default output is human-readable **text**. For scripting, switch to **JSON** with `-o json` (or
its shorthand `-n`). The accepted values are `text`, `json`, and `tui`; `tui` is reserved for a
future interactive mode and degrades to `text` at Tier 1. An unrecognized value is rejected with a
usage error (exit `2`) rather than silently falling back:

```bash
a2a-cli send "hi" -u http://127.0.0.1:9001 -o yaml
```

```text
Error [USAGE]: invalid --output value "yaml" (want one of: text, json, tui)
```

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

This is the normalized envelope: `taskId`, `contextId`, and `state` are always present (`null` when
no task was created), and `message` carries a direct message reply when there is one.

**Stdout stays clean.** In JSON mode, only the envelope goes to stdout; warnings, verbose logs, and
errors go to stderr. That means you can pipe straight into `jq` without stray text corrupting the
stream:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 -o json | jq -r '.message.parts[0].text'
```

```text
Hello from REST server!
```

## Inspecting an agent with `discover`

Before you send anything, `discover` lets you look at an agent's card: who it is, what it can do, how
to authenticate, and which transport `a2a-cli` would use to talk to it. It performs one network
operation — fetching and presenting the card — and makes no task calls.

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

The output walks the card top to bottom:

- **Identity** — name, description, and (when the card declares them) version, provider, and
  documentation URL.
- **Capabilities** — the streaming / push-notification / extended-card flags and any declared
  protocol extensions.
- **Interfaces** — every transport binding the card advertises, with its URL and protocol version.
- **Security schemes** — how the agent expects you to authenticate, or `(none — no authentication
  required)` when it doesn't.
- **Skills** — the operations the agent advertises, with their IDs, names, descriptions, and tags.
- **Selected transport** — the binding `a2a-cli` would use for this card and *why*. This is the same
  **card-driven transport selection** `send` uses: HTTP+JSON is the Tier-1 transport, and the client
  chooses it from the card's declared interfaces (or honors an explicit `--transport`).

### Pointing `discover` at a card

Like `send`, `discover` fetches the card from the well-known path (`<url>/.well-known/agent-card.json`)
when you pass `-u`. If the card lives elsewhere, point directly at it with `--card-url`:

```bash
a2a-cli discover --card-url http://127.0.0.1:9001/.well-known/agent-card.json
```

### Machine-readable output

Add `-o json` (or `-n`) for the normalized card envelope on stdout — handy for piping into `jq`:

```bash
a2a-cli discover -u http://127.0.0.1:9001 -o json
```

```json
{
  "name": "REST Hello World Agent",
  "description": "Just a rest hello world agent",
  "capabilities": {
    "streaming": true,
    "pushNotifications": false,
    "extendedAgentCard": false
  },
  "defaultInputModes": [
    "text"
  ],
  "defaultOutputModes": [
    "text"
  ],
  "interfaces": [
    {
      "transport": "HTTP+JSON",
      "url": "http://127.0.0.1:9001",
      "protocolVersion": "1.0"
    }
  ],
  "skills": [
    {
      "id": "hello_world",
      "name": "REST Hello world!",
      "description": "Returns a 'Hello from REST server!'",
      "tags": [
        "hello world"
      ],
      "examples": [
        "hi",
        "hello"
      ]
    }
  ],
  "selection": {
    "transport": "http-json",
    "url": "http://127.0.0.1:9001",
    "reason": "card-declared preference (first supported interface: http-json)"
  }
}
```

### Validating a card

Pass `--validate` to check the card's **required-field structure** before you trust it. It's a
conformance aid — a required-field / shape check, **not** a full JSON-Schema validation and **not** a
security check (it does not vet URLs, credentials, or trust). A valid card still prints normally,
with a note on stderr:

```bash
a2a-cli discover -u http://127.0.0.1:9001 --validate
```

```text
card passed structural validation (required fields present; not a full JSON-Schema or security check)
```

If the card is missing required fields (name, description, at least one interface with a URL and
protocol binding, or a skill's id/name), `discover` reports each problem and exits non-zero instead
of presenting the card.

## Retrieving a task with `get`

When an agent opens a stateful **task**, it returns a `taskId`. `get <taskId>` retrieves that task's
current state by id:

```bash
a2a-cli get <taskId> -u http://127.0.0.1:9001
```

By default `get` **summarizes** artifacts (it keeps their identifiers and names but drops their
contents). Pass `--include-artifacts` to fetch the full contents, and `--history <n>` to include up
to `n` recent history messages:

```bash
a2a-cli get <taskId> -u http://127.0.0.1:9001 --include-artifacts --history 20
```

`--history` guards its input: a **negative** value is a usage error (exit `2`) and the server is
never contacted, while an absurdly large value is **clamped to 1000** with a warning on stderr:

```bash
a2a-cli get <taskId> -u http://127.0.0.1:9001 --history -5
```

```text
Error [USAGE]: --history must be zero or a positive number
```

```bash
a2a-cli get <taskId> -u http://127.0.0.1:9001 --history 99999
```

```text
--history 99999 exceeds the client maximum of 1000; clamping to 1000
```

`get` is one-shot by default. Add `--wait` to poll until the task reaches a terminal or interrupted
state (reusing the same blocking-wait logic as `send`), or `--watch` to additionally print each
state transition to stderr. `--poll-interval` and `--timeout` tune the loop.

If the server doesn't know the task, `get` surfaces a normalized `NOT_FOUND` error. The process exit
is `1` (there is no dedicated numeric exit code for not-found at Tier 1), but the machine-readable
envelope still carries the precise codes:

```bash
a2a-cli get missing-task-id -u http://127.0.0.1:9001 -o json
```

```json
{
  "code": "NOT_FOUND",
  "message": "get task failed: failed to get task: task not found",
  "a2aCode": "TASK_NOT_FOUND"
}
```

## Cancelling a task with `cancel`

`cancel <taskId>` requests cancellation of a task and reports the resulting state. It is
**idempotent**: cancelling an already-terminal task is a clean no-op that reports the task's current
state rather than erroring, and a successful cancel exits `0`.

```bash
a2a-cli cancel <taskId> -u http://127.0.0.1:9001
```

As with `get`, an unknown task surfaces as a `NOT_FOUND` envelope (`a2aCode: TASK_NOT_FOUND`) and
exits `1`:

```bash
a2a-cli cancel missing-task-id -u http://127.0.0.1:9001
```

```text
Error [NOT_FOUND]: cancel failed: failed to cancel: cancelation failed: canceler setup failed: failed to load a task: task not found
```

## Streaming with `--stream`

`send --stream` streams the result over Server-Sent Events (SSE) instead of blocking on a poll loop —
**but only when the agent card advertises the streaming capability**. If the card does not advertise
streaming, `a2a-cli` does not attempt a stream; it prints a note on stderr and falls back to the
blocking request path. Behavior never changes silently.

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 --stream
```

```text
Message:   Hello from REST server!
```

In `-o json`, a stream emits **newline-delimited JSON (NDJSON)** — one object per line, each tagged
with a `type`, ending in a `final` record that carries the Appendix B task fields:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 --stream -o json
```

```json
{"type":"message","taskId":null,"contextId":null,"state":"","message":{"role":"ROLE_AGENT","parts":[{"text":"Hello from REST server!"}]}}
{"type":"final","taskId":null,"contextId":null,"state":"TASK_STATE_COMPLETED","message":{"role":"ROLE_AGENT","parts":[{"text":"Hello from REST server!"}]}}
```

After any stream (re)connect, `a2a-cli` reconciles the final state with a `get` before reporting, and
if the stream drops it falls back to polling — so a dropped connection never loses the task. A task
that ends in `INPUT_REQUIRED` exits `6` with a resume hint; errors on the stream are emitted as typed
NDJSON error records carrying the `taskId` when one exists.

## Choosing the target agent

Give the agent's base URL with `-u`/`--service-url`. `a2a-cli` fetches the card from the well-known
path (`<url>/.well-known/agent-card.json`). If the card lives somewhere else, point directly at it
with `--card-url`:

```bash
a2a-cli send "hello" --card-url http://127.0.0.1:9001/.well-known/agent-card.json -o json
```

## Session state and omitting `-u`

After a successful call, `a2a-cli` records the service URL and transport in a small session file at
`$XDG_CONFIG_HOME/a2a-cli/session.json` (falling back to `~/.config/a2a-cli/session.json`). On the
next call you can leave `-u` off and it reuses that URL:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001   # stores the URL
a2a-cli send "and again"                          # reuses it
```

To clear the stored state, delete that file. An explicit `-u` (or the `A2A_CLI_SERVICE_URL`
environment variable) always overrides what was stored.

Values resolve in this order, highest priority first:

1. an explicit flag (`-u ...`);
2. an environment variable (`A2A_CLI_SERVICE_URL`, `A2A_CLI_TRANSPORT`, etc. — the flag name
   uppercased, `-` replaced with `_`, prefixed `A2A_CLI_`);
3. the stored session;
4. the built-in default.

The service URL and transport are auto-resumed as above. Conversation **identifiers** (the
`contextId` and the latest `taskId`) are only resumed when you ask for them — see below.

> **Known limitation (CO-9).** The session store persists the **transport** alongside the service
> URL, and precedence is applied per field (explicit flag > env > session > default). So if you point
> a later `send` at a *different* server with a fresh `-u` but don't also pass `--transport`, the
> **stored** transport is still applied — and if the new server's card doesn't offer it, the command
> fails with a usage error (exit `2`):
>
> ```text
> Error [USAGE]: agent card does not offer transport http-json
> ```
>
> This is a documented precedence interaction, not a bug. When switching to a different
> server or transport, either pass `--transport` explicitly or run `a2a-cli session clear` first.
> (Tracked as Tier-2 follow-up CO-9.)

## Continuing a conversation

The session store also remembers the last `contextId` and `taskId`, but a bare `send` never silently
attaches to them. To resume, opt in explicitly:

- `--continue` reuses the stored `contextId`, so the next `send` is a **new task within the same
  conversation**.
- `--last` additionally sends **against the stored task** (the latest `taskId`) — for example, to
  reply to a task that stopped in `INPUT_REQUIRED`.

```bash
a2a-cli send "and here's the follow-up" --continue
```

Explicit `--context-id` / `--task-id` always override the stored values. If there is no stored
session to resume, `--continue`/`--last` is a usage error (exit `2`):

```text
Error [USAGE]: no stored session to resume (--continue/--last); run a send first or pass --context-id/--task-id
```

If the stored session recorded no task (for example, an agent that replies with a direct message
rather than opening a task), `--last` warns on stderr and sends a new message instead of failing.

## Managing the session store

Inspect the store with `session show` (or just `session`), which prints its path and current
contents and never touches the network:

```bash
a2a-cli session show
```

```text
Path:      /home/you/.config/a2a-cli/session.json
Context:   (unknown)
Last Task: (unknown)
Service:   http://127.0.0.1:9001
Transport: http-json
Updated:   2026-08-07T20:01:33Z
```

Clear it with `session clear` (idempotent — clearing when nothing is stored is not an error):

```bash
a2a-cli session clear
```

```text
session cleared: /home/you/.config/a2a-cli/session.json
```

The store is written atomically with `0600` permissions, and the persisted service URL has any
embedded credentials stripped. Caller-supplied credentials are **never** written to disk.

## Credentials

Attach caller-supplied credentials to every request:

```bash
# Bearer token -> Authorization: Bearer <token>
a2a-cli send "hello" -u https://agent.example.com --bearer "$TOKEN"

# API key -> X-API-Key: <key>
a2a-cli send "hello" -u https://agent.example.com --api-key "$KEY"

# Arbitrary headers, repeatable
a2a-cli send "hello" -u https://agent.example.com -H "X-Tenant: acme" -H "X-Env: prod"
```

The bearer token and API key can also come from the environment — `A2A_BEARER` and `A2A_API_KEY`
(note: these are **not** the `A2A_CLI_`-prefixed config variables). An explicit flag wins over the
environment variable. The Go hello-world sample server requires no auth, so these flags are accepted
but have no visible effect against it; use them with an agent that enforces authentication.

**Where credentials are sent.** Credentials are attached per request to the selected interface when
it is same-origin with the card. They are **withheld** from a target that is cross-origin (a
different host than the card was fetched from) or downgraded (an `http` interface reached from an
`https`-fetched card) — `a2a-cli` warns on stderr and sends nothing. Re-run with
`--allow-cross-origin-credentials` to forward them anyway (this also warns, and trusts that target
for the whole request lifecycle, including any redirects it issues). The agent-card fetch itself
never carries credentials.

> **Security:** never hard-code secrets into scripts or commit them. Prefer environment variables or
> a secrets manager, and pass them in as shown above.

## Transport and protocol version

`a2a-cli` speaks **HTTP+JSON** and **JSON-RPC**. Transport is normally chosen for you from the agent
card (run `discover` to see which binding it would pick and why), but you can state it explicitly:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 --transport http-json
```

- `--transport grpc` is rejected — gRPC is not supported (usage error, exit `2`).
- `--transport jsonrpc` and `--transport http-json` are accepted only if the agent's card advertises
  that interface; otherwise you get a usage error explaining the card doesn't offer it.
- With no `--transport`, the client selects the card's first supported interface, falling back to
  HTTP+JSON at your `-u` URL when the card declares no binding it speaks.

`a2a-cli` signals the A2A protocol version on every request via the `A2A-Version` header; the default
is `1.0`. Override it with `--a2a-version` if you need to target a different version the agent
supports. If the agent rejects the signaled version, `a2a-cli` surfaces a clear error (exit `1`,
`a2aCode` `VERSION_NOT_SUPPORTED`) rather than silently downgrading.

Use `--insecure` to skip TLS certificate verification for local development against a self-signed
endpoint. It prints a warning to stderr:

```text
WARNING: TLS certificate verification is disabled (--insecure)
```

## Exit codes

`a2a-cli` returns a specific exit code so scripts can branch on the outcome:

| Code | Meaning |
|---|---|
| `0` | Success / task completed |
| `1` | Generic failure |
| `2` | Usage error (bad flags or arguments) |
| `3` | Agent or transport unreachable |
| `4` | Authentication required or failed |
| `5` | Task failed or rejected |
| `6` | Input required (non-interactive) |
| `7` | Timeout |

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 || echo "failed with code $?"
```

## Troubleshooting

**`Error [USAGE]: send requires exactly one <text> argument`** — pass the message as a single
argument: `a2a-cli send "your message" -u <url>`. Quote it so the shell keeps it together.

**`Error [UNREACHABLE]: failed to fetch agent card: ...connection refused`** (exit 3) — the agent
isn't running or the URL/port is wrong. Confirm the server is up and the card is reachable:

```bash
curl -s http://127.0.0.1:9001/.well-known/agent-card.json
```

**`Error [USAGE]: transport not supported at Tier 1: grpc`** (exit 2) — drop `--transport grpc`;
gRPC is not supported. Use HTTP+JSON or JSON-RPC.

**`Error [USAGE]: agent card does not offer transport jsonrpc`** (exit 2) — the card doesn't
advertise that binding; let `a2a-cli` pick the transport, or name one the card actually offers.

**`Error [NOT_FOUND]: get task failed: ...task not found`** (exit 1) — the task id is unknown to the
server (expired, cancelled long ago, or mistyped). In `-o json` the envelope carries
`code: "NOT_FOUND"` and `a2aCode: "TASK_NOT_FOUND"`. Note that not-found has no dedicated numeric
exit code at Tier 1, so the process exit is the generic `1`.

Add `-v` for extra diagnostics on stderr while debugging.

## What's coming next

The Tier-1 command surface is complete. Still to land: a bundled, self-installable agent skill
(`SKILL.md`) and the Tier-1 conformance report, plus richer validation against an auth-enforcing and
multi-transport server. Beyond Tier 1, gRPC transport and interactive OAuth login are planned. This
guide will be extended as each merges. Until then, treat any behavior not documented here as not yet
available. Run `a2a-cli --help` or `a2a-cli <command> --help` to see the current surface at any time.
