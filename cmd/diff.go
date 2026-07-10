package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gjwnssud/url-trace/internal/model"
	"github.com/gjwnssud/url-trace/internal/policy"
	"github.com/spf13/cobra"
)

type diffOptions struct {
	policyPath string
	inputPath  string
	outputPath string
	format     string
	failOnNew  bool
}

func newDiffCmd() *cobra.Command {
	opts := &diffOptions{}
	cmd := &cobra.Command{
		Use:   "diff",
		Short: "Check an extract result against an existing policy",
		Long: "diff reports which newly observed URLs the policy does not cover " +
			"(candidates to whitelist) and which rules matched nothing this run " +
			"(candidates to retire).",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDiff(opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVar(&opts.policyPath, "policy", "", "policy JSON produced by export (required)")
	flags.StringVarP(&opts.inputPath, "input", "i", "", `extract result JSON ("-" for stdin, required)`)
	flags.StringVarP(&opts.outputPath, "output", "o", "", "output file (default: stdout)")
	flags.StringVarP(&opts.format, "format", "f", "text", "output format: text or json")
	flags.BoolVar(&opts.failOnNew, "fail-on-new", false, "exit non-zero when uncovered URLs are found (for CI)")
	_ = cmd.MarkFlagRequired("policy")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func runDiff(opts *diffOptions) error {
	pf, err := openInput(opts.policyPath)
	if err != nil {
		return err
	}
	loaded, err := policy.Load(pf)
	pf.Close()
	if err != nil {
		return err
	}

	var result model.Result
	if err := readJSONInput(opts.inputPath, &result); err != nil {
		return err
	}

	report := policy.Diff(loaded, result)

	w := os.Stdout
	if opts.outputPath != "" {
		f, err := os.Create(opts.outputPath)
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer f.Close()
		w = f
	}
	if err := writeReport(w, report, opts.format); err != nil {
		return err
	}

	if opts.failOnNew && len(report.NewURLs) > 0 {
		return fmt.Errorf("%d observed URL(s) not covered by policy", len(report.NewURLs))
	}
	return nil
}

func writeReport(w io.Writer, report policy.Report, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	case "text":
		return writeReportText(w, report)
	default:
		return fmt.Errorf("unsupported format %q (use text or json)", format)
	}
}

func writeReportText(w io.Writer, report policy.Report) error {
	if len(report.NewURLs) > 0 {
		fmt.Fprintf(w, "NEW — not covered by policy (%d):\n", len(report.NewURLs))
		for _, r := range report.NewURLs {
			fmt.Fprintf(w, "  + %s  (count=%d, confidence=%s, sources=%s)\n",
				r.URL, r.Count, r.Confidence, strings.Join(r.Sources, ";"))
		}
	}
	if len(report.UnusedRules) > 0 {
		fmt.Fprintf(w, "UNUSED RULES — matched nothing this run (%d):\n", len(report.UnusedRules))
		for _, rule := range report.UnusedRules {
			fmt.Fprintf(w, "  - %s\n", rule.Pattern)
		}
	}
	fmt.Fprintf(w, "checked %d URL(s): %d covered, %d new; %d unused rule(s)\n",
		report.Checked, report.Covered, len(report.NewURLs), len(report.UnusedRules))
	return nil
}
