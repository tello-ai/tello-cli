# Changelog

## Unreleased
- REST call safety: handle closed stdout, preserve unknown outcomes and
  idempotency keys on output failures, and stop retries when output fails.
  REST endpoints require HTTPS except on loopback; authentication and call
  termination responses are validated.
- `call get`, `call end` and `call transfer` refuse call ids not in the form
  the API prints (`c_` and a positive integer without leading zeros), and
  `--idempotency-key` refuses keys the API rejects (empty, over 255 bytes, a
  comma) or that cannot be sent (control characters), with a usage error
  (exit 2) before any request.


- Added REST-backed `agent list` and `crew list`, and portal-agent or crew
  calls via `call create --agent|--crew` with start variables. Create returns
  when accepted unless `--wait` follows the call; waiting can request an end
  on timeout or interruption, but a failed request or lost confirmation means
  the call may still be active.
- Added `call get`, `call list`, and `call end` for account calls, including
  WebSocket calls. `call get` replaces `call summary`; summaries, transcripts,
  and credit can still be null immediately after a call ends.
- Added `call transfer <callId> --to <number>`: requests an immediate transfer
  of an answered call (inbound calls included) to a phone number. The result
  is the call at acceptance; the call stays `inProgress` while the transfer
  runs or after it fails, and a request sent while another transfer runs is
  accepted but dropped, so check it with `call get`. Each run sends a new
  idempotency key unless `--idempotency-key` is given; failures, Ctrl-C and
  output failures after acceptance report it. Undocumented 5xx responses are
  `transferOutcomeUnknown` (exit 8, not retryable). `callNotInProgress` (still
  dialing or being handed to a counselor) is retryable (exit 75).
- Extended `call batch` to place portal-agent and crew calls sequentially.
  Documented pre-dial temporary refusals are retried with a per-task REST
  idempotency key within that batch run; an uncertain create outcome or
  unfinished call stops the remaining batch. Restarting it generates new keys
  and can redial tasks. Interactive and handler calls continue to use the
  WebSocket gateway and do not receive REST idempotency protection.
- REST creates use a generated or supplied idempotency key. Stored responses
  are replayed for 24 hours, not indefinitely; if saving an accepted response
  fails, the same key can cause another call after the in-progress record
  expires. When an outcome is unknown, inspect call records before redialing.
- Authentication verification now uses the REST API. `--api-url` and
  `TELLO_API_URL` configure REST separately from the WebSocket `--url` and
  `TELLO_URL`; `doctor` checks both.
- JSON output includes `cli.status` for REST wait polling alongside existing
  gateway frames and batch events. REST errors use camelCase codes, and failed
  create requests can report the key in `data.idempotencyKey`.

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
