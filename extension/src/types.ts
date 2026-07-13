// TypeScript mirrors of the Go JSON shapes in internal/model, internal/policy,
// and internal/sqlexport. Keep these in sync with those packages by hand —
// there is no code generation step; the WASM bridge (wasm/main_js.go) is the
// single source of truth for what these shapes actually mean.

/** Mirrors internal/model.URLRecord. Timestamps are RFC3339 strings. */
export interface URLRecord {
  url: string;
  sources: string[];
  firstSeen: string;
  lastSeen: string;
  count: number;
  party?: string;
  confidence?: string;
}

/** Mirrors internal/model.PatternSuggestion. */
export interface PatternSuggestion {
  pattern: string;
  distinctValues: number;
  totalCount: number;
  examples: string[];
}

/** Mirrors internal/model.Result — the extract pipeline's output. */
export interface Result {
  urls: URLRecord[];
  patternSuggestions: PatternSuggestion[];
}

/** Mirrors internal/policy.Rule. */
export interface Rule {
  pattern: string;
  party?: string;
  confidence?: string;
  sources?: string[];
  count?: number;
  firstSeen?: string;
  lastSeen?: string;
}

/** Mirrors internal/policy.Policy. */
export interface Policy {
  version: number;
  rules: Rule[];
}

/**
 * Mirrors internal/policy.BuildOptions. That Go struct has no json tags, so
 * encoding/json's case-insensitive field match accepts these camelCase keys
 * against the Go PascalCase fields (MinConfidence, Party, AcceptPatterns).
 */
export interface BuildOptions {
  minConfidence?: string;
  party?: string;
  acceptPatterns?: string[];
}

/** Mirrors internal/policy.Report (the diff result). */
export interface DiffReport {
  newUrls: URLRecord[];
  unusedRules: Rule[];
  checked: number;
  covered: number;
}

/** Mirrors internal/sqlexport.Column. */
export interface SqlColumn {
  name: string;
  value: string;
  type?: string;
  maxLength?: number;
}

/** Mirrors internal/sqlexport.Config. */
export interface SqlConfig {
  table: string;
  columns: SqlColumn[];
}

// String enum values — mirrors internal/model's Party*/Confidence* constants.
export const PARTY_FIRST = "first-party";
export const PARTY_THIRD = "third-party";
export const PARTY_UNKNOWN = "unknown";

export const CONFIDENCE_LOW = "low";
export const CONFIDENCE_MEDIUM = "medium";
export const CONFIDENCE_HIGH = "high";

/**
 * One raw observation captured by background.ts before it is aggregated by
 * the WASM pipeline — one per network request the extension observed.
 */
export interface CapturedRequest {
  url: string;
  /** epoch milliseconds, from chrome.webRequest's details.timeStamp */
  timeStamp: number;
}
