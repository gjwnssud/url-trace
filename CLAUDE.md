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
| `extension/` | Chrome 확장(MV3, TypeScript). `chrome.webRequest`로 캡처(`background.ts`)만 새로 구현, 팝업(`popup.ts`)과 정책 검토 페이지(`review.ts`/`review.html` — 패턴 승인→정책 생성, SQL export, diff)는 전부 `wasm/`를 통해 `internal/*` 재사용. `scripts/verify-wasm.mjs`(브리지 4개 함수 실동작 검증, CI 포함), `scripts/package.sh`(제출용 zip), `scripts/screenshots/`(Go+chromedp로 실제 스크린샷 생성). `PRIVACY.md`/`STORE_LISTING.md`는 웹스토어 제출 문서 |

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
- 공식 Google Chrome(브랜드 빌드)은 `--load-extension`/`--disable-extensions-except`를
  무시한다("is not allowed in Google Chrome, ignoring") — `scripts/screenshots`처럼
  자동화로 확장을 로드해야 하면 Chromium(오픈소스 빌드)에 `CHROME_PATH`로 지정할 것
