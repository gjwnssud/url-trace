# url-trace

애플리케이션이 **실제로 사용하는 URL을 추출**해, 화이트리스트 정책 검토에 쓸 수 있는
감사 친화적(audit-friendly) 레코드로 집계하는 CLI.

정찰(recon) 도구와 달리 목표는 "존재할 수 있는 모든 URL"이 아니라
"이 앱이 실제 호출하는 URL을 빠짐없이, 그러나 과하지 않게" 뽑는 것이다.
따라서 관측된 실제 트래픽을 1차 소스로 삼고, 각 URL이 **왜** 목록에 있는지
(출처·최초/최종 관측 시각·관측 빈도)를 함께 남긴다.

## 현재 상태 (Phase 4)

- **HAR** 캡처 파일에서 요청 URL 추출
- **헤드리스 브라우저 캡처(chromedp)** — 대상 URL을 실제 구동하며 페이지 로드·XHR/fetch·서드파티(CDN/폰트/애널리틱스) 요청 전량 기록. 같은 호스트 링크 자동 크롤(`--depth`, SPA 해시 라우트 `#/...` 포함), 로그인 세션 주입(`--cookie`/`--header`)으로 인증 페이지도 무인 수집. SPA는 여러 진입 URL(`--url` 반복 / `--url-file`)로 라우트를 열거해 각 화면의 API 호출 캡처. 쿠키 주입 시 중복 로그인으로 세션이 만료되는 앱은 `--headful`로 창에서 직접 로그인 후 캡처
- 두 소스 동시 수집 및 병합 (소스별 관측 이력 보존)
- 정규화(scheme/host 소문자화, 기본 포트·fragment 제거, 휘발성 쿼리 파라미터 제거)
- 중복 제거 및 관측 빈도/시간 범위 집계
- **1st/3rd-party 분류** — `--primary-domain` 또는 `--url`의 등록 도메인(eTLD+1) 기준
- **신뢰도 점수** (low/medium/high) — 관측 빈도 + 다중 소스 독립 관측(corroboration) 기반
- **와일드카드 패턴 제안** — 기계 생성 세그먼트(숫자 ID/UUID/해시/토큰)가 3개 이상 관측될 때만 `.../users/*` 형태로 **제안**(자동 적용 안 함)
- **정책 export** (`export`) — 결과를 버전 있는 정책 파일로 변환. `--accept-pattern`으로 사람이 승인한 패턴만 규칙으로 붕괴
- **정책 diff** (`diff`) — 재수집 결과를 기존 정책과 비교해 미커버 신규 URL과 미사용 규칙 리포트. `--fail-on-new`로 CI 게이트 가능
- JSON / CSV 출력

> 브라우저 캡처에는 로컬에 Chrome 또는 Chromium 설치가 필요하다.

## 설치

```sh
# Homebrew (macOS/Linux)
brew install gjwnssud/tap/url-trace

# Go가 있으면
go install github.com/gjwnssud/url-trace@latest

# 또는 GitHub Releases에서 플랫폼별 바이너리 다운로드
# https://github.com/gjwnssud/url-trace/releases
```

소스 빌드:

```sh
go build -o bin/url-trace .
```

## 사용법

```sh
# HAR 파일에서 URL 추출 → JSON (stdout)
url-trace extract --har examples/sample.har

# 대상 URL을 헤드리스 브라우저로 구동하며 네트워크 요청 캡처
url-trace extract --url https://app.example.com --wait 5s

# 로그인이 필요한 앱: 세션 주입 + 링크 자동 크롤 (사람이 안 눌러도 페이지 발견)
url-trace extract --url https://app.example.com --depth 2 \
  --cookie 'SESSION=abcd1234' --header 'Authorization: Bearer <token>'

# 중복 로그인으로 세션이 만료되는 앱: 창을 띄워 직접 로그인 후 캡처
# (열린 창에서 로그인·화면 준비 후 터미널에서 Enter → 인증된 세션으로 자동 크롤)
url-trace extract --headful --url https://app.example.com --depth 2

# SPA: 라우트를 진입 URL로 열거 (각 화면의 API 호출을 캡처)
url-trace extract \
  --url https://app.example.com/users \
  --url https://app.example.com/orders \
  --cookie 'SESSION=abcd1234'
# 또는 파일로
url-trace extract --url-file routes.txt --cookie 'SESSION=abcd1234'

# 두 소스 병합 → CSV 파일로 저장
url-trace extract --har examples/sample.har --url https://app.example.com --format csv -o urls.csv
```

`--har`와 `--url` 중 **최소 하나**는 지정해야 하며, 둘 다 주면 결과를 병합한다(재현율 최대화).

### 정책 워크플로우 (export / diff)

```sh
# 1. 수집 → 결과 저장
url-trace extract --har capture.har --primary-domain example.com -o result.json

# 2. 제안된 패턴을 사람이 검토·승인해 정책 생성
url-trace export -i result.json \
  --accept-pattern 'https://api.example.com/v1/users/*' \
  --min-confidence medium -o policy.json

# 3. 이후 재수집 결과를 정책과 비교 — 신규 URL만 검토하면 됨
url-trace extract --har new-capture.har --primary-domain example.com \
  | url-trace diff --policy policy.json -i -

# CI에서 정책 이탈 감지 (신규 URL 있으면 exit 1)
url-trace diff --policy policy.json -i rerun.json --fail-on-new
```

- **export**: `--min-confidence low|medium|high`, `--party first-party|third-party|unknown` 필터, `--format json|txt`(txt는 패턴 목록만). 승인한 패턴이 관측 URL과 하나도 안 맞으면 경고(오타 방지)
- **SQL export**: `--sql-config config.json` 지정 시 설정에 정의한 임의 테이블/컬럼 매핑대로 INSERT SQL 출력(미지정 시 기존 동작). 설정이 테이블명·컬럼명·값 템플릿을 전부 정의하므로 어떤 DB 스키마에도 연동 가능 — `examples/sqlexport-config.example.json` 참조. 값 템플릿 placeholder: `{pattern}` `{host}` `{path}` `{id}`(패턴 SHA-256, 결정적) `{party}` `{confidence}` `{count}` `{sources}` `{nowMs}` `{firstSeenMs}` `{lastSeenMs}`. `maxLength` 초과 시 truncation 경고, `type: "number"`는 따옴표 없이 출력
- **diff**: 와일드카드 `*`는 경로 세그먼트 **정확히 1개**만 매칭(승인 범위가 몰래 넓어지지 않음). 미사용 규칙도 함께 보고해 정책 은퇴 후보 식별
- 정책 파일은 `{version, rules[]}` 스키마이며 각 규칙에 관측 근거(출처·빈도·시간)가 보존됨. 다른 시스템 전용 포맷은 이 스키마 위에 어댑터로 추가하면 된다(`internal/sqlexport` 참조)
- 입력 경로에 `-`를 주면 stdin — extract와 파이프 조합 가능

## Chrome 확장 (`extension/`)

CLI의 능동 수집(chromedp)은 인증·SPA·중복 로그인 세션 만료 문제를 `--cookie`/`--headful`
같은 우회책으로 다뤄야 한다. 근본적인 대안은 **사용자 본인 브라우저 세션을 그대로
관찰**하는 것 — Chrome 확장이 `chrome.webRequest`로 사용자가 평소처럼 앱을 쓰는 동안의
요청을 수동(passive) 관찰한다. 쿠키 주입도, 중복 로그인도 필요 없다.

파이프라인 로직(정규화·집계·분류·패턴 제안·정책 빌드/diff/SQL export)은 재구현하지
않는다 — 이 로직이 CLI와 확장에서 갈라지면 보안 민감 규칙(재현율 우선, 과잉일반화 금지)이
표류할 수 있기 때문이다. 대신 **Go 코어를 WASM으로 컴파일**해 확장에서 그대로 호출한다
(`wasm/main_js.go`). 확장이 새로 구현하는 건 캡처 계층(`extension/src/background.ts`)뿐이다.

```sh
cd extension
npm install
npm run build      # go build (GOOS=js GOARCH=wasm) + wasm_exec.js 복사 + esbuild 번들
```

`chrome://extensions` → 개발자 모드 → "압축해제된 확장 프로그램 로드" → `extension/` 선택.

사용법: 팝업에서 대상 도메인 입력 → 녹화 시작(해당 도메인 권한을 그때 요청 —
`<all_urls>`를 기본 부여하지 않음) → 대상 앱을 평소처럼 사용 → 정지 →
Result JSON/HAR/CSV 내보내기. Result JSON은 CLI의 `extract` 출력과 완전히 동일한
스키마이므로 그대로 파이프 가능. 녹화 중 팝업·정책 검토 페이지 자신을 여닫아도 그
페이지의 리소스 요청(`chrome-extension://...`)은 자동으로 캡처에서 제외된다 — 대상
앱의 트래픽만 남는다:

```sh
url-trace export -i url-trace-result.json --min-confidence medium -o policy.json
url-trace diff --policy policy.json -i url-trace-result.json
```

CLI 없이 확장만으로도 같은 워크플로우를 끝낼 수 있다 — 팝업의 "정책 검토" 링크가
`review.html`(별도 탭)을 연다:

1. **데이터 불러오기**: 현재 캡처를 바로 불러오거나, 이전에 내보낸 Result JSON을 업로드
2. **패턴 승인 → 정책 생성**: 제안된 와일드카드 패턴을 체크박스로 승인(승인한 것만 규칙으로
   붕괴, 나머지는 exact 규칙), 최소 신뢰도·party로 필터 → `policy.json` 다운로드
3. **SQL export (선택)**: 테이블/컬럼 매핑 설정 JSON을 업로드하면 INSERT SQL 다운로드.
   실제 매핑 파일은 로컬에만 두고 커밋하지 않는다(CLI의 `--sql-config`와 동일한 원칙)
4. **정책 diff**: 기존 `policy.json`을 업로드해 1번에서 불러온 결과와 비교 — 신규 URL과
   미사용 규칙을 표로 확인

이 4단계는 CLI의 `export`/`diff` 커맨드와 완전히 동일한 Go 로직(WASM 경유)을 쓴다 — 결과가
갈릴 수 없다.

### 배포 준비 (Chrome 웹스토어)

```sh
cd extension
npm run package       # 런타임에 필요한 파일만 package/url-trace-extension-vX.Y.Z.zip으로 압축
npm run screenshots   # 리스팅용 스크린샷 자동 생성 (store-assets/screenshots/) — Chromium 필요, 아래 참고
```

- `PRIVACY.md`: 개인정보 처리방침 초안 (모든 처리 로컬, 전송 없음)
- `STORE_LISTING.md`: 제목/설명/카테고리/권한별 justification 초안
- `npm run screenshots`는 실제 빌드된 확장을 브라우저에 로드해 팝업·정책 검토 화면을
  캡처한다. **공식 Google Chrome(브랜드 빌드)는 `--load-extension`을 무시**하므로
  Chromium이 필요하다: `brew install --cask chromium` 후
  `CHROME_PATH="/Applications/Chromium.app/Contents/MacOS/Chromium" npm run screenshots`

### 플래그

| 플래그 | 단축 | 설명 | 기본값 |
|--------|------|------|--------|
| `--har` | | HAR 캡처 파일 경로 | |
| `--url` | | 헤드리스 브라우저로 구동·캡처할 진입 URL (반복 지정, SPA 라우트 열거용) | |
| `--url-file` | | 진입 URL 목록 파일 (한 줄에 하나, `#` 주석 허용) | |
| `--wait` | | 페이지 로드 후 늦은 요청까지 캡처할 대기 시간 (`--url`) | `3s` |
| `--timeout` | | 브라우저 캡처 전체 상한 (`--url`) | `30s` |
| `--insecure` | `-k` | 잘못된 TLS 인증서 허용 — 자체 서명·내부 CA 환경 (`--url`) | `false` |
| `--headful` | | 창을 띄워 수동 로그인 후 캡처 (중복 로그인 세션 만료 회피, 디스플레이 필요) | `false` |
| `--depth` | | `--url`에서 따라갈 링크 hop 수 (0 = 진입 페이지만, 같은 호스트만 크롤) | `0` |
| `--max-pages` | | 크롤 시 방문할 최대 페이지 수 (초과 시 경고) | `50` |
| `--cookie` | | 세션 쿠키 `"name=value"` (반복 지정 가능) | |
| `--header` | | HTTP 헤더 `"Key: Value"` (반복 지정 가능) | |
| `--primary-domain` | | 1st-party 도메인 (반복 지정 가능, 서브도메인 포함 매칭) | `--url`의 eTLD+1 |
| `--output` | `-o` | 출력 파일 (미지정 시 stdout) | stdout |
| `--format` | `-f` | 출력 포맷: `json` 또는 `csv` | `json` |

### 출력 예시 (JSON)

```json
{
  "urls": [
    {
      "url": "https://api.example.com/v1/login",
      "sources": ["browser", "har"],
      "firstSeen": "2026-07-01T09:00:04Z",
      "lastSeen": "2026-07-01T09:00:08Z",
      "count": 5,
      "party": "first-party",
      "confidence": "high"
    }
  ],
  "patternSuggestions": [
    {
      "pattern": "https://api.example.com/v1/users/*",
      "distinctValues": 3,
      "totalCount": 4,
      "examples": ["https://api.example.com/v1/users/101", "..."]
    }
  ]
}
```

- `count`·`firstSeen`/`lastSeen`: 상시 사용 엔드포인트 vs 1회성 관측 구분
- `sources`: 트래픽 캡처와 브라우저 양쪽에서 독립 관측되면 신뢰도 상승(corroboration)
- `confidence`: low(1회) / medium(2회+) / high(5회+ 또는 다중 소스 보정)
- `patternSuggestions`: 와일드카드 규칙 **제안** — 과잉 일반화는 보안 구멍이므로 사람 승인 전 자동 적용하지 않으며, 관측 URL 목록도 그대로 유지된다. CSV 출력에는 포함되지 않는다(JSON 전용).

## 구조

```
url-trace/
├── main.go              # 진입점, 시그널 기반 컨텍스트 취소
├── cmd/                 # cobra CLI (root, extract, export, diff)
├── internal/
│   ├── model/           # URLRecord, PatternSuggestion, Result (감사 메타 포함 공통 타입)
│   ├── source/          # Source 인터페이스 + HAR / 브라우저(chromedp) 소스
│   ├── pipeline/        # 정규화 + 중복 제거/집계
│   ├── classify/        # 1st/3rd-party 분류 + 신뢰도 점수
│   ├── patterns/        # 보수적 와일드카드 패턴 제안
│   ├── policy/          # 정책 빌드(승인 워크플로우)·매칭·diff
│   ├── sqlexport/       # 설정 기반 범용 테이블/컬럼 매핑 (INSERT SQL)
│   └── output/          # JSON / CSV 직렬화
├── wasm/                # internal/* 파이프라인을 syscall/js로 노출하는 WASM 브리지
└── extension/           # Chrome 확장 (MV3, TypeScript) — wasm/의 WASM 산출물을 호출
```

`Source`는 인터페이스로 추상화되어 있어, HAR·브라우저 캡처가 동일한 파이프라인으로
합류한다. 확장의 캡처(`chrome.webRequest`)도 같은 원칙 — 캡처 계층만 다르고
정규화/집계/분류/패턴 제안/정책 로직은 `internal/*`(WASM 경유) 한 곳에만 존재한다.

## 로드맵

- **Phase 2** (완료): chromedp 헤드리스 캡처 — 대상 URL 실제 구동하며 네트워크 요청 전량 기록
- **Phase 3** (완료): 경로 패턴 제안(보수적), 1st/3rd-party 분류, 신뢰도 점수
- **Phase 4** (완료): 정책 export(승인 워크플로우)·기존 정책 대비 diff·CI 게이트
- **Phase 5** (완료): 설정 기반 범용 SQL export (`--sql-config`)
- **Phase 6** (완료): MIT 라이선스, GoReleaser 릴리즈 + GitHub Actions CI
- **Phase 7** (진행 중): Chrome 확장 — Go 코어 WASM 재사용, `chrome.webRequest` 수동 캡처로
  인증/SPA/중복 로그인 문제 원천 해결. 캡처+extract 대응(MVP), 정책 export/diff 검토
  페이지(`review.html`) 완료. 웹스토어 제출 준비 완료 — 개인정보 처리방침·리스팅 문안·
  권한 점검·zip 패키징 스크립트·리스팅용 스크린샷(`store-assets/screenshots/`) 전부 준비됨.
  남은 건 Chrome 웹스토어에 실제 제출(개발자 계정 필요, 사용자 직접 진행)뿐

## 라이선스

[MIT](LICENSE)

## 테스트

```sh
go test ./...

# WASM 브리지 컴파일 확인 (다른 GOOS/GOARCH라 위 테스트에 포함 안 됨)
GOOS=js GOARCH=wasm go build -o /dev/null ./wasm

# 확장
cd extension && npm run typecheck && npm run build
```
