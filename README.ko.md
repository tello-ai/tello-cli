[English](README.md) | **한국어**

# tello-cli

`tello`는 터미널이나 코딩 에이전트(Claude Code, Codex)에서 Tello 통화를 걸고,
통화의 각 턴에 실시간으로 답하고, 통화 요약을 조회하는 CLI입니다.
[Tello Go SDK](https://github.com/tello-ai/tello-go)(WebSocket 프로토콜 `1.0`)를
단일 바이너리로 감쌌습니다.

> 저장소: `tello-cli` · npm: `@tello-ai/cli` · 바이너리: `tello`
>
> CLI가 하는 일은 `/sdk` 프로토콜이 제공하는 범위입니다. 통화 걸기, 턴에 답하기,
> DTMF, 취소, 끝난 통화의 요약 조회. 에이전트 설정은 Tello 포털에서 합니다.

## 1. 설치

```sh
npm install -g @tello-ai/cli
```

npm이 macOS·Linux·Windows(x64, arm64)용 빌드된 바이너리를 optional 의존성으로
설치합니다. `--omit=optional`로 설치하지 마세요.

Node.js가 없으면:

```sh
# macOS / Linux: ~/.local/bin에 설치
curl -fsSL https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.sh | sh
```

```powershell
# Windows PowerShell: %LOCALAPPDATA%\Programs\tello에 설치하고 PATH에 추가
irm https://raw.githubusercontent.com/tello-ai/tello-cli/main/scripts/install.ps1 | iex
```

설치 스크립트는 릴리스 아카이브의 SHA-256 체크섬을 확인합니다. 환경변수:
`TELLO_VERSION`(기본 latest), `TELLO_INSTALL_DIR`, `TELLO_DOWNLOAD_BASE_URL`
(GitHub 릴리스 미러).

## 2. API 키

```sh
tello auth login     # 키를 입력받아 게이트웨이에서 확인한 뒤 저장
tello auth status    # 키 출처와 게이트웨이 인증 여부
tello auth logout
```

키는 `TELLO_API_KEY` → OS 키체인 → `<사용자 설정 디렉터리>/tello/credentials.json`
순서로 찾습니다. 파일(권한 0600)은 키체인을 쓸 수 없을 때(헤드리스 Linux, WSL 등)만
씁니다. 키는 플래그로 받지 않고 어디에도 출력하지 않습니다. CI에서는
`TELLO_API_KEY`를 설정하세요.

게이트웨이는 `--url`이나 `TELLO_URL`이 없으면 `wss://api.telloai.io/sdk`입니다.

## 3. 통화 걸기

CLI가 통화의 두뇌라서 상대방의 각 턴에 답할 주체가 있어야 합니다. 응답 소스를
하나만 고르세요.

```sh
# 터미널에서 직접 답합니다. 한 줄이 답변 하나이고, /dtmf 1234#, /cancel, /help를 씁니다.
tello call create --to +821012345678 --prompt "예약 확인" -i

# 프로그램이 답합니다. -- 뒤의 명령을 자식 프로세스로 실행합니다(셸을 거치지 않음).
tello call create --to +821012345678 --metadata @booking.json -- python3 bot.py
```

`call create`는 통화가 끝날 때까지 블로킹합니다. Ctrl-C를 누르면 통화를 취소하고
게이트웨이의 확인을 최대 10초 기다립니다. 한 번 더 누르면 바로 종료합니다.
`--timeout 10m`을 주면 너무 길어진 통화를 취소합니다.

통화는 실제로 걸리고 과금됩니다. 수신 번호는 계정의 인증 번호여야 합니다
(아니면 `callerNotVerified`).

### 핸들러 프로토콜

핸들러는 stdin/stdout으로 한 줄에 JSON 하나(NDJSON)를 주고받습니다. 프레임
형식은 WebSocket 프로토콜과 같습니다.

- stdin: 첫 줄 `{"type":"cli.start","to":…,"prompt":…,"metadata":{…}}`, 이후
  게이트웨이 프레임 전부(`call.created`, `user.turn`, `agent.turn`,
  `call.statusChanged`, `call.completed`, `error`, …).
- stdout, 한 줄에 하나: `{"event":"answer","data":{"text":"…"}}`,
  `{"event":"sendDtmf","data":{"digits":"1234#"}}`,
  `{"event":"cancel","data":{}}`.
- stderr는 로그용으로 그대로 흘립니다.

`user.turn`에는 3초 안에 답하세요. 그보다 늦으면 게이트웨이가 필러 멘트를
냅니다. 통화가 끝나면 stdin이 닫히고 핸들러는 5초 안에 종료해야 합니다. 통화
도중 핸들러가 종료하면 통화를 취소합니다(exit 9). `cancel`을 출력한 뒤 종료하는
것은 정상적인 끊기입니다(exit 6).

핸들러는 자기 프로세스 그룹에서 돕니다. 터미널 Ctrl-C는 `tello`만 받아서 통화를
취소하고, 핸들러는 마지막 이벤트와 EOF를 그대로 받습니다. 핸들러에는
`TELLO_API_KEY`가 전달되지 않습니다.

```python
import json, sys

for line in sys.stdin:
    frame = json.loads(line)
    if frame.get("type") == "user.turn":
        reply = {"event": "answer", "data": {"text": "확인했습니다: " + frame["text"]}}
        print(json.dumps(reply, ensure_ascii=False), flush=True)
```

### 그 밖의 통화 명령

```sh
tello call summary <callId>                         # 완료된 통화만
tello call batch --tasks tasks.json -- python3 bot.py
```

`tasks.json`은 `[{"to":"+8210…","prompt":"…","metadata":{…}}]`입니다(`-`면
stdin). 한 건씩 차례로 걸고 건마다 핸들러를 새로 띄웁니다. 나중에 성공할 수 있는
거부는 재시도합니다(`--max-retries 3`, `--retry-delay 5s`, 매번 두 배).
`insufficientCredit`처럼 계정 전체에 해당하는 거부가 오면 남은 건을 멈춥니다.

## 4. JSON 출력과 exit code

`--json`을 주면 stdout에 한 줄에 JSON 하나를 씁니다. `call create`와
`call batch`는 먼저 게이트웨이 프레임을 그대로 흘립니다(batch는 `cli.task`,
`cli.retry`도). **모든 명령은 정확히 한 줄의 `cli.result`로 끝납니다:**

```json
{"type":"cli.result","ok":true,"data":{"callId":"…","status":"completed"}}
{"type":"cli.result","ok":false,"error":{"kind":"refused","code":"insufficientCredit","message":"…","retryable":false,"exitCode":4}}
```

분기는 `message`가 아니라 `error.code`로 하세요. `--help`, `--version`, 두 번째
Ctrl-C, `auth login` 프롬프트에서의 Ctrl-C만 `cli.result` 줄 없이 끝납니다.

| Exit | kind | 뜻 |
| --- | --- | --- |
| 0 | — | 성공 |
| 1 | `internal` | 예상하지 못한 오류 |
| 2 | `usage` | 플래그·인자 오류, `ws://`/`wss://`가 아닌 게이트웨이 URL(`invalidUrl`) |
| 3 | `auth` | API 키 없음·거부 |
| 4 | `refused` | 계정 정책 거부: `insufficientCredit`, `callerNotVerified`, `noRepresentativeNumber` |
| 5 | `invalid` | 게이트웨이 검증 오류: `toRequired`, `callNotFound`, `callNotCompleted`, `callRejected`, … |
| 6 | `callEnded` | 통화가 완료되지 못함: `noAnswer`, `callFailed`, `cancelled`, `timeout`, `batchIncomplete` |
| 7 | `connection` | 연결 실패·끊김(인증 중 끊김 포함), `summaryTimeout` |
| 8 | `server` | 서비스 측 오류: `internalError`, `callProviderUnauthorized`, `callSetupFailed` |
| 9 | `handler` | 핸들러 실행 실패, 통화 중 종료 |
| 75 | `temporary` | 나중에 재시도: `concurrentLimitExceeded`, `callProviderDraining`, `callProviderUnavailable`, `callAlreadyActive` |
| 130 | `interrupted` | Ctrl-C, SIGTERM |

## 5. 코딩 에이전트

```sh
tello init                          # .claude/skills/tello/SKILL.md, .codex/skills/tello/SKILL.md 생성
tello guide coding-agent --brief    # 코딩 에이전트용 운영 지침
tello doctor                        # 버전, 키, URL, 인증, 스킬 파일 점검
```

Claude Code와 Codex는 스킬 파일을 자동으로 읽습니다. 스킬 파일은 CLI가 관리하는
산출물이라 `tello init`이 현재 템플릿과 다르면 다시 쓰고, `tello doctor`가 오래된
파일을 알려 줍니다. 스킬을 지원하지 않는 에이전트는 지침 파일에
`tello guide coding-agent --brief`를 적어 두세요.

## 6. 개발

```sh
go test -race ./...
node --test 'npm/**/*.test.mjs'
go build -o tello ./cmd/tello
```

설계와 계약: [`docs/design.md`](docs/design.md). 릴리스는 `vX.Y.Z` 태그를 push하면
됩니다. GoReleaser가 GitHub 릴리스(아카이브와 `checksums.txt`)를 만들고, 이어서
플랫폼 패키지 6개와 `@tello-ai/cli`를 npm에 게시합니다. 프리릴리스 버전은 dist-tag
`next`로 게시합니다.

## 라이선스

Apache-2.0
