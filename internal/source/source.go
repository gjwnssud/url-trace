// Package source defines the URL collection sources and their common contract.
package source

import (
	"context"

	"github.com/gjwnssud/url-trace/internal/model"
)

// Source collects URLs from one origin — an observed-traffic capture, a live
// crawl, and so on — and streams them as records. Emitting to a channel lets the
// orchestrator run multiple sources concurrently and merge their output, which
// is how Phase 2's live crawler will join the HAR source without changing the
// pipeline.
type Source interface {
	// Name identifies the source in audit metadata (e.g. "har").
	Name() string

	// Fetch emits every URL it observes to out until it is done or ctx is
	// cancelled. It must not close out; the caller owns the channel's lifecycle.
	Fetch(ctx context.Context, out chan<- model.URLRecord) error
}
