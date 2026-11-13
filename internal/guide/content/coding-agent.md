# Tello CLI: operating guide for coding agents

`tello` places outbound phone calls through Tello. Pick who answers the
callee:

- **A portal agent or crew** (`--agent <id>` / `--crew <id>`): the agent
  configured in the Tello portal runs the conversation. The CLI uses the REST
  API: it places the call, follows it with `--wait`, and reads results with
  `tello call get`. This is the default choice.
- **You or a program, turn by turn** (`-i` / a handler after `--`): every time
  the callee speaks, a program you provide (the handler) decides what the
  Tello agent says next. The CLI uses the WebSocket gateway.

`tello call get`, `list`, `end` and `transfer` work for calls placed either way.
For agents that generate outbound scenarios, `tello call draft` lets the user
review the scenario before the call is placed. `tello tool`, `tello agent`
and `tello crew` also manage the tools, agents and crews themselves.

## Safety rules

- **Calls are real and billed.** Every `tello call create` dials a real phone
  and spends the account's call credit. Place a call only when the user
  explicitly asked for that call. Never place a call to "test" something;
  test handler logic offline (see "Testing a handler").
- **Without an assigned number, the callee must be verified.** An account
  with an active assigned outbound number (a dedicated 070 number, paid
  plans) can call any number. An account without one (Free plan) can call
  only numbers verified on the account in the portal; any other callee is
  refused with `calleeNotVerified` (REST) or `callerNotVerified` (handler
  calls), exit 4: tell the user, do not retry. Use E.164 form, e.g.
  `+821012345678`.
- **One call at a time.** The account has a limited number of concurrent
  lines. With `--agent`/`--crew`, `tello call create` returns as soon as the
  call is accepted, so pass `--wait` (or poll `tello call get`) before placing
  the next one. Handler and `-i` calls block until they end. For several calls
  use `tello call batch`, which calls one after another.
- **Never place the same call twice by accident.** A failed `--agent`/`--crew`
  create reports `data.idempotencyKey`. Reuse it for the same request. The
  service retains successful and outcome-unknown responses for 24 hours;
  if response storage fails, the same key can dial again after five minutes.
  On `callOutcomeUnknown` or an ambiguous network failure, check
  `tello call list` and ask the user before retrying.
- **Transfer a call only when the user asked for it.** `tello call transfer`
  hands a live call, inbound calls included, to another real phone number or
  to the agent's agent app counselors. A common request is to connect the
  callee to the user: see "Connect a call to the user".
- **A confirmed draft is a real call.** `tello call draft confirm` dials and
  bills like `call create`. Show the draft's `preview.summary` to the user and
  confirm only what they approved. `draft create` itself calls at once when
  the agent has a ready scenario (`status: confirmed`, `callId`).
- **Tool, agent and crew writes go live at once.** `create`, `update`,
  `delete` and `crew generate` save, deploy and publish in one request; the
  next call uses the result and there is no draft to discard. Write only what
  the user asked for, show them the `--body` first, and prefer `update` with
  just the fields to change. On `writeOutcomeUnknown` (exit 8), a lost
  response or Ctrl-C the change may already be live: check with `get` or
  `list` before sending it again, and create again only with the reported key
  or after confirming nothing was created.
- **Never print, echo, log or commit API keys.** Do not pass a key as a flag
  or argument, and do not read the keychain or `credentials.json` yourself.
  If no key is configured, ask the user to run `tello auth login` in their own
  terminal, or to set `TELLO_API_KEY` in the environment.
- An agent's persona and instructions live in the agent (portal or
  `tello agent update`). `--var` fills an agent's start variables,
  `--instructions` and the other per-call settings change one REST call, and
  `--prompt` and `--metadata` only add context to one handler call.

## Recommended flow (portal agent)

1. `tello auth status --json`: a key is configured and the API accepts it.
   Exit 3 means no key or a rejected key: ask the user to run `tello auth login`.
   Exit 7 is a network problem, not the key.
2. `tello doctor --json`: checks version, key, API and gateway URLs,
   authentication and skill files. Fix every `fail` before calling.
3. `tello agent list --json` (or `tello crew list --json`): pick the target id
   with the user. Agent and crew ids can collide; use each with its own flag.
4. Place the call and follow it:

   ```sh
   tello call create --to +821012345678 --agent 12 --var 고객명=홍길동 --wait --json
   ```

5. Read the **last** stdout line. With `--json` it is the `cli.result`
   (exceptions are listed under "Output contract"). With `--wait`, `data` is
   the final call object: `status`, `durationSeconds`, `endReason`, `summary`,
   `transcript`, `creditCharged`.
6. `summary`, `transcript` and `creditCharged` can still be null right after
   the call ends. Fetch again later with `tello call get <id> --json`.

## Connect a call to the user

When the user asks you to call someone and hand the call over to them ("call
the restaurant and put me through"), place the call without `--wait`, wait
for the callee to answer, then transfer to the user's own number. Each step
is a short command, so a shell timeout never ends the call.

1. Ask the user for their phone number (E.164). The transfer destination
   passes the same check as a new call: without an assigned outbound number
   it must be a number verified on the account.
2. Place the call and keep the returned id:

   ```sh
   tello call create --to +821012345678 --agent 12 --json
   ```

3. Run `tello call get <callId> --json` every few seconds until `status` is
   `inProgress` (the callee answered). An ending status (`noAnswer`,
   `failed`, `cancelled`, `completed`) means there is nothing to transfer:
   tell the user. `inProgress` only says the line was picked up, not that a
   person answered; the agent talks to whoever answered until the transfer.
4. Transfer to the user:

   ```sh
   tello call transfer <callId> --to +821098765432 --json
   # or brief the user with a summary before connecting:
   tello call transfer <callId> --to +821098765432 --mode warm --plan '{"mode":"warm-transfer-say-summary"}' --json
   ```

   `callNotInProgress` (exit 75) means the call is not answered yet: go back
   to step 3.
5. Keep polling `call get`. A successful transfer ends the AI's call
   (`completed`); the user and the callee keep talking on their own line. If
   the call stays `inProgress` for a minute or so, the transfer most likely
   failed (for example the user did not pick up) and the agent is still on
   the call: ask the user whether to try again (with a new key) or end it
   with `tello call end <callId> --json`.

Do not use `--wait` here: it blocks until the call ends, so the transfer
could not be sent, and a shell timeout or Ctrl-C ends the call.

## Commands

| Command | Purpose |
| --- | --- |
| `tello auth login [--no-verify]` | Store an API key (hidden prompt on a TTY, else the first line of stdin). Verifies it first. For humans |
| `tello auth status [--offline]` | Where the key comes from; whether the API accepts it |
| `tello auth logout` | Remove the stored key |
| `tello agent list` | Deployed agents calls can use: `id`, `name`, `description` |
| `tello crew list` | Crews with an active production version |
| `tello tool list [--limit n] [--cursor c]` | Tools of the account by id, one page |
| `tello {tool\|agent\|crew} get <id>` | One tool, agent or crew with all its settings |
| `tello {tool\|agent\|crew} create --body <JSON\|@file\|-> [--idempotency-key <key>]` | Create one and put it live |
| `tello {tool\|agent\|crew} update <id> --body <JSON\|@file\|->` | Change only the fields sent and put it live |
| `tello {tool\|agent\|crew} delete <id>` | Delete one |
| `tello crew generate --goal <text> [--wait [--timeout <dur>]] [--idempotency-key <key>]` | Have Tello build and publish a crew from a goal |
| `tello crew generation <generationId>` | A crew generation job: status, crew or error |
| `tello call create --to <number> (--agent <id> \| --crew <id>) [--var name=value]… [--max-duration-seconds n] [--instructions …] [--ivr-goal …] [--ivr-opening-message …] [--notify-on-end] [--wait [--timeout <dur>]] [--idempotency-key <key>]` | One call answered by a portal agent or crew |
| `tello call create --to <number> [--prompt …] [--metadata <JSON\|@file>] [--timeout <dur>] (-i \| -- <handler argv…>)` | One call answered turn by turn; blocks until it ends |
| `tello call draft create --agent <id> --to <number> [--instructions …] [--var name=value]… [per-call flags] [--idempotency-key <key>]` | A scenario to review before calling |
| `tello call draft revise <draftId> --request <text> [--idempotency-key <key>]` | A changed scenario as a new draft |
| `tello call draft confirm <draftId> [--idempotency-key <key>]` | Place the call of a draft (real, billed) |
| `tello call get <callId>` | A call's status, timing, summary, transcript and credit |
| `tello call list [--status s] [--agent id] [--crew id] [--created-after t] [--created-before t] [--limit n] [--cursor c]` | Calls of the account, newest first, one page |
| `tello call end <callId>` | Ask the service to end a live call |
| `tello call transfer <callId> (--to <number> \| --agent-app) [--mode cold\|warm] [--plan <JSON\|@file\|->] [--message <text>] [--idempotency-key <key>]` | Ask the service to transfer an answered call |
| `tello call batch --tasks <file\|-> (--agent <id> \| --crew <id> \| -i \| -- <handler argv…>) [--max-retries 3] [--retry-delay 5s] [--timeout <dur>]` | Several calls, one after another |
| `tello init [--target claude,codex] [--dir .]` | Install or refresh the agent skill files |
| `tello guide` | List guide topics |
| `tello guide coding-agent [--brief]` | This guide (`--brief`: the compact version) |
| `tello doctor` | Environment checks |
| `tello version` | CLI, SDK and protocol versions |

Global flags: `--json` (machine output), `--api-url <https://…>` (REST API;
precedence `--api-url` > `TELLO_API_URL` > `https://api.telloai.io/v1`) and
`--url <ws(s)://…>` (WebSocket gateway for `-i` and handler calls; precedence
`--url` > `TELLO_URL` > `wss://api.telloai.io/sdk`). HTTP REST URLs are allowed
only for loopback development servers.

`--wait` polls every `--poll-interval` (default 2s). `--timeout` requests an
end (exit 6, code `timeout`); with `--agent`/`--crew` it needs `--wait`. If
creation or termination cannot be confirmed the call can remain active:
check `call list` or `call get` before retrying. Call IDs are strings.

## Output contract (`--json`)

- stdout is NDJSON: one JSON object per line. Warnings and logs go to stderr.
- Every command ends with exactly one `cli.result` line:

  ```json
  {"type":"cli.result","ok":true,"data":{}}
  {"type":"cli.result","ok":false,"error":{"kind":"refused","code":"insufficientCredit","message":"…","retryable":false,"exitCode":4},"data":{}}
  ```

- Branch on `error.code` (and the exit code); `message` is for display only.
  `error.question` is present only for a handler call's `callRejected`. `data`
  may be present on failure too: the call so far, `idempotencyKey` of a failed
  create or transfer, or `missingVariables` (names to pass with `--var`).
- Exceptions, which end without a `cli.result` line: `--help` / `help` and
  `--version` (plain text, exit 0); a second Ctrl-C / SIGTERM (immediate exit
  130); Ctrl-C at the hidden `tello auth login` prompt (exit 130). Do not pass
  `--help` or `--version` when you parse output.
- `--agent`/`--crew` calls with `--wait` stream one
  `{"type":"cli.status","callId":"c_…","status":"…"}` line per status change
  (`dialing`, `inProgress`, `transferring`, then a final `completed`,
  `noAnswer`, `failed` or `cancelled`). `cli.result` `data` is the call object;
  without `--wait` it is the call as accepted (usually `dialing`).
- Handler and `-i` calls stream gateway frames verbatim (`call.created`,
  `call.statusChanged`, `user.turn`, `agent.turn`, `answer.accepted`,
  `dtmf.accepted`, `call.completed`, `call.noAnswer`, `call.failed`, `error`);
  their `cli.result` `data` is `callId` and `status`.
- `call batch` adds `cli.task` and `cli.retry` lines (`cli.status` lines carry
  `index` there).
- `crew generate --wait` streams
  `{"type":"cli.status","generationId":"…","status":"…"}` lines (`running`,
  then `succeeded` or `failed`); its `cli.result` `data` is the job.
- Do not assume every status arrives; a call may jump from `dialing` to its
  end.

## Call drafts (review the scenario first)

Agents that generate outbound scenarios can show the scenario before dialing:

```sh
tello call draft create --agent 12 --to +821012345678 --instructions "내일 2시 예약을 확인해 주세요" --json
tello call draft revise cd_481 --request "4시도 안 되면 6시로 물어봐 주세요" --json
tello call draft confirm cd_482 --json
```

- `draft create` takes `--to`, `--var` and the per-call settings of
  `call create` (`--instructions`, `--max-duration-seconds`, `--ivr-goal`,
  `--ivr-opening-message`, `--notify-on-end`); they are stored with the draft.
  `data` is the draft: `id` (`cd_…`), `status`, `preview.summary`,
  `question`, `revision`, `revisionsLeft`, `expiresAt`, `callId`.
- `status`: `pendingConfirmation` (show `preview.summary` to the user, then
  confirm or revise), `needsClarification` (answer `question` in
  `--instructions` and create again; nothing stored), `refused` (change the
  request), `confirmed` (the agent had a ready scenario and **the call was
  already placed**: follow `callId` with `call get`).
- `draft revise` returns a new draft with a new id; the old one is
  `draftSuperseded`. Up to the 5th revision (`draftRevisionLimit`).
- `draft confirm` places the real call and returns the call object, like
  `call create` without `--wait`; follow it with `call get`. A draft is
  confirmed once (`draftAlreadyConfirmed`, `data.callId`) and expires 10
  minutes after it was created (`draftExpired`). On a lost response, Ctrl-C
  or `callOutcomeUnknown`, resend only with the reported
  `data.idempotencyKey`: the same key replays the placed call.
- `draftNotApplicable` (exit 5): the agent does not use drafts and nothing
  was stored or dialed; use `call create` with the same values if the user
  wants the call. Crews have no drafts.
- Draft ids are `cd_` and digits as printed; anything else is a usage error.

## Tools, agents and crews

`tello tool`, `tello agent` and `tello crew` manage the account's resources
through the REST API. Writes deploy and publish in the same request: the next
call uses the result.

- `get <id>` returns the full object (`revision`, `productionRevision`, …).
  `agent list`/`crew list` keep listing only what calls can use; `tool list`
  pages by id (`--limit` 1-100, default 50; pass `nextCursor` as `--cursor`).
- `create --body …` and `update <id> --body …` take a JSON object: inline,
  `@file`, or `-` for stdin. Field names are those of the REST API docs
  (`systemPrompt`, `toolIds`, `graph`, `entryNodeId`, `config`, …). `update`
  changes only the fields sent; lists such as `toolIds` and objects such as a
  tool's `config` or a crew's `graph` are replaced whole. A body that is not
  a JSON object, or an id that is not digits as printed, is a usage error.
- `create` sends an idempotency key (random per run unless
  `--idempotency-key`); failures report it in `data.idempotencyKey`. Resending
  the same body with it replays the stored `201` or `writeOutcomeUnknown`
  instead of creating a second one.
- `delete <id>` returns `{"id":…,"deleted":true}`. Default tools
  (`defaultToolProtected`), tools used by a scenario (`toolInUse`) and
  agents or crews that are a default target or linked to a channel or
  number (`agentInUse`, `crewInUse`) cannot be deleted; `data.reason` says why.
- Refusals carry `data.reason` and a Korean `data.detail`; branch on
  `error.code` and `data.reason`. `*NotPublishable` saved nothing.
- `writeOutcomeUnknown` (exit 8): the write may be live. Check with `get` or
  `list`; for a create, resend only with the same key or create anew after
  confirming nothing was created. `upstreamUnavailable` (exit 75) was not
  processed and can be resent.

### Crew generation

```sh
tello crew generate --goal "치과 예약 고객에게 전화해 내일 예약을 확인한다" --wait --json
```

- Tello designs, deploys and publishes a crew for the goal. Without `--wait`
  the result is the job (`id`, `status: running`); follow it with
  `tello crew generation <id> --json`.
- `--wait` polls every `--poll-interval` (default 2s). On `succeeded`, `data`
  is the job with `crew` (call it with `--crew <crew.id>`). A `failed` job
  exits 8 with its `error.code` (`crewGenerationFailed`,
  `crewGenerationTimeout`, `crewDeployFailed`, `crewPromoteFailed`) and the
  job as `data`; when `data.crew` is set that crew was left in place.
- `--timeout` (`crewGenerationRunning`, exit 75) and Ctrl-C (exit 130) only
  stop waiting: the job keeps running. Polls that keep failing end in
  `crewGenerationStatusUnknown` (exit 7). Check the job with
  `crew generation <id>`.
- One job per account at a time: another goal while one runs is
  `crewGenerationInProgress` (exit 75, `data.id` is the running job). The
  same goal while it runs, or within 10 minutes of its success, returns that
  job, so rerunning the same command does not build a second crew.

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
| 2 | `usage` | bad flags or arguments, including a malformed call, draft or resource id, `--body`/`--plan` that is not a JSON object, or `--idempotency-key` | `usage`, `invalidUrl` |
| 3 | `auth` | no key, key rejected, or the key may not be used here | `apiKeyMissing`, `unauthenticated`, `insufficientScope` (`data.requiredScope`), `ipNotAllowed` |
| 4 | `refused` | account policy refusal; retrying will not help | `insufficientCredit`, `outboundLimitExceeded`, `calleeNotVerified`, `callerNotVerified`, `destinationBlocked`, `callerNumberUnavailable`, `noRepresentativeNumber` |
| 5 | `invalid` | request rejected by validation or the resource's state | `targetRequired`, `toRequired`, `agentNotFound`, `crewNotFound`, `agentInactive`, `crewInactive`, `publicationRevoked`, `missingVariables`, `invalidVariables`, `invalidRequest`, `idempotencyKeyReused`, `callNotFound`, `callNotActive`, `invalidTransferRequest`, `transferModeUnsupported`, `transferDestinationUnsupported`, `callControlUnsupported`, `invalidQuery`, `invalidCursor`, `callRejected`, `unsupportedForCrew`, `notifyUnavailable`, `callRefused`, `instructionsUnclear`, `draftNotApplicable`, `draftNotFound`, `draftAlreadyConfirmed`, `draftSuperseded`, `draftNotConfirmable`, `draftExpired`, `draftRevisionLimit`, `invalidTool`, `invalidAgent`, `invalidCrew`, `toolNotFound`, `defaultToolProtected`, `toolInUse`, `toolTypeManaged`, `toolNotPublishable`, `agentEngineImmutable`, `agentInUse`, `agentNotPublishable`, `crewInUse`, `crewNotPublishable`, `crewGenerationNotFound`, `dtmfDigitsInvalid`, `noActiveCall` |
| 6 | `callEnded` | the call did not complete | `noAnswer`, `callFailed`, `cancelled`, `timeout` (`--timeout`), `batchIncomplete` |
| 7 | `connection` | could not connect, or the connection dropped (also during authentication) | `connectionFailed`, `connectionClosed`, `sessionReplaced`, `crewGenerationStatusUnknown` |
| 8 | `server` | service-side error; for creates, writes and transfers the outcome may be unknown | `callOutcomeUnknown`, `transferOutcomeUnknown`, `writeOutcomeUnknown`, `internalError`, `callProviderUnauthorized`, `callSetupFailed`, `crewGenerationFailed`, `crewGenerationTimeout`, `crewDeployFailed`, `crewPromoteFailed` |
| 9 | `handler` | handler process failed | `handlerStartFailed`, `handlerExited` |
| 75 | `temporary` | try again later | `concurrentLimitExceeded`, `duplicateCall`, `idempotencyInProgress`, `voiceGatewayUnavailable`, `callRecordUnavailable`, `creditCheckUnavailable`, `callProviderDraining`, `callProviderUnavailable`, `callAlreadyActive`, `callNotInProgress`, `upstreamUnavailable`, `toolConflict`, `crewGenerationInProgress`, `crewGenerationRunning` |
| 130 | `interrupted` | Ctrl-C / SIGTERM; call termination is requested when possible (an interrupted `call transfer` may still have been requested) | `interrupted` |

`call create` with `--wait`, `-i` or a handler succeeds (exit 0) only when the
call completes. Without `--wait` it succeeds once the call is accepted.

## Retries

- Retry only when `error.retryable` is true (exit 75). Wait first (seconds to
  a minute); for `--agent`/`--crew`, reuse `data.idempotencyKey`. Check the
  call list first whenever the original outcome is uncertain: response storage
  failure can make even a same-key retry place another call.
- `missingVariables` (exit 5) placed no call: add the names from
  `data.missingVariables` with `--var` and retry with the same
  `--idempotency-key`.
- `callOutcomeUnknown` (exit 8, or 6/130 when creation times out/is interrupted): the call may have started. Look for it with
  `tello call list --agent <id> --created-after <time> --json` and ask the
  user. Never retry it with a new key.
- Never retry automatically on any other exit code. Exit 4 and 5 need a fix.
  Exit 6 means a call really happened (and may be billed), so calling again
  rings the same person again: ask the user. On exit 7 or 9 check whether
  `data` names a call (`id` or `callId`) before deciding anything.
- For `call batch`, exit 6 (`batchIncomplete`) only says that some task did
  not complete; it does not mean every failed task placed a call. Inspect each
  `cli.task` line: a task with `callId` placed a call; `error.code` and
  `error.exitCode` say why it failed. Re-run only the tasks that need it.

## `call get`, `call list`, `call end`, `call transfer`

- `tello call get <callId> --json`: `data` is the call object (`id`, `status`,
  `direction`, `source`, `agentId`, `crewId`, `createdAt`, `startedAt`,
  `endedAt`, `durationSeconds`, `endReason`, `transcript`, `summary`,
  `creditCharged`). Fields not available yet are null; judge progress by
  `status`. Unknown ids and other accounts' calls are `callNotFound` (exit 5).
  Pass ids exactly as printed (`c_` and digits, no leading zeros); anything
  else is a usage error (exit 2) and sends no request.
- `tello call list --json`: `data` is `{"data":[calls…],"nextCursor":…}`.
  Pass a non-null `nextCursor` as `--cursor`, keeping the other filters, for
  the next page. Dates with a zone like `2026-10-20T09:00:00+09:00`; a plain
  date means UTC midnight.
- `tello call end <callId> --json` asks the service to end a live call placed
  through the API, A2A or a handler. `data` is the call when the request was
  accepted, not the outcome: check with `call get`. An ended call is
  `callNotActive` (exit 5). Ending after the callee answered is billed for
  the time talked.
- `tello call transfer <callId> --to <number> --json` asks the service to
  hand an answered call (any source, inbound included) over to a phone
  number; `--agent-app` instead calls the agent app counselors of the call's
  agent (the call is `transferring` meanwhile). `--mode cold` (default)
  connects at once. `--mode warm` (phone numbers only) briefs the person who
  answers first and needs `--plan`, the REST `transferPlan` object, e.g.
  `{"mode":"warm-transfer-say-summary"}` or
  `{"mode":"warm-transfer-say-message","message":"…"}`; a cold phone transfer
  takes `--plan '{"mode":"blind-transfer-add-summary-to-sip-header"}'` only.
  `--message` is what the customer hears before the transfer. `data` is the
  call when the request was accepted, not the outcome: the call stays
  `inProgress` while the number is dialed and also when the transfer fails.
  Once it succeeds the AI leaves and a phone call ends; a browser (web) call
  can stay `inProgress` until that conversation ends. There is no field that
  says the transfer succeeded; check `status` with `call get`.
- An accepted transfer can do nothing: a request that arrives while the call
  is ending, while another transfer (this API or the agent's own transfer
  tool) is running, or after a portal counselor took the call over is
  accepted but dropped. Before trying again, give the previous transfer time
  to finish, and ask the user.
- `callNotInProgress` (exit 75): the call is not `inProgress` yet (still
  dialing, or being handed to a portal counselor); retry once `call get`
  shows `inProgress`. An ended call is `callNotActive`, and a malformed
  number, `--message` or `--plan` is `invalidTransferRequest` (exit 5).
  Combinations the API rules out (no or two destinations, warm without
  `--plan` or to the agent app, `--plan` with `--agent-app`) are usage
  errors and send nothing.
- Each `call transfer` run sends a new idempotency key unless
  `--idempotency-key` is given; failures report it in `data.idempotencyKey`.
  The transfer may have been requested after a connection error, Ctrl-C
  (`interrupted`), `transferOutcomeUnknown` (exit 8) or
  `voiceGatewayUnavailable`: check `call get`, then resend only with the same
  key. To try again after a transfer that was accepted but did not happen,
  use a new key: the same key only replays the stored acceptance. Never reuse
  a create key.

## Batch

```sh
tello call batch --tasks tasks.json --agent 12 --json
tello call batch --tasks tasks.json --json -- python3 handler.py
```

- With `--agent`/`--crew`, `tasks.json` is `[{"to":"+8210…","variables":{…}}]`.
  With `-i` or a handler it is `[{"to":"+8210…","prompt":"…","metadata":{…}}]`.
  `--tasks -` reads it from stdin.
- Calls run one after another; each ends before the next starts. Handler
  calls get a fresh handler process each (`cli.start` carries `index`).
- Temporary failures are retried automatically (`--max-retries`, default 3;
  `--retry-delay`, default 5s, doubling each time), each announced by a
  `cli.retry` line. `--agent`/`--crew` tasks keep their idempotency key across
  retries.
- After each task: `{"type":"cli.task","index":0,"to":"…","ok":true,"callId":"…","status":"completed","attempts":1}`
  (with `error` when it failed).
- The batch stops (remaining tasks are skipped) on `auth` errors, interruption,
  account-wide refusals (`insufficientCredit`, `outboundLimitExceeded`,
  `callerNumberUnavailable`, `noRepresentativeNumber`,
  `callProviderUnauthorized`), a target that cannot be called
  (`agentNotFound`, `crewNotFound`, `agentInactive`, `crewInactive`,
  `publicationNotFound`, `publicationRevoked`) and `callOutcomeUnknown`. Other
  failures are recorded and the batch moves on.
- Result `data`: `total`, `completed`, `failed`, `skipped`. Exit 0 when every
  task completed, the stopping error's exit code when it stopped, otherwise
  exit 6 (`batchIncomplete`). Failed tasks can have any cause (refusal,
  connection, handler, call not completed); see "Retries" for how to read
  their `cli.task` lines.
