import { appendWarnings, byId, cell, setMessage } from "./dom";
import { download, hostnameOfPattern, parsePatterns, toURLRecords } from "./records";
import type { BuildOptions, CapturedRequest, Policy, Result, Rule, SqlConfig, URLRecord } from "./types";
import * as wasmHost from "./wasm-host";

const loadCaptureBtn = byId<HTMLButtonElement>("loadCaptureBtn");
const resultFileInput = byId<HTMLInputElement>("resultFileInput");
const loadStatusEl = byId<HTMLDivElement>("loadStatus");

const minConfidenceSelect = byId<HTMLSelectElement>("minConfidence");
const partyFilterSelect = byId<HTMLSelectElement>("partyFilter");
const patternListEl = byId<HTMLDivElement>("patternList");
const buildPolicyBtn = byId<HTMLButtonElement>("buildPolicyBtn");
const downloadPolicyBtn = byId<HTMLButtonElement>("downloadPolicyBtn");
const policySummaryEl = byId<HTMLDivElement>("policySummary");

const sqlConfigFileInput = byId<HTMLInputElement>("sqlConfigFileInput");
const generateSqlBtn = byId<HTMLButtonElement>("generateSqlBtn");
const downloadSqlBtn = byId<HTMLButtonElement>("downloadSqlBtn");
const sqlSummaryEl = byId<HTMLDivElement>("sqlSummary");

const policyFileInput = byId<HTMLInputElement>("policyFileInput");
const diffBtn = byId<HTMLButtonElement>("diffBtn");
const diffSummaryEl = byId<HTMLDivElement>("diffSummary");
const newUrlsTable = byId<HTMLTableElement>("newUrlsTable");
const unusedRulesTable = byId<HTMLTableElement>("unusedRulesTable");

let currentResult: Result | null = null;
let builtPolicy: Policy | null = null;
let generatedSql: string | null = null;

async function sendMessage<T>(message: unknown): Promise<T> {
  return chrome.runtime.sendMessage(message) as Promise<T>;
}

async function primaryDomainsFromStorage(): Promise<string[]> {
  const { domainPatterns } = await chrome.storage.local.get("domainPatterns");
  if (typeof domainPatterns !== "string") return [];
  const seen = new Set<string>();
  for (const p of parsePatterns(domainPatterns)) {
    const host = hostnameOfPattern(p);
    if (host) seen.add(host);
  }
  return [...seen];
}

function setCurrentResult(result: Result, statusText: string): void {
  currentResult = result;
  setMessage(loadStatusEl, statusText, "info");
  renderPatterns(result.patternSuggestions);
  buildPolicyBtn.disabled = false;
  updateDiffButtonState();
}

function renderPatterns(suggestions: Result["patternSuggestions"]): void {
  patternListEl.replaceChildren();
  if (suggestions.length === 0) {
    const p = document.createElement("p");
    p.className = "empty";
    p.textContent = "제안된 패턴이 없습니다 (기계 생성 세그먼트가 distinct 3개 이상 관측될 때만 제안됨).";
    patternListEl.appendChild(p);
    return;
  }
  for (const s of suggestions) {
    const row = document.createElement("label");
    row.className = "pattern-row";

    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.value = s.pattern;
    checkbox.className = "pattern-checkbox";

    const info = document.createElement("span");
    const code = document.createElement("code");
    code.textContent = s.pattern;
    const meta = document.createElement("span");
    meta.className = "muted";
    meta.textContent = ` distinct ${s.distinctValues} · total ${s.totalCount}`;
    info.appendChild(code);
    info.appendChild(meta);

    row.appendChild(checkbox);
    row.appendChild(info);
    patternListEl.appendChild(row);
  }
}

loadCaptureBtn.addEventListener("click", () => {
  void (async () => {
    setMessage(loadStatusEl, "불러오는 중...", "info");
    const { captured } = await sendMessage<{ captured: CapturedRequest[] }>({ type: "export" });
    if (!captured || captured.length === 0) {
      setMessage(loadStatusEl, "캡처된 데이터가 없습니다 — 팝업에서 먼저 녹화하세요.");
      return;
    }
    try {
      const domains = await primaryDomainsFromStorage();
      const { result, skipped } = await wasmHost.process(toURLRecords(captured), domains);
      const skippedNote = skipped > 0 ? ` (파싱 실패 ${skipped}건)` : "";
      setCurrentResult(result, `현재 캡처 ${captured.length}건 → URL ${result.urls.length}개 불러옴${skippedNote}`);
    } catch (err) {
      setMessage(loadStatusEl, `처리 실패: ${String(err)}`);
    }
  })();
});

resultFileInput.addEventListener("change", () => {
  void (async () => {
    const file = resultFileInput.files?.[0];
    if (!file) return;
    try {
      const result = JSON.parse(await file.text()) as Result;
      setCurrentResult(result, `업로드한 Result JSON에서 URL ${result.urls.length}개 불러옴`);
    } catch (err) {
      setMessage(loadStatusEl, `파일 파싱 실패: ${String(err)}`);
    }
  })();
});

buildPolicyBtn.addEventListener("click", () => {
  void (async () => {
    if (!currentResult) return;
    const acceptPatterns = [...patternListEl.querySelectorAll<HTMLInputElement>(".pattern-checkbox:checked")].map(
      (c) => c.value
    );
    const opts: BuildOptions = {
      minConfidence: minConfidenceSelect.value || undefined,
      party: partyFilterSelect.value || undefined,
      acceptPatterns,
    };
    try {
      const { policy, warnings } = await wasmHost.buildPolicy(currentResult, opts);
      builtPolicy = policy;
      downloadPolicyBtn.disabled = false;
      updateSqlButtonState();
      updateDiffButtonState();
      setMessage(policySummaryEl, `정책 생성됨 — 규칙 ${policy.rules.length}개`, "info");
      appendWarnings(policySummaryEl, warnings);
    } catch (err) {
      setMessage(policySummaryEl, `정책 생성 실패: ${String(err)}`);
    }
  })();
});

downloadPolicyBtn.addEventListener("click", () => {
  if (!builtPolicy) return;
  download("policy.json", JSON.stringify(builtPolicy, null, 2), "application/json");
});

function updateSqlButtonState(): void {
  generateSqlBtn.disabled = !(builtPolicy && sqlConfigFileInput.files?.length);
}
sqlConfigFileInput.addEventListener("change", updateSqlButtonState);

generateSqlBtn.addEventListener("click", () => {
  void (async () => {
    if (!builtPolicy) return;
    const file = sqlConfigFileInput.files?.[0];
    if (!file) return;
    try {
      const config = JSON.parse(await file.text()) as SqlConfig;
      const { sql, warnings } = await wasmHost.exportSQL(builtPolicy, config, new Date());
      generatedSql = sql;
      downloadSqlBtn.disabled = false;
      const stmtCount = (sql.match(/INSERT INTO/g) ?? []).length;
      setMessage(sqlSummaryEl, `SQL 생성됨 — INSERT ${stmtCount}건`, "info");
      appendWarnings(sqlSummaryEl, warnings);
    } catch (err) {
      setMessage(sqlSummaryEl, `SQL 생성 실패: ${String(err)}`);
    }
  })();
});

downloadSqlBtn.addEventListener("click", () => {
  if (!generatedSql) return;
  download("policy-export.sql", generatedSql, "application/sql");
});

function updateDiffButtonState(): void {
  diffBtn.disabled = !(currentResult && policyFileInput.files?.length);
}
policyFileInput.addEventListener("change", updateDiffButtonState);

diffBtn.addEventListener("click", () => {
  void (async () => {
    if (!currentResult) return;
    const file = policyFileInput.files?.[0];
    if (!file) return;
    try {
      const policy = JSON.parse(await file.text()) as Policy;
      const report = await wasmHost.diff(policy, currentResult);
      setMessage(
        diffSummaryEl,
        `checked ${report.checked} · covered ${report.covered} · 신규 URL ${report.newUrls.length} · 미사용 규칙 ${report.unusedRules.length}`,
        "info"
      );
      renderNewUrls(report.newUrls);
      renderUnusedRules(report.unusedRules);
    } catch (err) {
      setMessage(diffSummaryEl, `비교 실패: ${String(err)}`);
    }
  })();
});

function renderNewUrls(records: URLRecord[]): void {
  const tbody = newUrlsTable.querySelector("tbody");
  if (!tbody) return;
  tbody.replaceChildren();
  newUrlsTable.style.display = records.length ? "" : "none";
  for (const r of records) {
    const tr = document.createElement("tr");
    tr.append(cell(r.url), cell(r.party ?? ""), cell(r.confidence ?? ""), cell(String(r.count)));
    tbody.appendChild(tr);
  }
}

function renderUnusedRules(rules: Rule[]): void {
  const tbody = unusedRulesTable.querySelector("tbody");
  if (!tbody) return;
  tbody.replaceChildren();
  unusedRulesTable.style.display = rules.length ? "" : "none";
  for (const r of rules) {
    const tr = document.createElement("tr");
    tr.append(cell(r.pattern), cell(r.party ?? ""), cell(String(r.count ?? 0)));
    tbody.appendChild(tr);
  }
}
