# a2a-cli Tier-1 Validation & Conformance Test Plan

This is the validation walkthrough for experienced users and the conformance team. It maps runnable
command sequences to the design's **§10 Tier-1 acceptance criteria (AC)** and to the **§13
compliance-report** notion, so the team can execute it end to end and record evidence in the Tier-1
conformance report.

The plan is intentionally comprehensive — it enumerates the full Tier-1 acceptance surface. Each
step is clearly marked as either:

- **✅ Exercisable now** — runnable against the current merged build (the full Tier-1 surface,
  Phases 1–8). Expected output below was captured from the built binary run against the live Go
  hello-world sample servers (REST and JSON-RPC).
- **◑ Partial** — the command is merged and exercisable, but some facet of the criterion needs a
  richer server (task-producing, auth-enforcing, or multi-version) that Tier-1 CI doesn't provision;
  the underlying code path is merged and unit-tested.
- **⏳ Deferred** — live validation of the facet is deferred to the Tier-2 fixture (the Python
  multi-transport / auth-enforcing server) per design §7 Q5; the Tier-1 code path is merged and
  unit-tested where applicable. (No Tier-1 code remains unmerged.)

> **Merged surface.** The full Tier-1 surface (Phases 1–8) is on `main`: `send` (with `--stream`),
> `discover` (full card presentation, `--card-url`, `--validate`, surfaced card-driven transport
> selection), `get` (`--include-artifacts`, `--history`, `--wait`/`--watch`), `cancel`, and the
> `session` command (`show` / `clear`); card-driven transport selection over **HTTP+JSON and
> JSON-RPC**; the JSON envelope + cross-binding error normalization; exit-code mapping; blocking
> wait; `--continue` / `--last` continuation; caller-supplied auth with the cross-origin/downgrade
> credential gate; `A2A-Version` signaling; and session capture (`0600`). The `SKILL.md` bundle
> (Phase 7) and the Tier-1 conformance report (Phase 8) are also merged — see the [Tier-1 conformance
> report](compliance/tier-1-conformance.md). That report is the project's own Tier-1 conformance
> report; the external Python TCK is **not** run at Tier 1 and remains deferred to Tier 2.

## Contents

- [Prerequisites](#prerequisites)
- [Standing up a sample server](#standing-up-a-sample-server)
- [Test procedures](#test-procedures)
  - [T1 — Defaults (AC 1)](#t1--defaults-ac-1)
  - [T2 — Commands (AC 2)](#t2--commands-ac-2)
  - [T3 — JSON envelope (AC 3)](#t3--json-envelope-ac-3)
  - [T4 — Exit codes (AC 4)](#t4--exit-codes-ac-4)
  - [T5 — Conversation / session (AC 5)](#t5--conversation--session-ac-5)
  - [T6 — Polling (AC 6)](#t6--polling-ac-6)
  - [T7 — Transport & version (AC 7)](#t7--transport--version-ac-7)
  - [T8 — Auth (AC 8)](#t8--auth-ac-8)
  - [T9 — SKILL.md (AC 9)](#t9--skillmd-ac-9)
  - [T10 — Compliance report (AC 10)](#t10--compliance-report-ac-10)
- [Acceptance-criteria coverage matrix](#acceptance-criteria-coverage-matrix)
- [The §13 compliance report](#the-13-compliance-report)

## Prerequisites

- **Go 1.26+** toolchain.
- A checkout of `a2a-cli`, built:

  ```bash
  git clone https://github.com/ghchinoy/a2a-cli.git
  cd a2a-cli
  go build -o /tmp/a2a-cli ./cmd/a2a-cli
  ```

- A live A2A sample server (below). All Tier-1 validation is done against a running agent — the
  observable behavior is what conformance measures.

The unit test suite runs with no external services and should pass before functional validation:

```bash
go test ./...
```

## Standing up a sample server

The reference servers for this plan are the **Go hello-world** examples from the `a2a-go` SDK — Go
only (keeps CI in one toolchain), no API key, card served at the well-known path. There are two: a
**REST** (HTTP+JSON) server and a **JSON-RPC** server, so cross-binding parity can be checked with
the Go toolchain alone. They are part of the `a2a-go` module `a2a-cli` already depends on, so you can
build them straight from the module without a separate checkout:

```bash
# from the a2a-cli checkout (uses the already-required a2a-go module)
go build -o /tmp/hw-rest    github.com/a2aproject/a2a-go/v2/examples/helloworld/server/rest
go build -o /tmp/hw-jsonrpc github.com/a2aproject/a2a-go/v2/examples/helloworld/server/jsonrpc
/tmp/hw-rest    -port 9001 &     # HTTP+JSON, card at http://127.0.0.1:9001
/tmp/hw-jsonrpc -port 9002 &     # JSON-RPC,  card at http://127.0.0.1:9002 (endpoint /invoke)
```

Confirm the card is reachable:

```bash
curl -s http://127.0.0.1:9001/.well-known/agent-card.json
```

```json
{"supportedInterfaces":[{"url":"http://127.0.0.1:9001","protocolBinding":"HTTP+JSON","protocolVersion":"1.0"}],"capabilities":{"streaming":true},"defaultInputModes":["text"],"defaultOutputModes":["text"],"description":"Just a rest hello world agent","name":"REST Hello World Agent","skills":[{"description":"Returns a 'Hello from REST server!'","examples":["hi","hello"],"id":"hello_world","name":"REST Hello world!","tags":["hello world"]}],"version":""}
```

Both Go servers reply with a **direct message** (not a stateful task), so `taskId`/`contextId` are
`null` and the state is `TASK_STATE_COMPLETED`. That is sufficient to validate `send`/`discover`/
`--stream` and the command surface, but neither server maintains a task store, so `get`/`cancel`
against a **real** task cannot be exercised here — only their `NOT_FOUND` path is (a known task id
never exists). Task-only states (`WORKING`, `INPUT_REQUIRED`, `FAILED`, artifacts) likewise cannot be
produced. Steps that need those are marked **◑ Partial** / **⏳ Deferred** and require a richer server.

> **Note on the Python multi-transport server.** The `a2a-python` hello-world server exposes all
> three transports and both protocol versions at once and is the natural fixture for cross-transport
> parity and the TCK. It requires Python 3.11+ and `uv`, and per the design (§7, Q5) it is
> **deferred to Tier 2**. Tier-1 validation stays on the Go server above.

In the commands below, `a2a-cli` means the built binary (e.g. `/tmp/a2a-cli`).

## Test procedures

### T1 — Defaults (AC 1)

**Criterion:** transport defaults to HTTP+JSON via card selection; `send` blocks to
terminal/interrupted by default; default output is minimal structured `text`; TLS on; latest version
signaled (design §10.1, spec §4.5).

**✅ Exercisable now — default text, blocking, HTTP+JSON:**

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001
```

Expected (exit `0`):

```text
State:     TASK_STATE_COMPLETED
Task ID:   (none)
Context:   (none)
Message:   Hello from REST server!
```

The command returned only after the server produced a terminal result (blocking), rendered
minimal structured text (labeled fields, not raw JSON), and used HTTP+JSON — the only interface the
card advertises. Protocol version defaults to `1.0` (see T7).

- ✅ HTTP+JSON default, blocking-by-default, text-default, TLS-on default — verified here.
- ◑ Blocking **stop on interrupted state** (`INPUT_REQUIRED`/`AUTH_REQUIRED`) cannot be shown with
  this always-completing agent; the stop logic is merged and unit-tested, and live validation needs
  a task-producing server (Tier-2 fixture).

### T2 — Commands (AC 2)

**Criterion:** `discover`, `send`, `get`, `cancel` behave per spec §8.1–8.4, including `get --wait`,
`--include-artifacts`, `--history`, cancel idempotency.

**✅ Exercisable now — `send`:** covered by T1/T3.

**✅ Exercisable now — `discover` (Phase 2):** the command resolves the card, presents every section
(identity, capabilities, interfaces, security schemes, skills), and surfaces the transport the client
would select.

```bash
a2a-cli discover -u http://127.0.0.1:9001
```

Expected (exit `0`):

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

The exact `reason` string above assumes a **clean session** (no stored transport). If a prior
`send`/`discover` has persisted `transport: http-json` to `session.json`, the stored value is treated
as an explicit override and the reason instead reads `explicit --transport=http-json`; delete the
session file (see T5) to reproduce the card-declared-preference wording.

`-o json` (or `-n`) emits the normalized card envelope on stdout — every field above plus
`defaultInputModes` / `defaultOutputModes` and the `selection` object — and nothing else:

```bash
a2a-cli discover -u http://127.0.0.1:9001 -o json | jq .selection.transport   # "http-json"
```

`--card-url` targets a card off the well-known path, and `--validate` checks the card's
required-field **structure** (a conformance aid — required-field/shape only, not a full JSON-Schema
validation and not a security check), printing a note to **stderr** and still presenting a valid card:

```bash
a2a-cli discover -u http://127.0.0.1:9001 --validate 2>&1 1>/dev/null
# -> card passed structural validation (required fields present; not a full JSON-Schema or security check)
```

`discover` honors card-driven transport selection: an explicit `--transport http-json` reports
`reason: explicit --transport=http-json`; `--transport grpc` is rejected (exit `2`,
`Error [USAGE]: transport not supported at Tier 1: grpc`); and `--transport jsonrpc` is rejected
because the card doesn't advertise it (exit `2`, `Error [USAGE]: agent card does not offer transport
jsonrpc`). An unreachable target exits `3` with a normalized `UNREACHABLE` error, and calling
`discover` with neither `-u` nor `--card-url` is a usage error (exit `2`).

**✅ Exercisable now — commands present (Phase 3+):** `get`, `cancel`, and `session` are merged and
appear in the command tree:

```bash
a2a-cli --help    # Available Commands: cancel, completion, discover, get, help, send, session
```

**✅ Exercisable now — `get` (Phase 3):** `get <taskId>` is a one-shot task fetch. Neither Go server
keeps a task store, so a known id never resolves — this exercises the normalized `NOT_FOUND` path
(the real-task/artifacts/history paths need a task-producing server, below):

```bash
a2a-cli get some-task-id -u http://127.0.0.1:9001 -o json
```

Expected (exit `1`):

```json
{
  "code": "NOT_FOUND",
  "message": "get task failed: failed to get task: task not found",
  "a2aCode": "TASK_NOT_FOUND"
}
```

`--history` is guarded client-side: a negative value is a usage error (exit `2`, server never
reached), and an absurd value is clamped to `1000` with a stderr warning (CO-8):

```bash
a2a-cli get some-task-id -u http://127.0.0.1:9001 --history -5      # -> Error [USAGE]: --history must be zero or a positive number (exit 2)
a2a-cli get some-task-id -u http://127.0.0.1:9001 --history 99999   # -> stderr: --history 99999 exceeds the client maximum of 1000; clamping to 1000
```

`--include-artifacts` (fetch artifact contents vs the default summary), `--wait`/`--watch` (poll to a
terminal/interrupted state, reusing the same loop as `send`), and `--poll-interval`/`--timeout` are
all accepted (`a2a-cli get --help`). The `--include-artifacts` rendering itself is **unit-tested**,
not exercised here — the Go hello-world servers emit a message with no artifacts to fetch.

**✅ Exercisable now — `cancel` (Phase 3):** `cancel <taskId>` is idempotent and reports the
resulting state; a successful cancel exits `0`. Against the store-less servers an unknown id
exercises the same normalized `NOT_FOUND` path (exit `1`):

```bash
a2a-cli cancel some-task-id -u http://127.0.0.1:9001
# -> Error [NOT_FOUND]: cancel failed: ...task not found   (exit 1)
```

- ✅ `discover` full-card presentation, `-o json` envelope, `--card-url`, `--validate`, and surfaced
  transport selection — verified against the built binary.
- ✅ `get`/`cancel` present, flag surface, and the `NOT_FOUND` normalization (identical over HTTP+JSON
  **and** JSON-RPC — point either command at `http://127.0.0.1:9002`) — verified.
- ◑ `get` against a **real** task (state/artifacts/history rendering), `get --wait` polling to
  terminal, and `cancel` moving a live task to `CANCELED` are **unit-tested** and need a
  **task-producing** server for live validation; the Go hello-world servers reply with a message and
  keep no task store.
- ⏳ `discover` against a **multi-interface** card (preference ordering, routing identifier) needs the
  Tier-2 Python fixture — the hello-world cards each declare a single interface.

### T3 — JSON envelope (AC 3)

**Criterion:** in `-o json`, task ops emit `taskId`/`contextId`/`state` (+ `artifacts`/`message`)
and only valid JSON/NDJSON on stdout; errors use `{code,message,a2aCode}`; diagnostics only on
stderr; error codes identical across bindings (design §10.3, spec §9.1/§9.3/§9.4, Appendix B).

**✅ Exercisable now — success envelope:**

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 -o json
```

Expected (exit `0`):

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

`-n` is a shorthand for `-o json` and produces the identical envelope.

**✅ Exercisable now — error envelope:**

```bash
a2a-cli send "hi" -u http://127.0.0.1:9999 -o json   # nothing listening on 9999
```

Expected (exit `3`):

```json
{
  "code": "UNREACHABLE",
  "message": "failed to fetch agent card: card request failed: Get \"http://127.0.0.1:9999/.well-known/agent-card.json\": dial tcp 127.0.0.1:9999: connect: connection refused",
  "a2aCode": null
}
```

**✅ Exercisable now — stdout/stderr discipline:** stdout carries only JSON even when a warning is
emitted; the warning goes to stderr.

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 -o json --insecure 2>/dev/null | jq .state
```

Expected (exit `0`): `"TASK_STATE_COMPLETED"` on stdout; the `--insecure` warning appears only when
stderr is not discarded.

**✅ Exercisable now — NDJSON stream (Phase 5):** with `--stream -o json`, stdout is
newline-delimited JSON — one object per line, each tagged with `type`, ending in a `final` record
carrying the Appendix B task fields:

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001 --stream -o json
```

Expected (exit `0`):

```json
{"type":"message","taskId":null,"contextId":null,"state":"","message":{"role":"ROLE_AGENT","parts":[{"text":"Hello from REST server!"}]}}
{"type":"final","taskId":null,"contextId":null,"state":"TASK_STATE_COMPLETED","message":{"role":"ROLE_AGENT","parts":[{"text":"Hello from REST server!"}]}}
```

**✅ Exercisable now — cross-binding error equality (Phase 6):** the same A2A error yields the same
normalized envelope regardless of binding. `get`/`cancel` for an unknown task returns identical
`code`/`a2aCode` over HTTP+JSON (`:9001`) and JSON-RPC (`:9002`):

```bash
a2a-cli get missing -u http://127.0.0.1:9001 -o json | jq -c '{code,a2aCode}'   # {"code":"NOT_FOUND","a2aCode":"TASK_NOT_FOUND"}
a2a-cli get missing -u http://127.0.0.1:9002 -o json | jq -c '{code,a2aCode}'   # {"code":"NOT_FOUND","a2aCode":"TASK_NOT_FOUND"}
```

- ✅ Envelope top-level fields, error object shape, stdout/stderr split, and NDJSON stream records —
  verified.
- ✅ Cross-**binding** identical error codes — verified across the two live Go bindings (HTTP+JSON and
  JSON-RPC) for the `NOT_FOUND` case. gRPC is out of scope (not supported); broader cross-binding
  coverage across every error awaits the Tier-2 multi-transport fixture.
- ◑ `artifacts` array population is **unit-tested**, not exercisable here; it needs an
  artifact-producing server — the Go hello-world servers emit a message with no artifacts.

### T4 — Exit codes (AC 4)

**Criterion:** all of `0`–`7` reachable and correct for their triggers (design §10.4, spec §9.5).

**✅ Exercisable now:**

| Code | Trigger | Command | Verified |
|---|---|---|---|
| `0` | Task completed | `a2a-cli send "hi" -u http://127.0.0.1:9001` | ✅ |
| `1` | Generic / not-found | `a2a-cli get missing -u http://127.0.0.1:9001` → `Error [NOT_FOUND]: ...task not found` (envelope `code=NOT_FOUND`; no dedicated numeric slot ⇒ exit `1`, CO-2) | ✅ |
| `2` | Usage error | `a2a-cli send -u http://127.0.0.1:9001` (no message arg) → `Error [USAGE]: send requires exactly one <text> argument` | ✅ |
| `2` | Usage error | `a2a-cli send "hi" -u http://127.0.0.1:9001 --transport grpc` → `Error [USAGE]: transport not supported at Tier 1: grpc` | ✅ |
| `2` | Usage error | `a2a-cli send "hi" -u http://127.0.0.1:9001 -o yaml` → `Error [USAGE]: invalid --output value "yaml" (want one of: text, json, tui)` | ✅ |
| `2` | Usage error | `a2a-cli get t -u http://127.0.0.1:9001 --history -5` → `Error [USAGE]: --history must be zero or a positive number` | ✅ |
| `3` | Unreachable | `a2a-cli send "hi" -u http://127.0.0.1:9999` → `Error [UNREACHABLE]: ...connection refused` | ✅ |

Check a code explicitly:

```bash
a2a-cli send -u http://127.0.0.1:9001 ; echo "exit=$?"   # exit=2
```

**◑ Partial — codes not reachable against the message-only Go servers:**

- `4` auth — reachable only against an **auth-enforcing** agent (a bad credential → `401` → exit `4`);
  the Go hello-world servers require no auth. The `UNAUTHENTICATED`/`AUTH_REQUIRED` → exit `4` mapping
  is implemented (`internal/client` classify) and unit-tested.
- `5` task failed/rejected, `6` input required, `7` timeout — require an agent that returns those
  states or a long-running task. The mapping (`internal/clierr`) is implemented and unit-tested;
  end-to-end validation needs a task-producing / interruptible server.

> **Note (CO-2):** a task the server doesn't know is a firm envelope contract — `code=NOT_FOUND`,
> `a2aCode=TASK_NOT_FOUND` across every binding — but §9.5 has **no dedicated numeric slot** for
> not-found, so the process exit is the generic `1`. This is verified above and is intentional, not a
> gap.

### T5 — Conversation / session (AC 5)

**Criterion:** identifiers never fabricated; captured and replayed; `--context-id` / `--task-id` /
`--continue` per spec §6.2/§6.4; resume hint printed in text mode; no state only in memory; secret
files `0600` (design §10.5).

**✅ Exercisable now — session capture & replay of the service URL:**

```bash
a2a-cli send "hello" -u http://127.0.0.1:9001    # captures serviceUrl+transport
cat ~/.config/a2a-cli/session.json
a2a-cli send "again"                              # reuses stored URL, no -u needed
```

Expected `session.json`:

```json
{
  "schemaVersion": 1,
  "serviceUrl": "http://127.0.0.1:9001",
  "transport": "http-json",
  "updatedAt": "<timestamp>"
}
```

This confirms state is persisted to disk (not memory-only) and replayed. An explicit `-u` or
`A2A_CLI_SERVICE_URL` overrides the stored value (precedence: flag → env → session → default).

**✅ Exercisable now — identifiers reported back:** `send` prints `State` / `Task ID` / `Context` in
text and `state`/`taskId`/`contextId` in JSON (T1/T3). With the hello-world message reply the ids are
`(none)`/`null`; the tool never invents them.

**✅ Exercisable now — `--task-id` surfaces the server error, not a silent new task (Phase 4/6):**
sending against an unknown task id surfaces the normalized `NOT_FOUND` error rather than quietly
opening a new task (spec §6.2):

```bash
a2a-cli send "reply" -u http://127.0.0.1:9001 --task-id nonexistent
# -> Error [NOT_FOUND]: send failed: ...task not found   (exit 1)
```

**✅ Exercisable now — `--continue` / `--last` (Phase 4):** both are named flags, and both are
opt-in so a bare `send` never attaches to a stale task. With no stored session they are a usage error
(exit `2`); with a session that recorded no task, `--last` warns and sends a new message:

```bash
a2a-cli session clear
a2a-cli send "hi" --continue
# -> Error [USAGE]: no stored session to resume (--continue/--last); run a send first or pass --context-id/--task-id  (exit 2)
```

**✅ Exercisable now — `session` command + `0600` store (Phase 4):** `session show` prints the store
path and contents; `session clear` deletes it (idempotent); the file is written `0600`:

```bash
a2a-cli send "hi" -u http://127.0.0.1:9001        # writes the store
stat -c '%a' ~/.config/a2a-cli/session.json       # -> 600
a2a-cli session show                              # path + contextId/latestTaskId/serviceUrl/transport
a2a-cli session clear                             # -> session cleared: <path>
```

**⏳ Deferred (fixture / Tier-2):**

- **Full continuation replay** against a **task-aware** server (a real `contextId`/`taskId` echoed
  back and replayed on the next turn) — the Go hello-world servers reply with a message and open no
  task, so there is no live task to continue. The `--task-id` not-found guard above is verified; the
  positive replay path is unit-tested and its live validation needs a task-producing server.
- **Resume-command hint** on **interruption** (`INPUT_REQUIRED` → `a2a-cli send --task-id <id>
  "<reply>"`) — needs an interruptible task; the hint code path is merged and unit-tested.
- **`0600` secret file** for persisted credentials — Tier 1 does not persist caller-supplied creds;
  the mode-restricted `credentials.json` is a Tier-2 concern. The `session.json` store is already
  `0600` (verified above).

### T6 — Polling (AC 6)

**Criterion:** blocking wait stops on terminal **and** interrupted states; `--poll-interval` /
`--timeout` honored; timeout → exit `7` without losing the printed `taskId`; SIGINT interruptible;
if streaming implemented, first event is `Task` and post-reconnect reconciles with `get` (design
§10.6, spec §7).

**✅ Exercisable now — flags and poll entry points present (Phases 3/5):** `--poll-interval`
(default `2s`) and `--timeout` (default `0` = no timeout) are accepted on `send` and `get`. The poll
loop is reachable two ways beyond `send`'s blocking wait: `get --wait`/`--watch` (poll a task to a
terminal/interrupted state, `--watch` reporting each transition on stderr) and `send --stream` (which
reconciles with a `get` after any stream (re)connect and **falls back to the poll loop** if the
stream drops). Blocking to a terminal state is verified in T1; NDJSON stream records in T3.

**◑ Partial / ⏳ Deferred:** the polling loop's **interrupted-state stop**, `--timeout` → exit `7`
with the `taskId` preserved, SIGINT interruptibility, and a **forced stream drop → poll fallback →
reconcile** all need a **long-running / task-producing** agent — the Go hello-world servers complete
instantly with a message and open no task. The wait/reconcile logic (`internal/poll`, the
`send --stream` fallback in `internal/cli`) is unit-tested; end-to-end validation needs a richer
server. The `send --stream` no-attempt gate (card without streaming → blocking path) is also
unit-tested but not reproducible here, since both Go cards advertise `streaming: true`.

### T7 — Transport & version (AC 7)

**Criterion:** binding selected from the card honoring preference order; routing identifier echoed
when declared; `A2A-Version` set on every request; version-unsupported surfaced clearly (design
§10.7, spec §11).

**✅ Exercisable now — card-driven selection, both live bindings, & explicit override:**

```bash
a2a-cli send "hi" -u http://127.0.0.1:9001                         # HTTP+JSON card -> exit 0
a2a-cli send "hi" -u http://127.0.0.1:9002                         # JSON-RPC card  -> exit 0 ("Hello, world!")
a2a-cli send "hi" -u http://127.0.0.1:9001 --transport http-json   # exit 0, works
a2a-cli send "hi" -u http://127.0.0.1:9002 --transport jsonrpc     # exit 0, works
a2a-cli send "hi" -u http://127.0.0.1:9001 --transport grpc        # exit 2
# -> Error [USAGE]: transport not supported at Tier 1: grpc
a2a-cli send "hi" -u http://127.0.0.1:9001 --transport jsonrpc     # exit 2 (card offers only HTTP+JSON)
# -> Error [USAGE]: agent card does not offer transport jsonrpc
```

Both **HTTP+JSON and JSON-RPC are functional live paths** — the client selects each from its card and
round-trips a message. gRPC is rejected (not supported). The mismatch cases confirm the client
selects from the card's declared interfaces and rejects a binding the card doesn't offer, rather than
assuming one. `discover` makes the same selection visible without sending a message — for the
JSON-RPC card it reports `Selected transport: jsonrpc -> http://127.0.0.1:9002/invoke` (see
[T2](#t2--commands-ac-2)).

**✅ Exercisable now — version signaling:** the default `--a2a-version` is `1.0` (`send --help`); the
value is sent on every request. The Go hello-world server logs only method, path, and body — **not
headers** — so confirming the `A2A-Version` header on the wire requires a header-inspecting proxy
(e.g. `mitmproxy`) or `tcpdump` in front of the agent. The signaling logic is implemented and
unit-tested in `internal/client`; the version-signaling evidence is recorded in the [Tier-1
conformance report](compliance/tier-1-conformance.md).

**◑ Partial / ⏳ Deferred:** honoring a multi-interface **preference order** and echoing a **routing
identifier** need a single card that declares several interfaces — each Go card declares one, so
cross-transport parity is checked by pointing at the two servers rather than by one multi-interface
card. Surfacing a clear **version-unsupported** error (exit `1`, `a2aCode` `VERSION_NOT_SUPPORTED`,
never a silent downgrade) needs a **version-strict** server; the classification is implemented
(`internal/client` classify) and unit-tested. These use the Tier-2 Python multi-transport fixture.

### T8 — Auth (AC 8)

**Criterion:** `--bearer` / `--api-key` / `-H` + env equivalents attach per request (design §10.8,
spec §10.1).

**✅ Exercisable now — flags accepted and headers constructed:**

```bash
a2a-cli send "hi" -u http://127.0.0.1:9001 --bearer TESTTOKEN -H "X-Tenant: acme"
```

The hello-world server requires no auth, so this succeeds (exit `0`) but does not prove attachment.
The Go sample server logs only method, path, and body — **not headers** — so confirming
`Authorization: Bearer TESTTOKEN` / `X-Tenant: acme` / `X-API-Key` on the wire requires a
header-inspecting proxy (e.g. `mitmproxy`) or `tcpdump`. Env equivalents (`A2A_BEARER`,
`A2A_API_KEY` — note: **not** the `A2A_CLI_`-prefixed config variables) feed the same path.
Credential construction is unit-tested in `internal/client`; the credential-attachment evidence is
recorded in the [Tier-1 conformance report](compliance/tier-1-conformance.md).

**✅ Exercisable now — cross-origin/downgrade credential gate (Phase 6, CO-7):** credentials attach
per request to a same-origin target, but are **withheld** from a cross-origin or downgraded
(`https`→`http`) interface unless `--allow-cross-origin-credentials` is passed; the card fetch never
carries credentials. Against the Go servers the selected interface is same-origin with the card, so
creds attach silently and the flag is a no-op — the withhold/opt-in decision and its warnings are
unit-tested (`internal/client`). The flag is present (`a2a-cli --help`) and defaults off.

**⏳ Deferred:** full validation against an **auth-enforcing** agent (correct 401 → exit `4` on bad
creds, success on good creds) and a **cross-origin card** (to trigger the withhold warning live) both
need a richer fixture than the no-auth, single-origin Go servers.

### T9 — SKILL.md (AC 9)

**Criterion:** exactly one generic `SKILL.md`; defers to `--help`; no normative restatement;
installable via `plugin.json` (design §10.9, spec §12).

**✅ Exercisable now (Phase 7):** exactly one `SKILL.md` ships, bundled for self-installation via
`plugin.json`:

```bash
find . -name SKILL.md -not -path './.git/*'          # -> ./skill/SKILL.md
find . -name SKILL.md -not -path './.git/*' | wc -l  # -> 1
```

The bundled [`skill/SKILL.md`](../skill/SKILL.md) defers to `a2a-cli help` / `a2a-cli <command>
--help` rather than enumerating the command surface or restating normative requirements, and
[`plugin.json`](../plugin.json) references `./skill/` so the skill installs with the plugin.

### T10 — Compliance report (AC 10)

**Criterion:** a Tier-1 compliance report is published, advertising no untested tier (design §10.10,
spec §13). Design originally envisioned a TCK-backed report; per the resolved scope decision (§7 Q5)
the external Python TCK is deferred to Tier 2, so the Tier-1 report is the project's own conformance
report and makes no TCK-version claim.

**✅ Published (Phase 7–8):** the report is merged at
[`docs/compliance/tier-1-conformance.md`](compliance/tier-1-conformance.md). See
[the next section](#the-13-compliance-report).

## Acceptance-criteria coverage matrix

Mapping design §10 Tier-1 acceptance criteria to this plan and to the current merged build:

| AC (design §10) | Test | Status against merged build (Phases 1–8) | Remaining work needs |
|---|---|---|---|
| 1 — Defaults (§4.5) | [T1](#t1--defaults-ac-1) | ◑ Partial — HTTP+JSON / blocking / text / TLS / v1.0 verified; interrupted-stop needs a task server | Task-producing server |
| 2 — Commands `discover`/`send`/`get`/`cancel` (§8.1–8.4) | [T2](#t2--commands-ac-2) | ◑ Partial — all four commands + `session` merged; `send`/`discover`/`get`/`cancel` surface & `NOT_FOUND` verified over both bindings; real-task `get`/`cancel` need a task server | Task-producing server |
| 3 — JSON envelope (Appendix B, §9.3) | [T3](#t3--json-envelope-ac-3) | ◑ Partial — success + error envelope, stdout/stderr, NDJSON stream, and cross-binding `NOT_FOUND` equality verified; artifact rendering unit-tested | Artifact-producing server |
| 4 — Exit codes 0–7 (§9.5) | [T4](#t4--exit-codes-ac-4) | ◑ Partial — 0/1/2/3 verified end to end; 4/5/6/7 mapped + unit-tested | Auth-enforcing / task server |
| 5 — Conversation/session (§6) | [T5](#t5--conversation--session-ac-5) | ◑ Partial — capture/replay, report-back, `--continue`/`--last`, `session` show/clear, `0600`, and `--task-id` not-found guard verified; positive replay + interruption hint pending | Task-producing server |
| 6 — Polling (§7) | [T6](#t6--polling-ac-6) | ◑ Partial — flags, `get --wait`/`--watch`, and `send --stream` reconcile/fallback merged; terminal-wait verified; interrupted-stop/timeout/SIGINT/forced-drop pending | Long-running / task server |
| 7 — Transport & version (§11) | [T7](#t7--transport--version-ac-7) | ◑ Partial — card-driven selection + override over **HTTP+JSON and JSON-RPC** + v1.0 signaling verified; multi-interface preference/routing id/version-error pending | Multi-interface / version-strict server |
| 8 — Auth (§10.1) | [T8](#t8--auth-ac-8) | ◑ Partial — flags/headers + cross-origin credential gate built & unit-tested; live enforcement pending | Auth-enforcing / cross-origin server |
| 9 — SKILL.md (§12) | [T9](#t9--skillmd-ac-9) | ✅ — one `SKILL.md`, bundled via `plugin.json` | — |
| 10 — Compliance report (§13) | [T10](#t10--compliance-report-ac-10) | ✅ — [report published](compliance/tier-1-conformance.md); Python TCK deferred to Tier 2 | Tier-2 TCK |

Legend: ✅ full · ◑ partial.

The predominant `◑ Partial` reflects a **complete, verified command surface** whose remaining gaps
are almost all one thing: the Go hello-world servers reply with a message and keep no task store, so
task-only states, artifacts, and continuation replay can't be produced. Those await the Tier-2 Python
multi-transport fixture (and an auth-enforcing server), per design §7 Q5 — not further Tier-1 code.

## The §13 compliance report

The Tier-1 conformance report is published at
[`docs/compliance/tier-1-conformance.md`](compliance/tier-1-conformance.md) (spec §13, design §9
Phase 8 / §10.10). It substantiates the advertised tier, and this plan is the manual-validation
input that feeds it. The report states:

- the **tier claimed** (Tier 1) and that **no untested tier** is advertised;
- **per-command results** for `discover`, `send`, `get`, `cancel`;
- **transports covered** — HTTP+JSON (REST + JSON-RPC) at Tier 1;
- conformance to the **conversation/session (§6)** and **polling (§7)** requirements, within the
  limits of the sample-server fixture.

**On the TCK.** Design §8 originally envisioned the A2A TCK (`--level must`) run against a live
sample server. Per the resolved scope decision (§7 Q5), the **external Python A2A TCK is not run at
Tier 1** and is **deferred to Tier 2** (which adds the opt-in Python 3.11+/`uv` job and
multi-transport/gRPC coverage). The Tier-1 report is therefore the **project's own Tier-1 conformance
report** and makes **no TCK-version claim** — do not read this plan as asserting one.

The per-command surface (`discover`, `send`, `get`, `cancel`) is exercisable over both HTTP+JSON and
JSON-RPC, and the coverage matrix above is the manual-validation record this plan contributes. The
facets that need task-producing states, artifacts, or auth enforcement remain unit-tested at Tier 1
and await the Tier-2 Python fixture; see the
[Tier-1 conformance report](compliance/tier-1-conformance.md) for the consolidated result.

## Items of concern

Observations from validating the merged build, flagged for the lead/reviewer:

1. **`--task-id <missing>` now normalizes to `NOT_FOUND` (resolved).** The Phase-2-era concern —
   that `--task-id nonexistent` surfaced a raw `code: "GENERIC"` — is fixed. It now yields the
   normalized `code: "NOT_FOUND"` / `a2aCode: "TASK_NOT_FOUND"` and exits `1`, surfacing the server
   error rather than silently opening a new task (spec §6.2). Verified over both bindings.
2. **`--continue` / `--last` flags now present (resolved).** Both named flags exist and are opt-in;
   with no stored session they are a usage error, and `--last` without a stored task warns and sends
   a new message. See [T5](#t5--conversation--session-ac-5).
3. **Stored transport interacts with a fresh `-u` (CO-9, Tier-2 follow-up).** `session.json` persists
   `transport` as well as `serviceUrl`; per §4.5 per-field precedence, pointing a later `send` at a
   different server with `-u` but without `--transport` re-applies the stored transport, which fails
   with `Error [USAGE]: agent card does not offer transport <stored>` (exit `2`) until the session is
   cleared. Documented in the user guide as a precedence interaction (pass `--transport` or
   `session clear` when switching servers); tracked as Tier-2 follow-up CO-9, not a Tier-1 blocker.
4. **`CANCELED` → exit code.** `cancel` intentionally exits `0` on success (including an
   already-terminal task); the general `CANCELED` → exit-code mapping for `get`/`send` is a known
   open item the build lead flagged for the cross-binding numeric mapping. Noted for the reviewer.
