package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Mocks

type MockIndexer struct {
	Index map[string]string
}

func (m *MockIndexer) Find(filename string) (string, bool) {
	val, ok := m.Index[filename]
	return val, ok
}

type MockCopier struct{}

func (m *MockCopier) Copy(src, dst string) error {
	// No-op for mock
	return nil
}

// Tests

func TestBazelGenerator_SimpleRule(t *testing.T) {
	// Setup Mocks
	indexer := &NixpkgsIndexer{Index: map[string]string{}}
	copier := &SourceCopier{}
	generator := NewBazelGenerator(indexer, copier, "/bin/nix-mock")

	// Construct simplest graph
	graph := Graph{
		"/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-hello-2.10.drv": Derivation{
			Builder: "bash",
			Args:    []string{"-c", "echo hello"},
			Env:     map[string]string{"name": "hello-2.10"},
			Outputs: map[string]struct {
				Path string `json:"path"`
			}{
				"out": {Path: "/nix/store/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb-hello-2.10"},
			},
		},
	}

	result, err := generator.Generate(graph)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	output := string(result.BuildFile)
	expectedName := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-hello-2.10"
	if !strings.Contains(output, `name = "`+expectedName+`"`) {
		t.Errorf("Expected output to contain rule name '%s', got:\n%s", expectedName, output)
	}
	if !strings.Contains(output, `builder_path = "bash"`) {
		t.Errorf("Expected output to contain builder_path 'bash'")
	}
}

func TestBazelGenerator_HttpFile(t *testing.T) {
	indexer := &NixpkgsIndexer{Index: map[string]string{}}
	copier := &SourceCopier{}
	generator := NewBazelGenerator(indexer, copier, "/bin/nix-mock")

	graph := Graph{
		"/nix/store/cccccccccccccccccccccccccccccccc-source.drv": Derivation{
			Builder: "builtin:fetchurl",
			Env: map[string]string{
				"name":       "source",
				"url":        "http://example.com/file.tar.gz",
				"outputHash": "sha256-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
			},
			Outputs: map[string]struct {
				Path string `json:"path"`
			}{
				"out": {Path: "/nix/store/dddddddddddddddddddddddddddddddd-source"},
			},
		},
	}

	result, err := generator.Generate(graph)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	nixDeps := string(result.NixDepsBzl)
	if !strings.Contains(nixDeps, `"nix_source_file_tar_gz"`) {
		t.Errorf("Expected nix_source_file_tar_gz in nix_deps.bzl, got:\n%s", nixDeps)
	}
	if !strings.Contains(nixDeps, "http://example.com/file.tar.gz") {
		t.Errorf("Expected url to be present")
	}
}

// Regression Test (Golden)
func TestGenerator_Golden(t *testing.T) {
	// This test loads a static JSON derivation graph and compares the output with expected golden files.
	// NOTE: You must have 'testdata/golden_input.json' and 'testdata/golden_build.bazel'

	// Ensure testdata dir exists
	if _, err := os.Stat("testdata"); os.IsNotExist(err) {
		t.Skip("Skipping golden test: testdata directory missing")
	}

	inputBytes, err := os.ReadFile("testdata/golden_input.json")
	if err != nil {
		t.Skipf("Skipping golden test: %v", err)
	}

	var graph Graph
	if err := json.Unmarshal(inputBytes, &graph); err != nil {
		t.Fatalf("Failed to unmarshal golden input: %v", err)
	}

	indexer := &NixpkgsIndexer{Index: map[string]string{
		"hello.c": "pkgs/applications/misc/hello/hello.c", // Mock index hit
	}}
	copier := &SourceCopier{}
	// Mock nix portable path not used unless hash conversion needed, which we should avoid in golden test input
	generator := NewBazelGenerator(indexer, copier, "mock-nix")

	result, err := generator.Generate(graph)
	if err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	checkGolden(t, "testdata/golden_build.bazel", result.BuildFile)
}

func checkGolden(t *testing.T, goldenPath string, actual []byte) {
	expected, err := os.ReadFile(goldenPath)
	if err != nil {
		// If golden doesn't exist, we might want to write it (dev mode) or fail
		// For CI/Strict, fail.
		t.Errorf("Failed to read golden file %s: %v", goldenPath, err)
		return
	}

	if string(expected) != string(actual) {
		t.Errorf("Output mismatch for %s.\nExpected:\n%s\nGot:\n%s", goldenPath, string(expected), string(actual))
	}
}

func TestBazelGenerator_Idempotency(t *testing.T) {
	indexer := &NixpkgsIndexer{Index: map[string]string{}}
	copier := &SourceCopier{}
	generator := NewBazelGenerator(indexer, copier, "mock-nix")

	graph := Graph{
		"/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-hello.drv": Derivation{
			Builder: "bash",
			Outputs: map[string]struct {
				Path string `json:"path"`
			}{
				"out": {Path: "/nix/store/out"},
			},
		},
	}

	res1, _ := generator.Generate(graph)
	res2, _ := generator.Generate(graph)

	if string(res1.BuildFile) != string(res2.BuildFile) {
		t.Error("Idempotency failure: BuildFile differs between runs")
	}
	if string(res1.NixDepsBzl) != string(res2.NixDepsBzl) {
		t.Error("Idempotency failure: NixDepsBzl differs between runs")
	}
}
