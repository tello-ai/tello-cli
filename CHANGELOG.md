# Changelog

## 0.1.0 (2026-10-01)

First release. Built on `github.com/tello-ai/tello-go` v0.2.4 (WS protocol `1.0`).

- `tello call create` places one call and blocks until it ends. Turns are
  answered from the terminal (`-i`: text, `/dtmf`, `/cancel`) or by a handler
  program run after `--` that speaks NDJSON over stdin/stdout using the
  protocol's own frames. The handler starts before any call is placed, so a
  handler that cannot start costs nothing. It runs in its own process group
  without `TELLO_API_KEY`, and writes to it never stall the call. Ctrl-C/SIGTERM
  and `--timeout` cancel the call and wait up to 10 s for the gateway to
  confirm; a cancel sent before `call.created` is repeated once the call exists.
- `tello call summary` fetches the summary and transcript of a completed call;
  gateway nulls stay null in JSON.
- `tello call batch` runs a task list sequentially, retrying only refusals
  that can succeed later and stopping on account-wide refusals.
- `tello auth login|status|logout`: the key comes from `TELLO_API_KEY`, the
  OS keychain, or a 0600 file when no keychain is available. It is verified
  before it is stored and never printed.
- `tello init` writes managed skill files for Claude Code and Codex;
  `tello guide coding-agent` prints the operating contract for agents;
  `tello doctor` checks version, key, URL, authentication and skill files;
  `tello version`.
- `--json` on every command: NDJSON on stdout ending with exactly one
  `cli.result` line. Exit codes separate usage, auth, refusal, validation,
  incomplete calls, connection, server, handler, temporary (75) and
  interrupted (130) outcomes.
- Distribution: GoReleaser archives for macOS, Linux and Windows on amd64 and
  arm64 with `checksums.txt`; npm `@tello-ai/cli` plus six platform packages
  (no install scripts); `install.sh` and `install.ps1` with checksum
  verification.
