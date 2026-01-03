package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bazelbuild/rules_go/go/runfiles"
)

// Main entry point for the Nix to Bazel generator
func main() {
	log.SetFlags(0)
	if len(os.Args) < 2 {
		log.Fatal("Usage: generator <package_name>")
	}
	packageName := os.Args[1]

	// Bazel run handling: Switch to workspace root if available
	if wd := os.Getenv("BUILD_WORKSPACE_DIRECTORY"); wd != "" {
		if err := os.Chdir(wd); err != nil {
			log.Fatalf("Failed to chdir to workspace root: %v", err)
		}
		log.Printf("Switched to workspace root: %s", wd)
	}

	// Runfiles lookup for tools
	r, err := runfiles.New()
	if err != nil {
		log.Fatalf("Failed to locate runfiles: %v", err)
	}

	nixPortablePath, err := r.Rlocation("nix_portable/file/nix-portable")
	if err != nil {
		log.Fatalf("Failed to locate nix-portable: %v", err)
	}

	// Make executable
	if err := os.Chmod(nixPortablePath, 0755); err != nil {
		log.Fatalf("Failed to chmod nix-portable: %v", err)
	}

	// Setup ephemeral HOME
	fakeHomeRel, err := os.MkdirTemp(".", "nix-generator-home")
	if err != nil {
		log.Fatalf("Failed to create temp HOME: %v", err)
	}
	fakeHome, err := filepath.Abs(fakeHomeRel)
	if err != nil {
		log.Fatalf("Failed to get absolute path of temp HOME: %v", err)
	}

	// GLOBAL ENV SET: Critical for subprocesses like copyFile
	os.Setenv("HOME", fakeHome)

	log.Printf("DEBUG: Using fakeHome: %s", fakeHome)

	defer func() {
		// Cleanup with permission fix (as in wrapper)
		exec.Command("chmod", "-R", "+w", fakeHome).Run()
		// DEBUG: Keep home to inspect
		os.RemoveAll(fakeHome)
	}()

	// Create local temp dir to avoid /tmp which is full
	localTmp := filepath.Join(fakeHome, "tmp")
	if err := os.MkdirAll(localTmp, 0755); err != nil {
		log.Fatalf("Failed to create local tmp: %v", err)
	}
	os.Setenv("TMPDIR", localTmp)

	// --- Component Orchestration ---

	// 1. Load Graph
	loader := NewGraphLoader(nixPortablePath)
	graph, err := loader.Load(packageName)
	if err != nil {
		log.Fatalf("Failed to load graph: %v", err)
	}

	// 2. Index Nixpkgs
	indexer := NewNixpkgsIndexer()

	// 3. Setup Copier
	copier := NewSourceCopier()

	// 4. Generate Build Artifacts
	builder := NewBazelGenerator(indexer, copier, nixPortablePath)
	artifacts, err := builder.Generate(graph, packageName)
	if err != nil {
		log.Fatalf("Failed to generate artifacts: %v", err)
	}

	// Write Artifacts
	if len(os.Args) > 2 {
		// If output path provided (usually not typical usage for this multi-file outcome),
		// we likely just write the MAIN BUILD file there?
		// The original code:
		// if len(os.Args) > 2 {
		// 		outputPath := os.Args[2]
		// 		if err := os.WriteFile(outputPath, buildFile, 0644); err != nil { ... }
		// }

		outputPath := os.Args[2]
		if err := os.WriteFile(outputPath, artifacts.BuildFile, 0644); err != nil {
			log.Fatalf("Failed to write to file %s: %v", outputPath, err)
		}
	} else {
		if _, err := os.Stdout.Write(artifacts.BuildFile); err != nil {
			log.Fatalf("Failed to write output: %v", err)
		}
	}

	// Side-effect verification:
	// Generate writes nix_sources/BUILD, nixpkgs/nix_deps.bzl etc.
	// The original code did those I/O operations INSIDE the generation logic.
	// Our new builder.Generate returns bytes but ALSO performs side-effects for nix_sources copying?
	// Ah, I inspected builder.go:
	// - Copier.Copy is called during Generate -> Side effect (files created).
	// - But nix_sources/BUILD and nix_deps.bzl return bytes?
	// Let's check builder.go again.
	// builder.generateNixSourcesBuild returns []byte.
	// builder.generateNixDepsBzl returns []byte.
	// builder.generateNixDepsUseRepo returns []byte.

	// So we need to write them here!

	if err := os.WriteFile("nix_sources/BUILD", artifacts.NixSourcesBuild, 0644); err != nil {
		log.Fatalf("Failed to write nix_sources/BUILD: %v", err)
	}

	if err := os.MkdirAll("nixpkgs", 0755); err != nil {
		log.Fatalf("Failed to create nixpkgs dir: %v", err)
	}

	if err := os.WriteFile("nixpkgs/nix_deps.bzl", artifacts.NixDepsBzl, 0644); err != nil {
		log.Fatalf("Failed to write nixpkgs/nix_deps.bzl: %v", err)
	}

	if err := os.WriteFile("nix_deps_use_repo.bzl", artifacts.NixDepsUseRepoBzl, 0644); err != nil {
		log.Printf("Warning: Failed to write nix_deps_use_repo.bzl: %v", err)
	}

	log.Printf("Generation complete.")
}
