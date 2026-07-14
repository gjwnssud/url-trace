// Pure data-shaping helpers — converting the extension's raw capture buffer
// into url-trace's record shape, and rendering a Result as the same
// alternate formats the CLI supports (CSV, HAR). None of this is policy
// logic (normalization, dedup, classification, pattern suggestion all stay
// in Go/WASM, see wasm-host.ts) — it's just serialization, same as
// internal/output on the CLI side.
import type { CapturedRequest, Result, URLRecord } from "./types";

/**
 * Converts raw captures into one URLRecord per observation, matching the
 * convention every url-trace Source follows (internal/source/har.go,
 * browser.go): Count 1, both timestamps set to the observation time,
 * Sources stamped with this collector's name. Aggregation across duplicates
 * happens downstream in the WASM pipeline (pipeline.Aggregate), not here.
 */
export function toURLRecords(captured: CapturedRequest[]): URLRecord[] {
  return captured.map((c) => {
    const seen = new Date(c.timeStamp).toISOString();
    return {
      url: c.url,
      sources: ["extension"],
      firstSeen: seen,
      lastSeen: seen,
      count: 1,
    };
  });
}

/** Builds a minimal spec-valid HAR — url-trace's HAR source only reads these two fields. */
export function toHAR(captured: CapturedRequest[]): string {
  const har = {
    log: {
      version: "1.2",
      creator: { name: "url-trace-extension", version: "0.1.0" },
      entries: captured.map((c) => ({
        startedDateTime: new Date(c.timeStamp).toISOString(),
        request: { method: "GET", url: c.url },
      })),
    },
  };
  return JSON.stringify(har, null, 2);
}

/** Mirrors internal/output's CSV columns exactly (sources joined with ';', pattern suggestions omitted). */
export function toCSV(records: URLRecord[]): string {
  const header = "url,sources,party,confidence,first_seen,last_seen,count";
  const rows = records.map((r) =>
    [
      csvField(r.url),
      csvField(r.sources.join(";")),
      csvField(r.party ?? ""),
      csvField(r.confidence ?? ""),
      csvField(r.firstSeen),
      csvField(r.lastSeen),
      String(r.count),
    ].join(",")
  );
  return [header, ...rows].join("\n") + "\n";
}

function csvField(value: string): string {
  if (/[",\n]/.test(value)) {
    return '"' + value.replace(/"/g, '""') + '"';
  }
  return value;
}

/** Triggers a browser download of `content` as `filename` via a blob URL. */
export function download(filename: string, content: string, mimeType: string): void {
  const blob = new Blob([content], { type: mimeType });
  const url = URL.createObjectURL(blob);
  chrome.downloads
    .download({ url, filename, saveAs: true })
    .catch((err) => console.error(`url-trace: download of ${filename} failed`, err))
    .finally(() => {
      // Revoke after a delay so the download has time to actually start reading the blob.
      setTimeout(() => URL.revokeObjectURL(url), 30_000);
    });
}

/** Parses a comma-separated domains input into match patterns, defaulting a bare domain to *://host/*. */
export function parsePatterns(raw: string): string[] {
  return raw
    .split(",")
    .map((s) => s.trim())
    .filter(Boolean)
    .map((s) => (s.includes("://") ? s : `*://${s}/*`));
}

/**
 * True for the extension's own resource loads (chrome-extension://, chrome://)
 * — Chrome lets an extension observe these via webRequest regardless of
 * granted host permissions, so background.ts must filter them out itself or
 * simply opening the popup/review page while recording pollutes the capture
 * with the extension's own UI assets instead of only the target app's URLs.
 */
export function isOwnResourceURL(url: string): boolean {
  return url.startsWith("chrome-extension://") || url.startsWith("chrome://");
}

/** Best-effort hostname extraction from a match pattern like "https://app.example.com/*". */
export function hostnameOfPattern(pattern: string): string | null {
  try {
    // Strip the path/wildcard portion match patterns allow that URL() rejects.
    const withoutPath = pattern.replace(/^([a-z-]+:\/\/[^/]+).*$/i, "$1");
    return new URL(withoutPath).hostname || null;
  } catch {
    return null;
  }
}

/** host:port (lowercased) of a URL, or "" if unparseable — used to keep the crawler on-site. */
export function hostOf(rawURL: string): string {
  try {
    return new URL(rawURL).host.toLowerCase();
  } catch {
    return "";
  }
}

/**
 * Keeps only http(s) links for the extension's crawler, and matches
 * internal/source/browser.go's normalizeLink() exactly: SPA route fragments
 * (#/path, #!/path) are preserved as distinct pages, but a plain in-page
 * anchor (#section) has its fragment dropped so it dedupes to the page it
 * points within. Blindly stripping every fragment used to break crawling on
 * dashboards whose side nav is hash-routed (e.g. /dashboard#/users) — every
 * nav link collapsed to the same string as the seed page's URL and got
 * skipped as "already visited", so the crawler never left the seed page
 * (CLAUDE.md 재현율 우선 — silently dropping real navigation targets is
 * exactly what this project forbids).
 */
export function normalizeLink(href: string): string {
  try {
    const u = new URL(href);
    if (u.protocol !== "http:" && u.protocol !== "https:") return "";
    const fragment = u.hash.slice(1); // drop the leading '#'
    if (fragment !== "" && !fragment.startsWith("/") && !fragment.startsWith("!")) {
      u.hash = "";
    }
    return u.toString();
  } catch {
    return "";
  }
}
