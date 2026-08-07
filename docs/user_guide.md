# a2a-cli User Guide

This guide gets a new user from nothing to a working round-trip against an A2A agent, then walks
through the options that matter day to day.

> **What works today.** `a2a-cli` is in **alpha**. Only **Phase 1** of the Tier-1 build is merged:
> the **`send`** command over the **HTTP+JSON** transport. Commands such as `discover`, `get`, and
> `cancel`, plus streaming and interactive OAuth login, are planned for later phases and are **not**
> available yet. Everything in this guide has been verified against the built binary; nothing here
> is aspirational. As more of Tier 1 lands, this guide grows with it.

## Contents

- [Prerequisites](#prerequisites)
- [Install](#install)
- [Quickstart](#quickstart)
- [Understanding the output](#understanding-the-output)
- [Text vs JSON](#text-vs-json)
- [Choosing the target agent](#choosing-the-target-agent)
- [Session state and omitting `-u`](#session-state-and-omitting--u)
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
its shorthand `-n`). Only `text` and `json` are valid; today an unrecognized value (e.g. `-o yaml`)
is silently treated as `text` rather than rejected — stricter validation of `--output` is landing
shortly, so don't rely on the silent fallback.

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

Each of these can also come from the environment (`A2A_CLI_BEARER`, `A2A_CLI_API_KEY`). The
hello-world sample server requires no auth, so these flags are accepted but have no visible effect
against it; use them with an agent that enforces authentication.

> **Security:** never hard-code secrets into scripts or commit them. Prefer environment variables or
> a secrets manager, and pass them in as shown above.

## Transport and protocol version

Phase 1 speaks **HTTP+JSON** only. Transport is normally chosen for you from the agent card, but you
can state it explicitly:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 --transport http-json
```

- `--transport grpc` is rejected — gRPC is not supported at Tier 1 yet (usage error, exit 2).
- `--transport jsonrpc` is only accepted if the agent's card advertises a JSON-RPC interface;
  otherwise you get a usage error explaining the card doesn't offer it.

`a2a-cli` signals the A2A protocol version on every request; the default is `1.0`. Override it with
`--a2a-version` if you need to target a different version the agent supports.

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
only HTTP+JSON works today.

**`Error [USAGE]: agent card does not offer transport jsonrpc`** (exit 2) — the card doesn't
advertise that binding; let `a2a-cli` pick the transport, or use `--transport http-json`.

Add `-v` for extra diagnostics on stderr while debugging.

## What's coming next

Later phases of the Tier-1 build add `discover`, `get`, and `cancel`; streaming (`--stream`);
`--context-id` / `--task-id` continuation with resume hints; and a bundled agent skill. This guide
will be extended as each merges. Until then, treat any behavior not documented here as not yet
available. Run `a2a-cli --help` and `a2a-cli send --help` to see the current surface at any time.
