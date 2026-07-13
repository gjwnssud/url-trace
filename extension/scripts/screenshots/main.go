// Command screenshots drives the built extension in a real Chrome to produce
// genuine Chrome Web Store listing screenshots — not mockups. Requires
// `npm run build` to have already produced extension/dist and
// extension/public/url-trace.wasm. Run from the extension/ directory:
//
//	go run ./scripts/screenshots
//
// It never touches the real extension/manifest.json: to populate the popup
// and review page with realistic data without hitting Chrome's native
// "allow this site?" permission bubble (which isn't a page-DOM element
// chromedp can drive), it copies the built extension into a temp directory
// and adds a mandatory host_permissions entry scoped to a local test server
// there. Unpacked/dev-mode extensions with declared host_permissions are
// auto-granted, no prompt — so chrome.permissions.request() in popup.ts
// simply resolves true instantly, exercising the real code path with no
// synthetic backdoor added to the shipped extension.
//
// IMPORTANT: official Google Chrome (the branded build) silently ignores
// --load-extension/--disable-extensions-except — it logs "...is not allowed
// in Google Chrome, ignoring." and this tool then can't find the extension.
// This is Google's own hardening of the branded binary, not a bug here.
// Chromium (the open-source, unbranded build) still honors these flags. Point
// CHROME_PATH at a Chromium binary to run this tool, e.g.:
//
//	brew install --cask chromium
//	CHROME_PATH="/Applications/Chromium.app/Contents/MacOS/Chromium" go run ./scripts/screenshots
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// testPageHTML fires several fetches designed to exercise both aggregation
// (users/1 hit twice) and pattern suggestion (three distinct numeric IDs).
const testPageHTML = `<!doctype html><html><body><h1>url-trace test page</h1><script>
fetch('/api/users/1');
fetch('/api/users/2');
fetch('/api/users/3');
fetch('/api/users/1');
fetch('/api/orders/9');
</script></body></html>`

// extensionAssets are the only entries a Chrome extension actually needs at
// load time; node_modules/src/tsconfig etc. are irrelevant to the browser
// and are skipped so the temp copy stays fast and small.
var extensionAssets = []string{"popup.html", "review.html", "styles.css", "icons", "dist", "public"}

func main() {
	extDir, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	requireBuilt(extDir)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			_, _ = io.WriteString(w, testPageHTML)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	tmpExtDir, err := os.MkdirTemp("", "url-trace-ext-screenshot-*")
	if err != nil {
		log.Fatal(err)
	}
	if os.Getenv("URLTRACE_KEEP_TMP") == "" {
		defer os.RemoveAll(tmpExtDir)
	} else {
		fmt.Println("keeping temp extension dir:", tmpExtDir)
	}

	if err := stageExtension(extDir, tmpExtDir, srv.URL+"/*"); err != nil {
		log.Fatalf("staging extension copy: %v", err)
	}

	outDir := filepath.Join(extDir, "store-assets", "screenshots")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		log.Fatal(err)
	}

	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", "new"), // legacy headless can't load extensions
		chromedp.Flag("disable-extensions-except", tmpExtDir),
		chromedp.Flag("load-extension", tmpExtDir),
		chromedp.WindowSize(1000, 900),
	)
	if p := os.Getenv("CHROME_PATH"); p != "" {
		allocOpts = append(allocOpts, chromedp.ExecPath(p))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), allocOpts...)
	defer cancelAlloc()

	ctx, cancelCtx := chromedp.NewContext(allocCtx)
	defer cancelCtx()

	if err := chromedp.Run(ctx); err != nil {
		log.Fatalf("start browser: %v", err)
	}

	extID, err := findExtensionID(ctx)
	if err != nil {
		log.Fatalf("%v (did you run `npm run build` first?)", err)
	}
	fmt.Println("loaded extension", extID)

	popupURL := fmt.Sprintf("chrome-extension://%s/popup.html", extID)
	reviewURL := fmt.Sprintf("chrome-extension://%s/review.html", extID)

	var statusText string
	runStep(ctx, "start recording", 15*time.Second,
		chromedp.Navigate(popupURL),
		chromedp.WaitVisible(`#domains`, chromedp.ByID),
		chromedp.SetValue(`#domains`, srv.URL+"/*", chromedp.ByID),
		chromedp.Click(`#startBtn`, chromedp.ByID),
		chromedp.Sleep(300*time.Millisecond), // permission grant + start message roundtrip
		chromedp.Text(`#statusText`, &statusText, chromedp.ByID),
	)
	fmt.Println("popup status after start:", statusText)

	// Generate real captured traffic by loading the test page in a second tab.
	runStep(ctx, "load test page", 15*time.Second,
		chromedp.Navigate(srv.URL),
		chromedp.Sleep(300*time.Millisecond),
	)

	runStep(ctx, "reload popup", 15*time.Second,
		chromedp.Navigate(popupURL),
		chromedp.WaitVisible(`#count`, chromedp.ByID),
		chromedp.Sleep(1200*time.Millisecond), // popup polls status every 1s
	)
	if err := screenshotElement(ctx, "body", filepath.Join(outDir, "1-popup.png")); err != nil {
		log.Fatal(err)
	}
	fmt.Println("captured 1-popup.png")

	runStep(ctx, "drive review page", 20*time.Second,
		chromedp.Navigate(reviewURL),
		chromedp.WaitVisible(`#loadCaptureBtn`, chromedp.ByID),
		chromedp.Click(`#loadCaptureBtn`, chromedp.ByID),
		chromedp.Sleep(1500*time.Millisecond), // first WASM instantiate + process()
		chromedp.Click(`.pattern-checkbox`, chromedp.ByQuery),
		chromedp.Click(`#buildPolicyBtn`, chromedp.ByID),
		chromedp.Sleep(500*time.Millisecond),
	)
	if err := screenshotElement(ctx, "body", filepath.Join(outDir, "2-review.png")); err != nil {
		log.Fatal(err)
	}
	fmt.Println("captured 2-review.png")

	fmt.Println("wrote screenshots to", outDir)
}

// runStep runs actions under a bounded per-step context so a stuck step fails
// fast with a clear name instead of hanging the whole run indefinitely.
func runStep(ctx context.Context, name string, timeout time.Duration, actions ...chromedp.Action) {
	fmt.Println("step:", name)
	stepCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := chromedp.Run(stepCtx, actions...); err != nil {
		log.Fatalf("%s: %v", name, err)
	}
}

func requireBuilt(extDir string) {
	must := []string{
		filepath.Join(extDir, "public", "url-trace.wasm"),
		filepath.Join(extDir, "public", "wasm_exec.js"),
		filepath.Join(extDir, "dist", "popup.js"),
		filepath.Join(extDir, "dist", "review.js"),
		filepath.Join(extDir, "dist", "background.js"),
	}
	for _, p := range must {
		if _, err := os.Stat(p); err != nil {
			log.Fatalf("missing %s — run `npm run build` first", p)
		}
	}
}

// stageExtension copies the runtime assets into dst and patches manifest.json
// with a mandatory host_permissions entry for originPattern, so the popup's
// chrome.permissions.request() resolves instantly with no native UI prompt.
// The real, committed manifest.json (optional_host_permissions only) is never
// modified.
func stageExtension(src, dst, originPattern string) error {
	for _, name := range extensionAssets {
		if err := copyPath(filepath.Join(src, name), filepath.Join(dst, name)); err != nil {
			return fmt.Errorf("copy %s: %w", name, err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(src, "manifest.json"))
	if err != nil {
		return err
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	manifest["host_permissions"] = []string{originPattern}
	patched, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dst, "manifest.json"), patched, 0o644)
}

func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst, info.Mode())
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

// findExtensionID looks specifically for OUR service worker
// (chrome-extension://<id>/dist/background.js, matching manifest.json's
// background.service_worker) rather than any chrome-extension:// target —
// Chrome's own bundled component extensions (PDF Viewer, Google Hangouts,
// Google Network Speech, ...) also show up as service_worker/background_page
// targets on every launch and would otherwise be matched by mistake.
func findExtensionID(ctx context.Context) (string, error) {
	const suffix = "/dist/background.js"
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		infos, err := chromedp.Targets(ctx)
		if err == nil {
			for _, ti := range infos {
				if strings.HasPrefix(ti.URL, "chrome-extension://") && strings.HasSuffix(ti.URL, suffix) {
					rest := strings.TrimPrefix(ti.URL, "chrome-extension://")
					return strings.SplitN(rest, "/", 2)[0], nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("no chrome-extension:// target appeared (extension failed to load)")
}

func screenshotElement(ctx context.Context, sel, path string) error {
	var buf []byte
	if err := chromedp.Run(ctx, chromedp.Screenshot(sel, &buf, chromedp.ByQuery)); err != nil {
		return fmt.Errorf("screenshot %s: %w", path, err)
	}
	return os.WriteFile(path, buf, 0o644)
}
