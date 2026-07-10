package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// openInput opens path for reading, treating "-" as stdin so extract output can
// be piped straight into export or diff.
func openInput(path string) (io.ReadCloser, error) {
	if path == "-" {
		return io.NopCloser(os.Stdin), nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// readJSONInput decodes a JSON document from path (or stdin for "-") into dst.
func readJSONInput(path string, dst any) error {
	r, err := openInput(path)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := json.NewDecoder(r).Decode(dst); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	return nil
}
