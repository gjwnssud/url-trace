package source

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/gjwnssud/url-trace/internal/model"
)

// BrowserSource drives one or more entry URLs in a headless browser and records
// every network request they make — the page load, XHR/fetch calls, and
// third-party resources (CDNs, fonts, analytics). A static crawl misses these
// dynamic and cross-origin requests, yet they are exactly what a whitelist must
// allow, so this is the primary active-collection source.
//
// For single-page apps, pass several entry URLs (the app's routes/deep links):
// each is loaded fresh so its client-side route bootstraps and fires that
// screen's API calls. With Depth > 0 it also follows same-host links —
// including SPA hash routes (#/path) — breadth-first. Cookies and Headers
// inject an authenticated session so login-gated pages are reachable without a
// person driving the browser.
type BrowserSource struct {
	URLs    []string
	Wait    time.Duration // idle time after load to let late XHR/fetch fire
	Timeout time.Duration // hard cap on the whole capture (across all pages)
	// InsecureTLS makes the browser accept invalid certificates (self-signed,
	// internal CA). Needed for internal validation environments; the capture
	// only reads URLs, so the usual MITM downgrade concern is limited to the
	// capture session itself.
	InsecureTLS bool
	// Depth is how many link hops to follow from each entry URL. 0 visits only
	// the entry URLs themselves.
	Depth int
	// MaxPages caps how many pages are visited regardless of Depth, so a large
	// site cannot run unbounded. Reaching it is reported, never silent.
	MaxPages int
	// Cookies ("name=value") and Headers ("Key: Value") inject an authenticated
	// session into every request.
	Cookies []string
	Headers []string
	// Headful opens a visible browser window and pauses for the operator to log
	// in manually before capturing. This avoids the duplicate-login session
	// expiry that injecting a copied cookie can trigger: the capture browser
	// holds the only session. Requires a display; not usable in headless CI.
	Headful bool
}

// NewBrowserSource creates a source that captures the requests made while
// loading the given entry URLs.
func NewBrowserSource(urls []string, wait, timeout time.Duration) *BrowserSource {
	return &BrowserSource{URLs: urls, Wait: wait, Timeout: timeout}
}

// Name identifies this source in audit metadata.
func (s *BrowserSource) Name() string { return "browser" }

// Fetch launches headless Chrome and crawls from the entry URLs, emitting a
// record for every request observed. Requests are buffered during the run and
// streamed out only afterward, so the browser's event loop is never blocked on
// a slow consumer. Hitting the configured timeout is a normal stop condition:
// whatever was captured so far is still emitted rather than discarded.
func (s *BrowserSource) Fetch(ctx context.Context, out chan<- model.URLRecord) error {
	if len(s.URLs) == 0 {
		return errors.New("browser source: no entry URLs")
	}

	allocOpts := chromedp.DefaultExecAllocatorOptions[:]
	if s.InsecureTLS {
		allocOpts = append(allocOpts, chromedp.Flag("ignore-certificate-errors", true))
	}
	if s.Headful {
		// Override the default headless flag so a real window opens for login.
		allocOpts = append(allocOpts, chromedp.Flag("headless", false))
	}
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()

	var (
		mu      sync.Mutex
		records []model.URLRecord
	)
	chromedp.ListenTarget(browserCtx, func(ev interface{}) {
		e, ok := ev.(*network.EventRequestWillBeSent)
		if !ok {
			return
		}
		now := time.Now()
		mu.Lock()
		records = append(records, model.URLRecord{
			URL:       e.Request.URL,
			Sources:   []string{s.Name()},
			FirstSeen: now,
			LastSeen:  now,
			Count:     1,
		})
		mu.Unlock()
	})

	setup, err := s.setupActions()
	if err != nil {
		return err
	}
	// Session setup and the manual-login pause run without the capture timeout:
	// waiting for a person to log in must not eat into the crawl budget.
	if err := chromedp.Run(browserCtx, setup...); err != nil {
		return fmt.Errorf("browser session setup: %w", err)
	}
	if s.Headful {
		if err := s.manualLogin(browserCtx); err != nil {
			return err
		}
	}

	runCtx, cancelRun := context.WithTimeout(browserCtx, s.Timeout)
	defer cancelRun()

	crawlErr := s.crawl(runCtx)
	// Our own timeout firing means "capture window closed", not failure — keep
	// the URLs gathered so far. A parent cancellation (Ctrl-C) still surfaces.
	if crawlErr != nil && !errors.Is(crawlErr, context.DeadlineExceeded) {
		return crawlErr
	}

	mu.Lock()
	captured := records
	mu.Unlock()

	for _, r := range captured {
		select {
		case out <- r:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// manualLogin opens the first entry URL in the visible window and blocks until
// the operator presses Enter (or EOF/cancellation), so they can log in by hand.
// Requests made during login are captured too, which is fine — login endpoints
// are legitimate whitelist entries.
func (s *BrowserSource) manualLogin(ctx context.Context) error {
	if err := chromedp.Run(ctx, chromedp.Navigate(s.URLs[0])); err != nil {
		return fmt.Errorf("open login page: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\n[headful] Log in and get the app ready in the opened window,\n"+
		"          then press Enter here to start capturing... ")
	if err := waitForSignal(ctx, os.Stdin); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "capturing.")
	return nil
}

// waitForSignal returns when a line is read from r (Enter), r reaches EOF (so a
// piped/empty stdin proceeds immediately), or ctx is cancelled.
func waitForSignal(ctx context.Context, r io.Reader) error {
	done := make(chan struct{})
	go func() {
		_, _ = bufio.NewReader(r).ReadString('\n')
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// setupActions enables the network domain and injects the authenticated session
// (extra headers, cookies) before any navigation. Cookies are set for every
// distinct entry host so all seeds are authenticated.
func (s *BrowserSource) setupActions() ([]chromedp.Action, error) {
	actions := []chromedp.Action{network.Enable()}

	if len(s.Headers) > 0 {
		headers := network.Headers{}
		for _, h := range s.Headers {
			name, value, ok := strings.Cut(h, ":")
			if !ok {
				return nil, fmt.Errorf("invalid --header %q (want \"Key: Value\")", h)
			}
			headers[strings.TrimSpace(name)] = strings.TrimSpace(value)
		}
		actions = append(actions, network.SetExtraHTTPHeaders(headers))
	}

	cookies, err := parseCookies(s.Cookies)
	if err != nil {
		return nil, err
	}
	for _, seed := range distinctHosts(s.URLs) {
		for _, c := range cookies {
			actions = append(actions, network.SetCookie(c.name, c.value).WithURL(seed))
		}
	}
	return actions, nil
}

type cookie struct{ name, value string }

// parseCookies expands the --cookie inputs into individual cookies. Each input
// may hold several cookies separated by ';' or ',', so a whole Cookie header
// copied from the browser (or a comma-joined list) works as one flag — those
// separators never appear unencoded in a cookie name or value.
func parseCookies(raw []string) ([]cookie, error) {
	splitSep := func(r rune) bool { return r == ';' || r == ',' }

	var out []cookie
	for _, entry := range raw {
		for _, part := range strings.FieldsFunc(entry, splitSep) {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			name, value, ok := strings.Cut(part, "=")
			if !ok {
				return nil, fmt.Errorf("invalid --cookie %q (want \"name=value\")", part)
			}
			out = append(out, cookie{strings.TrimSpace(name), strings.TrimSpace(value)})
		}
	}
	return out, nil
}

// crawl visits every entry URL and, up to Depth hops and MaxPages pages, follows
// same-host links breadth-first.
func (s *BrowserSource) crawl(ctx context.Context) error {
	hosts := map[string]bool{}
	visited := map[string]bool{}
	var queue []crawlItem

	for _, seed := range s.URLs {
		u, err := url.Parse(seed)
		if err != nil {
			return fmt.Errorf("invalid --url %q: %w", seed, err)
		}
		hosts[strings.ToLower(u.Host)] = true
		if !visited[seed] {
			visited[seed] = true
			queue = append(queue, crawlItem{url: seed, depth: 0})
		}
	}

	pages := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if s.MaxPages > 0 && pages >= s.MaxPages {
			fmt.Fprintf(os.Stderr, "browser capture stopped at --max-pages=%d; %d link(s) left unvisited\n",
				s.MaxPages, len(queue)+1)
			break
		}
		pages++

		var hrefs []string
		actions := []chromedp.Action{
			// Bounce through about:blank so navigating to a URL that differs
			// only by a hash route still triggers a full load — otherwise a
			// same-document hash change fires no load event and Navigate hangs.
			chromedp.Navigate("about:blank"),
			chromedp.Navigate(cur.url),
			chromedp.Sleep(s.Wait),
		}
		if cur.depth < s.Depth {
			actions = append(actions, chromedp.Evaluate(linkExtractJS, &hrefs))
		}
		if err := chromedp.Run(ctx, actions...); err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return err // stop the whole crawl; caller keeps captured records
			}
			// A single page failing (e.g. a broken link) must not abort the
			// crawl — note it and move on.
			fmt.Fprintf(os.Stderr, "browser capture of %s failed: %v\n", cur.url, err)
			continue
		}

		for _, href := range hrefs {
			next := normalizeLink(href)
			if next == "" || visited[next] || !hosts[hostOf(next)] {
				continue
			}
			visited[next] = true
			queue = append(queue, crawlItem{url: next, depth: cur.depth + 1})
		}
	}
	return nil
}

type crawlItem struct {
	url   string
	depth int
}

// linkExtractJS returns every anchor's resolved absolute href.
const linkExtractJS = `Array.from(document.querySelectorAll('a[href]')).map(a => a.href)`

// normalizeLink keeps only http(s) links. SPA hash routes (#/path, #!/path) are
// preserved as distinct pages; plain in-page anchors (#section), which do not
// change the view, have their fragment dropped so they dedupe to one page.
func normalizeLink(href string) string {
	u, err := url.Parse(href)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	if u.Fragment != "" && !strings.HasPrefix(u.Fragment, "/") && !strings.HasPrefix(u.Fragment, "!") {
		u.Fragment = ""
	}
	return u.String()
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// distinctHosts returns one representative URL per distinct host, preserving
// order, so per-host setup work runs once each.
func distinctHosts(urls []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urls {
		h := hostOf(u)
		if seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, u)
	}
	return out
}
