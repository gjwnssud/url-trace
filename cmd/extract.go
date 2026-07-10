package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"strings"
	"time"

	"github.com/gjwnssud/url-trace/internal/classify"
	"github.com/gjwnssud/url-trace/internal/model"
	"github.com/gjwnssud/url-trace/internal/output"
	"github.com/gjwnssud/url-trace/internal/patterns"
	"github.com/gjwnssud/url-trace/internal/pipeline"
	"github.com/gjwnssud/url-trace/internal/source"
	"github.com/spf13/cobra"
	"golang.org/x/net/publicsuffix"
	"golang.org/x/sync/errgroup"
)

type extractOptions struct {
	harPath        string
	urls           []string
	urlFile        string
	wait           time.Duration
	timeout        time.Duration
	insecure       bool
	headful        bool
	depth          int
	maxPages       int
	cookies        []string
	headers        []string
	primaryDomains []string
	outputPath     string
	format         string
}

func newExtractCmd() *cobra.Command {
	opts := &extractOptions{}
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract and aggregate URLs from a capture into policy records",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runExtract(cmd.Context(), opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.harPath, "har", "", "path to a HAR capture file")
	flags.StringArrayVar(&opts.urls, "url", nil, "entry URL to load in a headless browser and capture; repeatable (list SPA routes here)")
	flags.StringVar(&opts.urlFile, "url-file", "", "file of entry URLs, one per line ('#' comments allowed)")
	flags.DurationVar(&opts.wait, "wait", 3*time.Second, "idle time after page load to capture late requests (--url)")
	flags.DurationVar(&opts.timeout, "timeout", 30*time.Second, "hard cap on the browser capture, across all pages (--url)")
	flags.BoolVarP(&opts.insecure, "insecure", "k", false,
		"accept invalid TLS certificates in the browser capture (self-signed/internal CA)")
	flags.BoolVar(&opts.headful, "headful", false,
		"open a visible window and pause for manual login before capturing (avoids duplicate-login session expiry)")
	flags.IntVar(&opts.depth, "depth", 0, "link hops to follow from each entry URL (0 = entry pages only)")
	flags.IntVar(&opts.maxPages, "max-pages", 50, "max pages to visit while crawling (--url with --depth)")
	flags.StringArrayVar(&opts.cookies, "cookie", nil, `session cookie "name=value" for --url, repeatable`)
	flags.StringArrayVar(&opts.headers, "header", nil, `HTTP header "Key: Value" for --url, repeatable`)
	flags.StringSliceVar(&opts.primaryDomains, "primary-domain", nil,
		"first-party domain, repeatable; subdomains match (default: derived from --url)")
	flags.StringVarP(&opts.outputPath, "output", "o", "", "output file (default: stdout)")
	flags.StringVarP(&opts.format, "format", "f", "json", "output format: json or csv")
	return cmd
}

func runExtract(ctx context.Context, opts *extractOptions) error {
	if opts.urlFile != "" {
		fileURLs, err := readURLFile(opts.urlFile)
		if err != nil {
			return err
		}
		opts.urls = append(opts.urls, fileURLs...)
	}

	sources, err := buildSources(opts)
	if err != nil {
		return err
	}

	records, err := collect(ctx, sources)
	if err != nil {
		return err
	}

	aggregated, skipped := pipeline.Aggregate(records)
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "skipped %d unparseable URL(s)\n", skipped)
	}

	classify.Apply(aggregated, primaryDomains(opts))
	result := model.Result{
		URLs:               aggregated,
		PatternSuggestions: patterns.Suggest(aggregated),
	}
	if len(result.PatternSuggestions) > 0 && opts.format == string(output.FormatCSV) {
		fmt.Fprintf(os.Stderr, "%d pattern suggestion(s) omitted from CSV; use --format json to see them\n",
			len(result.PatternSuggestions))
	}

	return writeOutput(result, opts)
}

// primaryDomains returns the explicitly configured first-party domains, or —
// when none are given — derives the registrable domain (eTLD+1) of every entry
// URL so that sibling subdomains like cdn.example.com still classify as
// first-party. An explicit --primary-domain list is respected as-is: the user
// may be deliberately excluding the targets' siblings.
func primaryDomains(opts *extractOptions) []string {
	if len(opts.primaryDomains) > 0 {
		return opts.primaryDomains
	}
	seen := map[string]bool{}
	var domains []string
	for _, raw := range opts.urls {
		u, err := neturl.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		host := u.Hostname()
		// localhost, bare IPs, and other non-registrable hosts have no eTLD+1;
		// fall back to the host itself.
		domain, err := publicsuffix.EffectiveTLDPlusOne(host)
		if err != nil {
			domain = host
		}
		if !seen[domain] {
			seen[domain] = true
			domains = append(domains, domain)
		}
	}
	return domains
}

// readURLFile reads entry URLs one per line, ignoring blank lines and comments.
func readURLFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open url file: %w", err)
	}
	defer f.Close()

	var urls []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		urls = append(urls, line)
	}
	return urls, scanner.Err()
}

// buildSources assembles the collection sources selected by the flags. At least
// one must be provided; --har and --url may be combined to maximize recall.
func buildSources(opts *extractOptions) ([]source.Source, error) {
	var sources []source.Source
	if opts.harPath != "" {
		sources = append(sources, source.NewHARSource(opts.harPath))
	}
	if len(opts.urls) > 0 {
		browser := source.NewBrowserSource(opts.urls, opts.wait, opts.timeout)
		browser.InsecureTLS = opts.insecure
		browser.Headful = opts.headful
		browser.Depth = opts.depth
		browser.MaxPages = opts.maxPages
		browser.Cookies = opts.cookies
		browser.Headers = opts.headers
		sources = append(sources, browser)
	}
	if len(sources) == 0 {
		return nil, errors.New("provide at least one source: --har and/or --url")
	}
	return sources, nil
}

// collect runs every source concurrently and gathers their emitted records.
// A dedicated goroutine closes the channel once all sources finish so the range
// loop below terminates cleanly.
func collect(ctx context.Context, sources []source.Source) ([]model.URLRecord, error) {
	out := make(chan model.URLRecord)
	group, ctx := errgroup.WithContext(ctx)

	for _, s := range sources {
		group.Go(func() error {
			return s.Fetch(ctx, out)
		})
	}

	go func() {
		_ = group.Wait()
		close(out)
	}()

	var records []model.URLRecord
	for r := range out {
		records = append(records, r)
	}
	return records, group.Wait()
}

func writeOutput(result model.Result, opts *extractOptions) error {
	format, err := parseFormat(opts.format)
	if err != nil {
		return err
	}

	w := os.Stdout
	if opts.outputPath != "" {
		f, err := os.Create(opts.outputPath)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer f.Close()
		w = f
	}

	return output.Write(w, result, format)
}

func parseFormat(s string) (output.Format, error) {
	switch output.Format(s) {
	case output.FormatJSON:
		return output.FormatJSON, nil
	case output.FormatCSV:
		return output.FormatCSV, nil
	default:
		return "", fmt.Errorf("unsupported format %q (use json or csv)", s)
	}
}
