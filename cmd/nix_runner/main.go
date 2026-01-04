package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JonathanPerry651/nix-bazel-via-bwrap/pkg/sandbox"
)

func main() {

	// 0. Check for adjacent config file (Symlink mode)
	exePath := os.Args[0]
	configPath := exePath + ".nix-runner.json"

	var (
		cwd            string
		autoEnv        bool
		impureHostLibs bool
		cmdToRun       string
		cmdArgs        []string
	)
	mounts := make(map[string]string)
	extraEnvs := make(map[string]string)

	if _, err := os.Stat(configPath); err == nil {
		// Load from config file
		type RunnerConfig struct {
			Mounts  map[string]string `json:"mounts"`
			Env     map[string]string `json:"env"`
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			WorkDir string            `json:"work_dir"`
			Impure  bool              `json:"impure"`
		}
		data, err := os.ReadFile(configPath)
		if err != nil {
			log.Fatalf("Failed to read config %s: %v", configPath, err)
		}
		var cfg RunnerConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Fatalf("Failed to parse config %s: %v", configPath, err)
		}

		cwd = cfg.WorkDir
		impureHostLibs = cfg.Impure
		cmdToRun = cfg.Command
		cmdArgs = cfg.Args

		// Resolve relative mounts against runfiles dir
		runfilesDirEnv := os.Getenv("RUNFILES_DIR")
		if runfilesDirEnv == "" {
			runfilesDirEnv = exePath + ".runfiles"
		}

		for k, v := range cfg.Mounts {
			if !filepath.IsAbs(k) && runfilesDirEnv != "" {
				// Handle Bazel external repo logic ../repo/path -> repo/path
				if strings.HasPrefix(k, "../") {
					k = k[3:]
				} else if strings.HasPrefix(k, "external/") {
					k = k[9:]
				} else {
					// Assume main repo?
					// For Bzlmod, main artifacts are usually under <module_name>/
					// We might need to handle this later
					k = "_main/" + k
				}
				k = filepath.Join(runfilesDirEnv, k)
			}
			log.Printf("DEBUG: Resolved mount %s -> %s", k, v)
			mounts[v] = k
		}
		for k, v := range cfg.Env {
			extraEnvs[k] = v
		}

		// Append CLI args
		if len(os.Args) > 1 {
			cmdArgs = append(cmdArgs, os.Args[1:]...)
		}

	} else {
		if len(os.Args) < 2 {
			log.Fatalf("Usage: %s [flags...] -- <command> [args...]", os.Args[0])
		}
		// Legacy Flag Parsing
		args := os.Args[1:]
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if arg == "--" {
				if i+1 < len(args) {
					cmdToRun = args[i+1]
					cmdArgs = args[i+2:]
				}
				break
			}

			var val string
			if strings.HasPrefix(arg, "--mount=") {
				val = strings.TrimPrefix(arg, "--mount=")
			} else if arg == "--mount" && i+1 < len(args) {
				val = args[i+1]
				i++
			}

			if val != "" {
				parts := strings.SplitN(val, ":", 2)
				if len(parts) != 2 {
					log.Printf("WARNING: Invalid mount format '%s', skipping.", val)
				} else {
					mounts[parts[1]] = parts[0]
					log.Printf("DEBUG: Explicit mount: %s -> %s", parts[0], parts[1])
				}
				continue
			}

			if strings.HasPrefix(arg, "--cwd=") {
				cwd = strings.TrimPrefix(arg, "--cwd=")
				continue
			}
			if arg == "--auto-env" {
				autoEnv = true
				continue
			}
			if arg == "--impure-host-libs" {
				impureHostLibs = true
				continue
			}
		}
	}

	// 2. Auto-discovery (Conditional)
	if autoEnv {
		log.Printf("DEBUG: Starting auto-discovery (enabled)...")
		autoPaths, err := sandbox.CollectNixPathsFromRunfiles(os.Args[0])
		if err != nil {
			log.Printf("WARNING: Runfiles discovery failed: %v", err)
		} else {
			log.Printf("DEBUG: Found %d mount paths", len(autoPaths))
			for k, v := range autoPaths {
				// k is runfiles/host path, v is sandbox path (/nix/store/...)
				// Prioritize explicit mounts: Only add if not present
				if _, exists := mounts[v]; !exists {
					mounts[v] = k
				}
			}
		}
	} else {
		log.Printf("DEBUG: Auto-discovery disabled")
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

	// 4. Sandbox Configuration
	if cwd == "" && pwd != "" {
		cwd = pwd
	}
	cfg := &sandbox.SandboxConfig{
		Mounts:  mounts,
		Envs:    make(map[string]string),
		WorkDir: cwd,
	}

	if err := cfg.StandardSetup(impureHostLibs); err != nil {
		log.Fatalf("Failed to setup standard sandbox: %v", err)
	}

	// Ensure shell for generic commands (conditional)
	if err := cfg.EnsureShell(impureHostLibs); err != nil {
		log.Printf("WARNING: EnsureShell failed: %v", err)
	}

	// 5. Environment Variables
	// Base envs
	cfg.Envs["PATH"] = "/bin:/usr/bin"
	cfg.Envs["HOME"] = "/homeless-shelter"
	cfg.Envs["NIX_STORE"] = "/nix/store"

	// Add config envs
	for k, v := range extraEnvs {
		cfg.Envs[k] = v
	}

	// Auto-discover Envs from runfiles (Conditional)
	if autoEnv {
		log.Printf("DEBUG: Starting env discovery (enabled)...")
		autoEnvs, err := sandbox.CollectEnvFromRunfiles(os.Args[0])
		if err != nil {
			log.Printf("WARNING: Env discovery failed: %v", err)
		} else {
			log.Printf("DEBUG: Found %d env vars", len(autoEnvs))
			for k, v := range autoEnvs {
				// support $VAR expansion (e.g. PATH=$out/bin:$PATH)
				expanded := os.Expand(v, func(key string) string {
					return cfg.Envs[key]
				})
				cfg.Envs[k] = expanded
			}
		}
	} else {
		log.Printf("DEBUG: Env discovery disabled")
	}

	bwrapArgs, err := sandbox.BuildBwrapArgs(cfg)
	if err != nil {
		log.Fatalf("Failed to build bwrap args: %v", err)
	}

	bwrapArgs = append(bwrapArgs, "--")
	bwrapArgs = append(bwrapArgs, cmdToRun)
	bwrapArgs = append(bwrapArgs, cmdArgs...)

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
