# a2a-cli Tier-1 Validation & Conformance Test Plan

This is the validation walkthrough for experienced users and the conformance team. It maps runnable
command sequences to the design's **§10 Tier-1 acceptance criteria (AC)** and to the **§13
compliance-report** notion, so the team can execute it end to end at Phase 8 and record evidence.

The plan is intentionally comprehensive — it enumerates the full Tier-1 acceptance surface. Each
step is clearly marked as either:

- **✅ Exercisable now** — runnable against the current merged build (Phases 1–2: `send` and
  `discover` over HTTP+JSON). Expected output below was captured from the built binary against the
  live Go hello-world REST server.
- **⏳ Pending** — the criterion is specified but the code that satisfies it is not merged yet; the
  step names the phase (per design §9) that unlocks it.

> **Merged surface at time of writing.** Phases 1–2 are on `main`: the `send` command, the
> `discover` command (full card presentation, `--card-url`, `--validate`, surfaced card-driven
> transport selection), global-flag parsing, card-driven HTTP+JSON transport selection, the JSON
> envelope + error normalization, exit-code mapping, blocking wait, and session capture. `get` /
> `cancel`, streaming, `--context-id` / `--task-id` continuation semantics, and the `SKILL.md` bundle
> are not yet merged.

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

Tier-1 transport is **HTTP+JSON**. The reference server for this plan is the **Go hello-world REST**
example from the `a2a-go` SDK — it is Go-only (keeps CI in one toolchain), needs no API key, and
serves its card at the well-known path.

```bash
# from an a2a-go checkout
cd a2a-go/examples/helloworld/server/rest
go build -o /tmp/hw-rest .
/tmp/hw-rest -port 9001 &        # listens on http://127.0.0.1:9001
```

Confirm the card is reachable:

```bash
curl -s http://127.0.0.1:9001/.well-known/agent-card.json
```

```json
{"supportedInterfaces":[{"url":"http://127.0.0.1:9001","protocolBinding":"HTTP+JSON","protocolVersion":"1.0"}],"capabilities":{"streaming":true},"defaultInputModes":["text"],"defaultOutputModes":["text"],"description":"Just a rest hello world agent","name":"REST Hello World Agent","skills":[{"description":"Returns a 'Hello from REST server!'","examples":["hi","hello"],"id":"hello_world","name":"REST Hello world!","tags":["hello world"]}],"version":""}
```

This agent replies with a **direct message** (not a stateful task), so `taskId`/`contextId` are
`null` and the state is `TASK_STATE_COMPLETED`. That is sufficient to validate the Phase-1 slice, but
it cannot produce task-only states (`WORKING`, `INPUT_REQUIRED`, `FAILED`, artifacts). Steps that
need those are marked **⏳ Pending** and require either a later phase and/or a richer server.

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
- ⏳ Blocking **stop on interrupted state** (`INPUT_REQUIRED`/`AUTH_REQUIRED`) cannot be shown with
  this always-completing agent; validate with a task-producing server once `get`/continuation land
  (Phases 3–4).

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

`-o json` (or `-n`) emits the normalized card envelope on stdout — every field above plus
`defaultInputModes` / `defaultOutputModes` and the `selection` object — and nothing else:

```bash
a2a-cli discover -u http://127.0.0.1:9001 -o json | jq .selection.transport   # "http-json"
```

`--card-url` targets a card off the well-known path, and `--validate` checks the card's
required-field structure (a conformance aid, not a security check), printing a note to **stderr** and
still presenting a valid card:

```bash
a2a-cli discover -u http://127.0.0.1:9001 --validate 2>&1 1>/dev/null
# -> card is valid against the A2A card schema
```

`discover` honors card-driven transport selection: an explicit `--transport http-json` reports
`reason: explicit --transport=http-json`; `--transport grpc` is rejected (exit `2`,
`Error [USAGE]: transport not supported at Tier 1: grpc`); and `--transport jsonrpc` is rejected
because the card doesn't advertise it (exit `2`, `Error [USAGE]: agent card does not offer transport
jsonrpc`). An unreachable target exits `3` with a normalized `UNREACHABLE` error, and calling
`discover` with neither `-u` nor `--card-url` is a usage error (exit `2`).

**⏳ Pending — `get` + `cancel` (Phase 3).** These commands are not merged. Verify their absence:

```bash
a2a-cli --help    # Available Commands: completion, discover, help, send
```

`get` and `cancel` should **not** appear. Re-run T2 in full once Phase 3 lands.

- ✅ `discover` full-card presentation, `-o json` envelope, `--card-url`, `--validate`, and surfaced
  transport selection — verified against the built binary.
- ⏳ `discover` against a **multi-interface** card (preference ordering, routing identifier) needs the
  Tier-2 Python fixture — the hello-world card declares a single HTTP+JSON interface.

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

- ✅ Envelope top-level fields, error object shape, stdout/stderr split — verified.
- ⏳ `artifacts` array population and NDJSON streaming events require artifacts/streaming (Phases 3/5).
- ⏳ Cross-**binding** identical error codes: only HTTP+JSON is functional now, so equality across
  JSON-RPC/gRPC is untestable until ≥2 transports are live (Tier 2).

### T4 — Exit codes (AC 4)

**Criterion:** all of `0`–`7` reachable and correct for their triggers (design §10.4, spec §9.5).

**✅ Exercisable now:**

| Code | Trigger | Command | Verified |
|---|---|---|---|
| `0` | Task completed | `a2a-cli send "hi" -u http://127.0.0.1:9001` | ✅ |
| `2` | Usage error | `a2a-cli send -u http://127.0.0.1:9001` (no message arg) → `Error [USAGE]: send requires exactly one <text> argument` | ✅ |
| `2` | Usage error | `a2a-cli send "hi" -u http://127.0.0.1:9001 --transport grpc` → `Error [USAGE]: transport not supported at Tier 1: grpc` | ✅ |
| `3` | Unreachable | `a2a-cli send "hi" -u http://127.0.0.1:9999` → `Error [UNREACHABLE]: ...connection refused` | ✅ |

Check a code explicitly:

```bash
a2a-cli send -u http://127.0.0.1:9001 ; echo "exit=$?"   # exit=2
```

**⏳ Pending — codes not reachable against the hello-world agent:**

- `1` generic — reachable (e.g. sending `--task-id <missing>` yields a generic error today), but the
  clean not-found path is a continuation concern (Phase 4). See [items of concern](#items-of-concern).
- `4` auth, `5` task failed/rejected, `6` input required, `7` timeout — require an agent that returns
  those states or a long-running task. Validate against a richer server once continuation/streaming
  land (Phases 3–6). The mapping itself is implemented (`internal/clierr`) and unit-tested.

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

**⏳ Pending:**

- Full `--context-id` / `--task-id` continuation **semantics** (surface server not-found/state
  conflict rather than silently starting a new task) — Phase 4. The flags parse today, but against a
  message-only agent there is no task to continue.
- **Resume-command hint** (`a2a-cli send --task-id <id> "<reply>"`) on interruption — Phase 4, needs
  an interruptible task.
- `--continue` / `--last` explicit flags — Phase 4 (today the session auto-supplies the last URL, but
  there is no named `--continue` flag yet).
- **`0600` secret file** enforcement — Tier 1 does not persist secrets (caller-supplied creds are not
  stored); the mode-restricted `credentials.json` is a Tier-2 concern.

### T6 — Polling (AC 6)

**Criterion:** blocking wait stops on terminal **and** interrupted states; `--poll-interval` /
`--timeout` honored; timeout → exit `7` without losing the printed `taskId`; SIGINT interruptible;
if streaming implemented, first event is `Task` and post-reconnect reconciles with `get` (design
§10.6, spec §7).

**✅ Exercisable now — flags present and defaulted:** `--poll-interval` (default `2s`) and
`--timeout` (default `0` = no timeout) are accepted on `send` (`a2a-cli send --help`). Blocking to a
terminal state is verified in T1.

**⏳ Pending:** the polling loop's interrupted-state stop, `--timeout` → exit `7` with the `taskId`
preserved, and SIGINT interruptibility all need a long-running / task-producing agent — the
hello-world agent completes instantly. Validate at Phase 3 (`get --wait` poll loop) / Phase 5
(streaming). The wait logic in `internal/poll` is unit-tested.

### T7 — Transport & version (AC 7)

**Criterion:** binding selected from the card honoring preference order; routing identifier echoed
when declared; `A2A-Version` set on every request; version-unsupported surfaced clearly (design
§10.7, spec §11).

**✅ Exercisable now — card-driven selection & explicit override:**

```bash
a2a-cli send "hi" -u http://127.0.0.1:9001 --transport http-json   # exit 0, works
a2a-cli send "hi" -u http://127.0.0.1:9001 --transport grpc        # exit 2
# -> Error [USAGE]: transport not supported at Tier 1: grpc
a2a-cli send "hi" -u http://127.0.0.1:9001 --transport jsonrpc     # exit 2
# -> Error [USAGE]: agent card does not offer transport jsonrpc
```

The last case confirms the client selects from the card's declared interfaces and rejects a binding
the card doesn't offer, rather than assuming one. `discover` makes the same selection visible without
sending a message — its `Selected transport` line reports the chosen binding, URL, and reason (see
[T2](#t2--commands-ac-2)).

**✅ Exercisable now — version signaling:** the default `--a2a-version` is `1.0` (`send --help`); the
value is sent on every request. The Go hello-world server logs only method, path, and body — **not
headers** — so confirming the `A2A-Version` header on the wire requires a header-inspecting proxy
(e.g. `mitmproxy`) or `tcpdump` in front of the agent. The signaling logic is implemented and
unit-tested in `internal/client`.

**⏳ Pending:** honoring a multi-interface **preference order**, echoing a **routing identifier**, and
surfacing a clear **`VersionNotSupportedError`** need a multi-interface / version-strict server
(Tier-2 Python fixture). Only HTTP+JSON is functional now.

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
header-inspecting proxy (e.g. `mitmproxy`) or `tcpdump`. Env equivalents (`A2A_CLI_BEARER`,
`A2A_CLI_API_KEY`) feed the same path. Credential construction is unit-tested in `internal/client`.

**⏳ Pending:** full validation against an **auth-enforcing** agent (correct 401 → exit `4` on bad
creds, success on good creds) needs a server that gates on credentials — Phase 6 / a richer fixture.

### T9 — SKILL.md (AC 9)

**Criterion:** exactly one generic `SKILL.md`; defers to `--help`; no normative restatement;
installable via `plugin.json` (design §10.9, spec §12).

**⏳ Pending — Phase 7.** No `SKILL.md` or `plugin.json` is merged yet. Confirm absence:

```bash
ls SKILL.md skill/ plugin.json 2>&1   # expected: no such file(s)
```

Re-run this check when Phase 7 lands: assert exactly one skill file exists, it contains no full
command enumeration, and it restates no normative requirements.

### T10 — Compliance report (AC 10)

**Criterion:** a Tier-1 compliance report is published, TCK-backed, advertising no untested tier
(design §10.10, spec §13). **⏳ Pending — Phase 8.** See the next section.

## Acceptance-criteria coverage matrix

Mapping design §10 Tier-1 acceptance criteria to this plan and to the current merged build:

| AC (design §10) | Test | Status against merged build | Unlocked by |
|---|---|---|---|
| 1 — Defaults (§4.5) | [T1](#t1--defaults-ac-1) | ◑ Partial — HTTP+JSON / blocking / text / TLS / v1.0 verified; interrupted-stop pending | Phases 3–4 |
| 2 — Commands `discover`/`send`/`get`/`cancel` (§8.1–8.4) | [T2](#t2--commands-ac-2) | ◑ Partial — `send` + `discover` verified; `get`/`cancel` pending | Phase 3 |
| 3 — JSON envelope (Appendix B, §9.3) | [T3](#t3--json-envelope-ac-3) | ◑ Partial — success + error envelope + stdout/stderr verified; artifacts/NDJSON/cross-binding pending | Phases 3/5, Tier 2 |
| 4 — Exit codes 0–7 (§9.5) | [T4](#t4--exit-codes-ac-4) | ◑ Partial — 0/2/3 verified; 1/4/5/6/7 mapped + unit-tested, not yet triggerable end to end | Phases 3–6 |
| 5 — Conversation/session (§6) | [T5](#t5--conversation--session-ac-5) | ◑ Partial — capture/replay + report-back verified; continuation semantics, resume hint, `--continue` pending | Phase 4 |
| 6 — Polling (§7) | [T6](#t6--polling-ac-6) | ◑ Partial — flags present, terminal-wait verified; interrupted-stop/timeout/SIGINT pending | Phases 3/5 |
| 7 — Transport & version (§11) | [T7](#t7--transport--version-ac-7) | ◑ Partial — card-driven selection + override + v1.0 signaling verified; preference order/routing id/version error pending | Tier 2 |
| 8 — Auth (§10.1) | [T8](#t8--auth-ac-8) | ◑ Partial — flags/headers built + unit-tested; enforcement pending | Phase 6 |
| 9 — SKILL.md (§12) | [T9](#t9--skillmd-ac-9) | ✗ Not merged | Phase 7 |
| 10 — Compliance report (§13) | [T10](#t10--compliance-report-ac-10) | ✗ Not merged | Phase 8 |

Legend: ✅ full · ◑ partial · ✗ not present.

## The §13 compliance report

At **Phase 8**, the team produces the Tier-1 compliance report that substantiates the advertised
tier (spec §13, design §9 Phase 8 / §10.10). This plan is the manual validation input to it. The
report must state:

- the **tier claimed** (Tier 1) and that **no untested tier** is advertised;
- **per-command pass/fail** for `discover`, `send`, `get`, `cancel`;
- the **A2A TCK version(s)** exercised — the TCK `--level must` subset run against a live sample
  server (design §8; the TCK requires Python 3.11+/`uv`, provisioned at Tier 2 per §7 Q5);
- **transports covered** — HTTP+JSON at Tier 1;
- conformance to the **conversation/session (§6)** and **polling (§7)** requirements.

Until Phases 3–8 land, only the `send` and `discover` / HTTP+JSON rows of this report are
exercisable; the coverage matrix above is the interim record of what has been validated against the
merged build.

## Items of concern

Observations from validating the merged build, flagged for the lead/reviewer:

1. **Invalid `-o` value is silently accepted.** `a2a-cli send "hi" -u <url> -o yaml` does not error;
   it falls back to text output and exits `0`. Spec §9 treats bad flag values as a usage error
   (exit `2`). Confirm whether Phase 1 intends to validate the `--output` value.
2. **`--task-id <missing>` returns a raw generic error (exit `1`).** Against the sample server,
   `--task-id nonexistent` yields `code: "GENERIC"` with the SDK's `"task not found"` message rather
   than a normalized `NOT_FOUND` code. Full continuation semantics (spec §6.2, "surface server error,
   not silently start a new task") are a Phase 4 concern; noting that the current code path surfaces
   the error but does not yet normalize it.
3. **No `--continue` / `--last` flag yet.** Session auto-supplies the last service URL, which covers
   part of spec §6.4, but the named flag is not present. Expected at Phase 4 — documented as pending,
   not as working.
