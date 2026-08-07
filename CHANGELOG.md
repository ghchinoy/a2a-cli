# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/)
and [Common Changelog](https://common-changelog.org/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

_No release has been tagged yet; the Tier-1 command surface is merged and the project is in alpha._

### Added

- **`send`** — send a message to an A2A agent and block until the task settles, rendering the result as human-readable text or a normalized JSON envelope (`-o json`). [#1]
- **`discover`** — fetch and present an agent card in full, report which transport the client selects and why, and add `--validate` for a structural required-field conformance check (not JSON-Schema or security validation). [#3]
- **`get` and `cancel`** — retrieve a task by id (with `--include-artifacts`, `--history <n>`, and `--wait`/`--watch` polling) and request cancellation; `cancel` is idempotent, and an unknown task normalizes to a binding-independent `NOT_FOUND` envelope. [#5]
- **Multi-turn conversations** — a local session store records the last service URL, transport, `contextId`, and task id; resume with `--continue` / `--last`, and inspect or clear it with `session show` / `session clear`. [#6]
- **`send --stream`** — stream results over SSE when the agent card advertises the streaming capability, emitting newline-delimited JSON (NDJSON) under `-o json`; falls back to the blocking poll path when streaming is not advertised. [#8]
- **Caller-supplied authentication** — attach credentials with `--bearer`, `--api-key`, or repeatable `-H` headers (or the `A2A_BEARER` / `A2A_API_KEY` environment variables), and signal the protocol version with `--a2a-version`. [#9]
- **Deterministic exit-code scheme** (spec §9.5) mapping outcomes to stable process exit codes: `0` success, `1` failure, `2` usage, `3` unreachable, `4` auth, `5` task failed, `6` input required, `7` timeout. [#1]
- **Bundled agent skill and self-installable plugin** — [`skill/SKILL.md`](skill/SKILL.md) teaches an agent to drive the CLI non-interactively, packaged as a Claude Code plugin via [`plugin.json`](plugin.json). [#10]

### Documentation

- **Tier-1 conformance report** — a Tier-1 conformance report is published at [`docs/compliance/tier-1-conformance.md`](docs/compliance/tier-1-conformance.md), cross-linked from the README, user guide, and test plan as the Tier-1 evidence artifact; the external Python TCK remains deferred to Tier 2. [#14]

### Security

- Caller credentials are withheld from cross-origin or downgraded (`https`→`http`) targets by default and attached only per matching request; opt in with `--allow-cross-origin-credentials`. The agent-card fetch never carries credentials. [#9]
- Response bodies are capped at 64 MiB to bound memory use against oversized or hostile responses. [#9]
- Rendered output is sanitized centrally against terminal-escape injection, and service and card URLs are validated before use. [#3]
- Pinned to the patched Go 1.26.5 toolchain and `golang.org/x/text` v0.39.0 to resolve advisory GO-2026-5970. [#1]

[Unreleased]: https://github.com/ghchinoy/a2a-cli/commits/main
[#1]: https://github.com/ghchinoy/a2a-cli/pull/1
[#3]: https://github.com/ghchinoy/a2a-cli/pull/3
[#5]: https://github.com/ghchinoy/a2a-cli/pull/5
[#6]: https://github.com/ghchinoy/a2a-cli/pull/6
[#8]: https://github.com/ghchinoy/a2a-cli/pull/8
[#9]: https://github.com/ghchinoy/a2a-cli/pull/9
[#10]: https://github.com/ghchinoy/a2a-cli/pull/10
[#14]: https://github.com/ghchinoy/a2a-cli/pull/14
