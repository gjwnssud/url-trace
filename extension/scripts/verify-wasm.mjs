#!/usr/bin/env node
// Smoke-tests the built WASM bridge (public/url-trace.wasm) directly under
// Node, exercising all four exported functions with representative data.
// This guards the JSON boundary in wasm/main_js.go specifically — the
// individual internal/* packages already have their own Go unit tests, but
// nothing else exercises "does the bridge round-trip JSON correctly" (field
// casing, RFC3339 timestamps, omitempty, error propagation). Run after
// `npm run build:wasm`:
//
//   node scripts/verify-wasm.mjs
//
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const __dirname = path.dirname(fileURLToPath(import.meta.url));

// wasm_exec.js is a plain (non-module) script that sets globalThis.Go; it
// self-polyfills globalThis.fs/crypto/process so it works unmodified in
// Node, browsers, or a service worker. It can't be `require()`d here because
// extension/package.json sets "type": "module" (ESM) for the whole package,
// so every plain .js file under it is treated as an ES module by Node's
// resolver — even though it isn't one. Running it via vm.runInThisContext
// sidesteps module-type detection entirely and just executes it globally,
// exactly like a <script> tag would in the browser.
const wasmExecPath = path.join(__dirname, "..", "public", "wasm_exec.js");
vm.runInThisContext(fs.readFileSync(wasmExecPath, "utf8"), { filename: wasmExecPath });

async function loadBridge() {
  const go = new globalThis.Go();
  const wasmPath = path.join(__dirname, "..", "public", "url-trace.wasm");
  const bytes = fs.readFileSync(wasmPath);
  const { instance } = await WebAssembly.instantiate(bytes, go.importObject);
  // Not awaited: main_js.go registers globalThis.urltrace synchronously then
  // parks forever on select{} — see wasm-host.ts for the full explanation.
  void go.run(instance);
  if (!globalThis.urltrace) {
    throw new Error("globalThis.urltrace was not registered after go.run()");
  }
  return globalThis.urltrace;
}

function unwrap(raw) {
  const parsed = JSON.parse(raw);
  if (parsed && typeof parsed === "object" && typeof parsed.error === "string") {
    throw new Error(parsed.error);
  }
  return parsed;
}

const bridge = await loadBridge();

// --- process(): aggregate -> classify -> suggest patterns ---
const records = [
  { url: "https://example.com/api/users/1", sources: ["extension"], firstSeen: "2026-07-01T09:00:00Z", lastSeen: "2026-07-01T09:00:00Z", count: 1 },
  { url: "https://example.com/api/users/1", sources: ["extension"], firstSeen: "2026-07-01T09:05:00Z", lastSeen: "2026-07-01T09:05:00Z", count: 1 },
  { url: "https://example.com/api/users/2", sources: ["extension"], firstSeen: "2026-07-01T09:01:00Z", lastSeen: "2026-07-01T09:01:00Z", count: 1 },
  { url: "https://example.com/api/users/3", sources: ["extension"], firstSeen: "2026-07-01T09:02:00Z", lastSeen: "2026-07-01T09:02:00Z", count: 1 },
  { url: "https://cdn.other.com/lib.js", sources: ["extension"], firstSeen: "2026-07-01T09:03:00Z", lastSeen: "2026-07-01T09:03:00Z", count: 1 },
];
const { result, skipped } = unwrap(bridge.process(JSON.stringify(records), JSON.stringify(["example.com"])));

assert.equal(skipped, 0, "expected no skipped records");
assert.equal(result.urls.length, 4, "expected 4 distinct URLs after aggregation");

const user1 = result.urls.find((u) => u.url === "https://example.com/api/users/1");
assert.equal(user1.count, 2, "duplicate observations must aggregate into count=2");
assert.equal(user1.party, "first-party");

const cdn = result.urls.find((u) => u.url === "https://cdn.other.com/lib.js");
assert.equal(cdn.party, "third-party");

assert.equal(result.patternSuggestions.length, 1, "expected one wildcard suggestion for /users/{id}");
const suggestion = result.patternSuggestions[0];
assert.equal(suggestion.pattern, "https://example.com/api/users/*");
assert.equal(suggestion.distinctValues, 3);
assert.equal(suggestion.totalCount, 4); // 2 (users/1) + 1 (users/2) + 1 (users/3)

console.log("process(): OK —", result.urls.length, "urls,", result.patternSuggestions.length, "suggestion(s)");

// --- buildPolicy(): human-approved pattern collapses to one wildcard rule ---
const { policy, warnings: buildWarnings } = unwrap(
  bridge.buildPolicy(
    JSON.stringify(result),
    JSON.stringify({ minConfidence: "low", acceptPatterns: ["https://example.com/api/users/*"] })
  )
);
assert.equal(buildWarnings.length, 0, "accepted pattern should match observed URLs with no warnings");
assert.equal(policy.version, 1);

const wildcardRule = policy.rules.find((r) => r.pattern === "https://example.com/api/users/*");
assert.ok(wildcardRule, "expected the accepted wildcard pattern to become a rule");
assert.equal(wildcardRule.count, 4, "wildcard rule must fold all 3 covered URLs' counts");

const exactRule = policy.rules.find((r) => r.pattern === "https://cdn.other.com/lib.js");
assert.ok(exactRule, "un-accepted URLs must still become exact rules");

console.log("buildPolicy(): OK —", policy.rules.length, "rule(s)");

// --- diff(): same result against its own policy is fully covered; a new URL is not ---
const covering = unwrap(bridge.diff(JSON.stringify(policy), JSON.stringify(result)));
assert.equal(covering.checked, 4);
assert.equal(covering.covered, 4);
assert.equal(covering.newUrls.length, 0);

const withNewURL = {
  urls: [...result.urls, { url: "https://example.com/api/orders/9", sources: ["extension"], firstSeen: "2026-07-01T09:04:00Z", lastSeen: "2026-07-01T09:04:00Z", count: 1 }],
  patternSuggestions: [],
};
const withNew = unwrap(bridge.diff(JSON.stringify(policy), JSON.stringify(withNewURL)));
assert.equal(withNew.newUrls.length, 1, "an unrecognized URL must surface as newUrls, not be silently dropped");
assert.equal(withNew.newUrls[0].url, "https://example.com/api/orders/9");

console.log("diff(): OK —", withNew.newUrls.length, "new URL(s) detected as expected");

// --- exportSQL(): config-driven column mapping ---
const sqlConfig = {
  table: "URL_ALLOWLIST",
  columns: [
    { name: "pattern", value: "{pattern}", type: "string" },
    { name: "hit_count", value: "{count}", type: "number" },
  ],
};
const { sql, warnings: sqlWarnings } = unwrap(bridge.exportSQL(JSON.stringify(policy), JSON.stringify(sqlConfig), Date.now()));
assert.equal(sqlWarnings.length, 0);
const insertCount = (sql.match(/INSERT INTO URL_ALLOWLIST/g) ?? []).length;
assert.equal(insertCount, policy.rules.length, "one INSERT per policy rule");
assert.ok(sql.includes("'https://example.com/api/users/*'"), "wildcard rule's pattern must appear verbatim in SQL");

console.log("exportSQL(): OK —", insertCount, "INSERT statement(s)");

console.log("\nAll WASM bridge checks passed.");
