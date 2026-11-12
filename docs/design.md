# tello-cli 설계

> 상태: 구현 기준 문서. 명령·출력·exit code·배포 계약은 이 문서를 따른다.
> 전송: 포털 에이전트·크루와 통화 관리, 도구·에이전트·크루 관리는 REST API, 실시간 턴 응답은
> [`tello-go`](https://github.com/tello-ai/tello-go) `v0.2.4` (WS 프로토콜 `1.0`).
> 참고: [vox CLI](https://docs.tryvox.co/docs/ai/cli), [Claude Code·Codex 연결](https://docs.tryvox.co/docs/ai/clients)

## 1. 목표와 비목표

목표

- 터미널과 코딩 에이전트(Claude Code, Codex)에서 포털 에이전트·크루 또는 실시간 응답으로 Tello 통화를 걸고, 진행 상황과 결과를 조회한다. 시나리오 확인이 필요한 에이전트는 통화 초안으로 확인한 뒤 건다.
- 계정의 도구·에이전트·크루를 REST API로 조회·생성·수정·삭제하고, 업무 목표로 크루를 자동 생성한다.
- 모든 명령이 `--json`을 지원하고, 결과를 exit code로 구분한다. 사람 도움 없이 AI가 파싱할 수 있어야 한다.
- 단일 바이너리. 사용자는 `npm install -g @tello-ai/cli` 또는 curl 설치 스크립트로 받는다. Go 툴체인이 필요 없다.

비목표

- 지식 베이스 관리, 판 고정·롤백 같은 버전 관리(`agent push/promote` 류). API 쓰기가 저장·배포·운영 반영을 한 번에 하므로 별도 배포 명령을 두지 않는다
- 서버 캠페인(pause/resume), 번호·위젯·워크스페이스·채널(목소리·STT·TTS) 관리
- 전화 없는 텍스트 테스트(`vox chat` 류)

REST `/v1`은 통화 생성·조회·목록·종료·전환·초안과 도구·에이전트·크루 관리를 제공한다.
WebSocket `/sdk`는 CLI 또는 핸들러가 실시간으로 답하는 통화에 사용한다.
본문 필드(도구 `config`, 에이전트 `llm`, 크루 `graph` 등)는 CLI가 해석하지 않고 JSON 객체 그대로 보낸다. 필드 계약은 REST API 문서가 정본이다.

## 2. 결정

| 항목 | 결정 | 이유 |
| --- | --- | --- |
| 구현 언어 | Go + REST 클라이언트 + `tello-go` SDK | CGO 없이 6개 플랫폼 크로스 빌드. REST와 WS에 같은 바이너리를 쓴다 |
| 명령 파서 | `spf13/cobra` | Go CLI 표준. 하위 명령·도움말 |
| 1순위 배포 | npm (`@tello-ai/cli` + 플랫폼 패키지) | 대부분의 개발자·코딩 에이전트 사용자에게 Node가 있다. vox(`@vox-ai/cli`)와 Codex(`@openai/codex`)도 같은 구조 |
| 2순위 배포 | `install.sh` / `install.ps1` (GitHub Release) | Node 없는 사용자. Claude Code 공식 설치도 curl이라 Node가 없을 수 있다 |
| 후순위 | Homebrew tap | 수요 확인 후 GoReleaser `homebrew_casks`로 추가 |
| 릴리스 빌드 | GoReleaser OSS | 크로스 빌드·아카이브·체크섬·GitHub Release |
| npm 게시 | 자체 스크립트 | GoReleaser `npms`는 Pro 전용이고 postinstall 다운로드 방식(alpha)이라 `--ignore-scripts`·사내망에서 깨진다 |

## 3. 저장소 구조

```text
cmd/tello/main.go          진입점. 시그널 → context, 의존성 조립, os.Exit
internal/app/              App(입출력·전역 플래그·의존성), 오류 분류, exit code, JSON 결과 쓰기
internal/version/          빌드 시 주입되는 Version/Commit/Date
internal/cli/              루트 명령 조립(--json, --api-url, --url)
internal/rest/             REST API 클라이언트·응답과 오류 변환
internal/cmd/target/       agent / crew / tool: 목록, get / create / update / delete, crew generate / generation
internal/cmd/call/         call create / get / list / end / transfer / batch / draft
internal/callrun/          WS 통화 1건 실행기(연결, 이벤트 큐, 응답 소스, 취소·타임아웃)
internal/handler/          핸들러 자식 프로세스 NDJSON 브리지
internal/cmd/auth/         auth login / status / logout
internal/credentials/      API 키 해석·저장(env > keychain > file)
internal/cmd/initcmd/      init (스킬 파일)
internal/cmd/guide/        guide
internal/cmd/doctor/       doctor
internal/cmd/version/      version
internal/guide/            go:embed 가이드·스킬 템플릿
npm/cli/                   메인 npm 패키지 템플릿(런처 bin/tello.js)
npm/scripts/               플랫폼 패키지 생성·게시 스크립트
scripts/install.sh         macOS/Linux 설치
scripts/install.ps1        Windows 설치
.goreleaser.yaml
.github/workflows/         ci.yml, release.yml
```

## 4. 명령

전역 플래그: `--json`(기계용 출력), `--api-url <https://…>`(REST API:
`--api-url` > `TELLO_API_URL` > `https://api.telloai.io/v1`), `--url <ws(s)://…>`
(WS 게이트웨이: `--url` > `TELLO_URL` > `wss://api.telloai.io/sdk`).
REST의 HTTP는 로컬 loopback에서만 허용한다. 잘못된 스킴이나 호스트는
`invalidUrl`(exit 2)이다.

| 명령 | 설명 |
| --- | --- |
| `tello auth login [--no-verify]` | API 키 입력(TTY면 숨김 프롬프트, 아니면 stdin 첫 줄) → REST 인증 확인 → 저장. `--no-verify`는 확인 생략 |
| `tello auth status [--offline]` | 키 출처 표시, REST API 인증 확인. `--offline`은 확인 생략 |
| `tello auth logout` | 저장된 키 삭제(환경변수는 유지) |
| `tello agent list` / `tello crew list` | 배포된 에이전트 / 활성 운영 버전이 있는 크루 목록(통화에 쓸 수 있는 대상) |
| `tello tool list [--limit n] [--cursor c]` | 도구를 id 오름차순으로 한 페이지 조회 |
| `tello {tool\|agent\|crew} get <id>` | 도구·에이전트·크루 상세(배포 상태와 무관, 전체 설정) |
| `tello {tool\|agent\|crew} create --body <JSON\|@파일\|-> [--idempotency-key <key>]` | 생성. 저장·배포·운영 반영까지 한 번에 |
| `tello {tool\|agent\|crew} update <id> --body <JSON\|@파일\|->` | 보낸 필드만 수정하고 바로 반영 |
| `tello {tool\|agent\|crew} delete <id>` | 삭제 |
| `tello crew generate --goal <목표> [--wait] [--poll-interval 2s] [--timeout <dur>] [--idempotency-key <key>]` | 목표로 크루 자동 생성 작업 시작. `--wait`는 끝날 때까지 조회 |
| `tello crew generation <generationId>` | 크루 생성 작업 조회 |
| `tello call create --to <번호> (--agent <id> \| --crew <id>) [--var 이름=값]… [--max-duration-seconds n] [--instructions …] [--ivr-goal …] [--ivr-opening-message …] [--notify-on-end] [--wait] [--poll-interval 2s] [--timeout <dur>] [--idempotency-key <key>]` | 포털 대상이 답하는 REST 통화. 기본은 수락 시 반환, `--wait`는 종료까지 조회. `--timeout`은 `--wait` 필요 |
| `tello call create --to <번호> [--prompt …] [--metadata <JSON\|@파일>] [--timeout <dur>] (-i \| -- <핸들러 명령…>)` | 실시간 응답 WS 통화. 종료까지 블로킹 |
| `tello call draft create --agent <id> --to <번호> [--instructions …] [--var 이름=값]… [통화별 설정] [--idempotency-key <key>]` | 통화 초안(시나리오) 만들기 |
| `tello call draft revise <draftId> --request <고칠 점> [--idempotency-key <key>]` | 초안 고치기. 새 초안 반환 |
| `tello call draft confirm <draftId> [--idempotency-key <key>]` | 초안 확정 = 실제 발신. 통화 객체 반환 |
| `tello call get <callId>` | REST·WS로 건 통화의 상태·시간·요약·전사·요금 조회 |
| `tello call list [--status s] [--agent id] [--crew id] [--created-after t] [--created-before t] [--limit n] [--cursor c]` | 계정 통화를 최신순으로 한 페이지 조회 |
| `tello call end <callId>` | 진행 중인 통화의 종료 요청. 실제 종료는 `get`으로 확인 |
| `tello call transfer <callId> (--to <번호> \| --agent-app) [--mode cold\|warm] [--plan <JSON\|@파일\|->] [--message <안내>] [--idempotency-key <key>]` | 통화 중인 통화를 전화번호나 상담APP 상담사로 전환 요청. 결과는 `get`으로 확인 |
| `tello call batch --tasks <파일\|-> [--max-retries 3] [--retry-delay 5s] [--timeout <dur>] (--agent <id> \| --crew <id> \| -i \| -- <핸들러 명령…>)` | 여러 건을 순차 발신 |
| `tello init [--target claude,codex] [--dir .]` | 코딩 에이전트 스킬 파일 생성·갱신 |
| `tello guide coding-agent [--brief]` | 코딩 에이전트용 운영 지침 출력(`tello guide`만 치면 주제 목록) |
| `tello doctor` | 버전·키·URL·인증·스킬 파일 점검 |
| `tello version` | CLI·SDK·프로토콜 버전 |

## 5. 출력 계약

- 사람용 모드: 결과는 stdout, 진단·경고·오류는 stderr.
- `--json` 모드: stdout에는 한 줄에 JSON 하나(NDJSON)만 쓴다. 경고는 stderr.
- **`--json` 명령은 일반적으로 정확히 한 줄의 `cli.result`로 끝난다.** 스트리밍 명령은 그 앞에 이벤트 줄을 쓴다.
- 그룹 명령(`tello`, `tello auth`, `tello agent`, `tello crew`, `tello tool`, `tello call`, `tello call draft`)을 하위 명령 없이 실행하면 사람용 모드에서는 도움말(exit 0), `--json`에서는 usage 오류(exit 2). 모르는 하위 명령은 usage 오류이며 비슷한 이름을 제안한다. cobra 기본 `completion` 명령은 제공하지 않는다.
- 예외(`cli.result` 없이 끝남): `--help`/`help`, `--version`(사람용 텍스트), 두 번째 Ctrl-C/SIGTERM(즉시 종료), `auth login` 숨김 프롬프트에서의 Ctrl-C.

```json
{"type":"cli.result","ok":true,"data":{}}
{"type":"cli.result","ok":false,"error":{"kind":"refused","code":"insufficientCredit","message":"…","retryable":false,"exitCode":4},"data":{}}
```

- `error.question`은 값이 있을 때만(`callRejected`). `data`는 실패 시에도 있을 수 있다(발신·전환·생성 요청 실패의 `idempotencyKey`, `missingVariables`, 도구·에이전트·크루 거절의 `reason`·`detail`, 대기 중 실패의 마지막 통화 객체나 크루 생성 작업 등).
- REST `--wait`는 상태가 바뀔 때 `cli.status`(`callId`, `status`, batch에서는 `index`도)를 쓴다. `crew generate --wait`는 `generationId`, `status`를 쓴다. 폴링이므로 모든 중간 상태가 보인다는 보장은 없다. 성공 결과는 REST 통화 객체(크루 생성은 작업 객체)이며, `--wait`가 없으면 수락 시점의 객체다.
- WS 이벤트 줄은 게이트웨이 수신 프레임을 **그대로** 쓴다(`call.created`, `user.turn`, `agent.turn`, `answer.accepted`, `dtmf.accepted`, `call.statusChanged`, 종단 이벤트, `error`). `auth.ok`와 SDK 합성 이벤트 `disconnected`는 쓰지 않는다. WS 통화의 결과는 `callId`, `status`다.
- CLI가 만드는 줄은 `cli.` 접두사: `cli.result`, `cli.status`, `cli.task`(batch 건별 결과), `cli.retry`(batch 재시도).
- `delete`의 결과는 `{"id":…,"deleted":true}`다(API는 본문 없는 `204`). 사람용 모드에서 도구·에이전트·크루 객체는 들여쓴 JSON으로 출력한다.
- 사람용 오류 출력은 `c_`로 시작하는 id(`data.id`, `data.callId`, `data.call.id`)만 통화 ID로 표시하고, 다른 `data.id`(크루 생성 작업 등)는 `id`로 표시한다.
- REST의 snake_case 오류 코드는 camelCase로 변환한다. JSON은 HTML 이스케이프 없이 UTF-8 그대로 쓴다.

## 6. Exit code

| code | kind | 의미 | 대표 code |
| --- | --- | --- | --- |
| 0 | — | 성공 | |
| 1 | `internal` | 예상하지 못한 오류 | |
| 2 | `usage` | 플래그·인자 오류(형식이 틀린 통화·초안·자원 id, JSON 객체가 아닌 `--body`·`--plan`, 멱등 키 포함), 잘못된 REST·WS URL | `usage`, `invalidUrl` |
| 3 | `auth` | 키 없음·인증 실패, 키 권한·허용 IP 밖 | `apiKeyMissing`, `unauthenticated`, `insufficientScope`, `ipNotAllowed` |
| 4 | `refused` | 계정 정책 거부 | `insufficientCredit`, `outboundLimitExceeded`, `calleeNotVerified`, `callerNotVerified`, `callerNumberUnavailable` |
| 5 | `invalid` | 요청 검증·상태 오류 | `targetRequired`, `agentNotFound`, `crewNotFound`, `missingVariables`, `invalidRequest`, `idempotencyKeyReused`, `callNotFound`, `callNotActive`, `invalidTransferRequest`, `transferModeUnsupported`, `transferDestinationUnsupported`, `callRejected`, `unsupportedForCrew`, `notifyUnavailable`, `callRefused`, `instructionsUnclear`, `draft*`(`draftNotApplicable`, `draftNotFound`, `draftAlreadyConfirmed`, `draftSuperseded`, `draftNotConfirmable`, `draftExpired`, `draftRevisionLimit`), `invalidTool`·`invalidAgent`·`invalidCrew`, `toolNotFound`, `defaultToolProtected`, `toolInUse`, `toolTypeManaged`, `*NotPublishable`, `agentEngineImmutable`, `agentInUse`, `crewInUse`, `crewGenerationNotFound`, `invalidQuery`, `invalidCursor`, `payloadTooLarge` |
| 6 | `callEnded` | 통화가 완료되지 못함 | `noAnswer`, `callFailed`, `cancelled`, `timeout`, `batchIncomplete` |
| 7 | `connection` | 연결 실패·끊김(인증 중 포함), 크루 생성 상태 확인 실패 | `connectionFailed`, `connectionClosed`, `sessionReplaced`, `crewGenerationStatusUnknown` |
| 8 | `server` | 서비스 측 오류, 발신·전환·쓰기 결과 불명, 크루 생성 실패 | `callOutcomeUnknown`, `transferOutcomeUnknown`, `writeOutcomeUnknown`, `internalError`, `callSetupFailed`, `crewGenerationFailed`, `crewGenerationTimeout`, `crewDeployFailed`, `crewPromoteFailed` |
| 9 | `handler` | 핸들러 프로세스 실패 | `handlerStartFailed`, `handlerExited` |
| 75 | `temporary` | 나중에 재시도 | `concurrentLimitExceeded`, `duplicateCall`, `idempotencyInProgress`, `voiceGatewayUnavailable`, `callProviderUnavailable`, `callNotInProgress`, `upstreamUnavailable`, `toolConflict`, `crewGenerationInProgress`, `crewGenerationRunning` |
| 130 | `interrupted` | Ctrl-C / SIGTERM | `interrupted` |

`retryable`은 `kind == temporary`일 때만 true다. 이는 나중에 성공할 가능성의 분류이지 중복 발신이 불가능하다는 보장은 아니다. 분기는 `code`로 한다(`message`는 표시용).

## 7. 인증

- 키 해석 순서: `TELLO_API_KEY` 환경변수 > OS 키체인(service `tello-cli`, account `api-key`) > `<사용자 설정 디렉터리>/tello/credentials.json`(0600). REST·WS는 같은 서버 키를 사용한다.
- `auth login`은 REST `agent list`로 키를 확인한 뒤 키체인에 저장하고, 키체인을 쓸 수 없으면(헤드리스 Linux·WSL 등) 파일에 저장하고 경고한다. `--no-verify`는 인증 확인을 생략한다.
- `auth status`는 같은 REST 요청으로 확인하며 `--offline`은 네트워크 요청을 생략한다. `auth logout`은 저장된 키를 지우되 환경변수는 지우지 않는다.
- 키는 플래그로 받지 않는다(셸 히스토리·프로세스 목록 노출). 출력·로그·오류 어디에도 쓰지 않는다.
- CI: `TELLO_API_KEY`(필수), `TELLO_API_URL`과 `TELLO_URL`(선택).

## 8. `call create`

### 8.1 응답 소스 (넷 중 하나 필수)

- `--agent <id>` / `--crew <id>`: 포털에 배포된 대상이 대화를 진행하며 REST API로 발신한다. `--var 이름=값`을 반복해 시작 변수를 전달한다. `--prompt`, `--metadata`, `-i`, 핸들러는 함께 쓰지 않는다.
- `-i, --interactive`: WebSocket으로 사람이 답한다. 입력 한 줄 = `answer`. `/dtmf <digits>` → `sendDtmf`, `/cancel` → `cancel`, `/help`. stdin EOF는 입력 종료일 뿐 통화를 끝내지 않는다.
- 핸들러: WebSocket 통화의 각 턴에 답하는 `--` 뒤 명령을 자식 프로세스로 띄운다(셸을 거치지 않음). 예: `tello call create --to +8210… -- python3 bot.py`. `--prompt`, `--metadata`는 이 모드와 `-i`에서만 사용한다.

REST 통화별 설정은 그 통화에만 적용되고 대상의 운영 판은 바꾸지 않는다. 플래그와 본문 필드는
`--max-duration-seconds` → `maxDurationSeconds`(30~1800, 에이전트·크루), `--instructions` →
`instructions`, `--ivr-goal`·`--ivr-opening-message` → `ivr.goal`·`ivr.openingMessage`,
`--notify-on-end` → `notifyOnEnd: true`(이 넷은 에이전트만)다. 문서가 명시한 조합만 요청 전에
막는다: `--crew`에 에이전트 전용 설정(API는 `unsupportedForCrew`), 범위 밖 통화 시간, WS 통화의
통화별 설정은 usage 오류다. 길이 한도는 서버가 판단한다(`invalidRequest`). 설정하지 않은 값은
보내지 않으므로 기존 요청 본문과 멱등 비교는 그대로다. `--instructions`는 발신 전 AI 검토로
`callRefused`(`data.detectedIntent`)나 `instructionsUnclear`(`data.question`)가 될 수 있다(exit 5).

REST는 기본적으로 생성 수락 시 반환하고, `--wait`는 `--poll-interval`(기본 2초)마다
종료 상태를 조회한다. 대기 결과는 `completed`일 때만 성공하며 `noAnswer`, `failed`,
`cancelled`는 각각 `noAnswer`, `callFailed`, `cancelled` 오류(exit 6)와 통화 객체를
반환한다. `--wait` 없이는 `--timeout`을 사용할 수 없다. 기본 타임아웃은 없다.
대기 중 타임아웃 또는 첫 Ctrl-C/SIGTERM은 종료를 요청하고 최대 10초 동안 상태 확인을
시도한다. 종료 요청이나 확인이 실패하면 통화가 계속될 수 있으므로 `call get`으로
확인해야 한다. 폴링 오류로 대기를 중단할 때도 통화가 계속될 수 있다. REST HTTP
요청 상한은 발신 생성·초안 만들기·고치기·확정과 도구·에이전트·크루 쓰기 2분, 나머지 30초이며 사용자 지정 대기 제한은 별개다.

REST 발신에는 임의 생성 또는 `--idempotency-key`로 지정한 키를 보낸다. 지정한 키는 1~255바이트이고 쉼표와 제어문자가 없어야 한다(API가 거부하거나 헤더로 보낼 수 없는 값). 아니면 요청 없이 usage 오류다. 발신 요청
오류의 `data.idempotencyKey`는 해당 키다. 프로세스·결과 출력 유실에도 복구하려면
첫 요청 전에 작업별 키를 직접 정하고 보관해야 한다. 수락 후 대기 실패에는 키 대신
마지막 통화 객체가 반환될 수 있으므로 해당 id를 조회한다.

서버가 정상 저장한 `201` 수락 응답과 `502 callOutcomeUnknown`은 24시간 보관되며,
같은 계정에서 같은 키와 정규화된 동일 요청을 보내면 저장된 응답을 재생한다. 이는
최신 상태가 아니므로 id로 `call get`한다. 다른 요청에 저장·처리 중인 키를 재사용하면
`idempotencyKeyReused`다. 저장 기간 이후에는 같은 키도 새 요청으로 처리될 수 있다.
발신 수락 후 응답 저장에 실패하면 키가 잠시 `idempotencyInProgress`로 남았다가
5분 뒤 같은 키도 새 발신을 일으킬 수 있다. 따라서 멱등 키는 무조건적인 중복 방지나
정확히 한 번 발신을 보장하지 않는다.

응답을 받지 못했거나 `callOutcomeUnknown`이면 이미 발신했을 수 있다. 새 키로
재발신하지 말고 `call list`에서 대상과 시각을 확인하며, 불분명하면 지원팀에
문의한다. `idempotencyInProgress`가 계속될 때도 목록을 확인한다. 일시 오류를
재시도할 때는 동일한 키와 요청을 사용하고 위 저장 한계를 고려한다. 명확한 발신 전
검증 오류는 원인을 고친 뒤 재시도할 수 있다.
REST 멱등 키는 WS 통화에 적용되지 않는다. 실제 통화는 과금되며 수신 번호는 계정의
인증 번호여야 한다(`calleeNotVerified` / WS `callerNotVerified`).

### 8.2 핸들러 프로토콜 (stdin/stdout NDJSON)

- 핸들러 stdin: 첫 줄 `{"type":"cli.start","to":"…","prompt":"…","metadata":{…}}`(batch는 `"index"` 추가), 이후 게이트웨이 수신 프레임 전부(§5와 같은 집합).
- 핸들러 stdout: WS 명령 봉투 그대로(프로토콜 §4). 허용 명령은 세 개:

  ```json
  {"event":"answer","data":{"text":"확인했습니다.","messageId":"선택","requestId":"선택"}}
  {"event":"sendDtmf","data":{"digits":"1234#"}}
  {"event":"cancel","data":{}}
  ```

  빈 줄은 무시. 깨진 JSON·허용 외 명령은 stderr 경고 후 무시.
- 핸들러 stderr: CLI stderr로 그대로 흘린다(로그용).
- 핸들러는 자기 프로세스 그룹에서 돈다(Windows는 새 콘솔 프로세스 그룹). 터미널 Ctrl-C는 CLI만 받고, CLI가 통화를 취소한 뒤 핸들러를 끝낸다. 핸들러는 종단 이벤트와 stdin EOF를 받는다.
- 핸들러 환경변수에서 `TELLO_API_KEY`를 뺀다. 핸들러는 키가 필요 없다.
- 핸들러 stdin 쓰기는 별도 고루틴과 무제한 큐로 한다. 핸들러가 stdin을 읽지 않아도 실행기 루프(타임아웃·취소)는 멈추지 않는다.
- 순서: 핸들러를 먼저 띄운 뒤(실행 실패 시 과금 전에 `handlerStartFailed`) 연결·발신한다. 실행은 됐지만 발신 뒤에 죽으면 통화가 이미 걸렸을 수 있다(취소 후 `handlerExited`, `data.callId`).
- 통화가 끝나면 핸들러 stdin을 닫고 5초 기다린 뒤 프로세스 그룹째 종료시킨다. 이때의 핸들러 exit code는 결과에 영향이 없다(경고만).
- 통화 중 핸들러가 먼저 죽으면 `cancel`을 보내고 종단을 기다린 뒤 `handlerExited`(exit 9). 단 핸들러가 스스로 `cancel`을 보낸 뒤 종료하면 정상적인 끊기로 보고 종단을 기다려 `cancelled`(exit 6).
- 응답 지연: 게이트웨이 필러 기본 3초. 핸들러는 그보다 빨리 답해야 자연스럽다.

### 8.3 WS 통화 결과

| 종료 원인 | exit | code |
| --- | --- | --- |
| `call.completed` | 0 | — (`data: {callId, status}`) |
| `call.noAnswer` | 6 | `noAnswer` |
| `call.failed` | 6 | `callFailed` |
| 상대·서버 측, 또는 사용자(`/cancel`)·핸들러(`cancel`)가 보낸 `cancelled` | 6 | `cancelled` |
| `--timeout` 만료 → cancel | 6 | `timeout` |
| Ctrl-C / SIGTERM → cancel | 130 | `interrupted` |
| SDK 오류(`WaitClosed`) | §6 분류 | 게이트웨이 code |

실패 결과의 `data`에도 `callId`(있으면)와 마지막 `status`를 싣는다.

### 8.4 WS 취소와 시그널

- 첫 Ctrl-C/SIGTERM: `cancel` 전송 → 종단 이벤트를 최대 10초 기다림 → exit 130.
- `call.created` 전에 보낸 `cancel`(Ctrl-C, `--timeout`, 핸들러 종료·`cancel`, `/cancel`)은 게이트웨이가 무시할 수 있으므로(프로토콜 §4.4) `call.created`가 오면 한 번 더 보내고 10초 대기를 다시 시작한다.
- 두 번째: 즉시 종료(exit 130, 숨김 프롬프트였다면 터미널 에코 복구). 연결이 끊기면 게이트웨이가 통화 취소를 처리한다(프로토콜 §8). 종료가 확인되지 않았다면 재발신 전에 통화 상태를 확인한다.
- 구현: `main`이 첫 시그널에 루트 context를 취소하고, 두 번째에 `os.Exit(130)`. 실행기는 context 취소를 Ctrl-C로 해석하고, 이후 SDK 호출에는 별도 context를 쓴다.

### 8.5 SDK 사용상 주의

- SDK 핸들러는 수신 고루틴에서 동기로 돈다. 핸들러에서 블로킹하면 ping 처리가 멈추므로, 이벤트는 무제한 큐에 넣고 메인 루프에서 처리한다.
- `WaitClosed`는 종료 판정의 기준이다(오류 프레임이 통화를 끝내는지는 SDK가 requestId로 판단). 단, 통화 종료 채널이 닫힌 **뒤에** 마지막 이벤트가 핸들러로 emit 되므로, `WaitClosed`가 반환된 뒤 해당 이벤트(종단/`error`/`disconnected`)가 큐에 들어올 때까지(최대 2초) 기다렸다가 출력하고 끝낸다.

## 9. `call get` / `call list` / `call end` / `call transfer`

- 네 명령은 REST API를 이용하며 REST·WS, API·A2A·SDK 등으로 건 계정 통화를 조회하거나 제어한다. 통화 id는 `c_12345` 같은 문자열로 다룬다. API가 출력하는 형식(`c_` + 앞자리 0 없는 양의 정수, int64 범위)이 아니면 요청 없이 usage 오류(exit 2)다. `c_01`처럼 서버가 `c_1`로 정규화해 돌려주는 id는 요청이 처리된 뒤 응답 검사에서 `invalidResponse`가 되므로 미리 막는다.
- `call get <callId>`는 진행 중이거나 끝난 통화 객체를 반환한다. `status`, 시간, `endReason`, `summary`, `transcript`, `creditCharged` 등을 확인한다. 종료 직후에도 요약·전사·요금은 null일 수 있으므로 필요하면 나중에 다시 조회한다.
- `call list`는 최신순으로 한 페이지를 반환한다. JSON 결과의 `data`는 `{"data":[통화…],"nextCursor":…}`이며 `nextCursor`가 있으면 다른 필터를 유지한 채 `--cursor`로 다음 페이지를 요청한다. 필터는 `--status`, `--agent`, `--crew`, `--created-after`, `--created-before`이고 `--limit`은 1~100(기본 20)이다. 날짜만 지정하면 UTC 자정이며 시각에는 시간대를 지정한다.
- `call end <callId>`는 진행 중인 통화의 종료를 요청한다. 응답은 요청 수락 시점의 객체이므로 `call get`으로 실제 종료를 확인한다. 이미 종료된 통화는 `callNotActive`(exit 5)다.
- `call transfer <callId>`는 통화를 넘기도록 요청한다(`POST /calls/{callId}/transfer`). 대상은 `--to <번호>`(`destination: {"type":"phone","target":…}`)나 `--agent-app`(`{"type":"agentApp"}`, 그 통화 에이전트의 상담APP 상담사) 중 하나다. `--mode`는 `cold`(기본, 바로 연결)나 `warm`(받은 사람에게 먼저 안내, 전화번호만)이다. `--plan`은 API의 `transferPlan` 객체(JSON 객체·`@파일`·`-`)를 해석하지 않고 그대로 보낸다. `warm`은 필수이고, `cold` 번호 전환에는 `blind-transfer-add-summary-to-sip-header`를 쓸 때만 넣는다. `--message`는 전환 직전 고객 안내(`message`)다. 문서가 금지한 조합(대상 없음·둘 다, 알 수 없는 모드, `warm`인데 `--plan` 없음, `warm`·`--plan`과 `--agent-app`)은 요청 없이 usage 오류다. `transferPlan` 세부 규칙과 번호 정규화는 서버가 판단한다(`invalidTransferRequest`, `transferModeUnsupported`, `transferDestinationUnsupported`, exit 5). 선택 필드는 비어 있으면 보내지 않으므로 `--to`만 쓴 요청은 이전 버전과 같은 바이트(`{"mode":"cold","destination":{"type":"phone","target":…}}`)이고 저장된 키와도 같은 요청이다. `end`와 달리 출처를 따지지 않아 수신 전화도 넘길 수 있다.
  - 응답 `202`는 접수일 뿐이다. 번호를 호출하는 동안과 전환에 실패했을 때 모두 `inProgress`이고, 성공하면 AI가 빠지고 전화 통화는 끝난다. 브라우저로 연결된 통화(웹콜 등)는 성공 뒤에도 그 대화가 끝날 때까지 `inProgress`일 수 있다. 성공 여부를 알려 주는 필드가 없어 `--wait`는 두지 않는다. `202`가 아닌 성공 응답이나 다른 통화 객체는 `invalidResponse`다.
  - 접수돼도 실행되지 않는 요청이 있다. 통화가 끝나는 중이거나 다른 전환(이 API든 AI의 전환 도구든)이 진행 중일 때 온 요청, 상담APP 상담사가 이미 이어받은 통화에 온 요청은 `202`이지만 효과가 없다. 실패한 것처럼 보여도 앞선 전환이 끝날 시간을 두고 다시 요청한다.
  - `Idempotency-Key`는 필수다. `--idempotency-key`가 없으면 실행마다 새 키를 만든다. 서버는 `202`만 저장하므로 같은 키로는 실패한 전환을 다시 시도할 수 없다(저장된 `202`만 돌아온다). 같은 키는 응답이 유실된 같은 요청을 다시 보낼 때만 쓴다. 거절된 요청은 저장되지 않아 같은 키로 다시 실행되며, 한 통화가 두 번 넘어가지 않는 것은 키가 아니라 VGW의 «통화당 한 번 성공» 규칙 덕분이다. 키 공간은 `call create`와 같으므로 생성에 쓴 키를 쓰지 않는다.
  - 오류의 `data.idempotencyKey`는 보낸 키다. 전환이 요청됐을 수 있는 경우 — 연결 실패·응답 유실, Ctrl-C(`interrupted`, exit 130, 서버는 끊겨도 처리를 계속한다), 문서에 없는 5xx(프록시 오류 페이지 등, `transferOutcomeUnknown` exit 8), `voiceGatewayUnavailable`(늦게 전달돼 전환될 수 있음) — 에는 `call get`으로 확인한 뒤 같은 키로 다시 보낸다. 202 뒤 결과 출력에 실패하면 call id와 키를 stderr 경고로 남긴다.
  - `callNotInProgress`(exit 75)는 통화가 아직 `inProgress`가 아니라는 뜻이다(발신 중, 또는 상담APP 상담사에게 넘기는 중 — `--agent-app` 전환 중에는 `transferring`). `inProgress`가 된 뒤 같은 요청이 성공할 수 있다. 끝난 통화는 `callNotActive`, 번호·안내·전환 방식 오류는 `invalidTransferRequest`(exit 5)다.

## 10. `call batch`

- `--tasks`: REST 대상에는 `[{"to":"…","variables":{…}}]`, `-i`·핸들러에는 `[{"to":"…","prompt":"…","metadata":{…}}]`. `-`면 stdin이며 `-i`와 함께 사용할 수 없다.
- 한 건씩 순차 처리한다. REST는 건별 생성 후 폴링하고 WS는 건마다 새 연결로 실행한다. 핸들러도 건마다 새로 띄운다. 건별 `--timeout`의 기본은 제한 없음이다.
- 재시도는 최대 `--max-retries`(기본 3)회이며 대기 `--retry-delay`(기본 5초)를 매번 두 배로 늘리고 `cli.retry`를 출력한다. REST는 통화 id가 없고 발신 전 거부가 확인된 일시 오류(`concurrentLimitExceeded`, `duplicateCall`, `creditCheckUnavailable`, `callRecordUnavailable`, `voiceGatewayUnavailable`)만 자동 재시도한다. `idempotencyInProgress`, 결과 불명 오류와 생성 후 폴링 오류는 재발신하지 않는다. WS는 일시 오류를 재시도한다.
- REST는 한 batch 실행 안에서 작업마다 서로 다른 멱등 키를 생성하고 그 작업의 재시도에 재사용한다. batch를 다시 실행하면 새 키를 생성하므로 이전 실행이나 결과가 유실된 뒤 전체를 재실행하면 중복 발신될 수 있다. 먼저 `call list`와 보존된 `cli.task` 결과를 확인한다. WS 재시도에는 REST 멱등 키가 적용되지 않는다. 멱등 응답의 24시간 보관·저장 실패 한계는 §8.1과 같다.
- `auth`, `interrupted`, 계정 전체 거부, 사용할 수 없는 대상, `callOutcomeUnknown` 등은 남은 작업을 중단한다. REST 생성의 연결·서버 오류나 안전한 발신 전 거부로 확인되지 않은 일시 오류, 종료되지 않은 통화의 대기 실패도 남은 작업을 중단한다. 재발신 전에 통화를 확인한다.
- 건마다 `{"type":"cli.task","index":0,"to":"…","ok":true,"callId":"…","status":"completed","attempts":1}`(실패 시 `error`)를 출력한다.
- 결과 `data`: `total`, `completed`, `failed`, `skipped`. 모두 완료면 exit 0, 중단이면 중단 원인의 exit code, 일부 실패면 exit 6(`batchIncomplete`). 실패한 건이 모두 발신된 것은 아니므로 각 건의 id와 오류를 살핀다.
- batch 작업은 통화별 설정(§8.1)을 받지 않는다.

## 11. `call draft`

아웃바운드 시나리오 자동 생성을 쓰는 에이전트는 걸기 전에 시나리오를 확인할 수 있다(`/call-drafts`).

- `draft create --agent <id> --to <번호> [--var …] [통화별 설정]`은 `POST /call-drafts`다. 본문은 에이전트 통화 생성과 같고 `crewId`는 없다(크루는 초안이 없으므로 `--crew` 플래그를 두지 않는다). 번호·변수·설정은 초안에 저장되어 확정 때 다시 보내지 않는다. 결과는 초안 객체(`id` `cd_…` 또는 null, `status`, `preview.summary`, `question`, `excludedContent`, `revision`, `revisionsLeft`, `expiresAt`, `callId`)다.
  - `status`: `pendingConfirmation`(요약을 보여 주고 확정·수정), `needsClarification`(`question`에 답해 다시 요청, 저장 없음), `refused`, `confirmed`(에이전트에 완성된 시나리오가 있어 **이미 발신**, `callId`).
  - 만들기도 발신할 수 있으므로 `call create`와 같은 방식으로 보낸다: 2분 상한, 멱등 키(임의 생성 또는 `--idempotency-key`, 실패 시 `data.idempotencyKey`), 문서에 없는 503·연결 실패·Ctrl-C는 발신됐을 수 있다는 결과(`callOutcomeUnknown`·`connectionFailed`·`interrupted`)다.
  - `draftNotApplicable`(exit 5, `data.reason`)은 아무것도 저장·발신하지 않았다는 뜻이다. 같은 값으로 `call create`를 쓴다.
- `draft revise <draftId> --request <고칠 점>`은 `POST /call-drafts/{id}/revise`(`{"request":…}`, 정확히 200)다. 새 id의 초안이 오고 앞 초안은 `draftSuperseded`다. 5번째 판까지(`draftRevisionLimit`). 빈 `--request`는 usage 오류다.
- `draft confirm <draftId>`는 본문 없는 `POST /call-drafts/{id}/confirm`이다. **실제 과금 통화를 건다.** `Idempotency-Key`가 필수라 없으면 임의 생성한다. 결과는 `call create`와 같은 통화 객체이며 통화 객체 검사를 통과해야 한다. 같은 키는 저장된 응답을 재생해 두 번 걸지 않는다. 이미 확정한 초안은 `draftAlreadyConfirmed`(`data.callId`), 만료(10분)는 `draftExpired`다. 발신 조건 오류와 `callOutcomeUnknown`은 `call create`와 같다. `--wait`는 두지 않으며 `call get`으로 따라간다.
- 초안 id는 API가 출력하는 형식(`cd_` + 앞자리 0 없는 양의 정수, int64 범위)이 아니면 요청 없이 usage 오류다.

## 12. 도구·에이전트·크루 관리

- `tool list`(`GET /tools?limit&cursor`, `{data,nextCursor}`, `--limit` 1~100 기본 50, 0은 생략), `{tool|agent|crew} get <id>`(`GET /<자원>/{id}`). `agent list`·`crew list`는 계속 통화 대상 목록(`GET /agents`·`/crews`)이다.
- `create --body`(`POST`, 정확히 201), `update <id> --body`(`PATCH`, 정확히 200, 보낸 필드만), `delete <id>`(`DELETE`, 정확히 204·본문 없음). 다른 2xx는 `invalidResponse`다. 쓰기는 저장·배포·운영 반영을 한 번에 하므로 다음 통화부터 반영된다.
- `--body`는 JSON 객체 리터럴, `@파일`, `-`(stdin) 중 하나이며 숫자를 원문 그대로 보낸다. JSON 객체가 아니거나(배열·null·문자열 포함) 깨졌거나 뒤에 데이터가 더 있으면 요청 없이 usage 오류다. 필드 의미는 REST 문서가 정본이며 CLI는 해석하지 않는다.
- id는 API가 출력하는 형식(앞자리 0 없는 양의 정수, int64 범위)이 아니면 요청 없이 usage 오류다(경로 조작 방지 포함).
- 생성만 `Idempotency-Key`를 받는다(선택). CLI는 `call create`처럼 임의 생성 또는 `--idempotency-key`를 보내고 실패 시 `data.idempotencyKey`로 알린다. 서버는 `201`과 `502 write_outcome_unknown`만 24시간 저장한다. 수정·삭제에는 키를 보내지 않는다.
- 결과 불명: 연결 실패·응답 유실·Ctrl-C(`interrupted`), 문서에 없는 5xx(프록시 오류 등)는 쓰기가 반영됐을 수 있다는 뜻이다. 문서에 없는 5xx는 `writeOutcomeUnknown`(exit 8)으로 바꾼다. `get`·`list`로 확인한 뒤, 생성은 같은 키로만 다시 보내거나 없음을 확인한 뒤 새 키로 만든다. `upstreamUnavailable`(503)은 처리되지 않았으므로 다시 보내도 된다(exit 75).
- 거절은 `data.reason`(소문자 하위 코드)과 `data.detail`(한국어 설명)을 그대로 `data`로 전달한다.

### 12.1 `crew generate` / `crew generation`

- `crew generate --goal <목표>`는 `POST /crews/generate`(`{"goal":…}`)다. 새 작업은 202, 같은 목표의 진행 중이거나 10분 안에 성공한 작업을 돌려줄 때는 포털 상태 그대로 200일 수 있어 둘 다 성공으로 받는다. 그 밖의 2xx와 `id`·`status`가 없는 응답은 `invalidResponse`다. 멱등 키는 생성과 같은 규칙이다. 다른 목표가 진행 중이면 `crewGenerationInProgress`(exit 75, `data.id`)다.
- `--wait`가 없으면 작업을 그대로 반환한다(exit 0). `--wait`는 `--poll-interval`(기본 2초)마다 `GET /crew-generations/{id}`를 조회하고 상태가 바뀔 때 `cli.status`(`generationId`, `status`)를 쓴다. `succeeded`면 작업(`crew` 포함)이 결과다. `failed`면 작업의 `error.code`를 camelCase로(`crewGenerationFailed`·`crewGenerationTimeout`·`crewDeployFailed`·`crewPromoteFailed`, 없으면 `crewGenerationFailed`) exit 8과 작업을 `data`로 반환한다. `running`·`succeeded`·`failed`가 아닌 상태는 `invalidResponse`다.
- `--timeout`(`--wait` 필요, 기본 없음)은 대기만 멈춘다: `crewGenerationRunning`(exit 75, `data`는 마지막 작업). Ctrl-C는 `interrupted`(exit 130). 일시 오류 폴링은 연속 3회까지 재시도하고, 그 뒤에는 `crewGenerationStatusUnknown`(exit 7, 마지막 작업)이다. 어느 경우든 작업은 서버에서 계속된다. 같은 목표로 다시 실행하면 같은 작업이 돌아오므로 크루가 둘 생기지 않는다.
- `crew generation <generationId>`는 작업을 그대로 반환한다(상태와 무관하게 exit 0). 없는 작업은 `crewGenerationNotFound`(exit 5)다.

## 13. `init` / `guide` / `doctor` / `version`

- `init`: `.claude/skills/tello/SKILL.md`, `.codex/skills/tello/SKILL.md`를 만든다. CLI가 관리하는 산출물이라 내용이 다르면 덮어쓴다(머리말에 명시). 파일별 `created` / `updated` / `unchanged`를 보고한다. 스킬 본문은 짧게 두고 `tello guide coding-agent`를 가리킨다.
- `guide coding-agent`: 코딩 에이전트용 운영 계약(안전 규칙, 표준 흐름, 핸들러 프로토콜, 출력·exit code 요약). `--brief`는 요약판.
- `doctor`: `version`, `apiKey`, `apiUrl`(http/https 검사), `auth`(REST 인증), `gatewayUrl`(ws/wss 검사), `gateway`(WS 연결·인증), `skills`(현재 디렉터리 스킬 파일 최신 여부). 각 `ok`/`warn`/`fail`/`skip`. 실패가 있으면 첫 실패의 kind로 exit(URL 스킴 오류는 usage `invalidUrl`, exit 2).
- `version`: `version`, `commit`, `date`, `sdkVersion`(`tello.Version`), `protocolVersion`, `os`, `arch`.

## 14. 배포

### 14.1 빌드 산출물 (GoReleaser)

- 대상: `darwin/{amd64,arm64}`, `linux/{amd64,arm64}`, `windows/{amd64,arm64}`, `CGO_ENABLED=0`, `-trimpath`, `-s -w`, 버전은 ldflags로 `internal/version`에 주입.
- 아카이브: `tello_<version>_<os>_<arch>.tar.gz`(Windows는 `.zip`), 루트에 `tello`(`tello.exe`), `LICENSE`, `README.md`. 체크섬: `checksums.txt`(SHA-256).
- 버전의 단일 출처는 git 태그 `vX.Y.Z`다. npm 패키지 버전도 태그에서 만든다.

### 14.2 npm

- 플랫폼 패키지 `@tello-ai/cli-{darwin-arm64,darwin-x64,linux-arm64,linux-x64,win32-arm64,win32-x64}`: `os`/`cpu` 필드, `bin/tello`(`bin/tello.exe`)와 `LICENSE`만 담는다. `bin` 필드·스크립트 없음.
- 메인 패키지 `@tello-ai/cli`: 런처 `bin/tello.js` + 같은 버전으로 고정한 `optionalDependencies`. postinstall 없음 → `--ignore-scripts`에서도 동작.
- 게시 순서: 플랫폼 패키지 6개 → 메인. 이미 게시된 버전은 건너뛴다(재실행 안전). provenance 사용. 프리릴리스 버전(`1.2.0-rc.1`)은 dist-tag `next`로 게시한다.
- 런처 규칙:
  - 바이너리 위치: `TELLO_BINARY_PATH`가 있으면 그것, 아니면 `require.resolve("<플랫폼 패키지>/bin/tello[.exe]")`. 없으면 재설치·curl 설치를 안내하고 exit 1.
  - `stdio: "inherit"`로 실행(파이프 중계 금지).
  - SIGINT는 전달하지 않고 삼킨다: 터미널 Ctrl-C는 같은 프로세스 그룹의 Go 프로세스에 직접 가므로, 전달하면 두 번 받아 "두 번째 Ctrl-C = 강제 종료"가 된다.
  - SIGTERM·SIGHUP은 자식에게 전달한다.
  - 자식의 exit code를 그대로 돌려준다. 시그널로 죽었으면 같은 시그널로 자신을 종료한다.

### 14.3 curl / PowerShell

- `install.sh`: OS(`linux`/`darwin`)·아키텍처(`amd64`/`arm64`, Rosetta면 arm64) 판별 → 아카이브·`checksums.txt` 다운로드 → SHA-256 검증 → `~/.local/bin/tello` 설치 → PATH 안내. `curl` 또는 `wget`.
- `install.ps1`: Windows PowerShell 5.1 호환. zip·체크섬 검증 → `%LOCALAPPDATA%\Programs\tello` 설치 → 사용자 PATH에 추가.
- 환경변수: `TELLO_VERSION`(기본 latest), `TELLO_INSTALL_DIR`, `TELLO_DOWNLOAD_BASE_URL`(기본 `https://github.com/tello-ai/tello-cli/releases`, 미러·테스트용).

### 14.4 워크플로

- `ci.yml`: gofmt, `go vet`, `go build`, `go test -race`, `node --test 'npm/**/*.test.mjs'`(Node 22+는 디렉터리 인자를 받지 않는다), shellcheck, `install.ps1` 파싱 검사, `goreleaser check`.
- `release.yml`(태그 `v*`): job 두 개. `goreleaser`(테스트 → GoReleaser 릴리스 → 바이너리와 `artifacts.json`을 아티팩트로 업로드) → `npm`(플랫폼 패키지 생성 → npm 게시, OIDC trusted publishing, provenance). npm job만 다시 돌려도 안전하다.
- 새 npm 패키지 부트스트랩: trusted publisher는 이미 있는 패키지에만 등록할 수 있다(`npm trust`). 처음 게시하는 패키지는 `NPM_TOKEN` 시크릿(스코프 `@tello-ai` 쓰기, 2FA 우회 granular 토큰)으로 게시하고, 게시 뒤 `npm trust github <패키지> --repo tello-ai/tello-cli --file release.yml --allow-publish`로 등록한 다음 시크릿과 토큰을 지운다. npm은 OIDC를 먼저 시도하므로 등록이 끝나면 토큰은 쓰이지 않는다.

## 15. 테스트

- REST API는 `httptest` 가짜 서버, 게이트웨이는 `httptest` + `gorilla/websocket` 가짜 서버로 대체한다. 명령은 cobra 명령을 프로세스 안에서 실행해 stdout(NDJSON)과 exit code를 검증한다.
- 핸들러는 테스트 바이너리를 자식으로 재실행하는 helper-process 패턴으로 검증한다.
- Ctrl-C는 context 취소로 흉내 낸다. 실제 시그널·런처·설치 스크립트는 빌드 산출물로 스모크 테스트한다.

## 16. 열린 질문

| 항목 | 현황 | 필요한 결정·작업 |
| --- | --- | --- |
| `accountId` 표시 | SDK가 `auth.ok`의 `accountId`를 노출하지 않음 | `tello-go`에 접근자 추가 후 `auth status`에 표시 |
| 게이트웨이 식별 | CLI가 `sdk=go`로 기록됨 | `tello-go`에 식별자 지정 옵션 추가 여부 |
| GitHub 저장소 | `tello-ai/tello-cli` 생성(public) | — |
| npm 게시 권한 | 7개 패키지 모두 trusted publisher(`tello-ai/tello-cli`, `release.yml`) 등록, `NPM_TOKEN` 시크릿 삭제(v0.1.0) | 패키지를 새로 추가할 때만 §14.4 부트스트랩 |
| 설치 스크립트 URL | GitHub raw | 자체 도메인(예: `telloai.io/install.sh`) 연결 |
| 런처 부모 종료 | 런처가 SIGKILL 되면 Go 프로세스는 통화가 끝날 때까지 남는다 | 필요 시 부모 감시 추가 |
