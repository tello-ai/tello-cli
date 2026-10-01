**English** | [한국어](README.ko.md)

# tello-cli

`tello` places Tello phone calls from a terminal or a coding agent (Claude Code,
Codex), answers each turn of the call in real time, and fetches call summaries.
It wraps the [Tello Go SDK](https://github.com/tello-ai/tello-go) (WebSocket
protocol `1.0`) in a single binary.

> Repository: `tello-cli` · npm: `@tello-ai/cli` · binary: `tello`
>
> The CLI covers what the `/sdk` protocol offers: placing a call, answering
> turns, DTMF, cancelling, and the summary of a finished call. Agent
> configuration lives in the Tello portal.

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
tello auth login     # prompts for the key, checks it against the gateway, stores it
tello auth status    # where the key comes from, and whether the gateway accepts it
tello auth logout
```

The key is looked up in this order: `TELLO_API_KEY`, the OS keychain, then
`<user config dir>/tello/credentials.json` (mode 0600, used when no keychain is
available, e.g. headless Linux or WSL). The key is never accepted as a flag and
never printed. In CI, set `TELLO_API_KEY`.

The gateway is `wss://api.telloai.io/sdk` unless `--url` or `TELLO_URL` says
otherwise.

## 3. Place a call

The CLI is the brain of the call: somebody has to answer each turn of the
callee. Pick exactly one answer source.

```sh
# You answer in the terminal. A line is an answer; /dtmf 1234#, /cancel, /help.
tello call create --to +821012345678 --prompt "Booking check" -i

# A program answers. Everything after -- runs as a child process (no shell).
tello call create --to +821012345678 --metadata @booking.json -- python3 bot.py
```

`call create` blocks until the call ends. Ctrl-C cancels the call and waits up
to 10 s for the gateway to confirm; a second Ctrl-C exits immediately.
`--timeout 10m` cancels a call that runs too long.

Calls are real and billed. The callee must be a verified number of your
account (`callerNotVerified` otherwise).

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

### More calls

```sh
tello call summary <callId>                         # finished calls only
tello call batch --tasks tasks.json -- python3 bot.py
```

`tasks.json` is `[{"to":"+8210…","prompt":"…","metadata":{…}}]` (`-` reads
stdin). Calls run one after another with a fresh handler each; refusals that
can succeed later are retried (`--max-retries 3`, `--retry-delay 5s`, doubling).
Account-wide refusals such as `insufficientCredit` stop the batch.

## 4. JSON output and exit codes

With `--json`, stdout carries one JSON object per line. `call create` and
`call batch` first stream the gateway frames verbatim (plus `cli.task` and
`cli.retry` for batch). **Every command ends with exactly one `cli.result`
line:**

```json
{"type":"cli.result","ok":true,"data":{"callId":"…","status":"completed"}}
{"type":"cli.result","ok":false,"error":{"kind":"refused","code":"insufficientCredit","message":"…","retryable":false,"exitCode":4}}
```

Branch on `error.code`, not on `message`. Only `--help`, `--version`, a second
Ctrl-C and a Ctrl-C at the `auth login` prompt end without a `cli.result` line.

| Exit | Kind | Meaning |
| --- | --- | --- |
| 0 | — | Success |
| 1 | `internal` | Unexpected error |
| 2 | `usage` | Bad flags or arguments, or a gateway URL that is not `ws://`/`wss://` (`invalidUrl`) |
| 3 | `auth` | Missing or rejected API key |
| 4 | `refused` | Account policy refusal: `insufficientCredit`, `callerNotVerified`, `noRepresentativeNumber` |
| 5 | `invalid` | Rejected by the gateway: `toRequired`, `callNotFound`, `callNotCompleted`, `callRejected`, … |
| 6 | `callEnded` | The call did not complete: `noAnswer`, `callFailed`, `cancelled`, `timeout`, `batchIncomplete` |
| 7 | `connection` | Could not connect, the connection dropped (also during authentication), or `summaryTimeout` |
| 8 | `server` | Service-side error: `internalError`, `callProviderUnauthorized`, `callSetupFailed` |
| 9 | `handler` | The handler failed to start or exited mid-call |
| 75 | `temporary` | Retry later: `concurrentLimitExceeded`, `callProviderDraining`, `callProviderUnavailable`, `callAlreadyActive` |
| 130 | `interrupted` | Ctrl-C or SIGTERM |

## 5. Coding agents

```sh
tello init                          # writes .claude/skills/tello/SKILL.md and .codex/skills/tello/SKILL.md
tello guide coding-agent --brief    # the operating contract for agents
tello doctor                        # version, key, URL, authentication, skill files
```

Claude Code and Codex read the skill files automatically. They are managed by
the CLI: `tello init` rewrites them when they differ from the current template,
and `tello doctor` reports stale ones. Agents without skill support can put
`tello guide coding-agent --brief` in their instructions.

## 6. Development

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
