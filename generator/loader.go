package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// GraphLoader handles loading the derivation graph using nix show-derivation
type GraphLoader struct {
	NixPortablePath string
}

func NewGraphLoader(nixPortablePath string) *GraphLoader {
	return &GraphLoader{
		NixPortablePath: nixPortablePath,
	}
}

// Load executes nix show-derivation for the given package and returns the parsed Graph
func (l *GraphLoader) Load(packageName string) (Graph, error) {
	// Invoke nix show-derivation
	cmd := exec.Command(l.NixPortablePath, "nix", "--extra-experimental-features", "nix-command flakes", "show-derivation", "-r", "nixpkgs#legacyPackages.x86_64-linux."+packageName)

	// cmd.Env defaults to os.Environ() which now includes HOME and TMPDIR inherited from parent process

	// Capture output
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("failed to run nix: %v", err)
	}

	outputBytes := out.Bytes()
	// Find first '{'
	idx := bytes.IndexByte(outputBytes, '{')
	if idx < 0 {
		return nil, fmt.Errorf("no JSON object found in output. Output: %s", string(outputBytes))
	}
	jsonBytes := outputBytes[idx:]

	// Parse JSON output
	var graph Graph
	if err := json.Unmarshal(jsonBytes, &graph); err != nil {
		// Log a snippet for context
		errMsg := string(jsonBytes)
		if len(errMsg) > 200 {
			errMsg = errMsg[:200] + "..."
		}
		return nil, fmt.Errorf("failed to parse graph JSON: %v. JSON start: %s", err, errMsg)
	}

	return graph, nil
}
