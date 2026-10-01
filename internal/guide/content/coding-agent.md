# Tello CLI: operating guide for coding agents

`tello` places outbound phone calls through the Tello gateway. While a call
runs, every time the callee speaks, a program you provide (the handler)
decides what the Tello agent says next. After the call you can fetch its
summary and transcript.

## Safety rules

- **Calls are real and billed.** Every `tello call create` dials a real phone
  and spends the account's call credit. Place a call only when the user
  explicitly asked for that call. Never place a call to "test" something;
  test handler logic offline (see "Testing a handler").
- **The callee must be a verified number of the account.** Otherwise the call
  is refused with `callerNotVerified` (exit 4). Use E.164 form, e.g.
  `+821012345678`.
- **One call per invocation.** `tello call create` places exactly one call and
  blocks until it ends. Do not run calls in parallel: the account has a limited
  number of concurrent lines. For several calls use `tello call batch`, which
  calls one after another.
- **Never print, echo, log or commit API keys.** Do not pass a key as a flag
  or argument, and do not read the keychain or `credentials.json` yourself.
  If no key is configured, ask the user to run `tello auth login` in their own
  terminal, or to set `TELLO_API_KEY` in the environment.
- The agent's persona and instructions are configured on the server (portal).
  `--prompt` and `--metadata` only add context to one call.

## Recommended flow

1. `tello auth status --json`: a key is configured and the gateway accepts it.
   Exit 3 means no key or a rejected key: ask the user to run `tello auth login`.
   Exit 7 is a network or gateway problem, not the key.
2. `tello doctor --json`: checks version, key, gateway URL, authentication and
   skill files. Fix every `fail` before calling.
3. Write a handler program (see "Handler protocol") and test it offline.
4. Place the call:

   ```sh
   tello call create --to +821012345678 --prompt "Confirm tomorrow's 3pm booking" --json -- python3 handler.py
   ```

5. Read the **last** stdout line. With `--json` it is the `cli.result`
   (exceptions are listed under "Output contract"):
   `{"type":"cli.result","ok":true,"data":{"callId":"…","status":"completed"}}`.
6. Fetch the outcome: `tello call summary <callId> --json`.

## Commands

| Command | Purpose |
| --- | --- |
| `tello auth login [--no-verify]` | Store an API key (hidden prompt on a TTY, else the first line of stdin). Verifies it first. For humans |
| `tello auth status [--offline]` | Where the key comes from; whether the gateway accepts it |
| `tello auth logout` | Remove the stored key |
| `tello call create --to <number> [--prompt …] [--metadata <JSON\|@file>] [--timeout <dur>] (-i \| -- <handler argv…>)` | One call; blocks until it ends |
| `tello call summary <callId> [--timeout 15s]` | Summary and transcript of a completed call |
| `tello call batch --tasks <file\|-> [--max-retries 3] [--retry-delay 5s] [--timeout <dur>] (-i \| -- <handler argv…>)` | Several calls, one after another |
| `tello init [--target claude,codex] [--dir .]` | Install or refresh the agent skill files |
| `tello guide` | List guide topics |
| `tello guide coding-agent [--brief]` | This guide (`--brief`: the compact version) |
| `tello doctor` | Environment checks |
| `tello version` | CLI, SDK and protocol versions |

Global flags: `--json` (machine output) and `--url <ws(s)://…>` (gateway;
precedence `--url` > `TELLO_URL` > `wss://api.telloai.io/sdk`). A gateway URL
that is not `ws://` or `wss://` with a host fails with exit 2 (`invalidUrl`).

`-i` lets a human answer from the terminal; as an agent, use a handler.
`--timeout` cancels the call when it elapses (exit 6, code `timeout`).
`--metadata` takes a JSON object or `@path` to a JSON file.

## Output contract (`--json`)

- stdout is NDJSON: one JSON object per line. Warnings and logs go to stderr.
- Every command ends with exactly one `cli.result` line:

  ```json
  {"type":"cli.result","ok":true,"data":{}}
  {"type":"cli.result","ok":false,"error":{"kind":"refused","code":"insufficientCredit","message":"…","retryable":false,"exitCode":4},"data":{}}
  ```

- Branch on `error.code` (and the exit code); `message` is for display only.
  `error.question` is present only for `callRejected`. `data` may be present on
  failure too, e.g. the `callId` and last `status` of a call that started.
- Exceptions, which end without a `cli.result` line: `--help` / `help` and
  `--version` (plain text, exit 0); a second Ctrl-C / SIGTERM (immediate exit
  130); Ctrl-C at the hidden `tello auth login` prompt (exit 130). Do not pass
  `--help` or `--version` when you parse output.
- `call create` and `call batch` stream lines before the result: gateway
  frames verbatim (`call.created`, `call.statusChanged`, `user.turn`,
  `agent.turn`, `answer.accepted`, `dtmf.accepted`, `call.completed`,
  `call.noAnswer`, `call.failed`, `error`), plus `cli.task` and `cli.retry`
  lines in batch mode.
- Do not assume `dialing` or `ringing` statuses arrive; a call may jump to
  `inProgress`.

## Handler protocol

The handler is the command after `--`. It is started directly (no shell)
before the CLI connects. If it cannot be started at all (command not found,
not executable), the CLI exits 9 (`handlerStartFailed`) without placing a
call. A handler that starts and then crashes (a bad import, missing
configuration) may crash after the call was already placed: the CLI then
cancels the call and exits 9 (`handlerExited`), with `data.callId` set when
a call was created. So test the handler offline first (see "Testing a
handler") instead of relying on a crash to stop the call.

The handler does not receive `TELLO_API_KEY` in its environment; it never
needs the key. It runs in its own process group, so a Ctrl-C in the terminal
reaches only the CLI, which cancels the call; the handler still gets the
terminal event and then stdin EOF.

**stdin** (NDJSON, one object per line):

1. First line: `{"type":"cli.start","to":"…","prompt":"…","metadata":{…}}`
   (batch adds `"index"`, the task's position).
2. Then every gateway frame of the call, the same set as the stdout stream
   above. The one to act on is `user.turn`:
   `{"type":"user.turn","callId":"…","turnIndex":0,"text":"Hello?",…}`.

**stdout**: write one command per line. Three are allowed:

```json
{"event":"answer","data":{"text":"Hi, I'm calling to confirm your booking."}}
{"event":"sendDtmf","data":{"digits":"1234#"}}
{"event":"cancel","data":{}}
```

- `answer` makes the agent say `text`; optional `messageId` and `requestId`
  are echoed in `answer.accepted`. `agent.turn` follows when it was spoken.
- `sendDtmf` sends keypad tones; digits are `0-9`, `*`, `#` only.
- `cancel` hangs up; the call ends with status `cancelled` (exit 6). The
  handler may exit right after printing `cancel`; that still ends as
  `cancelled` (exit 6), not as a handler failure. A `cancel` printed before
  `call.created` is re-sent once the call exists.
- Answer each `user.turn` quickly, within about 3 seconds: after that the
  gateway plays a filler phrase.
- Flush stdout after every line (`print(..., flush=True)` in Python). Blank
  lines are ignored; invalid JSON or other commands are ignored with a warning.
- Log to stderr only; it is passed through to the CLI's stderr. Anything on
  stdout must be a command.

**Lifecycle**: when the call ends the CLI closes the handler's stdin, waits 5
seconds, then kills its process group; exit on stdin EOF. Its exit code then
does not matter. If the handler exits while the call is live without having
sent `cancel`, the CLI cancels the call and exits 9 (`handlerExited`).

Minimal Python handler:

```python
import json, sys

for line in sys.stdin:
    msg = json.loads(line)
    if msg.get("type") == "user.turn":
        reply = "Thanks. Is 3pm tomorrow still good for you?"  # decide from msg["text"]
        print(json.dumps({"event": "answer", "data": {"text": reply}}), flush=True)
    elif msg.get("type") == "cli.start":
        print("starting call to", msg["to"], file=sys.stderr)
```

### Testing a handler

Feed it sample lines without placing a call, and check that stdout holds only
valid commands:

```sh
printf '%s\n' '{"type":"cli.start","to":"+821012345678","prompt":"","metadata":{}}' \
  '{"type":"user.turn","callId":"c1","turnIndex":0,"text":"Hello?"}' | python3 handler.py
```

## Results and exit codes

| exit | kind | meaning | typical `error.code` |
| --- | --- | --- | --- |
| 0 | | success | |
| 1 | `internal` | unexpected error | |
| 2 | `usage` | bad flags or arguments | `usage`, `invalidUrl` |
| 3 | `auth` | no key or key rejected | `apiKeyMissing`, `unauthenticated` |
| 4 | `refused` | account policy refusal; retrying will not help | `insufficientCredit`, `callerNotVerified`, `noRepresentativeNumber` |
| 5 | `invalid` | request rejected by validation | `toRequired`, `callIdRequired`, `callNotFound`, `callNotCompleted`, `dtmfDigitsRequired`, `dtmfDigitsInvalid`, `callRejected`, `noActiveCall` |
| 6 | `callEnded` | the call did not complete | `noAnswer`, `callFailed`, `cancelled`, `timeout` (`call create --timeout`), `batchIncomplete` |
| 7 | `connection` | could not connect, the connection dropped (also during authentication), or `call summary` timed out | `connectionFailed`, `connectionClosed`, `sessionReplaced`, `summaryTimeout` |
| 8 | `server` | permanent service-side error | `internalError`, `callProviderUnauthorized`, `callSetupFailed` |
| 9 | `handler` | handler process failed | `handlerStartFailed`, `handlerExited` |
| 75 | `temporary` | try again later | `concurrentLimitExceeded`, `callProviderDraining`, `callProviderUnavailable`, `callAlreadyActive` |
| 130 | `interrupted` | Ctrl-C / SIGTERM; a live call is cancelled | `interrupted` |

`call create` succeeds (exit 0) only when the call ends with
`call.completed`; `data` holds `callId` and `status`.

## Retries

- Retry only when `error.retryable` is true (exit 75). Wait first (seconds to
  a minute), then run the same command again.
- Never retry automatically on any other exit code. Exit 4 and 5 need a fix.
  For `call create`, exit 6 means a call really happened (and may be billed),
  so calling again rings the same person again: ask the user. On exit 7 or 9
  check whether `data.callId` exists before deciding anything.
- For `call batch`, exit 6 (`batchIncomplete`) only says that some task did
  not complete; it does not mean every failed task placed a call. Inspect each
  `cli.task` line: a task with `callId` placed a call; `error.code` and
  `error.exitCode` say why it failed. Re-run only the tasks that need it.

## `call summary`

`tello call summary <callId> --json` works only for completed calls
(otherwise `callNotCompleted`, exit 5). `data` holds `callId`, `status`,
`durationSeconds`, `transcript`, `summary` and `creditCharged` (the last four
may be null). If the gateway does not answer within `--timeout` (default
15s), it fails with `summaryTimeout` (exit 7).

## Batch

```sh
tello call batch --tasks tasks.json --json -- python3 handler.py
```

- `tasks.json` is a JSON array: `[{"to":"+8210…","prompt":"…","metadata":{…}}]`.
  `--tasks -` reads it from stdin.
- Calls run one after another, each on a new connection with a fresh handler
  process (`cli.start` carries `index`).
- Temporary failures are retried automatically (`--max-retries`, default 3;
  `--retry-delay`, default 5s, doubling each time), each announced by a
  `cli.retry` line.
- After each task: `{"type":"cli.task","index":0,"to":"…","ok":true,"callId":"…","status":"completed","attempts":1}`
  (with `error` when it failed).
- The batch stops (remaining tasks are skipped) on `auth` errors, interruption,
  and account-wide refusals: `insufficientCredit`, `noRepresentativeNumber`,
  `callProviderUnauthorized`. Other failures are recorded and the batch moves on.
- Result `data`: `total`, `completed`, `failed`, `skipped`. Exit 0 when every
  task completed, the stopping error's exit code when it stopped, otherwise
  exit 6 (`batchIncomplete`). Failed tasks can have any cause (refusal,
  connection, handler, call not completed); see "Retries" for how to read
  their `cli.task` lines.
