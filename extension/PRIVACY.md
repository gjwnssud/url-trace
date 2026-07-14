# Privacy Policy — url-trace Capture

_Last updated: 2026-07-14_

## Summary

url-trace Capture does not collect, transmit, sell, or share any data with the
developer or any third party. Every piece of processing happens locally
inside your browser. Nothing leaves your machine unless you explicitly click
a "download" button to save a file to your own disk.

## What the extension observes

When you click "녹화 시작" (Start recording), the extension watches the
network requests your browser makes (via the `webRequest` API) and records
each request's URL and timestamp. This is the extension's entire purpose:
turning your own real usage of an application into a list of URLs for
building an allowlist/whitelist policy.

- Recording is **off by default** and only starts when you explicitly click
  "녹화 시작" in the popup.
- Starting recording requests permission to observe requests on **all
  sites** (`<all_urls>`), not just the domain(s) you type in the "내 서비스
  도메인" field — a firewall allowlist is only safe if it includes every
  domain the application actually depends on, including third-party CDN,
  auth, and analytics domains you may not know about in advance; scoping
  observation to a hand-typed domain would silently miss exactly those. This
  permission is requested at runtime via Chrome's own permission-grant UI
  (see "Permissions" below) — never granted by default — and you can revoke
  it at any time from `chrome://extensions`.
- The "내 서비스 도메인" field does not restrict what's captured; it is only
  used afterward, locally, to label each captured URL as first- or
  third-party in the exported result.
- Only the request **URL** and **timestamp** are recorded. Request/response
  bodies, headers, and cookies are never read or stored.
- Automated crawling (the optional "자동 크롤" feature) only ever navigates
  to links on the same host as the page it started from — the `<all_urls>`
  permission widens what's *observed*, not where the extension automatically
  clicks around.

## Where the data goes

- Captured URLs are held in `chrome.storage.session`, which is local to your
  browser profile and is cleared when the browser session ends. They are
  never sent over the network by this extension.
- All processing — normalization, deduplication, classification, wildcard
  pattern suggestion, and policy generation — runs in a WebAssembly module
  compiled from the same open-source Go code as the
  [url-trace CLI](https://github.com/gjwnssud/url-trace), executed entirely
  inside your browser. No server, no API call, no telemetry.
- Data leaves your browser **only** when you click an explicit
  "다운로드"/"Result JSON"/"HAR"/"CSV"/"policy.json"/".sql" button, which
  saves a file to a location you choose on your own device via Chrome's
  standard download flow.

## Permissions used

| Permission | Why |
|---|---|
| `webRequest` | Observe request URLs once you grant host access, to build the capture list. No requests are blocked or modified. |
| `storage` | Hold the in-progress capture buffer and your saved domain-pattern preference, locally. |
| `downloads` | Let you save Result JSON / HAR / CSV / policy.json / SQL export files you generate. |
| `optional_host_permissions` (`<all_urls>`, requested at runtime) | The extension has no host access by default. When you click "녹화 시작", it requests `<all_urls>` via Chrome's own permission-grant UI so it can capture every domain the app you're recording depends on (see "What the extension observes" above for why). You can revoke this at any time from `chrome://extensions`. |
| `tabs` | Used only by the optional "자동 크롤" (auto-crawl) feature: opens one background tab and reads/navigates its URL, restricted to the same host as the page the crawl started from. Never used outside an active crawl. |
| `scripting` | Used only by the optional auto-crawl feature to read link URLs from the current page inside that background crawl tab (including same-origin iframes and open shadow roots), so it knows which same-host pages to visit next. No script is ever injected into any other tab. |

## Third parties

None. This extension makes no network requests of its own, uses no
analytics or crash-reporting SDKs, and does not communicate with any server
operated by the developer or anyone else.

## Source code

This extension is open source. You can review exactly what it does at
<https://github.com/gjwnssud/url-trace/tree/main/extension>.

## Contact

Questions or concerns: open an issue at
<https://github.com/gjwnssud/url-trace/issues>.
