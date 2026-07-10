package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/gjwnssud/url-trace/cmd"
)

// version is stamped at release build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// Cancel the context on interrupt so long-running sources (Phase 2's live
	// crawler) stop promptly on Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.Execute(ctx, version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
