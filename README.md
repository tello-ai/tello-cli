**English** | [한국어](README.ko.md)

# tello-cli

`tello` places Tello phone calls from a terminal or a coding agent (Claude Code,
Codex), follows them, and reads their results. A call is answered either by an
agent or crew configured in the Tello portal (REST API), or turn by turn by you
or a program (WebSocket gateway, via the [Tello Go SDK](https://github.com/tello-ai/tello-go),
protocol `1.0`). One binary does both.

> Repository: `tello-cli` · npm: `@tello-ai/cli` · binary: `tello`
>
> Agent configuration lives in the Tello portal; the CLI picks an agent or
> crew by id and places, follows, lists, ends and transfers calls.

## 1. Install

```sh
npm install -g @tello-ai/cli
```

npm installs a prebuilt binary for macOS, Linux or Windows (x64, arm64) as an
optional dependency. Do not install with `--omit=optional`.

Without Node.js:

```sh
# macOS / Linux: installs to ~/.local/bin
curl -fsSL https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.sh | sh
```

```powershell
# Windows PowerShell: installs to %LOCALAPPDATA%\Programs\tello and adds it to PATH
irm https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.ps1 | iex
```

The scripts verify the SHA-256 checksum of the release archive. Environment
variables: `TELLO_VERSION` (default: latest), `TELLO_INSTALL_DIR`,
`TELLO_DOWNLOAD_BASE_URL` (mirror of the GitHub releases).

## 2. API key

```sh
tello auth login     # prompts for the key, checks it against the API, stores it
tello auth status    # where the key comes from, and whether the API accepts it
tello auth logout
```

The key is looked up in this order: `TELLO_API_KEY`, the OS keychain, then
`<user config dir>/tello/credentials.json` (mode 0600, used when no keychain is
available, e.g. headless Linux or WSL). The key is never accepted as a flag and
never printed. In CI, set `TELLO_API_KEY`. The same server key works for the
REST API, the WebSocket gateway, MCP and A2A.

Endpoints: the REST API is `https://api.telloai.io/v1` unless `--api-url` or
`TELLO_API_URL` says otherwise. Remote REST endpoints require HTTPS; HTTP is
allowed only for loopback development servers. The WebSocket gateway for `-i`
and handler calls is `wss://api.telloai.io/sdk` unless `--url` or `TELLO_URL`
says otherwise.

## 3. Place a call with a portal agent

```sh
tello agent list                    # deployed agents (tello crew list for crews)
tello call create --to +821012345678 --agent 12 --var 고객명=홍길동 --wait
```

Without `--wait` the command returns as soon as the call is accepted and
prints its id; follow it with `tello call get <id>`. With `--wait` it prints
each status change and the final call (summary, transcript, credit). Ctrl-C
or `--timeout 10m` requests that the call end; if creation or termination
cannot be confirmed, the call may still be active. Check the call list or
call status before retrying. `--var name=value` fills the agent's start
variables; a crew that needs more fails with `missingVariables` and the names.

Every create carries an idempotency key (random unless `--idempotency-key`
is given; a given key must be 1 to 255 bytes without commas or control
characters). A failed create reports it in `data.idempotencyKey`. Reuse that key
for the same request, but check the call list when the outcome is unknown:
responses are retained for 24 hours and a response-storage failure can allow
the same key to place another call after five minutes.

```sh
tello call get c_12345              # status, timing, summary, transcript, credit
tello call list --status completed --agent 12 --created-after 2026-10-20
tello call end c_12345              # ask the service to end a live call
tello call transfer c_12345 --to +821012345678   # hand an answered call over to a number
```

`transfer` only requests the transfer: the call stays `inProgress` while the
number is dialed and also when the transfer fails. Once it succeeds the AI
leaves and a phone call ends (a web call can stay `inProgress` until that
conversation ends). Check it with `tello call get`. A request sent while
another transfer is still running, while the call is ending, or after a
counselor took the call over is accepted but has no effect, so give the
previous transfer time before trying again. Each run uses a new idempotency
key unless `--idempotency-key` is given; reuse a key only to resend a request
whose response was lost. A call that is not `inProgress` yet (still dialing,
or being handed to a counselor) is `callNotInProgress` (exit 75).

Calls are real and billed. The callee must be a verified number of your
account (`calleeNotVerified` otherwise).

## 4. Answer a call yourself

Instead of a portal agent, you or a program can answer each turn of the
callee. Pick exactly one answer source.

```sh
# You answer in the terminal. A line is an answer; /dtmf 1234#, /cancel, /help.
tello call create --to +821012345678 --prompt "Booking check" -i

# A program answers. Everything after -- runs as a child process (no shell).
tello call create --to +821012345678 --metadata @booking.json -- python3 bot.py
```

These calls block until they end. Ctrl-C cancels the call and waits up to
10 s for the gateway to confirm; a second Ctrl-C exits immediately.
`--timeout 10m` cancels a call that runs too long. Unverified callees fail
with `callerNotVerified`.

### Handler protocol

The handler talks NDJSON over stdin/stdout, using the frames of the WebSocket
protocol:

- stdin: first `{"type":"cli.start","to":…,"prompt":…,"metadata":{…}}`, then
  every gateway frame (`call.created`, `user.turn`, `agent.turn`,
  `call.statusChanged`, `call.completed`, `error`, …).
- stdout, one per line: `{"event":"answer","data":{"text":"…"}}`,
  `{"event":"sendDtmf","data":{"digits":"1234#"}}`,
  `{"event":"cancel","data":{}}`.
- stderr is passed through for logs.

Answer each `user.turn` within about 3 seconds; the gateway plays filler after
that. When the call ends, stdin closes and the handler gets 5 s to exit. A
handler that exits during the call cancels it (exit 9); printing `cancel` and
then exiting is a normal hang-up (exit 6).

The handler runs in its own process group: a terminal Ctrl-C reaches only
`tello`, which cancels the call, and the handler still receives the final
event and then EOF. It does not inherit `TELLO_API_KEY`.

```python
import json, sys

for line in sys.stdin:
    frame = json.loads(line)
    if frame.get("type") == "user.turn":
        reply = {"event": "answer", "data": {"text": "Got it: " + frame["text"]}}
        print(json.dumps(reply, ensure_ascii=False), flush=True)
```

## 5. Several calls

```sh
tello call batch --tasks tasks.json --agent 12
tello call batch --tasks tasks.json -- python3 bot.py
```

With `--agent`/`--crew`, `tasks.json` is `[{"to":"+8210…","variables":{…}}]`;
with `-i` or a handler it is `[{"to":"+8210…","prompt":"…","metadata":{…}}]`
(`-` reads stdin). Each call ends before the next starts. Refusals that can
succeed later are retried (`--max-retries 3`, `--retry-delay 5s`, doubling;
the same idempotency key per task). Account-wide refusals such as
`insufficientCredit`, and a target that cannot be called, stop the batch.

## 6. JSON output and exit codes

With `--json`, stdout carries one JSON object per line. `call create --wait`
streams `cli.status` lines; handler and `-i` calls stream the gateway frames
verbatim; `call batch` adds `cli.task` and `cli.retry`. **Every command ends
with exactly one `cli.result` line:**

```json
{"type":"cli.result","ok":true,"data":{"id":"c_12345","status":"completed","summary":"…"}}
{"type":"cli.result","ok":false,"error":{"kind":"refused","code":"insufficientCredit","message":"…","retryable":false,"exitCode":4},"data":{"idempotencyKey":"…"}}
```

Branch on `error.code`, not on `message`. REST error codes are camelCased
(`insufficient_credit` → `insufficientCredit`). Only `--help`, `--version`, a
second Ctrl-C and a Ctrl-C at the `auth login` prompt end without a
`cli.result` line.

| Exit | Kind | Meaning |
| --- | --- | --- |
| 0 | — | Success |
| 1 | `internal` | Unexpected error |
| 2 | `usage` | Bad flags or arguments (including a malformed call id or idempotency key), or a URL with the wrong scheme (`invalidUrl`) |
| 3 | `auth` | Missing or rejected API key |
| 4 | `refused` | Account policy refusal: `insufficientCredit`, `outboundLimitExceeded`, `calleeNotVerified`, `callerNotVerified`, `callerNumberUnavailable`, … |
| 5 | `invalid` | Rejected request: `agentNotFound`, `agentInactive`, `missingVariables`, `callNotFound`, `callNotActive`, `invalidTransferRequest`, `callRejected`, … |
| 6 | `callEnded` | The call did not complete: `noAnswer`, `callFailed`, `cancelled`, `timeout`, `batchIncomplete` |
| 7 | `connection` | Could not connect, or the connection dropped (also during authentication) |
| 8 | `server` | Service-side error: `callOutcomeUnknown` (the call may have started), `transferOutcomeUnknown` (the transfer may have been requested), `internalError`, `callSetupFailed`, … |
| 9 | `handler` | The handler failed to start or exited mid-call |
| 75 | `temporary` | Retry later: `concurrentLimitExceeded`, `duplicateCall`, `voiceGatewayUnavailable`, `callProviderUnavailable`, … |
| 130 | `interrupted` | Ctrl-C or SIGTERM |

## 7. Coding agents

```sh
tello init                          # writes .claude/skills/tello/SKILL.md and .codex/skills/tello/SKILL.md
tello guide coding-agent --brief    # the operating contract for agents
tello doctor                        # version, key, API and gateway URLs, authentication, skill files
```

Claude Code and Codex read the skill files automatically. They are managed by
the CLI: `tello init` rewrites them when they differ from the current template,
and `tello doctor` reports stale ones. Agents without skill support can put
`tello guide coding-agent --brief` in their instructions.

## 8. Development

```sh
go test -race ./...
node --test 'npm/**/*.test.mjs'
go build -o tello ./cmd/tello
```

Design and contracts: [`docs/design.md`](docs/design.md). Releases are cut by
pushing a `vX.Y.Z` tag: GoReleaser publishes the GitHub release (archives and
`checksums.txt`), then the six platform packages and `@tello-ai/cli` go to npm.
Prerelease versions are published under the `next` dist-tag.

## License

Apache-2.0
