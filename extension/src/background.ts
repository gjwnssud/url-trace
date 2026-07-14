// Capture-only service worker. This mirrors the CLI's Source/pipeline split
// (see internal/source's package doc): this file's only job is "observe
// requests and hand over raw records" — normalization, dedup, classification
// and pattern suggestion all happen later, in WASM, driven by popup.ts.
import { hostOf, isOwnResourceURL, normalizeLink } from "./records";
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

function handleRequest(details: chrome.webRequest.WebRequestBodyDetails): void {
  if (!recording || isOwnResourceURL(details.url)) return;
  buffer.push({ url: details.url, timeStamp: details.timeStamp });
  scheduleFlush();
}

// chrome.webRequest.onBeforeRequest.addListener() throws ("You need to
// request host permissions...") unless the extension ALREADY holds at least
// one granted host permission — which is never true on a fresh install,
// since we deliberately ship with zero host_permissions and only request one
// per-domain at runtime (popup.ts) for least privilege. So the listener
// can't be registered unconditionally; it's registered once a permission
// actually exists, and re-registered (idempotently) whenever a new one is
// granted. Both calls below run synchronously during the service worker's
// top-level script evaluation — the MV3 requirement is that registration is
// not deferred into a later event callback (e.g. a message handler), not
// that the promise inside must already be resolved.
function registerCaptureListener(): void {
  if (chrome.webRequest.onBeforeRequest.hasListener(handleRequest)) return;
  chrome.webRequest.onBeforeRequest.addListener(handleRequest, { urls: ["<all_urls>"] });
}
chrome.permissions.getAll().then((perms) => {
  if ((perms.origins ?? []).length > 0) registerCaptureListener();
});
chrome.permissions.onAdded.addListener(() => registerCaptureListener());

// --- Optional auto-crawl -----------------------------------------------
//
// Recording alone requires a human to browse the target app; auto-crawl is
// an opt-in addition that follows same-host links breadth-first, mirroring
// internal/source/browser.go's crawl() on the CLI side (Depth/MaxPages) —
// but it runs inside the SAME already-authenticated browser profile via a
// dedicated background tab (chrome.tabs), not a fresh chromedp session, so
// it never hits the duplicate-login problem that motivated this extension.
// Only one crawl runs at a time.
interface CrawlItem {
  url: string;
  depth: number;
}

let crawling = false;
let crawlPagesVisited = 0;
let crawlMaxPages = 0;
let crawlCancelRequested = false;

// waitForTabComplete resolves once tabId reaches "complete". The listener is
// attached BEFORE returning, so callers that are about to trigger a
// navigation must call this first and await its promise only after starting
// the navigation — attaching after chrome.tabs.update() races: tabs.update()
// can return while chrome.tabs.get() still reports the PREVIOUS page's
// "complete" status, so checking status first (this function used to) can
// silently skip waiting for the new page entirely, then extract links from a
// half-loaded (or stale) document. The immediate chrome.tabs.get() check
// below instead covers the opposite race — a tab (e.g. one that
// chrome.tabs.create() just started navigating) finishing before this
// function was even called.
function waitForTabComplete(tabId: number): Promise<void> {
  return new Promise<void>((resolve) => {
    let settled = false;
    const finish = (): void => {
      if (settled) return;
      settled = true;
      chrome.tabs.onUpdated.removeListener(listener);
      resolve();
    };
    const listener = (updatedTabId: number, changeInfo: chrome.tabs.TabChangeInfo): void => {
      if (updatedTabId === tabId && changeInfo.status === "complete") finish();
    };
    chrome.tabs.onUpdated.addListener(listener);
    chrome.tabs.get(tabId).then((tab) => {
      if (tab.status === "complete") finish();
    });
  });
}

// Idle time after a page reaches "complete" for late XHR/fetch to still
// fire, mirroring the CLI's --wait.
function settleAfterLoad(): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, 1200));
}

// allFrames covers side-nav/menu content rendered inside an <iframe>; the
// recursive shadowRoot walk covers web-component menus (open shadow roots
// only — closed shadow roots are unobservable from injected scripts, a
// platform limitation, not a bug here). Frames the script couldn't reach
// (cross-origin without a granted host permission) are simply absent from
// `results`, not an error.
async function extractLinks(tabId: number): Promise<string[]> {
  const results = await chrome.scripting.executeScript({
    target: { tabId, allFrames: true },
    func: () => {
      function collect(root: Document | ShadowRoot): string[] {
        const hrefs = Array.from(root.querySelectorAll("a[href]")).map((a) => (a as HTMLAnchorElement).href);
        for (const el of Array.from(root.querySelectorAll("*"))) {
          if (el.shadowRoot) hrefs.push(...collect(el.shadowRoot));
        }
        return hrefs;
      }
      return collect(document);
    },
  });
  return results.flatMap((r) => r.result ?? []);
}

// Side nav content in dashboards is frequently rendered by an async fetch
// AFTER the page reaches "complete", so a single fixed-delay extraction can
// run before it exists yet. Poll until two consecutive reads return the same
// link count, instead of trusting one fixed wait.
async function waitForStableLinks(tabId: number): Promise<string[]> {
  const maxRounds = 8;
  const intervalMs = 500;
  let previous = await extractLinks(tabId).catch((err: unknown) => {
    console.error("url-trace: link extraction failed on tab", tabId, err);
    return [];
  });
  for (let round = 0; round < maxRounds; round++) {
    await new Promise((resolve) => setTimeout(resolve, intervalMs));
    const next = await extractLinks(tabId).catch(() => previous);
    if (next.length === previous.length) return next;
    previous = next;
  }
  return previous;
}

async function runCrawl(rawSeedURL: string, depth: number, maxPages: number): Promise<void> {
  if (crawling) return;
  crawling = true;
  crawlPagesVisited = 0;
  crawlMaxPages = maxPages;
  crawlCancelRequested = false;

  // Canonicalize the seed the same way normalizeLink() canonicalizes every
  // discovered link (e.g. new URL("http://h:1").toString() === "http://h:1/")
  // — otherwise a link back to the bare origin never matches the seed in
  // `visited` and the crawler burns a page revisiting where it started.
  const seed = normalizeLink(rawSeedURL) || rawSeedURL;
  const seedHost = hostOf(seed);
  const visited = new Set<string>([seed]);
  const queue: CrawlItem[] = [{ url: seed, depth: 0 }];

  const tab = await chrome.tabs.create({ url: seed, active: false });
  const tabId = tab.id;
  if (tabId === undefined) {
    crawling = false;
    return;
  }

  try {
    while (queue.length > 0 && crawlPagesVisited < maxPages && !crawlCancelRequested) {
      const item = queue.shift();
      if (!item) break;
      if (item.url !== seed) {
        const loaded = waitForTabComplete(tabId); // attach before navigating — see comment above
        await chrome.tabs.update(tabId, { url: item.url });
        await loaded;
      } else {
        await waitForTabComplete(tabId); // seed tab is already navigating from chrome.tabs.create()
      }
      await settleAfterLoad();
      crawlPagesVisited++;

      if (item.depth < depth) {
        const links = await waitForStableLinks(tabId);
        let queued = 0;
        for (const href of links) {
          const clean = normalizeLink(href);
          if (!clean || visited.has(clean) || hostOf(clean) !== seedHost) continue;
          visited.add(clean);
          queue.push({ url: clean, depth: item.depth + 1 });
          queued++;
        }
        // 무음 탈락 금지: 왜 나머지가 큐에 안 들어갔는지(이미 방문·다른 호스트·
        // http(s) 아님) 콘솔에서 확인할 수 있게 항상 남긴다.
        console.debug(
          `url-trace: ${item.url} → 링크 ${links.length}개 발견, 신규 ${queued}개 큐 추가`
        );
      }
    }
  } finally {
    await chrome.tabs.remove(tabId).catch(() => {});
    crawling = false;
  }
}

type Request =
  | { type: "getStatus" }
  | { type: "start" }
  | { type: "stop" }
  | { type: "clear" }
  | { type: "export" }
  | { type: "crawl"; seedURL: string; depth: number; maxPages: number };

type Response =
  | { recording: boolean; count: number; crawling: boolean; pagesVisited: number; maxPages: number }
  | { captured: CapturedRequest[] };

chrome.runtime.onMessage.addListener((message: Request, _sender, sendResponse: (r: Response) => void) => {
  void (async () => {
    await restored;
    switch (message.type) {
      case "getStatus":
        sendResponse({ recording, count: buffer.length, crawling, pagesVisited: crawlPagesVisited, maxPages: crawlMaxPages });
        return;
      case "start":
        recording = true;
        await chrome.storage.session.set({ recording: true });
        sendResponse({ recording, count: buffer.length, crawling, pagesVisited: crawlPagesVisited, maxPages: crawlMaxPages });
        return;
      case "stop":
        recording = false;
        crawlCancelRequested = true;
        await chrome.storage.session.set({ recording: false });
        sendResponse({ recording, count: buffer.length, crawling, pagesVisited: crawlPagesVisited, maxPages: crawlMaxPages });
        return;
      case "clear":
        buffer = [];
        await chrome.storage.session.set({ captured: buffer });
        sendResponse({ recording, count: buffer.length, crawling, pagesVisited: crawlPagesVisited, maxPages: crawlMaxPages });
        return;
      case "export":
        sendResponse({ captured: buffer });
        return;
      case "crawl":
        recording = true;
        await chrome.storage.session.set({ recording: true });
        void runCrawl(message.seedURL, message.depth, message.maxPages).catch((err: unknown) => {
          console.error("url-trace: crawl failed", err);
        });
        sendResponse({ recording, count: buffer.length, crawling: true, pagesVisited: 0, maxPages: message.maxPages });
        return;
    }
  })();
  return true; // keep the message channel open for the async work above
});
