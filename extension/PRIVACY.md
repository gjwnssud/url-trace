# Privacy Policy — url-trace Capture

_Last updated: 2026-07-13_

## Summary

url-trace Capture does not collect, transmit, sell, or share any data with the
developer or any third party. Every piece of processing happens locally
inside your browser. Nothing leaves your machine unless you explicitly click
a "download" button to save a file to your own disk.

## What the extension observes

When you turn recording on for a domain you specify, the extension watches
the network requests your browser makes to that domain (via the
`webRequest` API) and records each request's URL and timestamp. This is the
extension's entire purpose: turning your own real usage of an application
into a list of URLs for building an allowlist/whitelist policy.

- Recording is **off by default** and only starts when you explicitly click
  "녹화 시작" (Start recording) in the popup.
- The extension only ever requests browser permission for the specific
  domain(s) you type in — it does not hold blanket access to all sites by
  default (see "Permissions" below).
- Only the request **URL** and **timestamp** are recorded. Request/response
  bodies, headers, and cookies are never read or stored.

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
| `webRequest` | Observe request URLs on domains you explicitly grant, to build the capture list. No requests are blocked or modified. |
| `storage` | Hold the in-progress capture buffer and your saved domain-pattern preference, locally. |
| `downloads` | Let you save Result JSON / HAR / CSV / policy.json / SQL export files you generate. |
| `optional_host_permissions` (`<all_urls>`, requested per-domain at runtime) | The extension has no host access by default. When you click "녹화 시작", it requests permission only for the domain pattern(s) you typed, via Chrome's own permission-grant UI. You can revoke this at any time from `chrome://extensions`. |

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
