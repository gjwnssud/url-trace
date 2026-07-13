//go:build js && wasm

// Command wasm exposes url-trace's extraction/policy pipeline to JavaScript so
// the Chrome extension (extension/) can reuse the exact same logic the CLI
// uses, instead of re-implementing security-sensitive rules (recall-first
// aggregation, conservative pattern suggestion, policy matching) a second
// time in TypeScript. Only the capture layer differs between the CLI
// (chromedp) and the extension (chrome.webRequest); everything downstream of
// "here are the observed URLs" is this same Go code either way.
//
// Every exported function takes JSON-encoded strings as arguments and returns
// a JSON-encoded string, so the JS side only needs JSON.parse/stringify — no
// js.Value object graph to build by hand.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	"github.com/gjwnssud/url-trace/internal/classify"
	"github.com/gjwnssud/url-trace/internal/model"
	"github.com/gjwnssud/url-trace/internal/patterns"
	"github.com/gjwnssud/url-trace/internal/pipeline"
	"github.com/gjwnssud/url-trace/internal/policy"
	"github.com/gjwnssud/url-trace/internal/sqlexport"
)

func main() {
	ns := js.Global().Get("Object").New()
	ns.Set("process", wrap(processFunc))
	ns.Set("buildPolicy", wrap(buildPolicyFunc))
	ns.Set("diff", wrap(diffFunc))
	ns.Set("exportSQL", wrap(exportSQLFunc))
	js.Global().Set("urltrace", ns)

	// The Go runtime exits (and every registered js.Func becomes invalid) the
	// moment main returns, so keep it alive for as long as the JS host wants it.
	select {}
}

// wrap adapts a (args []js.Value) -> (any, error) Go function into a js.Func
// that never lets a Go panic escape into JS (which would abort the whole
// runtime) and always returns a JSON string: either the marshaled result or
// {"error": "..."} on failure. This mirrors the CLI's own rule of never
// failing silently — every error is reported, just to the caller instead of
// stderr.
func wrap(fn func(args []js.Value) (any, error)) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) any {
		result, err := safeCall(fn, args)
		if err != nil {
			return toJSON(map[string]string{"error": err.Error()})
		}
		return toJSON(result)
	})
}

func safeCall(fn func(args []js.Value) (any, error), args []js.Value) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn(args)
}

func toJSON(v any) js.Value {
	b, err := json.Marshal(v)
	if err != nil {
		// Marshaling our own result types should never fail; if it does, still
		// surface it as a string rather than let the caller get "undefined".
		b, _ = json.Marshal(map[string]string{"error": "marshal response: " + err.Error()})
	}
	return js.ValueOf(string(b))
}

// processFunc runs the full extract pipeline: aggregate -> classify -> suggest
// patterns. args: recordsJSON ([]model.URLRecord as captured by the
// extension), primaryDomainsJSON ([]string, optional).
func processFunc(args []js.Value) (any, error) {
	if len(args) < 1 {
		return nil, fmt.Errorf("process: expected (recordsJSON, primaryDomainsJSON)")
	}
	var records []model.URLRecord
	if err := json.Unmarshal([]byte(args[0].String()), &records); err != nil {
		return nil, fmt.Errorf("parse records: %w", err)
	}
	var domains []string
	if len(args) > 1 && args[1].Type() == js.TypeString {
		if err := json.Unmarshal([]byte(args[1].String()), &domains); err != nil {
			return nil, fmt.Errorf("parse primaryDomains: %w", err)
		}
	}

	aggregated, skipped := pipeline.Aggregate(records)
	classify.Apply(aggregated, domains)
	result := model.Result{
		URLs:               aggregated,
		PatternSuggestions: patterns.Suggest(aggregated),
	}
	return map[string]any{"result": result, "skipped": skipped}, nil
}

// buildPolicyFunc converts an extraction result into a policy. args:
// resultJSON (model.Result), optsJSON (policy.BuildOptions).
func buildPolicyFunc(args []js.Value) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("buildPolicy: expected (resultJSON, optsJSON)")
	}
	var result model.Result
	if err := json.Unmarshal([]byte(args[0].String()), &result); err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}
	var opts policy.BuildOptions
	if err := json.Unmarshal([]byte(args[1].String()), &opts); err != nil {
		return nil, fmt.Errorf("parse opts: %w", err)
	}

	p, warnings := policy.Build(result, opts)
	return map[string]any{"policy": p, "warnings": orEmpty(warnings)}, nil
}

// diffFunc checks an extraction result against an existing policy. args:
// policyJSON (policy.Policy), resultJSON (model.Result).
func diffFunc(args []js.Value) (any, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("diff: expected (policyJSON, resultJSON)")
	}
	p, err := policy.Load(strings.NewReader(args[0].String()))
	if err != nil {
		return nil, err
	}
	var result model.Result
	if err := json.Unmarshal([]byte(args[1].String()), &result); err != nil {
		return nil, fmt.Errorf("parse result: %w", err)
	}
	return policy.Diff(p, result), nil
}

// exportSQLFunc renders a policy as INSERT statements using a user-supplied
// table mapping. args: policyJSON (policy.Policy), configJSON
// (sqlexport.Config), nowMs (epoch milliseconds, supplied by JS since Go's
// wasm build has no reliable wall clock notion of "now" independent of the
// host — and it keeps output reproducible for a given nowMs).
func exportSQLFunc(args []js.Value) (any, error) {
	if len(args) < 3 {
		return nil, fmt.Errorf("exportSQL: expected (policyJSON, configJSON, nowMs)")
	}
	p, err := policy.Load(strings.NewReader(args[0].String()))
	if err != nil {
		return nil, err
	}
	cfg, err := sqlexport.LoadConfig(strings.NewReader(args[1].String()))
	if err != nil {
		return nil, err
	}
	now := time.UnixMilli(int64(args[2].Float()))

	var buf bytes.Buffer
	warnings, err := sqlexport.WriteSQL(&buf, p, cfg, now)
	if err != nil {
		return nil, err
	}
	return map[string]any{"sql": buf.String(), "warnings": orEmpty(warnings)}, nil
}

// orEmpty normalizes a nil slice to an empty (non-nil) one. encoding/json
// marshals a nil []string as JSON null, not [] — which would make every JS
// caller's `warnings.length` throw on the (common) success path instead of
// just seeing an empty array.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
