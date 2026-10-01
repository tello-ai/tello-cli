# tello-cli 설계 v0

> 상태: 구현 기준 문서. 명령·출력·exit code·배포 계약은 이 문서를 따른다.
> 기반: [`tello-go`](https://github.com/tello-ai/tello-go) `v0.2.4` (WS 프로토콜 `1.0`, `docs/protocol/sdk-ws.v1.md`)
> 참고: [vox CLI](https://docs.tryvox.co/docs/ai/cli), [Claude Code·Codex 연결](https://docs.tryvox.co/docs/ai/clients)

## 1. 목표와 비목표

목표

- 터미널과 코딩 에이전트(Claude Code, Codex)에서 Tello 통화를 걸고, 실시간 턴에 답하고, 결과를 조회한다.
- 모든 명령이 `--json`을 지원하고, 결과를 exit code로 구분한다. 사람 도움 없이 AI가 파싱할 수 있어야 한다.
- 단일 바이너리. 사용자는 `npm install -g @tello-ai/cli` 또는 curl 설치 스크립트로 받는다. Go 툴체인이 필요 없다.

비목표 (서버 API가 없어서 지금은 불가)

- 에이전트·프롬프트·도구·지식 베이스 관리, 버전·배포(`agent push/promote` 류)
- 통화 목록 조회, 서버 캠페인(pause/resume), 번호·위젯·워크스페이스 관리
- 전화 없는 텍스트 테스트(`vox chat` 류)

SDK 계약상 전송은 WebSocket `/sdk` 하나이고 명령은 `createCall` / `answer` / `sendDtmf` / `cancel` / `getSummary` 다섯 개뿐이다. 에이전트는 서버(포털)가 정한다.

## 2. 결정

| 항목 | 결정 | 이유 |
| --- | --- | --- |
| 구현 언어 | Go + `tello-go` SDK | CGO 없이 6개 플랫폼 크로스 빌드. 같은 바이너리를 npm·curl 양쪽에 쓴다 |
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
internal/cli/              루트 명령 조립(--json, --url)
internal/cmd/call/         call create / summary / batch
internal/callrun/          통화 1건 실행기(연결, 이벤트 큐, 응답 소스, 취소·타임아웃)
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

전역 플래그: `--json`(기계용 출력), `--url <ws(s)://…>`(게이트웨이. 우선순위 `--url` > `TELLO_URL` > `wss://api.telloai.io/sdk`).

| 명령 | 설명 |
| --- | --- |
| `tello auth login [--no-verify]` | API 키 입력(TTY면 숨김 프롬프트, 아니면 stdin 첫 줄) → 인증 확인 → 저장 |
| `tello auth status [--offline]` | 키 출처 표시, 게이트웨이 인증 확인 |
| `tello auth logout` | 저장된 키 삭제 |
| `tello call create --to <번호> [--prompt …] [--metadata <JSON\|@파일>] [--timeout <dur>] (-i \| -- <핸들러 명령…>)` | 통화 1건. 끝날 때까지 블로킹 |
| `tello call summary <callId> [--timeout 15s]` | 완료된 통화 요약·전사 |
| `tello call batch --tasks <파일\|-> [--max-retries 3] [--retry-delay 5s] [--timeout <dur>] (-i \| -- <핸들러 명령…>)` | 여러 건을 차례로 발신 |
| `tello init [--target claude,codex] [--dir .]` | 코딩 에이전트 스킬 파일 생성·갱신 |
| `tello guide coding-agent [--brief]` | 코딩 에이전트용 운영 지침 출력(`tello guide`만 치면 주제 목록) |
| `tello doctor` | 버전·키·URL·인증·스킬 파일 점검 |
| `tello version` | CLI·SDK·프로토콜 버전 |

## 5. 출력 계약

- 사람용 모드: 결과는 stdout, 진단·경고·오류는 stderr.
- `--json` 모드: stdout에는 한 줄에 JSON 하나(NDJSON)만 쓴다. 경고는 stderr.
- **`--json` 명령은 정확히 한 줄의 `cli.result`로 끝난다.** 스트리밍 명령(`call create`, `call batch`)은 그 앞에 이벤트 줄을 쓴다.
- 그룹 명령(`tello`, `tello auth`, `tello call`)을 하위 명령 없이 실행하면 사람용 모드에서는 도움말(exit 0), `--json`에서는 usage 오류(exit 2). 모르는 하위 명령은 usage 오류이며 비슷한 이름을 제안한다. cobra 기본 `completion` 명령은 제공하지 않는다.
- 예외(`cli.result` 없이 끝남): `--help`/`help`, `--version`(사람용 텍스트), 두 번째 Ctrl-C(즉시 종료), `auth login` 숨김 프롬프트에서의 Ctrl-C.

```json
{"type":"cli.result","ok":true,"data":{}}
{"type":"cli.result","ok":false,"error":{"kind":"refused","code":"insufficientCredit","message":"…","retryable":false,"exitCode":4},"data":{}}
```

- `error.question`은 값이 있을 때만(`callRejected`). `data`는 실패 시에도 있을 수 있다(예: 이미 생긴 `callId`).
- 이벤트 줄은 게이트웨이 수신 프레임을 **그대로** 쓴다(`call.created`, `user.turn`, `agent.turn`, `answer.accepted`, `dtmf.accepted`, `call.statusChanged`, 종단 이벤트, `call.summary`, `error`). `auth.ok`와 SDK 합성 이벤트 `disconnected`는 쓰지 않는다.
- CLI가 만드는 줄은 `cli.` 접두사: `cli.result`, `cli.task`(batch 건별 결과), `cli.retry`(batch 재시도).
- JSON은 HTML 이스케이프 없이 UTF-8 그대로 쓴다.

## 6. Exit code

| code | kind | 의미 | 대표 code |
| --- | --- | --- | --- |
| 0 | — | 성공 | |
| 1 | `internal` | 예상하지 못한 오류 | |
| 2 | `usage` | 플래그·인자 오류, ws/wss가 아닌 게이트웨이 URL | `usage`, `invalidUrl` |
| 3 | `auth` | 키 없음·인증 실패 | `apiKeyMissing`, `unauthenticated` |
| 4 | `refused` | 계정 정책 거부(재시도 무의미) | `insufficientCredit`, `callerNotVerified`, `noRepresentativeNumber` |
| 5 | `invalid` | 게이트웨이 검증 오류 | `toRequired`, `callIdRequired`, `callNotFound`, `callNotCompleted`, `dtmfDigitsRequired`, `dtmfDigitsInvalid`, `callRejected`, `noActiveCall` |
| 6 | `callEnded` | 통화가 완료되지 못함 | `noAnswer`, `callFailed`, `cancelled`, `timeout`, `batchIncomplete` |
| 7 | `connection` | 연결 실패·끊김(인증 응답 전에 끊긴 경우 포함), 요약 대기 만료 | `connectionFailed`, `connectionClosed`, `sessionReplaced`, `summaryTimeout` |
| 8 | `server` | 서비스 측 영구 오류 | `internalError`, `callProviderUnauthorized`, `callSetupFailed` |
| 9 | `handler` | 핸들러 프로세스 실패 | `handlerStartFailed`, `handlerExited` |
| 75 | `temporary` | 나중에 재시도 | `concurrentLimitExceeded`, `callProviderDraining`, `callProviderUnavailable`, `callAlreadyActive` |
| 130 | `interrupted` | Ctrl-C / SIGTERM | `interrupted` |

`retryable`은 `kind == temporary`일 때만 true. 분기는 `code`로 한다(`message`는 표시용).

## 7. 인증

- 키 해석 순서: `TELLO_API_KEY` 환경변수 > OS 키체인(service `tello-cli`, account `api-key`) > `<사용자 설정 디렉터리>/tello/credentials.json`(0600).
- `auth login`은 키체인에 저장하고, 키체인을 쓸 수 없으면(헤드리스 Linux·WSL 등) 파일에 저장하고 경고한다.
- 키는 플래그로 받지 않는다(셸 히스토리·프로세스 목록 노출). 출력·로그·오류 어디에도 쓰지 않는다.
- CI: `TELLO_API_KEY`(필수), `TELLO_URL`(선택).

## 8. `call create`

### 8.1 응답 소스 (둘 중 하나 필수)

SDK가 대화의 두뇌라서 답할 주체 없이 통화를 걸 수 없다(프로토콜 §8: 답할 사람 없는 레그를 막으려고 게이트웨이가 세션을 취소한다).

- `-i, --interactive`: 터미널에서 사람이 답한다. 입력 한 줄 = `answer`. `/dtmf <digits>` → `sendDtmf`, `/cancel` → `cancel`, `/help`. stdin EOF는 입력 종료일 뿐 통화를 끝내지 않는다.
- 핸들러: `--` 뒤의 명령을 자식 프로세스로 띄운다(셸을 거치지 않음). 예: `tello call create --to +8210… -- python3 bot.py`

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

### 8.3 결과

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

### 8.4 취소와 시그널

- 첫 Ctrl-C/SIGTERM: `cancel` 전송 → 종단 이벤트를 최대 10초 기다림 → exit 130.
- `call.created` 전에 보낸 `cancel`(Ctrl-C, `--timeout`, 핸들러 종료·`cancel`, `/cancel`)은 게이트웨이가 무시할 수 있으므로(프로토콜 §4.4) `call.created`가 오면 한 번 더 보내고 10초 대기를 다시 시작한다.
- 두 번째: 즉시 종료(exit 130, 숨김 프롬프트였다면 터미널 에코 복구). 연결이 끊기면 게이트웨이가 통화를 취소한다(프로토콜 §8).
- 구현: `main`이 첫 시그널에 루트 context를 취소하고, 두 번째에 `os.Exit(130)`. 실행기는 context 취소를 Ctrl-C로 해석하고, 이후 SDK 호출에는 별도 context를 쓴다.

### 8.5 SDK 사용상 주의

- SDK 핸들러는 수신 고루틴에서 동기로 돈다. 핸들러에서 블로킹하면 ping 처리가 멈추므로, 이벤트는 무제한 큐에 넣고 메인 루프에서 처리한다.
- `WaitClosed`는 종료 판정의 기준이다(오류 프레임이 통화를 끝내는지는 SDK가 requestId로 판단). 단, 통화 종료 채널이 닫힌 **뒤에** 마지막 이벤트가 핸들러로 emit 되므로, `WaitClosed`가 반환된 뒤 해당 이벤트(종단/`error`/`disconnected`)가 큐에 들어올 때까지(최대 2초) 기다렸다가 출력하고 끝낸다.

## 9. `call summary`

연결 → `getSummary(callId, requestId)` → 같은 `requestId`의 `call.summary` 또는 `error`를 기다린다(`--timeout`, 기본 15초. 만료 시 exit 7 `summaryTimeout`). `data`: `callId`, `status`, `durationSeconds`, `transcript`, `summary`, `creditCharged`(게이트웨이가 null을 보내면 null). completed 상태가 아니면 `callNotCompleted`(exit 5).

## 10. `call batch`

- `--tasks`: JSON 배열 `[{"to":"…","prompt":"…","metadata":{…}}]`. `-`면 stdin.
- 건마다 새 연결로 `call create`와 같은 실행기를 돈다. 핸들러도 건마다 새로 띄운다.
- 재시도: `kind == temporary`일 때만, 최대 `--max-retries`(기본 3)회, 대기 `--retry-delay`(기본 5초)를 매번 두 배로. 재시도마다 `cli.retry` 줄.
- 중단(남은 건 skip): `auth`, `interrupted`, 그리고 계정 전체에 해당하는 `insufficientCredit`, `noRepresentativeNumber`, `callProviderUnauthorized`.
- 그 밖의 실패는 기록하고 다음 건으로 넘어간다.
- 건마다 `{"type":"cli.task","index":0,"to":"…","ok":true,"callId":"…","status":"completed","attempts":1}`(실패 시 `error`).
- 결과 `data`: `total`, `completed`, `failed`, `skipped`. 모두 완료면 exit 0, 중단이면 중단 원인의 exit code, 일부 실패면 exit 6(`batchIncomplete`).
- 주의: 수신 번호가 계정의 인증 번호가 아니면 `callerNotVerified`로 거부된다(프로토콜 §6.2).

## 11. `init` / `guide` / `doctor` / `version`

- `init`: `.claude/skills/tello/SKILL.md`, `.codex/skills/tello/SKILL.md`를 만든다. CLI가 관리하는 산출물이라 내용이 다르면 덮어쓴다(머리말에 명시). 파일별 `created` / `updated` / `unchanged`를 보고한다. 스킬 본문은 짧게 두고 `tello guide coding-agent`를 가리킨다.
- `guide coding-agent`: 코딩 에이전트용 운영 계약(안전 규칙, 표준 흐름, 핸들러 프로토콜, 출력·exit code 요약). `--brief`는 요약판.
- `doctor`: `version`(정보), `apiKey`, `gatewayUrl`(ws/wss 검사), `auth`(연결·인증, 지연 ms), `skills`(현재 디렉터리 스킬 파일 최신 여부). 각 `ok`/`warn`/`fail`/`skip`. 실패가 있으면 첫 실패의 kind로 exit(URL 스킴 오류는 usage `invalidUrl`, exit 2).
- `version`: `version`, `commit`, `date`, `sdkVersion`(`tello.Version`), `protocolVersion`, `os`, `arch`.

## 12. 배포

### 12.1 빌드 산출물 (GoReleaser)

- 대상: `darwin/{amd64,arm64}`, `linux/{amd64,arm64}`, `windows/{amd64,arm64}`, `CGO_ENABLED=0`, `-trimpath`, `-s -w`, 버전은 ldflags로 `internal/version`에 주입.
- 아카이브: `tello_<version>_<os>_<arch>.tar.gz`(Windows는 `.zip`), 루트에 `tello`(`tello.exe`), `LICENSE`, `README.md`. 체크섬: `checksums.txt`(SHA-256).
- 버전의 단일 출처는 git 태그 `vX.Y.Z`다. npm 패키지 버전도 태그에서 만든다.

### 12.2 npm

- 플랫폼 패키지 `@tello-ai/cli-{darwin-arm64,darwin-x64,linux-arm64,linux-x64,win32-arm64,win32-x64}`: `os`/`cpu` 필드, `bin/tello`(`bin/tello.exe`)와 `LICENSE`만 담는다. `bin` 필드·스크립트 없음.
- 메인 패키지 `@tello-ai/cli`: 런처 `bin/tello.js` + 같은 버전으로 고정한 `optionalDependencies`. postinstall 없음 → `--ignore-scripts`에서도 동작.
- 게시 순서: 플랫폼 패키지 6개 → 메인. 이미 게시된 버전은 건너뛴다(재실행 안전). provenance 사용. 프리릴리스 버전(`1.2.0-rc.1`)은 dist-tag `next`로 게시한다.
- 런처 규칙:
  - 바이너리 위치: `TELLO_BINARY_PATH`가 있으면 그것, 아니면 `require.resolve("<플랫폼 패키지>/bin/tello[.exe]")`. 없으면 재설치·curl 설치를 안내하고 exit 1.
  - `stdio: "inherit"`로 실행(파이프 중계 금지).
  - SIGINT는 전달하지 않고 삼킨다: 터미널 Ctrl-C는 같은 프로세스 그룹의 Go 프로세스에 직접 가므로, 전달하면 두 번 받아 "두 번째 Ctrl-C = 강제 종료"가 된다.
  - SIGTERM·SIGHUP은 자식에게 전달한다.
  - 자식의 exit code를 그대로 돌려준다. 시그널로 죽었으면 같은 시그널로 자신을 종료한다.

### 12.3 curl / PowerShell

- `install.sh`: OS(`linux`/`darwin`)·아키텍처(`amd64`/`arm64`, Rosetta면 arm64) 판별 → 아카이브·`checksums.txt` 다운로드 → SHA-256 검증 → `~/.local/bin/tello` 설치 → PATH 안내. `curl` 또는 `wget`.
- `install.ps1`: Windows PowerShell 5.1 호환. zip·체크섬 검증 → `%LOCALAPPDATA%\Programs\tello` 설치 → 사용자 PATH에 추가.
- 환경변수: `TELLO_VERSION`(기본 latest), `TELLO_INSTALL_DIR`, `TELLO_DOWNLOAD_BASE_URL`(기본 `https://github.com/tello-ai/tello-cli/releases`, 미러·테스트용).

### 12.4 워크플로

- `ci.yml`: gofmt, `go vet`, `go build`, `go test -race`, `node --test 'npm/**/*.test.mjs'`(Node 22+는 디렉터리 인자를 받지 않는다), shellcheck, `install.ps1` 파싱 검사, `goreleaser check`.
- `release.yml`(태그 `v*`): job 두 개. `goreleaser`(테스트 → GoReleaser 릴리스 → 바이너리와 `artifacts.json`을 아티팩트로 업로드) → `npm`(플랫폼 패키지 생성 → npm 게시, OIDC trusted publishing, provenance). npm job만 다시 돌려도 안전하다.
- 새 npm 패키지 부트스트랩: trusted publisher는 이미 있는 패키지에만 등록할 수 있다(`npm trust`). 처음 게시하는 패키지는 `NPM_TOKEN` 시크릿(스코프 `@tello-ai` 쓰기, 2FA 우회 granular 토큰)으로 게시하고, 게시 뒤 `npm trust github <패키지> --repo tello-ai/tello-cli --file release.yml --allow-publish`로 등록한 다음 시크릿과 토큰을 지운다. npm은 OIDC를 먼저 시도하므로 등록이 끝나면 토큰은 쓰이지 않는다.

## 13. 테스트

- 게이트웨이는 `httptest` + `gorilla/websocket` 가짜 서버로 대체한다. 명령은 cobra 명령을 프로세스 안에서 실행해 stdout(NDJSON)과 exit code를 검증한다.
- 핸들러는 테스트 바이너리를 자식으로 재실행하는 helper-process 패턴으로 검증한다.
- Ctrl-C는 context 취소로 흉내 낸다. 실제 시그널·런처·설치 스크립트는 빌드 산출물로 스모크 테스트한다.

## 14. 열린 질문

| 항목 | 현황 | 필요한 결정·작업 |
| --- | --- | --- |
| `accountId` 표시 | SDK가 `auth.ok`의 `accountId`를 노출하지 않음 | `tello-go`에 접근자 추가 후 `auth status`에 표시 |
| 게이트웨이 식별 | CLI가 `sdk=go`로 기록됨 | `tello-go`에 식별자 지정 옵션 추가 여부 |
| GitHub 저장소 | `tello-ai/tello-cli` 생성(public) | — |
| npm 게시 권한 | 7개 패키지 모두 trusted publisher(`tello-ai/tello-cli`, `release.yml`) 등록, `NPM_TOKEN` 시크릿 삭제(v0.1.0) | 패키지를 새로 추가할 때만 §12.4 부트스트랩 |
| 설치 스크립트 URL | GitHub raw | 자체 도메인(예: `telloai.io/install.sh`) 연결 |
| 런처 부모 종료 | 런처가 SIGKILL 되면 Go 프로세스는 통화가 끝날 때까지 남는다 | 필요 시 부모 감시 추가 |
