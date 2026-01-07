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

	// 0. Environment discovery
	exePath := os.Args[0]
	runfilesDir := os.Getenv("RUNFILES_DIR")

	var (
		cwd            string
		autoEnv        bool
		impureHostLibs bool
		cmdToRun       string
		cmdArgs        []string
		explicitConfig string
	)
	configPath := exePath + ".nix-runner.json"
	mounts := make(map[string]string)
	extraEnvs := make(map[string]string)

	// Pre-scan for --config
	for i := 0; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--config=") {
			explicitConfig = strings.TrimPrefix(os.Args[i], "--config=")
			break
		} else if os.Args[i] == "--config" && i+1 < len(os.Args) {
			explicitConfig = os.Args[i+1]
			break
		}
	}

	if explicitConfig != "" {
		configPath = explicitConfig
	}

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

		// Resolve relative mounts
		runfilesDirEnv := os.Getenv("RUNFILES_DIR")
		if runfilesDirEnv == "" {
			runfilesDirEnv = exePath + ".runfiles"
		}
		// If the inferred runfiles dir doesn't exist, we are in a sandbox
		if _, err := os.Stat(runfilesDirEnv); err != nil {
			runfilesDirEnv = ""
		}

		for k, v := range cfg.Mounts {
			hostPath := k
			if !filepath.IsAbs(k) {
				if runfilesDirEnv != "" {
					relPath := k
					if strings.HasPrefix(relPath, "../") {
						relPath = relPath[3:]
					} else {
						relPath = "_main/" + relPath
					}
					hostPath = filepath.Join(runfilesDirEnv, relPath)
				} else {
					// Sandbox layout: external repositories are under external/
					if strings.HasPrefix(k, "../") {
						hostPath = "external/" + k[3:]
					} else {
						hostPath = k
					}
				}
			}
			mounts[v] = hostPath
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
		autoPaths, err := sandbox.CollectNixPathsFromRunfiles(os.Args[0])
		if err == nil {
			for k, v := range autoPaths {
				if _, exists := mounts[v]; !exists {
					mounts[v] = k
				}
			}
		}
	}

	// 3. Mount Runfiles Root & PWD
	const pwdPlaceholder = "." // Simplify
	pwd, _ := os.Getwd()
	if runfilesDir != "" {
		mounts[runfilesDir] = runfilesDir
	}
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
	cfg.Envs["PATH"] = "/bin:/usr/bin"
	cfg.Envs["HOME"] = "/homeless-shelter"
	cfg.Envs["NIX_STORE"] = "/nix/store"

	for k, v := range extraEnvs {
		cfg.Envs[k] = v
	}

	// Auto-discover Envs from runfiles (Conditional)
	if autoEnv {
		autoEnvs, err := sandbox.CollectEnvFromRunfiles(os.Args[0])
		if err == nil {
			for k, v := range autoEnvs {
				expanded := os.Expand(v, func(key string) string {
					return cfg.Envs[key]
				})
				cfg.Envs[k] = expanded
			}
		}
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
