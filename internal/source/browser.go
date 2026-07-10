package source

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/gjwnssud/url-trace/internal/model"
)

// BrowserSource drives a target URL in a headless browser and records every
// network request it makes — the page load, XHR/fetch calls, and third-party
// resources (CDNs, fonts, analytics). A static crawl misses these dynamic and
// cross-origin requests, yet they are exactly what a whitelist must allow, so
// this is the primary active-collection source.
//
// With Depth > 0 it follows same-host links breadth-first, so pages behind the
// entry point are discovered automatically instead of only the ones a human
// clicks. Cookies and Headers inject an authenticated session, so pages that
// require login are reachable without a person driving the browser.
type BrowserSource struct {
	URL     string
	Wait    time.Duration // idle time after load to let late XHR/fetch fire
	Timeout time.Duration // hard cap on the whole capture (across all pages)
	// InsecureTLS makes the browser accept invalid certificates (self-signed,
	// internal CA). Needed for internal validation environments; the capture
	// only reads URLs, so the usual MITM downgrade concern is limited to the
	// capture session itself.
	InsecureTLS bool
	// Depth is how many link hops to follow from the entry URL. 0 visits only
	// the entry URL (single-page capture).
	Depth int
	// MaxPages caps how many pages are visited regardless of Depth, so a large
	// site cannot run unbounded. Reaching it is reported, never silent.
	MaxPages int
	// Cookies ("name=value") and Headers ("Key: Value") inject an authenticated
	// session into every request.
	Cookies []string
	Headers []string
}

// NewBrowserSource creates a source that captures the requests made while
// loading url.
func NewBrowserSource(url string, wait, timeout time.Duration) *BrowserSource {
	return &BrowserSource{URL: url, Wait: wait, Timeout: timeout}
}

// Name identifies this source in audit metadata.
func (s *BrowserSource) Name() string { return "browser" }

// Fetch launches headless Chrome and crawls from the entry URL, emitting a
// record for every request observed. Requests are buffered during the run and
// streamed out only afterward, so the browser's event loop is never blocked on
// a slow consumer. Hitting the configured timeout is a normal stop condition:
// whatever was captured so far is still emitted rather than discarded.
func (s *BrowserSource) Fetch(ctx context.Context, out chan<- model.URLRecord) error {
	allocOpts := chromedp.DefaultExecAllocatorOptions[:]
	if s.InsecureTLS {
		allocOpts = append(allocOpts, chromedp.Flag("ignore-certificate-errors", true))
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

	runCtx, cancelRun := context.WithTimeout(browserCtx, s.Timeout)
	defer cancelRun()

	setup, err := s.setupActions()
	if err != nil {
		return err
	}
	if err := chromedp.Run(runCtx, setup...); err != nil {
		return fmt.Errorf("browser session setup: %w", err)
	}

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

// setupActions enables the network domain and injects the authenticated session
// (extra headers, cookies) before any navigation.
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

	for _, c := range s.Cookies {
		name, value, ok := strings.Cut(c, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --cookie %q (want \"name=value\")", c)
		}
		name, value = strings.TrimSpace(name), strings.TrimSpace(value)
		actions = append(actions, network.SetCookie(name, value).WithURL(s.URL))
	}
	return actions, nil
}

// crawl visits the entry URL and, up to Depth hops and MaxPages pages, follows
// same-host links breadth-first.
func (s *BrowserSource) crawl(ctx context.Context) error {
	start, err := url.Parse(s.URL)
	if err != nil {
		return fmt.Errorf("invalid --url %q: %w", s.URL, err)
	}

	type item struct {
		url   string
		depth int
	}
	queue := []item{{url: s.URL, depth: 0}}
	visited := map[string]bool{s.URL: true}
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
			if next == "" || visited[next] || !sameHost(start, next) {
				continue
			}
			visited[next] = true
			queue = append(queue, item{url: next, depth: cur.depth + 1})
		}
	}
	return nil
}

// linkExtractJS returns every anchor's resolved absolute href.
const linkExtractJS = `Array.from(document.querySelectorAll('a[href]')).map(a => a.href)`

// normalizeLink drops the fragment and keeps only http(s) links.
func normalizeLink(href string) string {
	u, err := url.Parse(href)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	u.Fragment = ""
	return u.String()
}

func sameHost(start *url.URL, rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.EqualFold(u.Host, start.Host)
}
