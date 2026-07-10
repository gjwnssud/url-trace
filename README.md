# url-trace

애플리케이션이 **실제로 사용하는 URL을 추출**해, 화이트리스트 정책 검토에 쓸 수 있는
감사 친화적(audit-friendly) 레코드로 집계하는 CLI.

정찰(recon) 도구와 달리 목표는 "존재할 수 있는 모든 URL"이 아니라
"이 앱이 실제 호출하는 URL을 빠짐없이, 그러나 과하지 않게" 뽑는 것이다.
따라서 관측된 실제 트래픽을 1차 소스로 삼고, 각 URL이 **왜** 목록에 있는지
(출처·최초/최종 관측 시각·관측 빈도)를 함께 남긴다.

## 현재 상태 (Phase 4)

- **HAR** 캡처 파일에서 요청 URL 추출
- **헤드리스 브라우저 캡처(chromedp)** — 대상 URL을 실제 구동하며 페이지 로드·XHR/fetch·서드파티(CDN/폰트/애널리틱스) 요청 전량 기록
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

## 빌드

```sh
go build -o bin/url-trace .
```

## 사용법

```sh
# HAR 파일에서 URL 추출 → JSON (stdout)
url-trace extract --har examples/sample.har

# 대상 URL을 헤드리스 브라우저로 구동하며 네트워크 요청 캡처
url-trace extract --url https://app.example.com --wait 5s

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

### 플래그

| 플래그 | 단축 | 설명 | 기본값 |
|--------|------|------|--------|
| `--har` | | HAR 캡처 파일 경로 | |
| `--url` | | 헤드리스 브라우저로 구동·캡처할 대상 URL | |
| `--wait` | | 페이지 로드 후 늦은 요청까지 캡처할 대기 시간 (`--url`) | `3s` |
| `--timeout` | | 브라우저 캡처 전체 상한 (`--url`) | `30s` |
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
└── internal/
    ├── model/           # URLRecord, PatternSuggestion, Result (감사 메타 포함 공통 타입)
    ├── source/          # Source 인터페이스 + HAR / 브라우저(chromedp) 소스
    ├── pipeline/        # 정규화 + 중복 제거/집계
    ├── classify/        # 1st/3rd-party 분류 + 신뢰도 점수
    ├── patterns/        # 보수적 와일드카드 패턴 제안
    ├── policy/          # 정책 빌드(승인 워크플로우)·매칭·diff
    ├── sqlexport/       # 설정 기반 범용 테이블/컬럼 매핑 (INSERT SQL)
    └── output/          # JSON / CSV 직렬화
```

`Source`는 인터페이스로 추상화되어 있어, HAR·브라우저 캡처가 동일한 파이프라인으로
합류한다. 향후 능동 크롤러도 이 인터페이스만 구현하면 변경 없이 얹힌다.

## 로드맵

- **Phase 2** (완료): chromedp 헤드리스 캡처 — 대상 URL 실제 구동하며 네트워크 요청 전량 기록
- **Phase 3** (완료): 경로 패턴 제안(보수적), 1st/3rd-party 분류, 신뢰도 점수
- **Phase 4** (완료): 정책 export(승인 워크플로우)·기존 정책 대비 diff·CI 게이트
- **Phase 5** (완료): 설정 기반 범용 SQL export (`--sql-config`)
- **다음**: GoReleaser 배포

## 테스트

```sh
go test ./...
```
