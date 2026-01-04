package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/JonathanPerry651/nix-bazel-via-bwrap/pkg/sandbox"
)

type OutputMapping struct {
	Name      string
	StorePath string
	BazelDir  string
}

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <builder> <realOutDirBase> [--mount host:sandbox...] -- [builderArgs...]", os.Args[0])
	}

	builder := os.Args[1]
	realOutDirBase := os.Args[2]

	var explicitMounts []string
	var explicitOutputs []string
	var builderArgs []string
	parsingMounts := true
	for i := 3; i < len(os.Args); i++ {
		arg := os.Args[i]
		if parsingMounts {
			if arg == "--mount" && i+1 < len(os.Args) {
				explicitMounts = append(explicitMounts, os.Args[i+1])
				i++
				continue
			} else if arg == "--output" && i+1 < len(os.Args) {
				explicitOutputs = append(explicitOutputs, os.Args[i+1])
				i++
				continue
			} else if arg == "--" {
				parsingMounts = false
				continue
			}
		}
		builderArgs = append(builderArgs, arg)
	}

	// Parse Outputs
	var outputMappings []OutputMapping
	for _, mapping := range explicitOutputs {
		parts := strings.SplitN(mapping, ":", 2)
		if len(parts) != 2 {
			log.Printf("WARNING: Invalid output format '%s', skipping.\n", mapping)
			continue
		}
		name := parts[0]
		storePath := parts[1]

		outputMappings = append(outputMappings, OutputMapping{
			Name:      name,
			StorePath: storePath,
			BazelDir:  filepath.Join(realOutDirBase, filepath.Base(storePath)),
		})
	}

	// Setup Temp Work Dir
	workDir, err := os.MkdirTemp(".", "bazel_bwrap_work_")
	if err != nil {
		log.Fatalf("Failed to create work dir: %v", err)
	}
	defer os.RemoveAll(workDir)

	hostStore := filepath.Join(workDir, "nix_store")
	os.MkdirAll(hostStore, 0755)
	buildDir := filepath.Join(workDir, "build")
	os.MkdirAll(buildDir, 0755)

	// Setup /etc for Nix
	etcDir := filepath.Join(workDir, "etc")
	os.MkdirAll(etcDir, 0755)
	os.WriteFile(filepath.Join(etcDir, "passwd"), []byte("nixbld:x:1000:100:Nix Build User:/build:/bin/sh\n"), 0644)
	os.WriteFile(filepath.Join(etcDir, "group"), []byte("nixbld:x:100:nixbld\n"), 0644)
	os.WriteFile(filepath.Join(etcDir, "hosts"), []byte("127.0.0.1 localhost\n"), 0644)
	homelessDir := filepath.Join(workDir, "homeless-shelter")
	os.MkdirAll(homelessDir, 0755)

	mounts := make(map[string]string)

	// Process Inputs & Runfiles
	// Explicit
	for _, m := range explicitMounts {
		parts := strings.SplitN(m, ":", 2)
		if len(parts) == 2 {
			mounts[parts[1]] = parts[0]
		}
	}
	// Auto-discover
	if paths, err := sandbox.CollectNixPathsFromRunfiles(os.Args[0]); err == nil {
		for k, v := range paths {
			// k=host, v=sandbox. mounts[sandbox] = host
			mounts[v] = k
		}
	}

	// Resolve mounts to host
	finalMounts := make(map[string]string)
	for s, h := range mounts {
		absHost, err := filepath.EvalSymlinks(h)
		if err != nil {
			absHost, _ = filepath.Abs(h)
		}
		finalMounts[s] = absHost
	}

	// Handle Builtins vs Real Build
	if strings.HasPrefix(builder, "builtin:") {
		for _, om := range outputMappings {
			dest := filepath.Join(hostStore, filepath.Base(om.StorePath))
			src := om.StorePath
			if hostSrc, ok := finalMounts[src]; ok {
				src = hostSrc
			}

			if err := sandbox.HandleBuiltin(builder, src, dest); err != nil {
				log.Fatalf("Builtin failed: %v", err)
			}
		}
	} else {
		// REAL BUILD via Bwrap
		cfg := sandbox.SandboxConfig{
			Mounts: finalMounts,
			Binds: map[string]string{
				"/nix/store": hostStore,
				"/build":     buildDir,
			},
			Envs: map[string]string{
				"IN_SANDBOX":    "1",
				"NIX_BUILD_TOP": "/build",
				"NIX_STORE":     "/nix/store",
				"TMPDIR":        "/build",
				"HOME":          "/homeless-shelter",
			},
			WorkDir: "/build",
		}

		// Always mount system libs for builder to ensure generic builders work (e.g. /bin/sh)
		// Builders are assumed to be non-hermetic until we enforce pure builders strictly.
		// For now matching previous behavior of mounting /bin etc.
		if err := cfg.StandardSetup(true); err != nil {
			log.Fatalf("StandardSetup failed: %v", err)
		}

		// Inject Output Paths as Environment Variables
		for _, om := range outputMappings {
			cfg.Envs[om.Name] = om.StorePath
		}

		cfg.Mounts["/etc/passwd"] = filepath.Join(etcDir, "passwd")
		cfg.Mounts["/etc/group"] = filepath.Join(etcDir, "group")
		cfg.Mounts["/etc/hosts"] = filepath.Join(etcDir, "hosts")
		cfg.Mounts["/homeless-shelter"] = homelessDir

		// EnsureShell is now a method of SandboxConfig
		// Note: StandardSetup(true) already mounts /bin etc, so this might not need to do much
		// but we call it to ensure `builder` path is available or warn.
		// However, the new API is cfg.EnsureShell(mountSystemLibs bool)
		if err := cfg.EnsureShell(true); err != nil {
			log.Printf("Warning: EnsureShell failed: %v", err)
		}

		bwrapArgs, err := sandbox.BuildBwrapArgs(&cfg)
		if err != nil {
			log.Fatalf("Failed to build bwrap args: %v", err)
		}

		// Append Builder Args
		bwrapArgs = append(bwrapArgs, "--", builder)
		bwrapArgs = append(bwrapArgs, builderArgs...)

		// Inject outputs placeholder logic
		placeholder := "/1rz4g4znpzjwh1xymhjpm42vipw92pr73vdgl6xs1hycac8kf2n9"
		var outPath string
		for _, om := range outputMappings {
			if om.Name == "out" {
				outPath = om.StorePath
				break
			}
		}
		if outPath != "" {
			for i, arg := range bwrapArgs {
				if strings.Contains(arg, placeholder) {
					bwrapArgs[i] = strings.ReplaceAll(arg, placeholder, outPath)
				}
			}
		}

		cmd := exec.Command("bwrap", bwrapArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		if err := cmd.Run(); err != nil {
			log.Fatalf("Builder failed: %v", err)
		}
	}

	// Copy Back Logic
	for _, om := range outputMappings {
		base := filepath.Base(om.StorePath)
		srcPath := filepath.Join(hostStore, base)
		finalMounts[om.StorePath] = srcPath
	}

	copier := sandbox.NewCopier(finalMounts)

	for _, om := range outputMappings {
		base := filepath.Base(om.StorePath)
		srcPath := filepath.Join(hostStore, base)

		if _, err := os.Stat(srcPath); err == nil {
			// Quiet copy-back
			err := copier.CopyRecursive(srcPath, om.BazelDir)
			if err != nil {
				log.Fatalf("Error during copy-back of %s: %v", om.Name, err)
			}
		} else {
			fmt.Printf("WARNING: Output %s (%s) was not produced\n", om.Name, srcPath)
		}
	}
}
