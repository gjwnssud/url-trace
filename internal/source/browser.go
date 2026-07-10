package source

import (
	"context"
	"errors"
	"fmt"
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
type BrowserSource struct {
	URL     string
	Wait    time.Duration // idle time after load to let late XHR/fetch fire
	Timeout time.Duration // hard cap on the whole capture
}

// NewBrowserSource creates a source that captures the requests made while
// loading url.
func NewBrowserSource(url string, wait, timeout time.Duration) *BrowserSource {
	return &BrowserSource{URL: url, Wait: wait, Timeout: timeout}
}

// Name identifies this source in audit metadata.
func (s *BrowserSource) Name() string { return "browser" }

// Fetch launches headless Chrome, navigates to the target, and emits a record
// for every request observed. Requests are buffered during the run and streamed
// out only afterward, so the browser's event loop is never blocked on a slow
// consumer. Hitting the configured timeout is a normal stop condition: whatever
// was captured so far is still emitted rather than discarded.
func (s *BrowserSource) Fetch(ctx context.Context, out chan<- model.URLRecord) error {
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, chromedp.DefaultExecAllocatorOptions[:]...)
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

	err := chromedp.Run(runCtx,
		network.Enable(),
		chromedp.Navigate(s.URL),
		chromedp.Sleep(s.Wait),
	)
	// Our own timeout firing means "capture window closed", not failure — keep
	// the URLs gathered so far. A parent cancellation (Ctrl-C) still surfaces.
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("browser capture of %s: %w", s.URL, err)
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
