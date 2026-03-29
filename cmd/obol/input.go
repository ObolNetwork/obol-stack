package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// readJSONInput reads JSON from a file path or stdin (when path is "-").
// Returns the raw bytes for the caller to unmarshal as needed.
func readJSONInput(path string) ([]byte, error) {
	var data []byte
	var err error

	if path == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return nil, fmt.Errorf("read JSON input: %w", err)
	}

	// Validate that it's valid JSON.
	if !json.Valid(data) {
		return nil, fmt.Errorf("input is not valid JSON")
	}

	return data, nil
}
