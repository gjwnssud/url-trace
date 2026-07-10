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

새 export 포맷은 `internal/policy` 위에 얇은 어댑터 패키지로 추가한다(sqlexport 참조).

## 빌드·테스트·검증

```sh
go build -o bin/url-trace .
go test ./...           # source 패키지는 로컬 Chrome 있으면 실제 구동, 없으면 skip
go vet ./...
```

수정 후에는 반드시 vet + build + 관련 테스트 실행. 브라우저 캡처를 건드리면
`internal/source`의 통합 테스트(로컬 httptest 서버 + 실제 Chrome)까지 돌릴 것.

## 주의점

- `Source.Fetch`는 out 채널을 닫지 않는다 — 채널 수명은 호출자(collect) 소유
- 시간 zero value는 "관측 시각 불명"의 의미 — 병합 시 실제 시각이 항상 이긴다
- 정책 스키마 버전은 `policy.CurrentVersion` — 스키마 변경 시 반드시 올리고 Load 검증 유지
- sqlexport의 `{id}`는 패턴 SHA-256 hex(결정적, 재수출 시 동일 ID) — `maxLength`로
  잘라 쓰며 `{id}` 단독 컬럼의 truncation은 경고 없음(해시 prefix도 유효한 키)
- 실제 매핑 설정(`sqlexport-config*.json`)은 gitignore 대상 — 예제만 커밋
