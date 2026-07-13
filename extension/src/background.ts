// Capture-only service worker. This mirrors the CLI's Source/pipeline split
// (see internal/source's package doc): this file's only job is "observe
// requests and hand over raw records" — normalization, dedup, classification
// and pattern suggestion all happen later, in WASM, driven by popup.ts.
import { isOwnResourceURL } from "./records";
import type { CapturedRequest } from "./types";

// The in-memory buffer is the source of truth while this service worker
// instance is alive. chrome.storage.session is a best-effort mirror so a
// recording survives MV3's aggressive SW eviction (~30s idle) — but we only
// ever overwrite it with the *whole* current buffer, never a
// read-modify-write against storage per request. A read-modify-write would
// race under concurrent request bursts (two overlapping onBeforeRequest
// calls could both read the old array, append their own item, and the
// second write clobbers the first) and silently drop captures — exactly
// what CLAUDE.md's "무음 탈락 금지" rules out.
let buffer: CapturedRequest[] = [];
let recording = false;

// Restore whatever was captured before this SW instance was last evicted.
// Requests that arrive while this is in flight are pushed onto `buffer`
// normally; the restored data is merged in front of them rather than
// clobbering them when this resolves.
const restored: Promise<void> = chrome.storage.session
  .get(["captured", "recording"])
  .then((data) => {
    const priorCaptured = (data.captured as CapturedRequest[] | undefined) ?? [];
    buffer = [...priorCaptured, ...buffer];
    recording = Boolean(data.recording);
  })
  .catch((err) => {
    console.error("url-trace: failed to restore capture state", err);
  });

let flushScheduled = false;
function scheduleFlush(): void {
  if (flushScheduled) return;
  flushScheduled = true;
  setTimeout(() => {
    flushScheduled = false;
    chrome.storage.session.set({ captured: buffer }).catch((err) => {
      console.error("url-trace: failed to persist captured buffer", err);
    });
  }, 250);
}

// Registered synchronously during the service worker's top-level script
// evaluation — an MV3 requirement, since listeners added later (e.g. inside
// an async message handler) can silently fail to re-attach after the SW is
// woken back up. The filter is intentionally "<all_urls>": webRequest only
// actually delivers events for origins the extension currently holds a
// granted permission for (requested per-target-domain from popup.ts), so
// scoping happens via chrome.permissions, not this filter.
chrome.webRequest.onBeforeRequest.addListener(
  (details) => {
    if (!recording || isOwnResourceURL(details.url)) return;
    buffer.push({ url: details.url, timeStamp: details.timeStamp });
    scheduleFlush();
  },
  { urls: ["<all_urls>"] }
);

type Request =
  | { type: "getStatus" }
  | { type: "start" }
  | { type: "stop" }
  | { type: "clear" }
  | { type: "export" };

type Response = { recording: boolean; count: number } | { captured: CapturedRequest[] };

chrome.runtime.onMessage.addListener((message: Request, _sender, sendResponse: (r: Response) => void) => {
  void (async () => {
    await restored;
    switch (message.type) {
      case "getStatus":
        sendResponse({ recording, count: buffer.length });
        return;
      case "start":
        recording = true;
        await chrome.storage.session.set({ recording: true });
        sendResponse({ recording, count: buffer.length });
        return;
      case "stop":
        recording = false;
        await chrome.storage.session.set({ recording: false });
        sendResponse({ recording, count: buffer.length });
        return;
      case "clear":
        buffer = [];
        await chrome.storage.session.set({ captured: buffer });
        sendResponse({ recording, count: buffer.length });
        return;
      case "export":
        sendResponse({ captured: buffer });
        return;
    }
  })();
  return true; // keep the message channel open for the async work above
});
