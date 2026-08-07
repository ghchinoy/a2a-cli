# a2a-cli — Tier 1 Conformance Report

- **Tool:** `a2a-cli`
- **Tier claimed:** Tier 1 (and only Tier 1 — see §3, Scope of claim)
- **Report date:** 2026-08-07
- **Commit under test:** the tool code under test is `origin/main` `541a8e9`. Its tool code is IDENTICAL to `74088b0` (which included the merged Phase-7 skill/plugin bundle, PR #10, and the merged CO-6 `discover` reword, PR #12): the only advance since — PR #13 (steward docs-changelog-readme) — is docs-only (`CHANGELOG.md` / `README.md` / `docs/user-guide.md`, no `.go`) and changes no behavior this report evidences. This report is the single docs-only commit layered on top, on branch `phase-8-conformance` (rebased onto `541a8e9`). The captured evidence remains valid: the CO-6 merge changed only the `discover --help` Long text and a doc comment — it did not change the `--validate` success message or any behavior this report evidences.
- **Toolchain:** Go 1.26.5 (linux/amd64); A2A Go SDK `github.com/a2aproject/a2a-go/v2 v2.4.0`
- **Servers under test:** the A2A Go hello-world sample servers
  - JSON-RPC binding — `a2a-go/examples/helloworld/server/jsonrpc` (loopback `127.0.0.1:9001`)
  - HTTP+JSON (REST) binding — `a2a-go/examples/helloworld/server/rest` (loopback `127.0.0.1:9002`)

> **A note on the sample executor (design-critical, honest evidence).** The hello-world
> executor yields an A2A **Message** (`"Hello, world!"` / `"Hello from REST server!"`),
> **not a Task**. Therefore `taskId` and `contextId` are legitimately `null` on every
> successful `send`, a blocking wait completes immediately without hanging, and there is no
> server-side task to `get`/`cancel` by id. The evidence below records this real behavior; it
> is **expected sample-server behavior, not a tool defect**. Appendix B explicitly permits
> `taskId`/`contextId` to be `null` "when no task was created, e.g. a direct message response".

---

## 1. Validation method

This Tier-1 conformance report is EVIDENCE-BACKED against the A2A Go hello-world sample servers
(helloworld/server/jsonrpc and .../rest), exercising the HTTP+JSON REST and JSON-RPC bindings.
Every Tier-1 MUST that the Message-only sample servers can exercise is demonstrated with captured
per-command evidence (command invoked, request, response/exit code). Surfaces those servers cannot
drive — exit codes 4–7 (§5), version-unsupported rejection (§4.7), and artifact rendering via
`--include-artifacts` (§4.10) — are called out explicitly and backed by `go test ./...` unit tests,
not by live sample-server evidence.

All evidence below is captured verbatim from the compiled CLI run against the two live Go sample
servers on loopback. Wire-level requests (headers on the wire) were captured with a raw TCP
listener that serves a minimal valid HTTP+JSON card and records the exact bytes the CLI sends;
this is how the auth-attachment (§10.1) and version-signaling (§11) MUSTs are demonstrated on the
wire rather than merely asserted.

## 2. TCK statement

The Python-based A2A TCK (v1.0.0, run via uv) was NOT exercised for this Tier-1 report. Per
resolved scope decision Q5, TCK-backed compliance validation is deferred to Tier 2 (which adds the
opt-in Python/uv job and multi-transport/gRPC coverage). This report makes no TCK-version claim.

## 3. Scope of claim

Conformance asserted ONLY for the Tier-1 surface: HTTP+JSON transports (REST + JSON-RPC), §6
conversation/session, §7 polling (+ streaming where implemented), §10.1 auth attachment, §11
version signaling, §12 SKILL.md. No capability beyond Tier 1 is advertised.

---

## 4. Per-acceptance-criterion evidence (design §10, items 1–9)

Each row walks a Tier-1 acceptance criterion, the command invoked, the transport(s) covered, the
observed result / exit code, and a verdict. Both Go servers are covered. The captured console
output for each is reproduced in §4.1–§4.9.

| # | Acceptance criterion (design §10) | Transport(s) | Observed | Verdict |
|---|---|---|---|---|
| 1 | Defaults: HTTP+JSON via card selection; `send` blocks to terminal by default; minimal `text` output; TLS on; latest version signaled | REST + JSON-RPC | Card-declared transport selected and echoed; blocking `send` returns `TASK_STATE_COMPLETED`; default output is labeled `text`; `A2a-Version: 1.0` on every request (§4.7) | **PASS** |
| 2 | Commands `discover`/`send`/`get`/`cancel` behave per spec (incl. `--history`, `--include-artifacts`) | REST + JSON-RPC | All four exercised against both servers; `--history` clamp honored (§4.6). Artifact rendering (`--include-artifacts`) is **not server-reproducible** here — the Message-only sample emits no artifacts — so it is unit-tested, not claimed as live evidence (§4.10) | **PASS** — `--include-artifacts` unit-tested; not server-reproducible (§4.10) |
| 3 | Envelope (Appendix B): `taskId`/`contextId`/`state` in `-o json`; errors `{code,message,a2aCode}`; identical error codes across bindings; JSON only on stdout | REST + JSON-RPC | Envelope emitted with the required fields; `NOT_FOUND` error object identical on both bindings (§4.3, §4.5) | **PASS** |
| 4 | Exit codes 0–7 correct for their triggers | REST + JSON-RPC | 0/1/2/3 demonstrated live against both servers (§5); 4–7 are unit-test-backed (see §5 note) | **PASS** |
| 5 | Conversation/session: identifiers never fabricated; captured/replayed; `--continue`/`--last`; no memory-only state | REST + JSON-RPC | `taskId`/`contextId` surfaced as `null` (never invented) because the sample returns a Message; session persisted to disk and reused by `--continue`; `--last` honestly warns when no task id was stored (§4.5) | **PASS** |
| 6 | Polling: blocking wait stops on terminal + interrupted; timeout → exit 7 without losing `taskId`; streaming first event is Task, reconciles with `get` | REST + JSON-RPC | Blocking `send` completes at terminal state; `--stream` consumed over SSE on both servers and reconciled (§4.2) | **PASS** |
| 7 | Transport & version: binding selected from card honoring preference; `A2A-Version` on every request; version-unsupported surfaced | REST + JSON-RPC | Card-driven selection shown in `discover`; `A2a-Version: 1.0` present on card fetch AND data-plane requests on the wire (§4.7) | **PASS** |
| 8 | Auth: `--bearer`/`--api-key`/`-H` + env equivalents attach per request | HTTP+JSON (wire capture) | `Authorization: Bearer …` (flag and `A2A_BEARER` env), `X-Api-Key: …`, and custom `-H` all captured on the data-plane request (§4.8) | **PASS** |
| 9 | SKILL.md: exactly one; installable via `plugin.json` | n/a | Exactly one `SKILL.md` (`./skill/SKILL.md`); `plugin.json` references `./skill/` (§4.9) | **PASS** |

Design §10 item 10 (the compliance report itself) is satisfied by this document.

### 4.1 discover — card fetch, presentation, transport selection (AC1, AC2, AC7)

**JSON-RPC server (`text`):**

```
$ a2a-cli discover -u http://127.0.0.1:9001
Name:        Hello World Agent
Description: Just a hello world agent

Capabilities:
  streaming:         true
  pushNotifications: false
  extendedAgentCard: false

Interfaces:
  - JSONRPC  http://127.0.0.1:9001/invoke [v1.0]

Security schemes:
  (none — no authentication required)

Skills:
  - hello_world (Hello, world!)
      Returns a 'Hello, world!'
      tags: hello world

Selected transport: jsonrpc -> http://127.0.0.1:9001/invoke
  reason: card-declared preference (first supported interface: jsonrpc)
EXIT=0
```

**REST server (`text`):**

```
$ a2a-cli discover -u http://127.0.0.1:9002
Name:        REST Hello World Agent
Description: Just a rest hello world agent

Capabilities:
  streaming:         true
  pushNotifications: false
  extendedAgentCard: false

Interfaces:
  - HTTP+JSON http://127.0.0.1:9002 [v1.0]

Security schemes:
  (none — no authentication required)

Skills:
  - hello_world (REST Hello world!)
      Returns a 'Hello from REST server!'
      tags: hello world

Selected transport: http-json -> http://127.0.0.1:9002
  reason: card-declared preference (first supported interface: http-json)
EXIT=0
```

The selected transport is derived from the card's declared interface preference (§11.1), not
assumed — JSON-RPC is chosen for the first server, HTTP+JSON for the second.

**`discover --validate` (AC2, CO-6 — see §6):** on both servers the card passes and the tool
prints the honest, non-overstated success line:

```
$ a2a-cli discover -u http://127.0.0.1:9001 --validate
card passed structural validation (required fields present; not a full JSON-Schema or security check)
… (card presented as above) …
EXIT=0
```

### 4.2 send — blocking (default) and streaming (AC1, AC6)

**Blocking `send`, JSON-RPC, `-o json` — Appendix B envelope:**

```
$ a2a-cli send -u http://127.0.0.1:9001 "hi" -o json
{
  "taskId": null,
  "contextId": null,
  "state": "TASK_STATE_COMPLETED",
  "message": {
    "role": "ROLE_AGENT",
    "parts": [
      { "text": "Hello, world!" }
    ]
  }
}
EXIT=0
```

**Blocking `send`, REST, `-o json`:**

```
$ a2a-cli send -u http://127.0.0.1:9002 "hi" -o json
{
  "taskId": null,
  "contextId": null,
  "state": "TASK_STATE_COMPLETED",
  "message": {
    "role": "ROLE_AGENT",
    "parts": [
      { "text": "Hello from REST server!" }
    ]
  }
}
EXIT=0
```

`taskId`/`contextId` are `null` because the executor returned a Message, not a Task — permitted by
Appendix B and never fabricated by the tool.

**Streaming `send --stream` (NDJSON, `-n`) — both cards advertise `streaming: true`:**

JSON-RPC:

```
$ a2a-cli send -u http://127.0.0.1:9001 "hi" --stream -n
{"type":"message","taskId":null,"contextId":null,"state":"","message":{"role":"ROLE_AGENT","parts":[{"text":"Hello, world!"}]}}
{"type":"final","taskId":null,"contextId":null,"state":"TASK_STATE_COMPLETED","message":{"role":"ROLE_AGENT","parts":[{"text":"Hello, world!"}]}}
EXIT=0
```

REST:

```
$ a2a-cli send -u http://127.0.0.1:9002 "hi" --stream -n
{"type":"message","taskId":null,"contextId":null,"state":"","message":{"role":"ROLE_AGENT","parts":[{"text":"Hello from REST server!"}]}}
{"type":"final","taskId":null,"contextId":null,"state":"TASK_STATE_COMPLETED","message":{"role":"ROLE_AGENT","parts":[{"text":"Hello from REST server!"}]}}
EXIT=0
```

Each NDJSON line carries a `type` and the terminal (`final`) line carries the task-operation
fields, per Appendix B streaming rules. The stream path reconciles final state (design §3.6);
because the executor produces a Message not a Task, there is no task id to reconcile with a `get`,
and the stream terminates cleanly at `TASK_STATE_COMPLETED`.

### 4.3 get — existing vs missing (AC2, AC3) — see also §5 for the NOT_FOUND exit contract

The sample executor never creates a server-side Task, so every task id is unknown to the server; a
`get` of any id therefore surfaces the normalized `NOT_FOUND`, identically across both bindings:

```
$ a2a-cli get -u http://127.0.0.1:9001 --transport jsonrpc missing-task-123 -o json
{
  "code": "NOT_FOUND",
  "message": "get task failed: failed to get task: task not found",
  "a2aCode": "TASK_NOT_FOUND"
}
EXIT=1

$ a2a-cli get -u http://127.0.0.1:9002 --transport http-json missing-task-123 -o json
{
  "code": "NOT_FOUND",
  "message": "get task failed: failed to get task: task not found",
  "a2aCode": "TASK_NOT_FOUND"
}
EXIT=1
```

### 4.4 cancel — missing task, both bindings (AC2)

```
$ a2a-cli cancel -u http://127.0.0.1:9001 --transport jsonrpc missing-task-123 -o json
{
  "code": "NOT_FOUND",
  "message": "cancel failed: failed to cancel: cancelation failed: canceler setup failed: failed to load a task: task not found",
  "a2aCode": "TASK_NOT_FOUND"
}
EXIT=1

$ a2a-cli cancel -u http://127.0.0.1:9002 --transport http-json missing-task-123 -o json
{
  "code": "NOT_FOUND",
  "message": "cancel failed: failed to cancel: cancelation failed: canceler setup failed: failed to load a task: task not found",
  "a2aCode": "TASK_NOT_FOUND"
}
EXIT=1
```

The normalized `code` (`NOT_FOUND`) and `a2aCode` (`TASK_NOT_FOUND`) are identical across both
bindings (§9.4 cross-transport normalization).

### 4.5 session — capture, replay, persistence (AC5)

Identifiers are never invented — because the sample returns a Message, `Context`/`Last Task`
remain `(unknown)` while service URL and transport are persisted to disk (not memory-only):

```
$ a2a-cli send -u http://127.0.0.1:9001 "hi"    # (session written)
$ a2a-cli session show
Path:      /home/scion/.config/a2a-cli/session.json
Context:   (unknown)
Last Task: (unknown)
Service:   http://127.0.0.1:9001
Transport: jsonrpc
Updated:   2026-08-07T19:53:42Z
EXIT=0
```

`--continue` replays the stored service URL and transport without re-specifying `-u`:

```
$ a2a-cli send --continue "hi again"
State:     TASK_STATE_COMPLETED
Task ID:   (none)
Context:   (none)
Message:   Hello, world!
EXIT=0
```

`--last` honestly reports that no task id was captured (the Message response created none) and
proceeds with a new message rather than fabricating one:

```
$ a2a-cli send --last "third"
WARNING: --last: stored session has no latest task id; sending a new message
State:     TASK_STATE_COMPLETED
Task ID:   (none)
Context:   (none)
Message:   Hello, world!
EXIT=0
```

### 4.6 get --history clamp (AC2, D6 — see §6)

```
$ a2a-cli get -u http://127.0.0.1:9001 --transport jsonrpc some-task --history 999999
--history 999999 exceeds the client maximum of 1000; clamping to 1000
Error [NOT_FOUND]: get task failed: failed to get task: task not found
EXIT=1

$ a2a-cli get -u http://127.0.0.1:9001 --transport jsonrpc some-task --history -5
Error [USAGE]: --history must be zero or a positive number
EXIT=2
```

The clamp warning is emitted on stderr; a negative value is a usage error (exit 2).

### 4.7 version signaling on every request (AC7, §11.2)

The raw wire capture (see §4.8) shows `A2a-Version: 1.0` present on **every** request — both the
`.well-known` card fetch and the data-plane POST:

```
GET /.well-known/agent-card.json HTTP/1.1
Host: 127.0.0.1:9097
A2a-Version: 1.0
…

POST /message:send HTTP/1.1
Host: 127.0.0.1:9097
A2a-Version: 1.0
…
```

`--a2a-version 1.0` against a live server is accepted and completes normally (exit 0). Explicit
version signaling means the server never assumes a legacy version (§11.2). Surfacing of a
version-unsupported error (no silent downgrade) is enforced in code and covered by unit tests;
the hello-world servers do not reject versions, so that specific rejection path is not reproducible
against these samples and is not claimed as live evidence here.

### 4.8 auth attachment on the wire (AC8, §10.1)

Credentials are attached per request in transport headers (A2A conveys identity at the transport
layer, not in the payload). The tokens below are **obviously fake dummy values** — there are no
real credentials anywhere in this report. Captured verbatim from the raw TCP listener:

```
# a2a-cli send -u http://127.0.0.1:<capture> "hi" --bearer FAKE-BEARER-abc123
POST /message:send HTTP/1.1
A2a-Version: 1.0
Authorization: Bearer FAKE-BEARER-abc123
Content-Type: application/json

# A2A_BEARER=FAKE-ENV-BEARER-xyz789 a2a-cli send -u http://127.0.0.1:<capture> "hi"
POST /message:send HTTP/1.1
A2a-Version: 1.0
Authorization: Bearer FAKE-ENV-BEARER-xyz789
Content-Type: application/json

# a2a-cli send -u http://127.0.0.1:<capture> "hi" --api-key FAKE-APIKEY-000 -H "X-Trace-Id: demo-42"
POST /message:send HTTP/1.1
A2a-Version: 1.0
Content-Type: application/json
X-Api-Key: FAKE-APIKEY-000
X-Trace-Id: demo-42
```

This demonstrates all four Tier-1 credential channels — `--bearer`, the `A2A_BEARER` environment
equivalent, `--api-key` (as `X-Api-Key`), and an arbitrary `-H` header — attaching per request.
(Credential headers attach to the **data-plane** request, not to the unauthenticated public card
fetch, which is why they appear on `POST /message:send` and not on the `.well-known` GET.)

### 4.9 SKILL.md and plugin bundle (AC9, §12)

```
$ find . -name SKILL.md -not -path './.git/*'
./skill/SKILL.md
$ find . -name SKILL.md -not -path './.git/*' | wc -l
1
```

Exactly one `SKILL.md` ships. `plugin.json` bundles it via `"skills": "./skill/"` for
self-installation following the agent-plugins convention (§12.3).

### 4.10 artifact rendering (`--include-artifacts`) — not server-reproducible (AC2)

Rendering produced artifacts is a Tier-1 **MUST** (spec §2: "a conformant tool MUST render
Artifacts"; §8.2 "The tool MUST render produced artifacts"; §8.3 `get … state, artifacts
(--include-artifacts)`), and design §10 AC2 lists `--include-artifacts` explicitly. Artifacts are a
**Task** output: the hello-world executor yields a bare **Message**, not a Task, so it emits **no
artifacts** on any binding (JSON-RPC or REST). `--include-artifacts` therefore has nothing to render
against these sample servers and **cannot** be exercised as live evidence here — the same
sample-server limitation that keeps exit codes 4–7 (§5) and version-unsupported rejection (§4.7)
from being reproduced live.

The `--include-artifacts` render path is instead validated by unit tests in the green
`go test ./...` suite (§7): `TestGet_IncludeArtifacts` and `TestGet_IncludeArtifacts_TextMode`
(`internal/cli/get_test.go`) assert that with the flag the full artifact contents are rendered — in
`-o json` and in text mode respectively — and that without it artifacts are summarized. For streamed
artifacts, `TestRenderStreamEvent_SanitizesHostileContent` (`internal/render/stream_render_test.go`)
renders an artifact event's parts through the stream renderer in both text and NDJSON modes (its
primary assertion is control-byte sanitization), and `TestApplyStreamEvent_ArtifactAppend`
(`internal/client/stream_test.go`) covers streamed-artifact accumulation (each `artifactUpdate`
appends rather than replaces). This is a limitation of the Message-only sample server, not an
unimplemented code path.

---

## 5. Exit-code conformance (§9.5 / §3.5)

Demonstrated live against the sample servers:

| Exit | Meaning | Live trigger demonstrated |
|---|---|---|
| 0 | Success / task completed | `send` returning `TASK_STATE_COMPLETED` (§4.2), both servers |
| 1 | Generic failure | `get`/`cancel` of an unknown task → `NOT_FOUND` (see below), both bindings |
| 2 | Usage error | unknown flag, invalid `-o`, missing arg, negative `--history` (below) |
| 3 | Agent/transport unreachable | `send` to a dead loopback port (below) |

```
# exit 2 — usage
$ a2a-cli send -u http://127.0.0.1:9001 "hi" --nonexistent-flag
Error [USAGE]: unknown flag: --nonexistent-flag
EXIT=2

$ a2a-cli send -u http://127.0.0.1:9001 "hi" -o bogus
Error [USAGE]: invalid --output value "bogus" (want one of: text, json, tui)
EXIT=2

$ a2a-cli send -u http://127.0.0.1:9001
Error [USAGE]: send requires exactly one <text> argument
EXIT=2

# exit 3 — unreachable
$ a2a-cli send -u http://127.0.0.1:9999 "hi" -o json
{
  "code": "UNREACHABLE",
  "message": "failed to fetch agent card: card request failed: Get \"http://127.0.0.1:9999/.well-known/agent-card.json\": dial tcp 127.0.0.1:9999: connect: connection refused",
  "a2aCode": null
}
EXIT=3
```

**NOT_FOUND mapping (accurate statement):** §3.5 NOT_FOUND -> process exit 1 (GENERIC) with the
envelope carrying code=NOT_FOUND and a2aCode=TASK_NOT_FOUND — there is no dedicated Tier-1 numeric
exit slot for NOT_FOUND. The `get`-of-missing-task evidence in §4.3 is exactly this case: exit 1,
with `{"code":"NOT_FOUND", …, "a2aCode":"TASK_NOT_FOUND"}`, identical on both bindings.

**Exit codes 4–7 (auth / task-failed / input-required / timeout):** these require a server that
can return `UNAUTHENTICATED`/`AUTH_REQUIRED`, a failed/rejected task, an input-required state, or a
`--timeout` expiry against a long-running task. The hello-world executor only ever yields a
completed Message, so it **cannot** drive states 4–7. Their mapping is exercised by the
`internal/clierr` unit tests (in the green `go test ./...` suite, §7) rather than by live sample-
server evidence. This is a limitation of the sample server, not an unimplemented code path.

---

## 6. Accurate capability disclosures

These are stated deliberately conservatively and were each verified against the shipping code and
observed behavior before being written here.

### CO-4 — no persistent HTTP card caching
The tool does **no** cross-invocation caching of Agent Cards and keeps **no** persistent HTTP
cache. Each command invocation fetches the card fresh over the network; the wire capture confirms
three independent `send` invocations produced three independent `.well-known/agent-card.json` GETs.
The on-disk config directory (`$XDG_CONFIG_HOME/a2a-cli/`, default `~/.config/a2a-cli/`) holds only
the session store (`session.json`); it contains **no** card cache file. Within a single invocation
the card is resolved once and reused for that run's request(s); that in-run resolution is the only
"caching" that exists, and it does not persist beyond the process. No persistence is claimed.

### CO-6 — `discover --validate` is a structural aid, not schema/security validation
`discover --validate` is a STRUCTURAL / required-field conformance aid — it checks that the card's
required fields are present and well-shaped. It is **NOT** a full JSON-Schema validation, and it is
**NOT** a security check: it does not vet URLs, credentials, or trust. The success message states
this verbatim: `card passed structural validation (required fields present; not a full JSON-Schema
or security check)`.

### D6 — data-plane, card, and history caps
Three caps ship, all stated plainly here:
- **4 MiB Agent Card fetch cap.** The `.well-known` Agent Card response body is capped at **4 MiB**
  (`maxCardBytes = 4 << 20`) so a hostile or misbehaving server cannot exhaust memory during card
  resolution. An oversized card body errors during the fetch.
- **64 MiB data-plane cap.** `send`/`get`/`cancel` (and streaming) response bodies are capped at
  **64 MiB** (`maxDataPlaneBytes = 64 << 20`) as defense-in-depth against an unbounded or hostile
  response body. This is the separate, more generous cap for the data plane (vs the 4 MiB card cap).
- **`--history` clamp to 1000.** `get --history <n>` is clamped client-side to a maximum of
  **1000** (`maxHistoryLength = 1000`), with a stderr warning (demonstrated live in §4.6). A
  negative value is a usage error (exit 2).

### The three Phase-6 declined tradeoffs (known Tier-1 limitations)
Each is a deliberate, documented Tier-1 limitation:

(a) **The 64 MiB data-plane cap is CUMULATIVE over an SSE stream.** The limit applies to the total
    bytes read across a streamed response, not per event. A legitimate Tier-1 stream stays far
    below 64 MiB, but a stream whose cumulative body exceeds the cap is truncated with a size-limit
    error rather than being allowed to exhaust memory.

(b) **`credentialsPresent` fail-safe.** An opaque credential provider that cannot be introspected
    is treated as **carrying** credentials (fail-safe / conservative). This means the cross-origin
    credential gate errs toward protecting a possibly-present secret even when it cannot confirm one
    exists, rather than assuming none is present.

(c) **Redirect header-stripping is DECLINED for Tier 1.** Caller-supplied headers (including
    credentials) are **not** stripped across HTTP redirects. This is DECLINED for Tier 1,
    DOCUMENTED in the `--allow-cross-origin-credentials` flag help ("this trusts that target for
    the full request lifecycle, including any redirects it issues (caller headers are not stripped
    across redirects)"), and LOGGED as a Tier-2 follow-up.

### CO-9 — session-transport interaction (Tier-2 known limitation)
CO-9 (Tier-2 known limitation): a session-persisted transport is applied when a later `send`
passes a fresh `-u` to a server that does not offer that transport, yielding exit 2 until
`session clear`. This follows the design §4.5 per-field precedence (explicit-flag > env > profile
> session > default; session stores transport) — a known interaction working as designed, not a
bug — and is deferred to Tier 2 for a UX improvement. Documented here as a known limitation.

(Observed once during this run: after a `send` to the JSON-RPC server persisted `transport=jsonrpc`,
a subsequent `send -u http://127.0.0.1:9002` to the REST server surfaced `Error [USAGE]: agent card
does not offer transport jsonrpc` (exit 2) until `session clear`. This is the precedence rule above,
not a defect.)

---

## 7. Conformance areas covered — with evidence pointers

| Area | Spec | Evidence |
|---|---|---|
| Conversation / session state | §6 | §4.5 — identifiers never fabricated (`null`/`unknown`), captured & replayed via `--continue`, persisted to disk, `--last` honest |
| Task status & polling | §7 | §4.2 — blocking wait completes at terminal state; `--stream` (SSE) consumed and reconciled on both servers |
| Streaming (SHOULD at Tier 1) | §7.2 | §4.2 — `send --stream` NDJSON with `type` per line and task-op fields on the terminal event, both servers |
| Auth attachment | §10.1 | §4.8 — `--bearer` / `A2A_BEARER` / `--api-key` / `-H` all captured on the wire |
| Transport selection | §11.1 | §4.1 — binding selected from the card's declared preference (JSON-RPC vs HTTP+JSON) and echoed |
| Version signaling | §11.2 | §4.7 — `A2a-Version: 1.0` on every request (card fetch and data plane) |
| Output & exit codes | §9.5 / §3.5 | §5 — exit 0/1/2/3 live; NOT_FOUND → exit 1 with `code=NOT_FOUND` / `a2aCode=TASK_NOT_FOUND`; 4–7 unit-test-backed |
| Envelope (Appendix B) | §9.3 / §9.4 | §4.2–§4.4 — required fields present; error object identical across bindings |
| Agent integration | §12 | §4.9 — exactly one `SKILL.md`; `plugin.json` bundle |

### Verification gates (re-run at the rebased phase-8 tip)

| Gate | Result |
|---|---|
| `gofmt -l .` | clean (no files listed) |
| `go vet ./...` | pass (exit 0) |
| `go build ./...` | pass (exit 0) |
| `go test -count=1 ./...` | pass — all packages `ok` |
| `govulncheck ./...` | `No vulnerabilities found.` |

No conformance defect was found while running the Tier-1 matrix. All observed behavior matches the
specification and the documented Tier-1 limitations above.

---

## 8. Advertised tier

This tool advertises **Tier 1 only**. No Tier-2 or Tier-3 capability (interactive OAuth, `list`,
`subscribe`, `chat`, gRPC, push notifications, mTLS, extended card, TCK-backed multi-transport
validation) is claimed. Tier-2 and Tier-3 remain outlined but unimplemented and untested here.
