// Bundles the extension's TypeScript into plain IIFE scripts under dist/, so
// the manifest and popup.html can reference them with no module-loader setup.
// Run via `npm run build:js` (or `npm run build` for wasm + js together).
import { build } from "esbuild";

const entryPoints = ["src/background.ts", "src/popup.ts", "src/review.ts"];

await build({
  entryPoints,
  bundle: true,
  outdir: "dist",
  format: "iife",
  target: "es2022",
  sourcemap: true,
  logLevel: "info",
});
