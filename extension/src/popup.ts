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

function setMessage(text: string, kind: "error" | "info" = "error"): void {
  setMessageOn(messageEl, text, kind);
}

function setStatus(recording: boolean, count: number): void {
  dot.classList.toggle("on", recording);
  statusText.textContent = recording ? "녹화 중" : "대기 중";
  countEl.textContent = `${count}건`;
  startBtn.disabled = recording;
  stopBtn.disabled = !recording;
}

async function sendMessage<T>(message: unknown): Promise<T> {
  return chrome.runtime.sendMessage(message) as Promise<T>;
}

async function refreshStatus(): Promise<void> {
  const res = await sendMessage<{ recording: boolean; count: number }>({ type: "getStatus" });
  setStatus(res.recording, res.count);
}

const STORAGE_KEY_PATTERNS = "domainPatterns";

async function restoreDomainsInput(): Promise<void> {
  const { [STORAGE_KEY_PATTERNS]: saved } = await chrome.storage.local.get(STORAGE_KEY_PATTERNS);
  if (typeof saved === "string") domainsInput.value = saved;
}

startBtn.addEventListener("click", () => {
  // chrome.permissions.request must be called synchronously within the
  // click handler's call stack — it requires an active user gesture.
  void (async () => {
    setMessage("", "info");
    const patterns = parsePatterns(domainsInput.value);
    if (patterns.length === 0) {
      setMessage("대상 도메인을 하나 이상 입력하세요.");
      return;
    }
    try {
      const granted = await chrome.permissions.request({ origins: patterns });
      if (!granted) {
        setMessage("권한이 승인되지 않아 녹화를 시작할 수 없습니다.");
        return;
      }
    } catch (err) {
      setMessage(`권한 요청 실패: ${String(err)}`);
      return;
    }
    await chrome.storage.local.set({ [STORAGE_KEY_PATTERNS]: domainsInput.value });
    const res = await sendMessage<{ recording: boolean; count: number }>({ type: "start" });
    setStatus(res.recording, res.count);
    setMessage("녹화 중 — 대상 앱을 평소처럼 사용하세요.", "info");
  })();
});

stopBtn.addEventListener("click", () => {
  void sendMessage<{ recording: boolean; count: number }>({ type: "stop" }).then((res) =>
    setStatus(res.recording, res.count)
  );
});

clearBtn.addEventListener("click", () => {
  if (!confirm("캡처된 데이터를 모두 지울까요?")) return;
  void sendMessage<{ recording: boolean; count: number }>({ type: "clear" }).then((res) => {
    setStatus(res.recording, res.count);
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

void restoreDomainsInput();
void refreshStatus();
setInterval(() => void refreshStatus(), 1000);
