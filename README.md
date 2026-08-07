# a2a-cli

A conformant **Tier-1** command-line client for the [A2A protocol](https://a2a-protocol.org),
built on the official [`a2a-go` v2 SDK](https://github.com/a2aproject/a2a-go).

> Status: **alpha** — Phase 1 (`send` vertical slice + walking skeleton). No
> stability guarantees while the spec is Draft.

## Install / build

```bash
go build ./cmd/a2a-cli
```

## Usage

Send a message to an agent and wait (blocking) for the result:

```bash
# text output (default)
a2a-cli send "hello" -u http://127.0.0.1:9001

# machine-readable Appendix B envelope on stdout
a2a-cli send "hello" -u http://127.0.0.1:9001 -o json
```

### Key flags (spec §5.2)

| Flag | Meaning |
|---|---|
| `-u, --service-url` | Base URL of the A2A agent (required) |
| `-o, --output` | Output format: `text` (default) or `json` |
| `-n, --no-tui` | Shorthand for `--output json` |
| `--transport` | `http-json` \| `jsonrpc` \| `grpc` (default: card-driven, HTTP+JSON) |
| `--context-id`, `--task-id` | Continue a conversation / send against a task |
| `--poll-interval` | Poll interval while waiting (default `2s`) |
| `--timeout` | Max wait for a task (`0` = no timeout) |
| `--a2a-version` | Protocol version to signal (default `1.0`) |
| `--bearer`, `--api-key`, `-H` | Caller-supplied credentials |
| `--insecure` | Skip TLS verification (warns) |
| `-v, --verbose` | Verbose diagnostics on stderr |

## Behavior

- **Transport** defaults to HTTP+JSON via card-driven selection; an explicit
  `--transport` overrides, else the card's declared preference is honored.
- **Blocking** by default: waits until the task reaches a terminal state, and
  stops immediately on an interrupted state (`INPUT_REQUIRED` / `AUTH_REQUIRED`).
- **Output discipline:** in `-o json`, stdout carries only the Appendix B
  envelope; all diagnostics go to stderr.
- **Exit codes** (spec §9.5): `0` success · `2` usage · `3` unreachable ·
  `4` auth · `5` task failed/rejected · `6` input required · `7` timeout.
- **Session:** the most recent conversation is captured to
  `$XDG_CONFIG_HOME/a2a-cli/session.json` (fallback `~/.config/a2a-cli/`).

## License

Apache-2.0. See [LICENSE](./LICENSE).
