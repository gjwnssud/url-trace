package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/gjwnssud/url-trace/cmd"
)

// version is stamped at release build time via -ldflags "-X main.version=...".
var version = "dev"

// resolveVersion falls back to Go module info so binaries installed with
// `go install ...@vX.Y.Z` (which bypasses release ldflags) still report their
// real version.
func resolveVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return version
}

func main() {
	// Cancel the context on interrupt so long-running sources (Phase 2's live
	// crawler) stop promptly on Ctrl-C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.Execute(ctx, resolveVersion()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
