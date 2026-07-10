package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"time"

	"github.com/gjwnssud/url-trace/internal/model"
	"github.com/gjwnssud/url-trace/internal/policy"
	"github.com/gjwnssud/url-trace/internal/sqlexport"
	"github.com/spf13/cobra"
)

type exportOptions struct {
	inputPath      string
	minConfidence  string
	party          string
	acceptPatterns []string
	sqlConfig      string
	outputPath     string
	format         string
}

func newExportCmd() *cobra.Command {
	opts := &exportOptions{}
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Convert an extract result into a whitelist policy",
		Long: "export turns the JSON produced by extract into a versioned policy file. " +
			"Wildcard suggestions are never applied automatically: pass each approved " +
			"pattern via --accept-pattern to collapse the URLs it covers into one rule.",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runExport(opts)
		},
	}
	flags := cmd.Flags()
	flags.StringVarP(&opts.inputPath, "input", "i", "", `extract result JSON ("-" for stdin, required)`)
	flags.StringVar(&opts.minConfidence, "min-confidence", model.ConfidenceLow, "drop records below this confidence: low, medium, or high")
	flags.StringVar(&opts.party, "party", "", "keep only this party: first-party, third-party, or unknown (default: all)")
	flags.StringArrayVar(&opts.acceptPatterns, "accept-pattern", nil, "approved wildcard pattern, repeatable")
	flags.StringVar(&opts.sqlConfig, "sql-config", "",
		"table/column mapping JSON; when set, output is INSERT SQL for the mapped table instead of --format")
	flags.StringVarP(&opts.outputPath, "output", "o", "", "output file (default: stdout)")
	flags.StringVarP(&opts.format, "format", "f", "json", "output format: json (policy file) or txt (patterns only)")
	_ = cmd.MarkFlagRequired("input")
	return cmd
}

func runExport(opts *exportOptions) error {
	if err := validateExportOptions(opts); err != nil {
		return err
	}

	var result model.Result
	if err := readJSONInput(opts.inputPath, &result); err != nil {
		return err
	}

	built, warnings := policy.Build(result, policy.BuildOptions{
		MinConfidence:  opts.minConfidence,
		Party:          opts.party,
		AcceptPatterns: opts.acceptPatterns,
	})
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", w)
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

	if opts.sqlConfig != "" {
		return writeSQLExport(w, built, opts.sqlConfig)
	}
	return writePolicy(w, built, opts.format)
}

func writeSQLExport(w io.Writer, built policy.Policy, configPath string) error {
	cf, err := openInput(configPath)
	if err != nil {
		return err
	}
	cfg, err := sqlexport.LoadConfig(cf)
	cf.Close()
	if err != nil {
		return err
	}
	warnings, err := sqlexport.WriteSQL(w, built, cfg, time.Now())
	for _, warn := range warnings {
		fmt.Fprintln(os.Stderr, "warning:", warn)
	}
	return err
}

func validateExportOptions(opts *exportOptions) error {
	switch opts.minConfidence {
	case model.ConfidenceLow, model.ConfidenceMedium, model.ConfidenceHigh:
	default:
		return fmt.Errorf("invalid --min-confidence %q (use low, medium, or high)", opts.minConfidence)
	}
	switch opts.party {
	case "", model.PartyFirst, model.PartyThird, model.PartyUnknown:
	default:
		return fmt.Errorf("invalid --party %q (use first-party, third-party, or unknown)", opts.party)
	}
	return nil
}

func writePolicy(w io.Writer, p policy.Policy, format string) error {
	switch format {
	case "json":
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(p)
	case "txt":
		for _, rule := range p.Rules {
			if _, err := fmt.Fprintln(w, rule.Pattern); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unsupported format %q (use json or txt)", format)
	}
}
