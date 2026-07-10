package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestBrowserSourceFetch(t *testing.T) {
	requireChrome(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><head></head><body>
<script>fetch('/api/data');</script>
<img src="/img/logo.png">
</body></html>`)
	})
	mux.HandleFunc("/api/data", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"ok":true}`)
	})
	mux.HandleFunc("/img/logo.png", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47}) // PNG magic bytes
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := NewBrowserSource(srv.URL, 1*time.Second, 20*time.Second)
	records := drain(t, src)

	got := make(map[string]bool)
	for _, r := range records {
		got[r.URL] = true
		if len(r.Sources) != 1 || r.Sources[0] != "browser" {
			t.Errorf("record sources = %v, want [browser]", r.Sources)
		}
	}

	// The browser must surface not just the page load but the fetch() call and
	// the image subresource — the dynamic/subresource requests a static crawl
	// would miss but a whitelist must include.
	for _, want := range []string{srv.URL + "/", srv.URL + "/api/data", srv.URL + "/img/logo.png"} {
		if !got[want] {
			t.Errorf("missing captured URL %q; captured: %v", want, got)
		}
	}
}

// requireChrome skips the test when no Chrome/Chromium is available, so the
// suite stays green in environments without a browser.
func requireChrome(t *testing.T) {
	t.Helper()
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser"} {
		if _, err := exec.LookPath(name); err == nil {
			return
		}
	}
	for _, path := range []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
	} {
		if _, err := os.Stat(path); err == nil {
			return
		}
	}
	t.Skip("no Chrome/Chromium found; skipping browser integration test")
}
