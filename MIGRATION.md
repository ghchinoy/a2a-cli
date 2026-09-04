# Migrating from `ghchinoy/a2a-cli` to the official `a2a`

This repository (`ghchinoy/a2a-cli`, binary **`a2a-cli`**) is **deprecated**. The official,
community-standard tool lives at:

**https://github.com/a2aproject/a2a-cli** (binary: **`a2a`**)

This guide maps the commands and behaviors of this tool to the official one so you can migrate.
Where a mapping cannot be stated with certainty from the official tool's current surface, it is
marked **"verify against official `a2a --help`"** — check the official help output before relying
on it.

## Install the official tool

The official tool is Go-based; during its pre-release, build from source:

```bash
git clone https://github.com/a2aproject/a2a-cli.git
cd a2a-cli
go build .          # produces the `a2a` binary
```

Move the resulting `a2a` binary onto your `PATH`. Confirm your version with `a2a --version`
(the official tool ships a version command; this deprecated tool never did).

For current install options (release archives, package registries), see the official repo's
README — verify against https://github.com/a2aproject/a2a-cli.

## Command mapping

The official tool uses the spec's **noun-verb** grammar (`card get`, `task get`, …) instead of
this tool's flat verbs (`discover`, `get`, …). Scripts are **not** portable between the two —
update every invocation.

| This tool (`a2a-cli`) | Official (`a2a`) | Notes |
|---|---|---|
| `a2a-cli discover <url>` | `a2a card get` | Fetch the agent card. Official emits the raw A2A `AgentCard`; this tool reshaped it (renamed `interfaces`, injected a `selection` block). |
| `a2a-cli send "..."` | `a2a send "..."` | Send a message and wait for the result. |
| `a2a-cli send "..." --stream` | `a2a send "..." --stream` *(or `a2a task subscribe`)* | Official also offers a dedicated `task subscribe`. Verify against official `a2a --help`. |
| `a2a-cli get <taskId>` | `a2a task get <taskId>` | Retrieve a task by id. |
| `a2a-cli cancel <taskId>` | `a2a task cancel <taskId>` | Cancel a task by id. |
| *(not available here)* | `a2a task list` | Official adds task listing (Tier 2). |
| `a2a-cli --bearer <token>` | `a2a --auth "Bearer <token>"` | Official attaches credentials via `--auth`; it does **not** currently expose canonical `--bearer`/`--api-key` flags. Verify against official `a2a --help`. |
| `a2a-cli --api-key <key>` | *(use `--auth` / header)* | No canonical `--api-key` flag on the official tool today. Verify against official `a2a --help`. |
| `a2a-cli session show` / `session clear` | *(no equivalent)* | The official tool is **stateless** — there is no session store to inspect or clear. |
| `a2a-cli --continue` / `--last` | *(no equivalent)* | The official tool does not persist or resume session state. Pass identifiers explicitly (`--context-id` / `--task-id`) each run. Verify flag names against official `a2a --help`. |
| `a2a-cli --include-artifacts` (on `get`) | *(no equivalent / not needed)* | Official `task get` returns artifacts and does not strip them, so there is no inclusion flag. |
| *(no version command here)* | `a2a --version` / `a2a version` | Official reports its build version. |

Multi-turn continuation via `--context-id` / `--task-id` works on both tools.

## Behavioral differences you must know

These are real, script-affecting differences — not cosmetic. Review them before migrating any
automation.

1. **Output is real A2A protocol types, not this tool's flattened schema.**
   The official tool emits the A2A protocol's own JSON types on `-o json` — a bare `Task`,
   the raw `AgentCard`, and `StreamResponse` wrappers. This deprecated tool emitted a
   *substitute* flattened schema (e.g. `{taskId, contextId, state, message, ...}` and a
   reshaped card with an injected `selection` block). **Any consumer that parses this tool's
   JSON will need to be rewritten** to read the A2A types. Field names differ (e.g. this tool's
   `taskId` is `id` on the protocol `Task`).

2. **No persisted session state.** The official tool does not store the last service URL,
   context, or task, and has no `--continue` / `--last`. Provide `-u`/service URL and any
   `--context-id` / `--task-id` on every invocation. Behavior no longer depends on hidden
   on-disk state between runs — safer for concurrent/CI use.

3. **Exit codes do not fold in the task outcome.** This tool mapped agent verdicts into the
   process exit code (task `FAILED` → `5`, `INPUT_REQUIRED` → `6`, `AUTH_REQUIRED` → `4`,
   timeout → `7`). The official tool keeps the exit code a pure "did the CLI work" signal:
   a task that ends `FAILED` or pauses `INPUT_REQUIRED` still exits **`0`**, and any CLI-level
   failure exits **`1`**. **Rewrite shell logic that branches on exit codes `4/5/6/7`** — inspect
   the task's `status.state` in the JSON output instead.

4. **Streaming output shape differs.** This tool emitted single-line JSONL with an injected
   `"type"` discriminator and a synthetic `final` event. The official tool emits the A2A
   `StreamResponse` types (`task` / `statusUpdate` / `artifactUpdate`) **without** a custom
   discriminator, but currently **pretty-prints each event across multiple physical lines**, so
   a per-line `JSON.parse` will not work the way it did here. Adjust stream parsing accordingly;
   verify the current streaming format against the official tool.

5. **Error output differs.** This tool printed a machine-readable error object
   (`{code, message, a2aCode}`) on failures. The official tool currently prints a plain
   `Error: ...` text line (on stderr) and does **not** emit a JSON error envelope, even under
   `-o json`. Scripts that classified failures by parsing this tool's error JSON will need to
   fall back to exit-code + stderr handling until the official tool ships a JSON error envelope.

For the authoritative, up-to-date behavior of the official tool, always check
https://github.com/a2aproject/a2a-cli and its `a2a --help` output.
