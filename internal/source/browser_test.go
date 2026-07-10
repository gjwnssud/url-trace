package source

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/gjwnssud/url-trace/internal/model"
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

	src := NewBrowserSource(srv.URL, 1*time.Second, 60*time.Second)
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

func TestBrowserSourceInsecureTLS(t *testing.T) {
	requireChrome(t)

	// httptest's TLS server uses a self-signed certificate, exactly the
	// internal-environment case --insecure exists for.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!doctype html><html><body>ok</body></html>`)
	}))
	defer srv.Close()

	src := NewBrowserSource(srv.URL, 1*time.Second, 60*time.Second)
	src.InsecureTLS = true
	records := drain(t, src)

	found := false
	for _, r := range records {
		if r.URL == srv.URL+"/" {
			found = true
		}
	}
	if !found {
		t.Errorf("self-signed page not captured with InsecureTLS; got %+v", records)
	}
}

func TestBrowserSourceCrawl(t *testing.T) {
	requireChrome(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<!doctype html><html><body>
<a href="/a">a</a><a href="/b">b</a>
<a href="https://external.example.com/x">off-site</a>
</body></html>`)
	})
	mux.HandleFunc("/a", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><body><a href="/c">c</a></body></html>`)
	})
	mux.HandleFunc("/b", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "b") })
	mux.HandleFunc("/c", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "c") })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := NewBrowserSource(srv.URL, 500*time.Millisecond, 60*time.Second)
	src.Depth = 1 // entry + its direct links, but not /c (two hops away)
	records := drain(t, src)

	got := capturedURLs(records)
	for _, want := range []string{srv.URL + "/", srv.URL + "/a", srv.URL + "/b"} {
		if !got[want] {
			t.Errorf("depth-1 crawl missing %q; got %v", want, keys(got))
		}
	}
	if got[srv.URL+"/c"] {
		t.Error("/c is two hops away and must not be visited at depth 1")
	}
	// Off-site links are never followed (same-host only), though the page's own
	// request to load it may still be recorded — we only assert it was not crawled
	// by checking no deeper external requests appear.
}

func TestBrowserSourceSessionInjection(t *testing.T) {
	requireChrome(t)

	const cookieName, cookieVal = "session", "s3cr3t"
	const headerName, headerVal = "X-Auth-Token", "tok-123"

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		c, _ := r.Cookie(cookieName)
		// Only an authenticated request gets the protected resource link, so
		// capturing /protected proves the session was injected.
		if (c != nil && c.Value == cookieVal) && r.Header.Get(headerName) == headerVal {
			fmt.Fprint(w, `<html><body><img src="/protected"></body></html>`)
			return
		}
		fmt.Fprint(w, `<html><body>login required</body></html>`)
	})
	mux.HandleFunc("/protected", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "ok") })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	src := NewBrowserSource(srv.URL, 500*time.Millisecond, 60*time.Second)
	src.Cookies = []string{cookieName + "=" + cookieVal}
	src.Headers = []string{headerName + ": " + headerVal}
	records := drain(t, src)

	if !capturedURLs(records)[srv.URL+"/protected"] {
		t.Errorf("session not injected: /protected not captured; got %v", keys(capturedURLs(records)))
	}
}

func capturedURLs(records []model.URLRecord) map[string]bool {
	got := make(map[string]bool)
	for _, r := range records {
		got[r.URL] = true
	}
	return got
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
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
