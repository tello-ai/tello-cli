# Tello CLI for coding agents (brief)

Full contract: `tello guide coding-agent`.

Rules
- Calls are real and billed. Call only when the user explicitly asked for that call; never call to test.
- The callee (`--to`, E.164 like `+821012345678`) must be a verified number of the account, else `calleeNotVerified` / `callerNotVerified` (exit 4).
- One call at a time. `--agent`/`--crew` creates return once accepted: use `--wait` (or poll `tello call get`) before the next call; use `tello call batch` for several.
- A failed `--agent`/`--crew` create reports `data.idempotencyKey`; retry only with `--idempotency-key <it>`. On `callOutcomeUnknown`, regardless of exit code, check `tello call list` and ask the user; never re-dial with a new key.
- Never print, echo, log, commit or pass the API key as a flag. Missing/rejected key (exit 3): ask the user to run `tello auth login`.

Flow (portal agent answers; default)
1. `tello auth status --json`, then `tello doctor --json`; fix every `fail`.
2. `tello agent list --json` (or `tello crew list --json`); confirm the id with the user.
3. `tello call create --to +8210… --agent <id> [--var name=value]… --wait [--timeout 10m] --json`
4. `--wait` streams `{"type":"cli.status","callId":"c_…","status":…}`; the last line is `{"type":"cli.result","ok":…}` with the call object in `data` (`status`, `durationSeconds`, `summary`, `transcript`, `creditCharged`; may be null right after the end).
5. Later: `tello call get <callId> --json`. Also `tello call list [--status …] [--agent …] [--created-after …] --json`, `tello call end <callId> --json`.

Flow (you answer each turn)
- `tello call create --to +8210… [--prompt "…"] [--metadata @meta.json] [--timeout 10m] --json -- python3 handler.py` blocks until the call ends; `data` has `callId`, `status`.
- Handler stdin: first `{"type":"cli.start","to":…,"prompt":…,"metadata":…}`, then every gateway frame as one JSON line. Act on `{"type":"user.turn","text":…}`.
- Handler stdout, one per line, flushed: `{"event":"answer","data":{"text":"…"}}`, `{"event":"sendDtmf","data":{"digits":"0-9*#"}}`, `{"event":"cancel","data":{}}`.
- Answer each `user.turn` within ~3s. Log to stderr only. Exit on stdin EOF (killed 5s after the call ends). Exiting during the call without `cancel` cancels it (exit 9); printing `cancel` then exiting ends as `cancelled` (exit 6). Test the handler offline by piping sample stdin lines into it.

Output (`--json`)
- stdout is NDJSON; every command ends with exactly one `cli.result` line. Branch on `error.code`, not `message`. `data` may carry `missingVariables` (pass them with `--var`).
- No `cli.result` for `--help`/`help`, `--version`, a second Ctrl-C, or Ctrl-C at the `auth login` prompt.

Exit codes
- 0 ok · 1 internal · 2 usage (incl. `invalidUrl`) · 3 auth · 4 refused (do not retry) · 5 invalid · 6 call did not complete (`noAnswer`, `callFailed`, `cancelled`, `timeout`, `batchIncomplete`)
- 7 connection · 8 server (incl. `callOutcomeUnknown`) · 9 handler · 75 temporary (`error.retryable: true`) · 130 interrupted (termination is requested when possible; confirm the call's status)
- Retry only on exit 75, after waiting, with the same `--idempotency-key`. Exit 6 means a real call happened: ask the user before calling again.

Batch
- `tello call batch --tasks tasks.json --agent <id> --json` with tasks `[{"to":"…","variables":{…}}]`, or `… --json -- python3 handler.py` with tasks `[{"to":"…","prompt":"…","metadata":{…}}]` (`-` = stdin).
- Sequential; temporary failures retried automatically; account-wide and target refusals stop the batch.
- Per task a `cli.task` line; result `data`: `total`, `completed`, `failed`, `skipped`; partial failure exits 6. Check each `cli.task`'s `callId` and `error.exitCode`.
