# Tello CLI for coding agents (brief)

Full contract: `tello guide coding-agent`.

Rules
- Calls are real and billed. Call only when the user explicitly asked for that call; never call to test.
- The callee (`--to`, E.164 like `+821012345678`) must be a verified number of the account, else `callerNotVerified` (exit 4).
- One call per `tello call create`; it blocks until the call ends. Never run calls in parallel; use `tello call batch` for several.
- Never print, echo, log, commit or pass the API key as a flag. Missing/rejected key (exit 3): ask the user to run `tello auth login`.

Flow
1. `tello auth status --json`, then `tello doctor --json`; fix every `fail`.
2. Write a handler program; test it offline by piping sample stdin lines into it.
3. `tello call create --to +8210… [--prompt "…"] [--metadata @meta.json] [--timeout 10m] --json -- python3 handler.py`
4. The last stdout line is `{"type":"cli.result","ok":…}`; on success `data` has `callId`, `status`.
5. `tello call summary <callId> --json` → `summary`, `transcript`, `durationSeconds`, `creditCharged`.

Handler (argv after `--`, no shell)
- stdin: first `{"type":"cli.start","to":…,"prompt":…,"metadata":…}`, then every gateway frame as one JSON line. Act on `{"type":"user.turn","text":…}`.
- stdout, one per line, flushed: `{"event":"answer","data":{"text":"…"}}`, `{"event":"sendDtmf","data":{"digits":"0-9*#"}}`, `{"event":"cancel","data":{}}`.
- Answer each `user.turn` within ~3s (the gateway plays filler after that). Log to stderr only.
- Exit on stdin EOF (killed 5s after the call ends). Exiting during the call without sending `cancel` cancels it (exit 9, `data.callId` if placed); printing `cancel` then exiting ends as `cancelled` (exit 6).
- A handler that cannot start exits 9 before any call; one that crashes after starting may already have a call placed. It gets no `TELLO_API_KEY` and no terminal Ctrl-C.

Output (`--json`)
- stdout is NDJSON; every command ends with exactly one `cli.result` line. Branch on `error.code`, not `message`.
- No `cli.result` for `--help`/`help`, `--version`, a second Ctrl-C, or Ctrl-C at the `auth login` prompt.
- `call create`/`call batch` first stream gateway frames (`call.created`, `user.turn`, `agent.turn`, `call.completed`, …) and `cli.task`/`cli.retry`.

Exit codes
- 0 ok · 1 internal · 2 usage (incl. `invalidUrl`) · 3 auth · 4 refused (do not retry) · 5 invalid · 6 call did not complete (`noAnswer`, `callFailed`, `cancelled`, `timeout`, `batchIncomplete`)
- 7 connection (incl. drop during auth, `summaryTimeout`) · 8 server · 9 handler · 75 temporary (`error.retryable: true`) · 130 interrupted
- Retry only on exit 75, after waiting. For `call create`, exit 6 means a real call happened: ask the user before calling again.

Batch
- `tello call batch --tasks tasks.json --json -- python3 handler.py`, tasks = `[{"to":"…","prompt":"…","metadata":{…}}]` (`-` = stdin).
- Sequential, fresh handler per task (`cli.start` has `index`), temporary failures retried automatically.
- Per task a `cli.task` line; result `data`: `total`, `completed`, `failed`, `skipped`; partial failure exits 6. Exit 6 here does not mean every failed task placed a call: check each `cli.task`'s `callId` and `error.exitCode`.
