---
name: a2a-cli
description: Drive the a2a-cli command-line client to interact with A2A protocol agents non-interactively — send messages, retrieve and cancel tasks, discover agent cards, and sustain multi-turn conversations from scripts and agents. Use when an A2A agent endpoint needs to be driven from the terminal or automation.
license: Apache-2.0
---

# Driving a2a-cli

`a2a-cli` is a command-line client for the A2A protocol. This skill states the
operating rules for driving it well; it does not list the commands or flags —
discover those at runtime, because `--help` always matches the installed build.

## Discover the surface at runtime

- Run `a2a-cli help` to see the available commands.
- Run `a2a-cli <command> --help` for that command's arguments, flags, and
  behavior.

Treat `--help` as the source of truth. Do not assume a command or flag exists
without checking it there first.

## Operating rules

- **Ask for machine-readable output.** Pass `--output json` (`-o json`) and parse
  the result rather than scraping human text.
- **Let the tool wait; never sleep.** Commands block until the task settles by
  default; where a command is one-shot, use its `--wait` flag. Do not add your own
  `sleep`/poll loops.
- **Decide from the reported task state.** Read success or next-step from the
  state in the output, not from timing or log wording.
- **Sustain a conversation by replaying identifiers.** Each turn reports a
  `taskId` and a `contextId`, which the tool keeps in a local session store.
  Continue the same conversation with `--continue` (or `--last`), or pass the ids
  explicitly, so a follow-up turn joins the existing context instead of starting a
  new one. Inspect or clear that state with the session command.
- **Supply credentials non-interactively.** Provide auth via the `--bearer` /
  `--api-key` flags or the `A2A_BEARER` / `A2A_API_KEY` environment variables.
  Do not depend on an interactive login prompt in automation.

## When something fails

Re-run with `--output json`, then read the reported error object and task state
to decide what to do next; check `a2a-cli <command> --help` for the flag or
argument you were missing.
