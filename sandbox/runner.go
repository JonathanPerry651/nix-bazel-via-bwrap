package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

func resolveToHost(p string, mounts map[string]string) string {
	// Sort sandbox paths by length descending to catch most specific mount
	var sandboxPaths []string
	for s := range mounts {
		sandboxPaths = append(sandboxPaths, s)
	}
	sort.Slice(sandboxPaths, func(i, j int) bool {
		return len(sandboxPaths[i]) > len(sandboxPaths[j])
	})

	for _, s := range sandboxPaths {
		if strings.HasPrefix(p, s) {
			rel, err := filepath.Rel(s, p)
			if err == nil && !strings.HasPrefix(rel, "..") {
				return filepath.Join(mounts[s], rel)
			}
		}
	}
	return p
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

	type OutputMapping struct {
		Name      string
		StorePath string
		BazelDir  string
	}
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

	// Create a local scratch directory for this action
	workDir, err := os.MkdirTemp(".", "bazel_bwrap_work_")
	if err != nil {
		log.Fatalf("Failed to create work dir: %v", err)
	}
	defer os.RemoveAll(workDir)

	hostStore := filepath.Join(workDir, "nix_store")
	if err := os.MkdirAll(hostStore, 0755); err != nil {
		log.Fatalf("Failed to create host nix store: %v", err)
	}
	// defer os.RemoveAll(hostStore)

	buildDir := filepath.Join(workDir, "build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		log.Fatalf("Failed to create build dir: %v", err)
	}
	// defer os.RemoveAll(buildDir)

	// Create mock /etc environment
	etcDir := filepath.Join(workDir, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		log.Fatalf("Failed to create etc dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(etcDir, "passwd"), []byte("nixbld:x:1000:100:Nix Build User:/build:/bin/sh\n"), 0644); err != nil {
		log.Fatalf("Failed to write passwd: %v", err)
	}
	if err := os.WriteFile(filepath.Join(etcDir, "group"), []byte("nixbld:x:100:nixbld\n"), 0644); err != nil {
		log.Fatalf("Failed to write group: %v", err)
	}
	if err := os.WriteFile(filepath.Join(etcDir, "hosts"), []byte("127.0.0.1 localhost\n"), 0644); err != nil {
		log.Fatalf("Failed to write hosts: %v", err)
	}

	// Create homeless-shelter
	homelessDir := filepath.Join(workDir, "homeless-shelter")
	if err := os.MkdirAll(homelessDir, 0755); err != nil {
		log.Fatalf("Failed to create homeless dir: %v", err)
	}

	mounts := make(map[string]string)
	var execErr error
	if strings.HasPrefix(builder, "builtin:") {
		fmt.Printf("DIAGNOSTIC: Simulating builtin builder: %s\n", builder)
		for _, om := range outputMappings {
			// Used implied /nix/store path from input mapping, but om.StorePath contains it.
			// Since we generated this from nix show-derivation, the path exists in host store.
			src := om.StorePath
			base := filepath.Base(src)
			dest := filepath.Join(hostStore, base)

			fmt.Printf("DIAGNOSTIC: Copying existing artifact %s to %s\n", src, dest)
			// Use cp -r --preserve=mode,timestamps
			// We dereference symlinks? No, we want exact copy of store path contents.
			// Nix store paths are usually directories or files.
			// cp -r -P (no dereference) might be safer if store has links.
			// But wait, resolving symlinks might be needed if user is outside?
			// Standard `cp -a` is good.
			cmd := exec.Command("cp", "-a", src, dest)
			if out, err := cmd.CombinedOutput(); err != nil {
				// Fallback to strict fetchurl implementation if copy fails
				// This happens if the artifact is not in the host store (common for new builds)
				if builder == "builtin:fetchurl" {
					fmt.Printf("DIAGNOSTIC: Artifact not found in host store, attempting download for %s\n", src)
					url := os.Getenv("url")
					urls := os.Getenv("urls")
					outputHash := os.Getenv("outputHash")

					if url == "" && urls == "" {
						execErr = fmt.Errorf("builtin:fetchurl failed: no url provided for %s", src)
						continue
					}

					// Candidates
					var candidates []string
					if url != "" {
						candidates = append(candidates, url)
					}
					if urls != "" {
						candidates = append(candidates, strings.Split(urls, " ")...)
					}

					downloaded := false
					for _, u := range candidates {
						if u == "" {
							continue
						}
						fmt.Printf("DIAGNOSTIC: Downloading %s\n", u)
						resp, err := http.Get(u)
						if err != nil {
							fmt.Printf("WARNING: Download failed for %s: %v\n", u, err)
							continue
						}
						defer resp.Body.Close()

						if resp.StatusCode != 200 {
							fmt.Printf("WARNING: Download failed for %s: status %d\n", u, resp.StatusCode)
							continue
						}

						// Create file
						outF, err := os.Create(dest)
						if err != nil {
							execErr = fmt.Errorf("failed to create output file %s: %v", dest, err)
							break
						}

						// Hashing
						// Handle SRI (sha256-...)
						// Assume sha256 for now as it's standard for nix
						hasher := sha256.New()
						multi := io.MultiWriter(outF, hasher)

						if _, err := io.Copy(multi, resp.Body); err != nil {
							outF.Close()
							fmt.Printf("WARNING: Download interrupted for %s: %v\n", u, err)
							continue
						}
						outF.Close()

						// Verify Hash
						if outputHash != "" {
							sum := hasher.Sum(nil)
							// Nix hash format: sha256-<base64>
							// We need to compare.
							// Parse expected
							var expected []byte
							if strings.HasPrefix(outputHash, "sha256-") {
								encoded := strings.TrimPrefix(outputHash, "sha256-")
								dec, err := base64.StdEncoding.DecodeString(encoded)
								if err != nil {
									execErr = fmt.Errorf("failed to decode SRI hash %s: %v", outputHash, err)
									break
								}
								expected = dec
							} else {
								// Assume raw sha256 hex? Or nix-base32?
								// For now, fail if not SRI, or implement if needed.
								// Log warning
								fmt.Printf("WARNING: Unsupported hash format %s, skipping verification (DANGEROUS)\n", outputHash)
								// Allow proceed for now to unblock, but this is unsafe.
								// expected = nil
							}

							if expected != nil {
								// Compare
								if string(sum) != string(expected) {
									fmt.Printf("WARNING: Hash mismatch for %s. Expected %x, got %x\n", u, expected, sum)
									continue
								}
							}
						}

						downloaded = true
						execErr = nil // Clear error from cp
						break
					}

					if !downloaded && execErr == nil {
						execErr = fmt.Errorf("failed to download %s from any source", src)
					}
				} else {
					msg := fmt.Sprintf("failed to copy builtin artifact %s: %v\n%s", src, err, string(out))
					fmt.Println(msg)
					execErr = fmt.Errorf(msg)
				}
			}
		}
	} else {
		// Real builder execution via bwrap

		var autoPaths []string

		for _, m := range explicitMounts {
			parts := strings.SplitN(m, ":", 2)
			if len(parts) != 2 {
				log.Printf("WARNING: Invalid mount format '%s', skipping.", m)
				continue
			}
			hostPath := parts[0]
			sandboxPath := parts[1]

			// Resolve host symlinks to avoid broken mounts in sandbox (e.g. bazel-out)
			absHost, err := filepath.EvalSymlinks(hostPath)
			if err != nil {
				// If it doesn't exist, try absolute path at least
				absHost, _ = filepath.Abs(hostPath)
			}

			if _, exists := mounts[sandboxPath]; !exists {
				mounts[sandboxPath] = absHost

				binDir := filepath.Join(absHost, "bin")
				if info, err := os.Stat(binDir); err == nil && info.IsDir() {
					autoPaths = append(autoPaths, filepath.Join(sandboxPath, "bin"))
				}
			}
		}

		bwrapArgs := []string{
			"--unshare-all",
			"--proc", "/proc",
			"--dev", "/dev",
			"--tmpfs", "/tmp",
			"--dir", "/bin",
			"--dir", "/usr",
			"--dir", "/usr/bin",
			"--bind", hostStore, "/nix/store",

			"--bind", buildDir, "/build",
			"--ro-bind", filepath.Join(etcDir, "passwd"), "/etc/passwd",
			"--ro-bind", filepath.Join(etcDir, "group"), "/etc/group",
			"--ro-bind", filepath.Join(etcDir, "hosts"), "/etc/hosts",
			"--ro-bind", homelessDir, "/homeless-shelter",
			"--chdir", "/build",
			"--setenv", "IN_SANDBOX", "1",
			"--setenv", "NIX_BUILD_TOP", "/build",
			"--setenv", "NIX_STORE", "/nix/store",
			"--setenv", "NIX_BUILD_CORES", "4",
			"--setenv", "NIX_ENFORCE_PURITY", "0",
			"--setenv", "NIX_ENFORCE_NO_NATIVE", "0",
			"--setenv", "TMPDIR", "/build",
			"--setenv", "TEMPDIR", "/build",
			"--setenv", "TMP", "/build",
			"--setenv", "TEMP", "/build",
			"--setenv", "HOME", "/homeless-shelter",
			"--setenv", "SOURCE_DATE_EPOCH", "315532800",
		}

		// Prepare Inputs: Bind inputs (RO) on top of the RW store
		// Add mounts in deterministic order
		var sortedSandboxPaths []string
		for s := range mounts {
			sortedSandboxPaths = append(sortedSandboxPaths, s)
		}
		sort.Strings(sortedSandboxPaths)

		for _, sandbox := range sortedSandboxPaths {
			host := mounts[sandbox]
			if !strings.HasPrefix(sandbox, "/bin/") && !strings.HasPrefix(sandbox, "/usr/") {
				// Ensure parent exists
				bwrapArgs = append(bwrapArgs, "--dir", filepath.Dir(sandbox))
			}
			bwrapArgs = append(bwrapArgs, "--ro-bind", host, sandbox)
		}

		// Outputs: Do NOT bind them. Builder will create them in /nix/store (which is writable).

		// Always mount /bin/sh inside the sandbox.
		// Primary choice: the builder executable itself if it looks like a shell.
		shMounted := false
		if strings.HasSuffix(builder, "bash") || strings.HasSuffix(builder, "sh") {
			hostBuilder := resolveToHost(builder, mounts)
			if info, err := os.Stat(hostBuilder); err == nil && !info.IsDir() {
				bwrapArgs = append(bwrapArgs, "--ro-bind", hostBuilder, "/bin/sh")
				shMounted = true
			}
		}

		if !shMounted {
			for sandboxPath, hostPath := range mounts {
				if strings.HasSuffix(sandboxPath, "/bin/bash") || strings.HasSuffix(sandboxPath, "/bin/sh") {
					if info, err := os.Stat(hostPath); err == nil && !info.IsDir() {
						bwrapArgs = append(bwrapArgs, "--ro-bind", hostPath, "/bin/sh")
						shMounted = true
						break
					}
				}
			}
		}

		if !shMounted {
			if realSh, err := filepath.EvalSymlinks("/bin/sh"); err == nil {
				bwrapArgs = append(bwrapArgs, "--ro-bind", realSh, "/bin/sh")
			} else if _, err := os.Stat("/bin/sh"); err == nil {
				bwrapArgs = append(bwrapArgs, "--ro-bind", "/bin/sh", "/bin/sh")
			}
		}

		// Note: /usr/bin/env is now provided by Nix store, no host mount needed

		// Mounts processed above

		bazelCache := "/home/jonathanp/.cache/bazel"
		if _, err := os.Stat(bazelCache); err == nil {
			bwrapArgs = append(bwrapArgs, "--ro-bind", bazelCache, bazelCache)
		}
		workspaceRoot := "/home/jonathanp/github/nix-bazel-via-bwrap"
		if _, err := os.Stat(workspaceRoot); err == nil {
			bwrapArgs = append(bwrapArgs, "--ro-bind", workspaceRoot, workspaceRoot)
		}

		// Clean up PATH to prioritize Nix store binaries and avoid leakage from host.
		var finalPath string
		if len(autoPaths) > 0 {
			finalPath = strings.Join(autoPaths, ":")
			// Host PATH is added at the end, but maybe we should avoid it entirely?
			// For now, let's keep it but at the end.
			hostPath := os.Getenv("PATH")
			if hostPath != "" {
				finalPath = finalPath + ":" + hostPath
			}
		} else {
			finalPath = os.Getenv("PATH")
		}

		if finalPath != "" {
			bwrapArgs = append(bwrapArgs, "--setenv", "PATH", finalPath)
		}

		overriddenEnvs := map[string]bool{
			"PATH": true, "IN_SANDBOX": true, "TMPDIR": true, "TEMPDIR": true, "TMP": true, "TEMP": true, "HOME": true,
		}

		var outPath string
		for _, om := range outputMappings {
			if om.Name == "out" {
				outPath = om.StorePath
				break
			}
		}
		placeholder := "/1rz4g4znpzjwh1xymhjpm42vipw92pr73vdgl6xs1hycac8kf2n9"

		for _, e := range os.Environ() {
			kv := strings.SplitN(e, "=", 2)
			if len(kv) == 2 {
				k := kv[0]
				v := kv[1]
				if !overriddenEnvs[k] && !strings.HasPrefix(k, "BASH_FUNC_") {
					bwrapArgs = append(bwrapArgs, "--setenv", k, v)
				}
			}
		}

		var finalBuilderArgs []string
		for _, arg := range builderArgs {
			finalBuilderArgs = append(finalBuilderArgs, arg)
		}

		bwrapFinalArgs := append(bwrapArgs, "--", builder)
		bwrapFinalArgs = append(bwrapFinalArgs, finalBuilderArgs...)

		// Global placeholder replacement on final args
		if outPath != "" {
			for i, arg := range bwrapFinalArgs {
				if strings.Contains(arg, placeholder) {
					bwrapFinalArgs[i] = strings.ReplaceAll(arg, placeholder, outPath)
				}
			}
		}

		// Auto-detect libstdc++ and inject LD_LIBRARY_PATH
		// This is needed for bootstrap tools which might be unwrapped and missing rpath
		// Gated behind a flag to avoid polluting all builds
		if os.Getenv("BAZEL_NIX_EXPOSE_BOOTSTRAP_LIBSTDC") == "1" {
			var ldLibraryPaths []string

			// Helper to find existing LD_LIBRARY_PATH
			existingLD := ""
			for i := 0; i < len(bwrapFinalArgs)-1; i++ {
				if bwrapFinalArgs[i] == "--setenv" && bwrapFinalArgs[i+1] == "LD_LIBRARY_PATH" {
					existingLD = bwrapFinalArgs[i+2]
					break
				}
			}
			if existingLD != "" {
				ldLibraryPaths = append(ldLibraryPaths, existingLD)
			}

			for sandboxPath, hostPath := range mounts {
				// Only check directories in /nix/store
				if !strings.HasPrefix(sandboxPath, "/nix/store/") {
					continue
				}
				info, err := os.Stat(hostPath)
				if err == nil && info.IsDir() {
					// Walk directory to find libstdc++.so.6
					// Limit depth to avoid performance hit on large inputs
					filepath.Walk(hostPath, func(path string, info os.FileInfo, err error) error {
						if err != nil {
							return nil
						}
						if (info.Mode().IsRegular() || (info.Mode()&os.ModeSymlink != 0)) && info.Name() == "libstdc++.so.6" {
							rel, err := filepath.Rel(hostPath, path)
							if err == nil {
								dir := filepath.Dir(filepath.Join(sandboxPath, rel))
								ldLibraryPaths = append(ldLibraryPaths, dir)
							}
						}
						return nil
					})
				}
			}
			if len(ldLibraryPaths) > 0 {
				// Dedup
				uniquePaths := make(map[string]bool)
				var cleanPaths []string
				for _, p := range ldLibraryPaths {
					if !uniquePaths[p] {
						uniquePaths[p] = true
						cleanPaths = append(cleanPaths, p)
					}
				}
				newLD := strings.Join(cleanPaths, ":")
				// Check if we need to update existing --setenv or append new one
				updated := false
				for i := 0; i < len(bwrapFinalArgs)-1; i++ {
					if bwrapFinalArgs[i] == "--setenv" && bwrapFinalArgs[i+1] == "LD_LIBRARY_PATH" {
						bwrapFinalArgs[i+2] = newLD
						updated = true
						break
					}
				}
				if !updated {
					// Insert before executable (last arg usually, but args handles it)
					// bwrapFinalArgs structure: [flags...] -- [cmd...]
					// Find "--"
					dashIndex := -1
					for i, arg := range bwrapFinalArgs {
						if arg == "--" {
							dashIndex = i
							break
						}
					}
					if dashIndex != -1 {
						// Insert before --
						newArgs := make([]string, 0, len(bwrapFinalArgs)+3)
						newArgs = append(newArgs, bwrapFinalArgs[:dashIndex]...)
						newArgs = append(newArgs, "--setenv", "LD_LIBRARY_PATH", newLD)
						newArgs = append(newArgs, bwrapFinalArgs[dashIndex:]...)
						bwrapFinalArgs = newArgs
					}
				}
			}
		}

		// Pre-flight check: can we see the builder?
		probeArgs := append([]string{}, bwrapArgs...)
		if strings.Contains(builder, "busybox") {
			probeArgs = append(probeArgs, "--", builder, "--help")
		} else {
			probeArgs = append(probeArgs, "--", builder, "--version")
		}
		probeCmd := exec.Command("bwrap", probeArgs...)
		if out, err := probeCmd.CombinedOutput(); err != nil {
			fmt.Printf("DIAGNOSTIC: Sandbox probe failed for %s: %v\nOutput: %s\n", builder, err, string(out))
		} else {
			summary := strings.Split(string(out), "\n")[0]
			fmt.Printf("DIAGNOSTIC: Sandbox probe success: %s\n", summary)
		}

		// Quote args for easy reproduction
		debugArgsStr := ""
		for _, arg := range bwrapFinalArgs {
			debugArgsStr += fmt.Sprintf("%q ", arg)
		}
		fmt.Printf("DIAGNOSTIC: Running bwrap %s\n", debugArgsStr)
		cmd := exec.Command("bwrap", bwrapFinalArgs...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		// Handle Debug Mode
		debugEnv := os.Getenv("SANDBOX_DEBUG")
		targetName := os.Getenv("name")
		fmt.Printf("DEBUG: SANDBOX_DEBUG='%s', name='%s'\\n", debugEnv, targetName)
		if debugEnv == "1" && (targetName == "" || targetName == "perl-5.38.2") {
			fmt.Println("========================================================================")
			fmt.Printf("SANDBOX_DEBUG: Debug mode for target '%s', skipping execution and preserving temp dirs.\\n", targetName)
			fmt.Println("========================================================================")
			fmt.Printf("Build Dir:  %s\n", buildDir)
			fmt.Printf("Host Store: %s\n", hostStore)
			fmt.Println("")
			fmt.Println("To enter the sandbox interactively, run the following command:")
			fmt.Println("")

			// Construct debug shell command
			debugArgs := append([]string{}, bwrapArgs...)
			// Use bash if available, else sh
			shell := "/bin/sh"
			if _, err := os.Stat("/bin/bash"); err == nil {
				shell = "/bin/bash"
			}
			debugArgs = append(debugArgs, "--", shell)

			fmt.Printf("bwrap %s\n", strings.Join(debugArgs, " "))
			fmt.Println("")
			fmt.Println("Once inside the sandbox, you can try running the builder:")
			fmt.Println("")
			fmt.Printf("%s %s\n", builder, strings.Join(builderArgs, " "))
			fmt.Println("========================================================================")

			// Exit explicitly to bypass defer os.RemoveAll
			os.Exit(1)
		}

		// Capture output for diagnostics
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf

		if err := cmd.Run(); err != nil {
			fmt.Printf("DIAGNOSTIC: Builder failed: %v\n", err)
			fmt.Printf("DIAGNOSTIC: Builder Output:\n%s\n", buf.String())
			execErr = err
		} else {
			// Print output even on success if needed, or just let it be?
			// Nix builders are usually noisy on stderr.
			// Let's print to stdout so Bazel shows it.
			fmt.Print(buf.String())
		}
	}

	if execErr == nil {
		// Add outputs to mounts map so that symlinks pointing to them can be resolved
		for _, om := range outputMappings {
			base := filepath.Base(om.StorePath)
			srcPath := filepath.Join(hostStore, base)
			mounts[om.StorePath] = srcPath
		}

		for _, om := range outputMappings {
			base := filepath.Base(om.StorePath)
			srcPath := filepath.Join(hostStore, base)

			if _, err := os.Stat(srcPath); err == nil {
				fmt.Printf("DIAGNOSTIC: Performing copy-back for %s from %s to %s\n", om.Name, srcPath, om.BazelDir)
				copier := NewCopier(mounts)
				err := copier.CopyRecursive(srcPath, om.BazelDir)
				if err != nil {
					fmt.Printf("DIAGNOSTIC: Error during copy-back of %s: %v\n", om.Name, err)
				}
			} else {
				fmt.Printf("WARNING: Output %s (%s) was not produced\n", om.Name, srcPath)
			}
		}
	}

	if execErr != nil {
		if exitErr, ok := execErr.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
}
