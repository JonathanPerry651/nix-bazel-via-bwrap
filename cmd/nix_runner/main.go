package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JonathanPerry651/nix-bazel-via-bwrap/pkg/sandbox"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatalf("Usage: %s [flags...] -- <command> [args...]", os.Args[0])
	}

	var explicitMounts []string
	var workDir string
	var command []string

	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--mount" && i+1 < len(args) {
			explicitMounts = append(explicitMounts, args[i+1])
			i++
		} else if arg == "--cwd" && i+1 < len(args) {
			workDir = args[i+1]
			i++
		} else if arg == "--" {
			command = args[i+1:]
			break
		} else {
			if !strings.HasPrefix(arg, "-") {
				command = args[i:]
				break
			}
		}
	}

	if len(command) == 0 {
		log.Fatal("No command specified")
	}

	mounts := make(map[string]string)

	// 1. Process explicit mounts
	for _, m := range explicitMounts {
		parts := strings.SplitN(m, ":", 2)
		if len(parts) != 2 {
			log.Printf("WARNING: Invalid mount format '%s', skipping.", m)
			continue
		}
		mounts[parts[1]] = parts[0]
	}

	// 2. Auto-discovery
	autoPaths, err := sandbox.CollectNixPathsFromRunfiles(os.Args[0])
	if err != nil {
		log.Printf("WARNING: Runfiles discovery failed: %v", err)
	}
	for _, p := range autoPaths {
		if _, exists := mounts[p]; !exists {
			mounts[p] = p
		}
	}

	// 3. Mount Runfiles Root & PWD
	runfilesDir := os.Getenv("RUNFILES_DIR")
	if runfilesDir != "" {
		mounts[runfilesDir] = runfilesDir
	}
	pwd, _ := os.Getwd()
	if pwd != "" {
		mounts[pwd] = pwd
	}

	// 4. Configure Sandbox
	projectRoot := "/home/jonathanp/github/nix-bazel-via-bwrap"

	finalMounts := make(map[string]string)
	for s, h := range mounts {
		absHost, err := filepath.EvalSymlinks(h)
		if err != nil {
			absHost, _ = filepath.Abs(h)
		}
		finalMounts[s] = absHost
	}

	cfg := sandbox.SandboxConfig{
		Mounts:        finalMounts,
		Envs:          make(map[string]string),
		WorkDir:       workDir,
		StandardSetup: true,
		AdditionalRoBinds: []string{
			"/home/jonathanp/.cache/bazel",
			projectRoot,
		},
	}

	for _, e := range os.Environ() {
		kv := strings.SplitN(e, "=", 2)
		if len(kv) == 2 {
			cfg.Envs[kv[0]] = kv[1]
		}
	}
	// Set Nix envs
	cfg.Envs["NIX_STORE"] = "/nix/store"

	// Ensure /bin/sh is available (esp if command depends on it)
	// For runner, the command[0] is the builder-equivalent
	sandbox.EnsureShell(cfg.Mounts, command[0])

	bwrapArgs, err := sandbox.BuildBwrapArgs(cfg)
	if err != nil {
		log.Fatalf("Failed to build bwrap args: %v", err)
	}

	bwrapArgs = append(bwrapArgs, "--")
	bwrapArgs = append(bwrapArgs, command...)

	cmd := exec.Command("bwrap", bwrapArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}
