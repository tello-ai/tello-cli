[English](README.md) | **한국어**

# tello-cli

`tello`는 터미널이나 코딩 에이전트(Claude Code, Codex)에서 Tello 통화를 걸고,
진행 상황과 결과를 조회하는 CLI입니다. Tello 포털에 설정한 에이전트나 크루가
답하는 REST 방식과, 사람이나 프로그램이 각 턴에 답하는 WebSocket 방식
([Tello Go SDK](https://github.com/tello-ai/tello-go), 프로토콜 `1.0`)을
하나의 바이너리로 제공합니다.

> 저장소: `tello-cli` · npm: `@tello-ai/cli` · 바이너리: `tello`
>
> 에이전트 설정은 Tello 포털에서 합니다. CLI는 에이전트나 크루를 선택해 통화를
> 걸고, 조회하고, 종료·전환을 요청합니다.

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
tello auth login     # 키를 입력받아 REST API에서 확인한 뒤 저장
tello auth status    # 키 출처와 REST API 인증 여부
tello auth logout
```

키는 `TELLO_API_KEY` → OS 키체인 → `<사용자 설정 디렉터리>/tello/credentials.json`
순서로 찾습니다. 파일(권한 0600)은 키체인을 쓸 수 없을 때(헤드리스 Linux, WSL 등)
대신 씁니다. 키는 플래그로 받거나 출력하지 않습니다. CI에서는 `TELLO_API_KEY`를
설정하세요. 같은 서버 키를 REST API, WebSocket 게이트웨이, MCP, A2A에서 사용합니다.
`auth login --no-verify`는 검증을 생략하고, `auth status --offline`은 네트워크에
연결하지 않습니다. `logout`은 저장된 키를 지우지만 환경변수는 지우지 않습니다.

REST API는 `--api-url` → `TELLO_API_URL` → `https://api.telloai.io/v1` 순서로,
WebSocket 게이트웨이는 `--url` → `TELLO_URL` → `wss://api.telloai.io/sdk` 순서로
선택합니다. REST 원격 서버는 HTTPS가 필수이며 HTTP는 로컬 loopback 서버에서만
허용합니다.

## 3. 포털 에이전트로 통화 걸기

```sh
tello agent list                    # 배포된 에이전트
tello crew list                     # 활성 운영 버전이 있는 크루
tello call create --to +821012345678 --agent 12 --var 고객명=홍길동 --wait
```

`--agent` 또는 `--crew` 중 하나를 선택합니다(크루라면 예를 들어 `--crew 34`).
`--var 이름=값`은 포털에 설정된 시작 변수이며 반복할 수 있습니다. 필수 변수가
빠지면 `missingVariables`와 필요한 이름을 반환합니다. `--wait`가 없으면 발신
수락 즉시 통화 객체와 id를 반환합니다. 수락은 통화 완료가 아닙니다. `--wait`는
기본 2초 간격(`--poll-interval`)으로 상태를 조회합니다. 종료 상태가 `completed`일
때만 성공하며 그 밖의 종료 상태는 오류입니다. 요약·전사·요금은 종료 직후에도 null일
수 있으므로 나중에 다시 조회하세요.

```sh
tello call get c_12345              # 상태, 시간, 요약, 전사, 요금
tello call list --status completed --agent 12 --created-after 2026-10-20
tello call end c_12345              # 진행 중인 통화의 종료 요청
tello call transfer c_12345 --to +821012345678   # 통화 중인 통화를 번호로 전환 요청
```

`list`는 최신순으로 한 페이지를 반환합니다. 다음 페이지가 있으면 같은 필터와
`--cursor <nextCursor>`를 사용하세요. 날짜만 입력하면 UTC 자정으로 해석합니다.
`end`는 종료 요청이 수락된 시점의 객체를 반환하므로 `get`으로 실제 종료를 확인하세요.
`transfer`도 요청 접수일 뿐입니다. 번호를 호출하는 동안과 전환에 실패했을 때 모두
`inProgress`이고, 성공하면 AI가 빠지고 전화 통화는 끝납니다(웹콜은 그 대화가 끝날
때까지 `inProgress`일 수 있습니다). `get`으로 확인하세요. 다른 전환이 진행 중이거나
통화가 끝나는 중, 또는 상담사가 이미 이어받은 통화에 보낸 요청은 접수되지만 효과가
없으니 앞선 전환이 끝날 시간을 두고 다시 요청하세요. `--idempotency-key`가 없으면
실행마다 새 키를 쓰며, 같은 키는 응답을 받지 못한 같은 요청을 다시 보낼 때만 쓰세요.
아직 `inProgress`가 아닌 통화(발신 중, 상담사에게 넘기는 중)는 `callNotInProgress`(exit 75)입니다.
이 명령들은 REST와 WebSocket으로 건 통화를 모두 조회·제어할 수 있습니다.

`--timeout 10m`은 대기 중인 통화의 종료를 요청하며 REST 방식에서는 `--wait`가
필수입니다. 기본 타임아웃은 없습니다. 생성 응답이 늦거나 Ctrl-C·타임아웃 뒤
종료 요청·확인에 실패하면 통화가 계속될 수 있습니다. ID를 받았다면 `call get`,
발신 결과가 불명이면 `call list`를 확인한 뒤 재시도 여부를 판단하세요.

통화는 실제로 걸리고 과금됩니다. 요청받은 통화만 걸고, 다음 통화 전에 이전 통화의
종료를 확인하세요. 수신 번호는 계정의 인증 번호여야 합니다(REST에서는
`calleeNotVerified`, WebSocket에서는 `callerNotVerified`).

### 응답 유실과 멱등 재시도

REST 발신마다 멱등 키를 사용합니다. 기본은 임의 생성이며 `--idempotency-key`로
직접 지정할 수 있습니다(1~255바이트, 쉼표·제어문자 불가). 프로세스나 결과 출력이 유실될 수 있다면 첫 요청부터 작업별
고유 키를 지정해 별도로 보관하세요.

```sh
tello call create --to +821012345678 --agent 12 --idempotency-key booking-20261020-001 --json
```

발신 요청이 오류를 반환하면 JSON 결과의 `data.idempotencyKey`를 확인하고,
재시도가 적절한 경우 같은 대상·번호·변수와 같은 키를 사용하세요. 생성 수락 후 대기
중 실패한 경우에는 키 대신 마지막 통화 객체가 반환될 수 있으므로 id로 조회하세요.
임의 생성한 키를 받기 전에 출력이나 프로세스가 유실되면 그 키를 복구하지 못할 수
있습니다.

서버가 응답을 정상 저장한 경우 같은 계정의 같은 키와 정규화된 동일 요청은 저장된
응답을 재생합니다. 저장 기간은 24시간이며, 그 뒤 같은 키도 새 발신이 될 수 있습니다.
발신 수락 후 응답 저장에 실패하면 키가 `idempotencyInProgress`로 남았다가 5분 뒤
같은 키도 새 요청으로 처리되어 중복 발신될 수 있습니다. 따라서 키가 중복 발신을
무조건 막지는 않습니다.

응답을 받지 못했거나 `callOutcomeUnknown`이면 먼저 `call list`에서 대상과
시각을 확인하세요. `idempotencyInProgress`가 계속될 때도 목록을 확인하세요. 발신
여부가 불분명하면 지원팀에 문의하고 새 키로 다시 걸지 마세요. `error.retryable`은
일시 오류의 분류이지 중복 발신이 불가능하다는 보장은 아닙니다. 이미 통화 id를
받았다면 생성 요청을 반복하지 말고 `call get`을 사용하세요. WebSocket 방식에는
이 REST 멱등 키가 적용되지 않습니다.

## 4. 직접 응답하기

포털 에이전트 대신 사람이나 프로그램이 수신자의 각 턴에 답할 수 있습니다. 응답
소스는 `--agent`, `--crew`, `-i`, 핸들러 중 하나만 선택하세요.

```sh
# 터미널에서 직접 답합니다. 한 줄이 답변 하나이고, /dtmf 1234#, /cancel, /help를 씁니다.
tello call create --to +821012345678 --prompt "예약 확인" -i

# 프로그램이 답합니다. -- 뒤의 명령을 자식 프로세스로 실행합니다(셸을 거치지 않음).
tello call create --to +821012345678 --metadata @booking.json -- python3 bot.py
```

WebSocket 통화는 끝날 때까지 블로킹합니다. Ctrl-C 또는 `--timeout 10m`은 취소를
요청하고 게이트웨이의 확인을 최대 10초 기다립니다. 두 번째 Ctrl-C는 즉시
종료합니다. 취소 확인이 없거나 연결이 끊긴 경우 재발신 전에 통화 여부를 확인하세요.

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

## 5. 여러 통화

```sh
tello call batch --tasks tasks.json --agent 12
tello call batch --tasks tasks.json -- python3 bot.py
```

REST 방식의 `tasks.json`은 `[{"to":"+8210…","variables":{…}}]`, `-i` 또는
핸들러 방식은 `[{"to":"+8210…","prompt":"…","metadata":{…}}]`입니다.
`--tasks -`는 stdin에서 읽으며 `-i`와 함께 쓸 수 없습니다. 한 건씩 차례로
처리하고 핸들러는 건마다 새로 띄웁니다. REST 배치는 발신 전 거부로 확인된 일부
일시 오류만, WebSocket 배치는 일시 오류를 기본 최대 3회 재시도합니다. 첫 대기
시간은 5초이고 매번 두 배가 됩니다(`--max-retries`, `--retry-delay`).
REST 발신 결과나 진행 중인 통화 상태를 확인할 수 없거나 계정·대상 거부가 발생하면
남은 건을 멈춥니다.

REST 배치는 한 실행 안에서 작업별 멱등 키를 만들고 재시도할 때 재사용합니다. 배치를
다시 실행하면 새 키가 생기므로 같은 작업을 통째로 다시 실행하면 중복 발신될 수
있습니다. 결과가 유실되었거나 불확실하면 먼저 통화 목록과 각 `cli.task` 결과를
확인하세요. WebSocket 배치 재시도에는 REST 멱등 보호가 없습니다. 각 통화의
`--timeout` 기본값은 제한 없음입니다.

## 6. JSON 출력과 exit code

`--json`을 주면 stdout에 한 줄에 JSON 하나를 씁니다. REST `call create --wait`는
상태가 바뀔 때마다 `cli.status`를, WebSocket 통화는 게이트웨이 프레임을 그대로
출력합니다. 배치는 `cli.task`, `cli.retry`도 출력합니다. 경고와 로그는 stderr에
씁니다. **일반적으로 모든 명령은 정확히 한 줄의 `cli.result`로 끝납니다:**

```json
{"type":"cli.result","ok":true,"data":{"id":"c_12345","status":"completed","summary":"…"}}
{"type":"cli.result","ok":false,"error":{"kind":"refused","code":"insufficientCredit","message":"…","retryable":false,"exitCode":4},"data":{"idempotencyKey":"…"}}
```

REST 통화 결과는 통화 객체이고, WebSocket 통화 결과는 `callId`와 `status`입니다.
실패 시에도 `data`에 통화 객체, 멱등 키 또는 `missingVariables`가 있을 수 있습니다.
분기는 `message`가 아니라 `error.code`와 exit code로 하세요. REST의 snake_case 오류
코드는 camelCase로 변환합니다. `--help`/`help`, `--version`, 두 번째 Ctrl-C/SIGTERM,
`auth login` 숨김 프롬프트에서의 Ctrl-C만 `cli.result` 없이 끝납니다.

| Exit | kind | 뜻 |
| --- | --- | --- |
| 0 | — | 성공 |
| 1 | `internal` | 예상하지 못한 오류 |
| 2 | `usage` | 플래그·인자 오류(형식이 틀린 통화 id·멱등 키 포함), 잘못된 API·게이트웨이 URL(`invalidUrl`) |
| 3 | `auth` | API 키 없음·거부 |
| 4 | `refused` | 계정 정책 거부: `insufficientCredit`, `outboundLimitExceeded`, `calleeNotVerified`, `callerNotVerified`, … |
| 5 | `invalid` | 요청 검증 오류: `agentNotFound`, `agentInactive`, `missingVariables`, `callNotFound`, `callNotActive`, `invalidTransferRequest`, `callRejected`, … |
| 6 | `callEnded` | 통화가 완료되지 못함: `noAnswer`, `callFailed`, `cancelled`, `timeout`, `batchIncomplete` |
| 7 | `connection` | 연결 실패·끊김(인증 중 끊김 포함) |
| 8 | `server` | 서비스 측 오류: `callOutcomeUnknown`(발신됐을 수 있음), `transferOutcomeUnknown`(전환이 요청됐을 수 있음), `internalError`, `callSetupFailed`, … |
| 9 | `handler` | 핸들러 실행 실패, 통화 중 종료 |
| 75 | `temporary` | 나중에 재시도: `concurrentLimitExceeded`, `duplicateCall`, `voiceGatewayUnavailable`, `callProviderUnavailable`, … |
| 130 | `interrupted` | Ctrl-C, SIGTERM |

## 7. 코딩 에이전트

```sh
tello init                          # .claude/skills/tello/SKILL.md, .codex/skills/tello/SKILL.md 생성
tello guide coding-agent --brief    # 코딩 에이전트용 운영 지침
tello doctor                        # 버전, 키, URL, 인증, 스킬 파일 점검
```

Claude Code와 Codex는 스킬 파일을 자동으로 읽습니다. 스킬 파일은 CLI가 관리하는
산출물이라 `tello init`이 현재 템플릿과 다르면 다시 쓰고, `tello doctor`가 오래된
파일을 알려 줍니다. 스킬을 지원하지 않는 에이전트는 지침 파일에
`tello guide coding-agent --brief`를 적어 두세요.

## 8. 개발

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
