# Chrome Web Store 제출 문안 (초안)

개발자 대시보드(chrome.google.com/webstore/devconsole)에 그대로 붙여넣을 수 있도록
정리한 초안. 실제 제출 전에 스크린샷·프로모 타일만 채우면 된다.

## 기본 정보

- **이름**: `url-trace Capture`
- **카테고리**: Developer Tools
- **언어**: Korean (필요 시 English 버전 별도 등록 가능)

## 짧은 설명 (132자 이내, 검색 결과에 노출)

```
앱을 평소처럼 쓰는 동안 실제 요청 URL을 관찰해 화이트리스트 정책용 목록으로 정리합니다. 쿠키 주입·중복 로그인 없음.
```
(66자)

## 상세 설명

```
url-trace Capture는 애플리케이션이 실제로 호출하는 URL을 수집해, URL 화이트리스트
정책을 만들 때 쓸 수 있는 감사 친화적 목록으로 정리하는 확장입니다.

■ 왜 필요한가
방화벽/프록시의 URL 화이트리스트 정책을 만들려면 "이 앱이 실제로 어떤 URL을
호출하는가"를 빠짐없이 파악해야 합니다. 트래픽을 수동으로 캡처하거나, 쿠키를
복사해 자동화 도구에 주입하는 방식은 인증 세션이 꼬이거나(중복 로그인으로 세션
만료) 페이지를 놓치는 문제가 흔합니다.

■ 어떻게 다른가
이 확장은 당신이 브라우저에서 실제로 로그인해 앱을 쓰는 동안의 요청을 그대로
관찰합니다. 쿠키를 주입하지 않고, 별도 로그인도 필요 없습니다 — 이미 인증된
당신의 세션을 옆에서 지켜볼 뿐입니다.

■ 선택적 자동 크롤
사람이 앱을 계속 눌러보지 않아도 되도록, 원하면 depth·최대 페이지 수를 지정해
같은 호스트 링크를 자동으로 따라가게 할 수 있습니다. 별도 백그라운드 탭에서
진행되지만 같은 브라우저 프로필의 로그인 세션을 그대로 씁니다 — 새 세션을
만들지 않으므로 이 확장의 핵심 장점(중복 로그인 문제 없음)이 그대로 유지됩니다.

■ 무엇을 만들어주는가
- 관측된 URL 목록 (출처·빈도·최초/최종 관측 시각 포함)
- 기계 생성 세그먼트(숫자 ID, UUID 등)가 3회 이상 반복될 때만 제안하는 보수적
  와일드카드 패턴(자동 적용 안 함 — 항상 사람이 승인)
- 사람이 승인한 패턴만 반영하는 화이트리스트 정책 파일(JSON)
- 재수집 결과와 기존 정책을 비교해 신규 URL·미사용 규칙을 보여주는 diff
- 필요 시 SQL INSERT 문 (테이블/컬럼 매핑은 사용자가 직접 업로드하는 설정 파일로
  정의 — 실제 스키마는 개발자 서버로 전송되지 않습니다)

■ 개인정보
이 확장은 아무 데이터도 수집·전송하지 않습니다. 모든 처리(정규화, 집계, 분류,
패턴 제안, 정책 생성)는 브라우저 안에서 WebAssembly로 실행되며, 오픈소스
url-trace CLI와 완전히 동일한 코드입니다. 데이터가 브라우저 밖으로 나가는
유일한 경우는 사용자가 직접 "다운로드" 버튼을 눌러 파일로 저장할 때뿐입니다.

■ 오픈소스
전체 소스코드: https://github.com/gjwnssud/url-trace
```

## Single purpose description (필수 입력란)

```
Observes the network requests a web app makes, once the user grants host
access by clicking start, and turns them into an audit-friendly URL list for
building a URL allowlist/whitelist policy.
```

## 원격 코드 사용 여부

**No.** 모든 코드(팝업/백그라운드/리뷰 페이지 JS, WASM 바이너리)는 패키지에
번들되어 있으며 런타임에 원격에서 가져오지 않는다. CSP도
`script-src 'self' 'wasm-unsafe-eval'`로 자기 자신만 허용 — `wasm-unsafe-eval`은
번들 동봉된 WASM을 브라우저에서 컴파일·실행하기 위한 표준 요구사항일 뿐, 원격
스크립트 평가와는 무관.

## 권한별 정당화 (Justification 입력란)

| 권한 | 정당화 문구 |
|------|------------|
| `webRequest` | "Observes request URLs (not bodies/headers/cookies) once the user grants host access by clicking start, to build a complete URL allowlist candidate list — including third-party CDN/auth/analytics domains the target app depends on, which a hostname-scoped capture would silently miss. No requests are blocked or modified." |
| `storage` | "Holds the in-progress capture buffer and the user's saved domain-pattern text locally, so recording survives the service worker's normal suspend/resume cycle." |
| `downloads` | "Lets the user save the Result JSON / HAR / CSV / policy.json / SQL files they generate to their own device." |
| `optional_host_permissions` (`<all_urls>`, 런타임 요청) | "No host access is granted by default. The extension requests <all_urls> only when the user clicks start, via Chrome's own permission prompt — broad access is needed because a firewall allowlist is only safe if it includes every domain the app depends on, not just the one the user thinks to type in. Users can revoke at any time from chrome://extensions." |
| `tabs` | "Used only by the optional auto-crawl feature: opens one background tab and navigates it only to links on the same host as the page the crawl started from (never to other domains, regardless of host permission scope), reusing the user's existing signed-in session rather than creating a new one." |
| `scripting` | "Used only by the optional auto-crawl feature to read the current page's link URLs (document.querySelectorAll('a[href]'), including same-origin iframes and open shadow roots) inside the background crawl tab, so it knows which same-host pages to visit next. No script is injected into any other tab." |

## 데이터 사용 공개 (Data usage 탭 체크박스)

아래 항목 전부 **해당 없음(수집 안 함)** 으로 체크:
- Personally identifiable information
- Health info
- Financial and payment info
- Authentication info
- Personal communications
- Location
- Web history
- User activity
- Website content

근거: 관측된 URL은 브라우저 밖으로 전송되지 않고(서버 없음), 사용자가 명시적으로
다운로드하기 전까지 로컬 `chrome.storage.session`에만 머문다. "Web history"류
체크박스는 통상 브라우징 기록을 수집·전송하는 확장에 해당하는 것으로, 로컬
처리만 하고 전송하지 않는 이 확장에는 해당하지 않는다(단, 정책 상 애매하면 폼의
문구를 다시 확인해 보수적으로 표시할 것 — 최종 판단은 제출자 책임). `tabs` 권한도
동일한 원칙 — 크롤 중에만, 크롤 시작점과 같은 호스트 범위 안에서만(호스트 권한
범위 전체가 아니라) 탭 URL을 읽고 이동시키며, 그 데이터도 서버로 전송되지 않는다.

## 개인정보 처리방침 URL

```
https://github.com/gjwnssud/url-trace/blob/main/extension/PRIVACY.md
```

## 권한 최종 점검

**2026-07-13 (v0.1.1)**: `webRequest`/`storage`/`downloads`/`optional_host_permissions`
4개를 실제 코드 사용처와 대조 확인 — 전부 실사용 중이며 `activeTab`이나
`<all_urls>`를 `host_permissions`에 상시 등록하는 것은 요청하지 않는다.

**v0.2.0 갱신**: 선택적 자동 크롤 기능 추가로 `tabs`·`scripting` 2개 권한 추가 —
둘 다 크롤이 실제로 켜졌을 때만 쓰이고, 이미 사용자가 승인한 도메인 범위 안에서만
동작한다(새 host 권한을 추가로 요구하지 않음). 새 권한 추가라 웹스토어 재심사를
받는다 — 위 justification 표 참고. `chrome.tabs.create`(팝업→리뷰 페이지 이동)는
자체 확장 페이지 URL을 여는 것뿐이라 `tabs` 권한과 무관.

**v0.2.1 갱신**: 권한 변경 없음(버그 수정만) — 재심사 대상 아님. 자동 크롤 실사용
중 발견된 버그 3건: 팝업 폼 상태 미저장, 해시 라우팅 사이드 내비게이션에서 크롤이
시작 페이지 밖으로 못 나감, iframe/웹 컴포넌트 메뉴 링크 미인식. 세부는 README
로드맵 참고.

**v0.2.2 갱신**: 권한 변경 없음. CLI(`internal/source/browser.go`)와 크롤 로직을
서로 맞춤 — 해시 프래그먼트 처리를 CLI와 동일한 휴리스틱(SPA 라우트만 보존, 순수
인앵커는 제거)으로 정교화. iframe/shadow DOM 인식과 링크 안정화 대기는 반대로
CLI 쪽에 포팅함.

**v0.2.3 갱신**: 권한 변경 없음. 크롤이 자연 종료돼도 팝업이 그냥 "녹화 중"만 보여줘
사용자가 완료 여부를 알 수 없던 문제를 발견해 수정 — `crawlCompleted` 상태를 추가해
"크롤 완료(N페이지) · 녹화 중"으로 구분 표시(녹화 자체는 의도적으로 계속 켜둠).

**v0.3.0 갱신**: 선언된 권한 목록(`manifest.json`)은 그대로(`optional_host_permissions`에
`<all_urls>`가 이미 있었음) — 하지만 **런타임에 실제로 요청하는 범위가 바뀌었다**:
이전엔 "대상 도메인" 입력 패턴만 요청했는데, 이제는 "녹화 시작"을 누르면 항상
`<all_urls>` 전체를 요청한다. 화이트리스트가 안전하려면 대상 앱이 의존하는 서드파티
(CDN·인증·애널리틱스) 도메인까지 빠짐없이 잡아야 하는데, 사용자가 입력한 도메인으로
캡처를 제한하면 정작 그런 서드파티가 조용히 빠지기 때문(CLAUDE.md 재현율 우선 원칙).
"내 서비스 도메인" 입력 필드는 이제 캡처 범위가 아니라 1st/3rd-party 라벨링에만 쓰인다.
반면 자동 크롤의 **탐색 범위(어디로 자동 이동할지)는 그대로 시작점과 같은 호스트로
제한** — 이건 그대로 유지. 매니페스트에 새 권한 타입이 추가된 게 아니라 기존
`optional_host_permissions`를 더 넓게 요청하는 것뿐이지만, 실제 요청 범위가 크게
바뀐 만큼 재제출 시 위 justification 문구(특히 `optional_host_permissions`/`webRequest`
행)를 다시 확인해서 반영할 것.

## 스크린샷 (완료 — extension/store-assets/screenshots/)

1. `1-popup.png` — 팝업: 대상 도메인 입력 + 녹화 중 상태 + 캡처 건수
2. `2-review.png` — 정책 검토 페이지: 패턴 승인 체크박스 + 생성된 정책 요약

Chrome Web Store는 스크린샷이 **정확히** 1280x800 또는 640x400이어야 업로드된다
(그 외 크기는 "이미지 크기가 잘못되었습니다" 오류로 거부됨 — 최소 크기가 아니라
정확한 캔버스 크기 요구). `scripts/screenshots`가 실제 UI를 원본 크기로 캡처한 뒤
1280x800 캔버스 중앙에 배치하고, 캡처된 이미지 자신의 모서리 픽셀 색으로 여백을
채워 이음매 없이 보이게 만든다(`image`/`image/draw` 표준 라이브러리만 사용).
