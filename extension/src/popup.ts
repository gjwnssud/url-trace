import { byId, setMessage as setMessageOn } from "./dom";
import { download, hostnameOfPattern, parsePatterns, toCSV, toHAR, toURLRecords } from "./records";
import type { CapturedRequest } from "./types";
import * as wasmHost from "./wasm-host";

const domainsInput = byId<HTMLInputElement>("domains");
const startBtn = byId<HTMLButtonElement>("startBtn");
const stopBtn = byId<HTMLButtonElement>("stopBtn");
const clearBtn = byId<HTMLButtonElement>("clearBtn");
const dot = byId<HTMLSpanElement>("dot");
const statusText = byId<HTMLSpanElement>("statusText");
const countEl = byId<HTMLSpanElement>("count");
const messageEl = byId<HTMLDivElement>("message");
const exportJsonBtn = byId<HTMLButtonElement>("exportJson");
const exportHarBtn = byId<HTMLButtonElement>("exportHar");
const exportCsvBtn = byId<HTMLButtonElement>("exportCsv");
const reviewLink = byId<HTMLAnchorElement>("reviewLink");
const crawlEnabled = byId<HTMLInputElement>("crawlEnabled");
const crawlDepth = byId<HTMLInputElement>("crawlDepth");
const crawlMaxPages = byId<HTMLInputElement>("crawlMaxPages");

interface Status {
  recording: boolean;
  count: number;
  crawling: boolean;
  pagesVisited: number;
  maxPages: number;
  crawlCompleted: boolean;
}

function setMessage(text: string, kind: "error" | "info" = "error"): void {
  setMessageOn(messageEl, text, kind);
}

function setStatus(status: Status): void {
  dot.classList.toggle("on", status.recording);
  statusText.textContent = status.crawling
    ? `녹화 중 · 크롤 ${status.pagesVisited}/${status.maxPages}`
    : status.recording
      ? status.crawlCompleted
        ? `크롤 완료(${status.pagesVisited}p) · 녹화 중`
        : "녹화 중"
      : "대기 중";
  countEl.textContent = `${status.count}건`;
  startBtn.disabled = status.recording;
  stopBtn.disabled = !status.recording;
  // 실행 중인 크롤 상태를 background(진실의 원천)에서 반영 — 팝업을 껐다 켜도
  // 체크박스가 항상 초기값(꺼짐)으로 돌아가던 문제의 일부 원인이었음.
  if (status.recording) crawlEnabled.checked = status.crawling;
  domainsInput.disabled = status.recording;
  crawlEnabled.disabled = status.recording;
  crawlDepth.disabled = status.recording;
  crawlMaxPages.disabled = status.recording;
}

async function sendMessage<T>(message: unknown): Promise<T> {
  return chrome.runtime.sendMessage(message) as Promise<T>;
}

async function refreshStatus(): Promise<void> {
  const res = await sendMessage<Status>({ type: "getStatus" });
  setStatus(res);
}

// Single settings blob (domains + crawl controls) so reopening the popup
// restores the whole form, not just the domain text — previously only
// domainsInput was persisted, so "자동 크롤" checkbox/depth/최대 페이지 always
// reset to their HTML defaults on every popup open, even mid-recording.
const STORAGE_KEY_SETTINGS = "captureSettings";

interface SavedSettings {
  domains: string;
  crawlEnabled: boolean;
  crawlDepth: number;
  crawlMaxPages: number;
}

async function restoreSettings(): Promise<void> {
  const { [STORAGE_KEY_SETTINGS]: saved } = await chrome.storage.local.get(STORAGE_KEY_SETTINGS);
  const s = saved as Partial<SavedSettings> | undefined;
  if (!s) return;
  if (typeof s.domains === "string") domainsInput.value = s.domains;
  if (typeof s.crawlEnabled === "boolean") crawlEnabled.checked = s.crawlEnabled;
  if (typeof s.crawlDepth === "number") crawlDepth.value = String(s.crawlDepth);
  if (typeof s.crawlMaxPages === "number") crawlMaxPages.value = String(s.crawlMaxPages);
}

function saveSettings(): void {
  const settings: SavedSettings = {
    domains: domainsInput.value,
    crawlEnabled: crawlEnabled.checked,
    crawlDepth: Number(crawlDepth.value) || 0,
    crawlMaxPages: Number(crawlMaxPages.value) || 50,
  };
  void chrome.storage.local.set({ [STORAGE_KEY_SETTINGS]: settings });
}

domainsInput.addEventListener("change", saveSettings);
crawlEnabled.addEventListener("change", saveSettings);
crawlDepth.addEventListener("change", saveSettings);
crawlMaxPages.addEventListener("change", saveSettings);

startBtn.addEventListener("click", () => {
  // chrome.permissions.request must be called synchronously within the
  // click handler's call stack — it requires an active user gesture.
  void (async () => {
    setMessage("", "info");
    try {
      // Request <all_urls> rather than only the patterns typed into "대상
      // 도메인" — this tool exists to build a *complete* URL inventory for a
      // whitelist policy (CLAUDE.md 재현율 우선), and scoping capture to a
      // hand-typed pattern silently misses exactly what matters most for
      // that: third-party CDN/auth/analytics domains the target app depends
      // on but the user didn't think to list. classify (WASM) still splits
      // 1st vs 3rd party afterward using the domain field below, so nothing
      // about that distinction is lost by capturing more broadly. Auto-crawl
      // navigation itself stays restricted to the seed's own host (see
      // background.ts) — this only widens what's *observed*, not where the
      // extension automatically clicks around.
      const granted = await chrome.permissions.request({ origins: ["<all_urls>"] });
      if (!granted) {
        setMessage("권한이 승인되지 않아 녹화를 시작할 수 없습니다.");
        return;
      }
    } catch (err) {
      setMessage(`권한 요청 실패: ${String(err)}`);
      return;
    }
    saveSettings();

    if (crawlEnabled.checked) {
      const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
      const seedURL = tab?.url;
      if (!seedURL || !/^https?:\/\//.test(seedURL)) {
        setMessage("자동 크롤은 현재 탭이 http(s) 페이지여야 합니다 — 대상 앱 탭에서 눌러주세요.");
        return;
      }
      const depth = Math.max(0, Number(crawlDepth.value) || 0);
      const maxPages = Math.max(1, Number(crawlMaxPages.value) || 50);
      const res = await sendMessage<Status>({ type: "crawl", seedURL, depth, maxPages });
      setStatus(res);
      setMessage(`자동 크롤 시작 — ${seedURL}에서 depth ${depth}까지 따라갑니다.`, "info");
      return;
    }

    const res = await sendMessage<Status>({ type: "start" });
    setStatus(res);
    setMessage("녹화 중 — 대상 앱을 평소처럼 사용하세요.", "info");
  })();
});

stopBtn.addEventListener("click", () => {
  void sendMessage<Status>({ type: "stop" }).then(setStatus);
});

clearBtn.addEventListener("click", () => {
  if (!confirm("캡처된 데이터를 모두 지울까요?")) return;
  void sendMessage<Status>({ type: "clear" }).then((res) => {
    setStatus(res);
    setMessage("초기화했습니다.", "info");
  });
});

async function currentCaptured(): Promise<CapturedRequest[]> {
  const res = await sendMessage<{ captured: CapturedRequest[] }>({ type: "export" });
  return res.captured ?? [];
}

function primaryDomainsFromInput(): string[] {
  const seen = new Set<string>();
  for (const p of parsePatterns(domainsInput.value)) {
    const host = hostnameOfPattern(p);
    if (host) seen.add(host);
  }
  return [...seen];
}

exportJsonBtn.addEventListener("click", () => {
  void (async () => {
    const captured = await currentCaptured();
    if (captured.length === 0) {
      setMessage("캡처된 데이터가 없습니다.");
      return;
    }
    try {
      const { result, skipped } = await wasmHost.process(toURLRecords(captured), primaryDomainsFromInput());
      if (skipped > 0) {
        setMessage(`${skipped}건 파싱 실패로 건너뜀 (콘솔 참고)`, "info");
      } else {
        setMessage(`${result.urls.length}개 URL 내보냄`, "info");
      }
      download("url-trace-result.json", JSON.stringify(result, null, 2), "application/json");
    } catch (err) {
      setMessage(`처리 실패: ${String(err)}`);
    }
  })();
});

exportHarBtn.addEventListener("click", () => {
  void (async () => {
    const captured = await currentCaptured();
    if (captured.length === 0) {
      setMessage("캡처된 데이터가 없습니다.");
      return;
    }
    download("url-trace-capture.har", toHAR(captured), "application/json");
    setMessage(`${captured.length}건 HAR로 내보냄`, "info");
  })();
});

exportCsvBtn.addEventListener("click", () => {
  void (async () => {
    const captured = await currentCaptured();
    if (captured.length === 0) {
      setMessage("캡처된 데이터가 없습니다.");
      return;
    }
    try {
      const { result } = await wasmHost.process(toURLRecords(captured), primaryDomainsFromInput());
      download("url-trace-result.csv", toCSV(result.urls), "text/csv");
      setMessage(`${result.urls.length}개 URL CSV로 내보냄 (패턴 제안은 CSV에 미포함)`, "info");
    } catch (err) {
      setMessage(`처리 실패: ${String(err)}`);
    }
  })();
});

reviewLink.addEventListener("click", (e) => {
  e.preventDefault();
  void chrome.tabs.create({ url: chrome.runtime.getURL("review.html") });
});

void restoreSettings();
void refreshStatus();
setInterval(() => void refreshStatus(), 1000);
