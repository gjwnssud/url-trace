#!/usr/bin/env bash
# Packages exactly what Chrome needs to run the extension into a zip ready
# for Chrome Web Store submission (or manual "load unpacked" distribution).
# Excludes node_modules, src/, scripts/, config files, and source maps —
# none of that is read by the browser at runtime.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

required=(
  "public/url-trace.wasm"
  "public/wasm_exec.js"
  "dist/background.js"
  "dist/popup.js"
  "dist/review.js"
)
for f in "${required[@]}"; do
  if [ ! -f "$f" ]; then
    echo "missing $f — run 'npm run build' first" >&2
    exit 1
  fi
done

version=$(node -p "require('./manifest.json').version")
out_dir="package"
out_zip="$out_dir/url-trace-extension-v${version}.zip"

rm -rf "$out_dir"
mkdir -p "$out_dir/stage"

cp manifest.json popup.html review.html styles.css "$out_dir/stage/"
cp -R icons "$out_dir/stage/icons"
mkdir -p "$out_dir/stage/dist" "$out_dir/stage/public"
cp dist/background.js dist/popup.js dist/review.js "$out_dir/stage/dist/"
cp public/url-trace.wasm public/wasm_exec.js "$out_dir/stage/public/"

(cd "$out_dir/stage" && zip -r -X "../../${out_zip}" . >/dev/null)
rm -rf "$out_dir/stage"

echo "wrote $out_zip"
unzip -l "$out_zip"
