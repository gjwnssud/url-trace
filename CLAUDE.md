# url-trace

애플리케이션이 실제 사용하는 URL을 추출해 **URL 화이트리스트 정책**용 감사 레코드로
집계하는 Go CLI. 화이트리스트 정책 시스템의 입력을 만드는 것이 최종 목적이다.

## 핵심 원칙 (설계 판단 기준)

1. **재현율 우선**: 정상 URL 누락은 사용자 앱 차단(장애)이다. 수집 단계에서는 버리지
   말고, 걸러내는 판단은 사람(정책 검토)에게 넘긴다. 타임아웃 등 부분 실패 시에도
   그때까지 수집분은 보존한다.
2. **과잉 일반화 금지**: 와일드카드 패턴은 **제안만** 하고 절대 자동 적용하지 않는다.
   일반화는 기계 생성 세그먼트(숫자 ID/UUID/해시/토큰) + distinct 값 3개 이상일 때만.
   와일드카드 `*`는 경로 세그먼트 정확히 1개만 매칭한다.
3. **감사 추적 보존**: 모든 레코드/규칙은 출처(sources)·관측 빈도(count)·시간
   범위(firstSeen/lastSeen)를 유지한다. 병합 시에도 근거를 접어 넣지 버리지 않는다.
4. **무음 탈락 금지**: 무언가를 건너뛰면(파싱 실패, 컬럼 truncation 등) 반드시
   stderr로 보고한다.

## 명령 구조

- `extract`: HAR(`--har`) + 헤드리스 브라우저(`--url`, chromedp) 수집 → 정규화 →
  집계 → 분류/점수 → `{urls, patternSuggestions}` JSON (또는 CSV)
- `export`: 결과 JSON → 정책 파일. `--accept-pattern`으로 승인한 패턴만 규칙으로
  붕괴. `--sql-config` 지정 시 설정에 정의된 테이블/컬럼 매핑대로 INSERT SQL 출력
- `diff`: 재수집 결과를 정책과 비교 → 신규 URL·미사용 규칙. `--fail-on-new`는 CI 게이트
- 입력 경로 `-`는 stdin (extract | export/diff 파이프)

## 패키지 배치

| 패키지 | 역할 |
|--------|------|
| `cmd/` | cobra CLI만. 로직 없음 |
| `internal/model` | 공통 타입 (URLRecord, Result, Policy 관련 상수) |
| `internal/source` | `Source` 인터페이스 + har/browser 구현. 새 수집기는 이것만 구현 |
| `internal/pipeline` | 정규화(휘발성 쿼리 제거 등)·중복 제거/집계 |
| `internal/classify` | 1st/3rd-party 분류(eTLD+1), 신뢰도 점수 |
| `internal/patterns` | 보수적 와일드카드 제안 |
| `internal/policy` | 정책 빌드·매칭·diff (정책 의미론의 단일 소스) |
| `internal/sqlexport` | 설정 기반 범용 SQL 매핑. 정책 의미론 금지 — 컬럼 렌더링만. **대상 스키마(테이블·컬럼명)를 코드에 넣지 말 것** — 전부 설정 파일 소관 |
| `internal/output` | JSON/CSV 직렬화 |
| `wasm/` | `internal/{pipeline,classify,patterns,policy,sqlexport}`를 syscall/js로 노출하는 브리지. `GOOS=js GOARCH=wasm` 전용(`main_js.go`) + 호스트 빌드용 빈 stub(`main_other.go`). **로직 추가 금지** — 기존 internal 함수 호출만 |
| `extension/` | Chrome 확장(MV3, TypeScript). `chrome.webRequest`로 캡처(`background.ts`)만 새로 구현, 팝업(`popup.ts`)과 정책 검토 페이지(`review.ts`/`review.html` — 패턴 승인→정책 생성, SQL export, diff)는 전부 `wasm/`를 통해 `internal/*` 재사용. 선택적 자동 크롤(`tabs`+`scripting`, 같은 인증 세션의 백그라운드 탭에서 링크 BFS — CLI `browser.go`의 crawl()과 같은 개념이지만 재구현이 아니라 캡처 계층의 새 코드)도 `background.ts`에 포함. `scripts/verify-wasm.mjs`(브리지 4개 함수 실동작 검증, CI 포함), `scripts/package.sh`(제출용 zip), `scripts/screenshots/`(Go+chromedp로 실제 스크린샷 생성). `PRIVACY.md`/`STORE_LISTING.md`는 웹스토어 제출 문서 |

새 export 포맷은 `internal/policy` 위에 얇은 어댑터 패키지로 추가한다(sqlexport 참조).
CLI와 확장은 서로 다른 캡처 계층일 뿐, 파이프라인 의미론(정규화·분류·패턴 제안·정책)의
단일 소스는 항상 `internal/*`다 — 확장에 로직을 재구현하지 말 것.

## 빌드·테스트·검증

```sh
go build -o bin/url-trace .
go test ./...           # source 패키지는 로컬 Chrome 있으면 실제 구동, 없으면 skip
go vet ./...

# wasm/는 GOOS=js GOARCH=wasm 전용이라 위 명령에 포함되지 않음 — 별도 확인 필수
GOOS=js GOARCH=wasm go build -o /dev/null ./wasm
```

수정 후에는 반드시 vet + build + 관련 테스트 실행. 브라우저 캡처를 건드리면
`internal/source`의 통합 테스트(로컬 httptest 서버 + 실제 Chrome)까지 돌릴 것.
`internal/{pipeline,classify,patterns,policy,sqlexport}`를 건드리면 wasm 빌드도 확인할 것
(`wasm/`가 그 패키지들을 그대로 재노출하므로 시그니처 변경이 여기서도 깨진다).
확장(`extension/`)을 건드리면 `npm run typecheck && npm run build`(내부에서 wasm도 다시 빌드).
`background.ts`의 캡처/크롤 로직(비동기 탭·이벤트 타이밍)은 CI에 자동화 테스트가 없다
(공식 Chrome은 자동화 로드를 막고, Chromium 의존은 CI에 안 둠) — 건드리면
`CHROME_PATH=<Chromium 경로> go run ./scripts/screenshots`로 최소 한 번은 실제
브라우저에서 캡처가 되는지 확인할 것. 타이밍 관련 코드는 몇 번 반복 실행해 간헐적
실패가 없는지도 볼 것(위 tabs.onUpdated race처럼 1~2회는 통과해도 재현될 수 있다).

## 주의점

- `Source.Fetch`는 out 채널을 닫지 않는다 — 채널 수명은 호출자(collect) 소유
- 시간 zero value는 "관측 시각 불명"의 의미 — 병합 시 실제 시각이 항상 이긴다
- 정책 스키마 버전은 `policy.CurrentVersion` — 스키마 변경 시 반드시 올리고 Load 검증 유지
- sqlexport의 `{id}`는 패턴 SHA-256 hex(결정적, 재수출 시 동일 ID) — `maxLength`로
  잘라 쓰며 `{id}` 단독 컬럼의 truncation은 경고 없음(해시 prefix도 유효한 키)
- 실제 매핑 설정(`sqlexport-config*.json`)은 gitignore 대상 — 예제만 커밋
- `extension/src/background.ts`의 `webRequest` 리스너는 반드시 `isOwnResourceURL()`
  (`records.ts`)로 `chrome-extension://`/`chrome://` 요청을 걸러야 한다 — Chrome은
  host_permissions 범위와 무관하게 확장 자신의 리소스 요청은 항상 관찰시켜주므로, 이
  필터가 없으면 팝업/리뷰 페이지를 열기만 해도 그 페이지 자산 로드가 캡처에 섞인다
  (실제로 한 번 발견된 버그)
- `chrome.webRequest.onBeforeRequest.addListener()`는 확장이 **현재 승인받은
  host permission이 0개**면 예외를 던진다("You need to request host permissions
  in the manifest file..."). `optional_host_permissions`만 선언(런타임에 도메인별
  요청, 최소권한 원칙)하는 이 확장은 최초 설치 시 반드시 0개 상태이므로, 최상위에서
  무조건 `addListener` 호출하면 안 된다 — `chrome.permissions.getAll()`(SW 재시작 시
  기존 권한 복원)과 `chrome.permissions.onAdded`(런타임에 새로 승인될 때) 양쪽에서
  `hasListener()`로 멱등하게 등록할 것(실제로 한 번 발견된 버그 — 실제 제출 후 로컬
  테스트에서 발견됨)
- 공식 Google Chrome(브랜드 빌드)은 `--load-extension`/`--disable-extensions-except`를
  무시한다("is not allowed in Google Chrome, ignoring") — `scripts/screenshots`처럼
  자동화로 확장을 로드해야 하면 Chromium(오픈소스 빌드)에 `CHROME_PATH`로 지정할 것
- `background.ts`의 크롤러에서 `chrome.tabs.update()` 호출 **후** `chrome.tabs.get()`으로
  로드 완료를 확인하면 race condition이 생긴다 — update()가 반환된 시점에도 get()이
  아직 이전 페이지의 "complete" 상태를 돌려줄 수 있어, 새 페이지 로드를 기다리지 않고
  바로 링크를 추출해버린다(간헐적으로만 재현되는 실제 버그였음). `chrome.tabs.onUpdated`
  리스너는 반드시 네비게이션을 트리거하기 **전에** 붙일 것(`waitForTabComplete()` 참고)
- `records.ts`의 `normalizeLink()`는 `internal/source/browser.go`의 동명 함수와 정확히
  같은 휴리스틱이어야 한다: SPA 라우트 프래그먼트(`#/path`, `#!/path`)는 보존하고, 순수
  인앵커(`#section`)만 스트립. 한때 확장 쪽에서 프래그먼트를 무조건 스트립했다가(사이드
  내비게이션이 해시 라우팅인 앱에서 모든 nav 링크가 seed URL과 같은 문자열로 뭉개져
  `visited`에 이미 있다고 보고 전부 스킵 → 크롤이 시작 페이지 밖으로 못 나감, 재현율
  우선 원칙 위반), 그다음엔 무조건 보존으로 고쳤다가(CLI보다 거칠어짐 — 인앵커까지
  별개 페이지로 취급) 결국 CLI와 동일한 휴리스틱으로 맞춤. 둘 중 하나만 고치면 크롤
  결과가 CLI/확장 사이에서 갈린다
- 링크 추출은 CLI(`browser.go`의 `linkExtractJS`)와 확장(`background.ts`의
  `extractLinks()`) 양쪽 다 iframe(같은 출처만 — cross-origin은 원천적으로 접근 불가)과
  open shadow root(웹 컴포넌트 메뉴)를 재귀 순회하고, 페이지 "complete" 이후 비동기로
  늦게 뜨는 사이드 메뉴에 대응해 링크 수가 연속 두 번 같아질 때까지 폴링한다
  (`waitForStableLinks()` — 최대 8회, 500ms 간격, CLI는 `chromedp.Run` 반복, 확장은
  `chrome.scripting.executeScript` 반복). closed shadow root는 둘 다 원천적으로 못 본다
  (플랫폼 제약, 우회 불가)
- **CLI의 크롤러(`browser.go`)와 확장의 크롤러(`background.ts`)는 서로 별개 구현**이라
  한쪽에 크롤 관련 개선/버그 수정을 넣으면 다른 쪽엔 자동으로 반영되지 않는다(공유하는
  건 `internal/{pipeline,classify,patterns,policy,sqlexport}`뿐 — 캡처 계층은 각자
  소유). 크롤 로직을 고칠 때는 반드시 다른 쪽도 대조해서 같이 맞출 것
- 팝업(`popup.ts`)의 입력 상태(대상 도메인·자동 크롤 체크·depth·최대 페이지)는 전부
  `chrome.storage.local`에 한 번에(`captureSettings`) 저장해야 한다 — MV3 팝업은 닫히면
  DOM이 통째로 사라지므로, 저장하지 않은 컨트롤은 재오픈 시 항상 HTML의 초기값으로
  돌아간다(과거엔 도메인 텍스트만 저장해 체크박스/숫자 입력이 매번 초기화되던 버그).
  진행 중인 자동 크롤 여부처럼 background가 진실의 원천인 상태는 `chrome.storage`가
  아니라 매초 `getStatus` 응답으로 반영할 것 — 저장된 폼 값과 실제 실행 상태가 다를 수
  있다(예: 크롤 중 팝업을 열면 체크박스는 status.crawling을 따라야 한다)
