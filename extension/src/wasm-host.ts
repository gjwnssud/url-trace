// Loads url-trace.wasm and exposes typed wrappers around the four functions
// wasm/main_js.go registers on globalThis.urltrace. This is the *only* file
// that should know the bridge speaks JSON-in/JSON-out strings — everywhere
// else in the extension works with plain TS objects.
import type { BuildOptions, DiffReport, Policy, Result, SqlConfig, URLRecord } from "./types";

// wasm_exec.js (copied from $(go env GOROOT)/lib/wasm/wasm_exec.js by
// `npm run build:wasm`) is loaded as a classic <script> before this bundle in
// popup.html, so it defines a global `Go` constructor rather than an ES
// export. See https://github.com/golang/go/wiki/WebAssembly for the pattern
// this mirrors.
interface GoInstance {
  importObject: WebAssembly.Imports;
  run(instance: WebAssembly.Instance): Promise<void>;
}
declare const Go: { new (): GoInstance };

interface UrlTraceBridge {
  process(recordsJSON: string, primaryDomainsJSON: string): string;
  buildPolicy(resultJSON: string, optsJSON: string): string;
  diff(policyJSON: string, resultJSON: string): string;
  exportSQL(policyJSON: string, configJSON: string, nowMs: number): string;
}

let loaded: Promise<void> | null = null;

/**
 * Instantiates the WASM module exactly once. main_js.go's main() registers
 * globalThis.urltrace synchronously and then parks forever on `select{}` to
 * keep the instance (and its registered js.Func callbacks) alive — so
 * go.run(instance) must NOT be awaited, it would never resolve. By the time
 * the (un-awaited) call to go.run() returns control to us, urltrace is
 * already registered: Go's js/wasm runtime executes main() synchronously up
 * to its first blocking point before yielding back to the JS event loop.
 */
function ensureLoaded(): Promise<void> {
  if (!loaded) {
    loaded = (async () => {
      const go = new Go();
      const resp = await fetch(chrome.runtime.getURL("public/url-trace.wasm"));
      const bytes = await resp.arrayBuffer();
      const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
      void go.run(instance).catch((err: unknown) => {
        console.error("url-trace wasm instance exited unexpectedly:", err);
      });
    })();
  }
  return loaded;
}

function bridge(): UrlTraceBridge {
  const b = (globalThis as unknown as { urltrace?: UrlTraceBridge }).urltrace;
  if (!b) {
    throw new Error("url-trace wasm bridge not loaded yet — call ensureLoaded() first");
  }
  return b;
}

/** Parses a bridge response, throwing if the Go side reported {"error": ...}. */
function unwrap<T>(raw: string): T {
  const parsed = JSON.parse(raw) as T & { error?: unknown };
  if (parsed && typeof parsed === "object" && typeof (parsed as { error?: unknown }).error === "string") {
    throw new Error((parsed as { error: string }).error);
  }
  return parsed;
}

/** Runs the full extract pipeline: aggregate -> classify -> suggest patterns. */
export async function process(
  records: URLRecord[],
  primaryDomains: string[]
): Promise<{ result: Result; skipped: number }> {
  await ensureLoaded();
  return unwrap(bridge().process(JSON.stringify(records), JSON.stringify(primaryDomains)));
}

/** Converts an extraction result into a policy (only accepted patterns collapse to wildcards). */
export async function buildPolicy(
  result: Result,
  opts: BuildOptions
): Promise<{ policy: Policy; warnings: string[] }> {
  await ensureLoaded();
  return unwrap(bridge().buildPolicy(JSON.stringify(result), JSON.stringify(opts)));
}

/** Checks an extraction result against an existing policy. */
export async function diff(policy: Policy, result: Result): Promise<DiffReport> {
  await ensureLoaded();
  return unwrap(bridge().diff(JSON.stringify(policy), JSON.stringify(result)));
}

/** Renders a policy as INSERT statements using a user-supplied table mapping. */
export async function exportSQL(
  policy: Policy,
  config: SqlConfig,
  now: Date
): Promise<{ sql: string; warnings: string[] }> {
  await ensureLoaded();
  return unwrap(bridge().exportSQL(JSON.stringify(policy), JSON.stringify(config), now.getTime()));
}
