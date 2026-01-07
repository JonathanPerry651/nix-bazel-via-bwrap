package nix

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JonathanPerry651/nix-bazel-via-bwrap/cache"
	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"github.com/bazelbuild/rules_go/go/runfiles"
)

// resolveLabelToRunfiles converts a Bazel label (e.g. @repo//pkg:target) to an absolute runfiles path.
// It assumes the target is a file or directory mapped into runfiles.
func resolveLabelToRunfiles(label string) (string, error) {
	// Heuristic parsing of label to runfiles path:
	// @repo//pkg:target -> repo/pkg/target
	// //pkg:target -> <main_workspace>/pkg/target (TODO: cleaner main workspace detection)

	// For now, assume strict form: @repo//pkg:name for external, and relative for local?
	// Actually, Rlocation expects "repo/pkg/file".

	// Simplify: just strip '@' and Replace '//' with '/' and ':' with '/'
	// This is very rough but matches standard structure for file targets.
	// @nixpkgs//:src -> nixpkgs/src (if mapped correctly)

	// We only support external repos cleanly for now as per use case (@nixpkgs//...)
	// We only support external repos cleanly for now as per use case (@nixpkgs//...)
	path := strings.TrimLeft(label, "@")
	path = filepath.Clean(path)
	// Replace // with /
	// Replace : with /
	// This isn't perfect label parsing but often sufficient for filegroup/file references.

	// Better: use strings.Replace
	// path = strings.ReplaceAll(path, "//", "/")
	// path = strings.ReplaceAll(path, ":", "/")

	// Note: In runfiles, external repos are just top level dirs usually.
	// e.g. "nixpkgs/..."

	r, err := runfiles.New()
	if err != nil {
		return "", err
	}

	// Let's implement a very simple parser compatible with Rlocation expectations
	// Input: @nixpkgs//:src
	// Expect: nixpkgs/src (if that's where it is)
	// Actually, http_archive with generated build file...
	// If output is tree artifact, it's just the dir.

	// Using a manual simple transform for the prototype:
	// Remove leading @
	s := label
	if s[0] == '@' {
		s = s[1:]
	}
	// Replace // with /
	// Replace : with /
	// This converts @r//p:t -> r/p/t
	targetPath := ""
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '/' && i+1 < len(runes) && runes[i+1] == '/' {
			targetPath += "/"
			i++ // skip next /
		} else if runes[i] == ':' {
			targetPath += "/"
		} else {
			targetPath += string(runes[i])
		}
	}
	targetPath = filepath.Clean(targetPath)

	// log.Printf("DEBUG: resolving %q -> %q", label, targetPath)
	loc, err := r.Rlocation(targetPath)
	if err == nil {
		return checkFileOrDir(loc)
	}

	// Bzlmod repo name mangling support.
	// The label provided by the user (e.g. @nixpkgs) might be an apparent name,
	// but Rlocation expects a canonical name.
	// We try to resolve the repository part of the path using the _repo_mapping file.
	if err != nil {
		parts := strings.SplitN(targetPath, "/", 2)
		if len(parts) == 2 {
			repoName := parts[0]
			rest := parts[1]

			canonical, ok := resolveRepoName(r, repoName)
			if ok {
				remappedPath := canonical + "/" + rest
				loc, err := r.Rlocation(remappedPath)
				if err == nil {
					return checkFileOrDir(loc)
				}
			}
		}
	}

	if err != nil {
		log.Printf("Warning: Rlocation failed for %q: %v", targetPath, err)
		return "", err
	}
	return checkFileOrDir(loc)
}

func checkFileOrDir(path string) (string, error) {
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return filepath.Dir(path), nil
	}
	return path, nil
}

// resolveRepoName attempts to find the canonical name for a given repository name
// by inspecting the _repo_mapping file in the runfiles root.
func resolveRepoName(r *runfiles.Runfiles, repo string) (string, bool) {
	// Find _repo_mapping file.
	// It is located at the top of the runfiles tree.
	// We can guess its location relative to a known file or try Rlocation("_repo_mapping").
	// Rlocation("_repo_mapping") works if the tool itself includes it as data? Usually implicit.

	mappingPath, err := r.Rlocation("_repo_mapping")
	if err != nil {
		// Fallback: Use env var RUNFILES_DIR if available?
		// Or RUNFILES_MANIFEST_FILE and replace MANIFEST with _repo_mapping?
		manifest := os.Getenv("RUNFILES_MANIFEST_FILE")
		if manifest != "" {
			// manifest usually <...>/MANIFEST
			mappingPath = filepath.Join(filepath.Dir(manifest), "_repo_mapping")
			log.Printf("DEBUG: _repo_mapping not found via Rlocation, trying manifest sibling: %s", mappingPath)
		} else {
			log.Printf("DEBUG: _repo_mapping not found and no manifest env var")
			return "", false
		}
	} else {
		log.Printf("DEBUG: Found _repo_mapping via Rlocation at %s", mappingPath)
	}

	content, err := os.ReadFile(mappingPath)
	if err != nil {
		log.Printf("DEBUG: Failed to read _repo_mapping: %v", err)
		return "", false
	}

	// Format of _repo_mapping:
	// <source_repo>,<apparent_name>,<canonical_name>
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) >= 3 {
			// We assume we are looking up from the main repository context (source_repo == "")
			// or potentially the current module's context if we knew it.
			// Given we are running as a tool in the workspace root, "" is the most likely context
			// for user-provided labels like @nixpkgs.
			source := parts[0]
			apparent := parts[1]
			canonical := parts[2]

			// log.Printf("DEBUG: Mapping: %q [%q] -> %q", source, apparent, canonical)

			if source == "" && apparent == repo {
				// log.Printf("DEBUG: Resolved repo %q -> %q (canonical)", repo, canonical)
				return canonical, true
			}
		}
	}

	log.Printf("DEBUG: Repo %q not found in _repo_mapping", repo)
	return "", false
}

func splitLabel(l string) []string {
	return nil // unused placeholder
}

// GenerateArgs contains arguments for rule generation.
type GenerateArgs = language.GenerateArgs

// GenerateResult contains the result of rule generation.
type GenerateResult = language.GenerateResult

func (l *nixLang) getLockFile(path string) *cache.LockFile {
	l.mu.Lock()
	defer l.mu.Unlock()

	if lf, ok := l.lockFiles[path]; ok {
		return lf
	}

	lf, err := cache.LoadLockFile(path)
	if err != nil {
		log.Fatalf("failed to load lockfile %s: %v", path, err)
	}
	l.lockFiles[path] = lf
	return lf
}

// GenerateRules implements language.Language.GenerateRules.
// It discovers flake.nix files and generates nix_package or sh_binary rules.
func (l *nixLang) GenerateRules(args GenerateArgs) GenerateResult {
	cfg := GetNixConfig(args.Config)
	if !cfg.Enabled {
		return GenerateResult{}
	}

	// Check for flake.nix in this directory
	flakePath := filepath.Join(args.Dir, "flake.nix")
	if _, err := os.Stat(flakePath); os.IsNotExist(err) {
		return GenerateResult{}
	}

	// deps := inputsToDeps(inputs, args.Rel)
	var deps []string

	// Load the correct lockfile
	lf := l.getLockFile(cfg.LockPath)

	// Update lockfile with NixpkgsCommit if specified
	if cfg.NixpkgsCommit != "" && lf != nil {
		l.mu.Lock()
		if lf.NixpkgsCommit != cfg.NixpkgsCommit {
			lf.NixpkgsCommit = cfg.NixpkgsCommit
			lf.Save(cfg.LockPath)
		}
		l.mu.Unlock()
	}

	// Resolve derivation to get output hash
	nixpkgsOverride := ""
	if cfg.NixpkgsLabel != "" {
		if path, err := resolveLabelToRunfiles(cfg.NixpkgsLabel); err != nil {
			log.Printf("Warning: failed to resolve NixpkgsLabel %q: %v", cfg.NixpkgsLabel, err)
		} else {
			nixpkgsOverride = "path:" + path
		}
	} else if cfg.NixpkgsCommit != "" {
		nixpkgsOverride = "github:NixOS/nixpkgs/" + cfg.NixpkgsCommit
	}

	storePath, drvHash, env, err := resolveFlakeOutput(args.Config, args.Dir, nixpkgsOverride)
	if err != nil {
		log.Printf("Warning: failed to resolve derivation for %s: %v", args.Dir, err)
	}

	// For mkShell flakes, extract dependencies from PATH in env
	// instead of from the shell's own output store path
	cacheName := cfg.CacheName
	if cacheName == "" {
		cacheName = "nix_cache"
	}

	// Extract dependencies from ALL env vars by scanning for /nix/store paths
	// This captures PATH, JAVA_HOME, LD_LIBRARY_PATH, etc.
	storePathRe := regexp.MustCompile(`/nix/store/([a-z0-9]{32}-[^/:]+)`)
	seenPaths := make(map[string]bool)

	for _, value := range env {
		matches := storePathRe.FindAllStringSubmatch(value, -1)
		for _, m := range matches {
			storePath := "/nix/store/" + m[1]
			if seenPaths[storePath] {
				continue
			}
			seenPaths[storePath] = true

			// Look up in cache to add as dependency
			hash := cache.StoreHash(storePath)
			info, err := l.cacheClient.LookupNarInfo(hash)
			if err == nil && info != nil {
				depLabel := fmt.Sprintf("@%s//:s_%s", cacheName, hash)
				deps = append(deps, depLabel)
				// Also crawl this dependency's closure
				l.crawlClosure(lf, storePath)
			}
		}
	}

	// Update lockfile with flake info (without the shell's own store path as a required dep)
	label := "//" + args.Rel + ":default"
	if args.Rel == "" {
		label = "//:default"
	}
	l.updateLockfile(lf, label, storePath, drvHash, deps, "", env)

	// Save the correct lockfile
	if err := lf.Save(cfg.LockPath); err != nil {
		log.Fatalf("failed to save lockfile %s: %v", cfg.LockPath, err)
	}

	var rules []*rule.Rule

	// Only generate 'nix_package' named 'default'
	// Users define their own nix_flake_run_under targets
	pkgRule := rule.NewRule("nix_package", "default")
	pkgRule.SetAttr("flake", "flake.nix")
	pkgRule.SetAttr("output_path", storePath)
	pkgRule.SetAttr("visibility", []string{"//visibility:public"})
	if len(deps) > 0 {
		pkgRule.SetAttr("deps", deps)
	}
	if len(env) > 0 {
		pkgRule.SetAttr("env", env)
	}
	rules = append(rules, pkgRule)

	imports := make([]interface{}, len(rules))
	for i := range rules {
		imports[i] = nil
	}

	return GenerateResult{
		Gen:     rules,
		Imports: imports,
	}
}

// updateLockfile queries the cache and updates the lockfile.
func (l *nixLang) updateLockfile(lf *cache.LockFile, label string, storePath string, drvHash string, deps []string, executable string, env map[string]string) {
	if lf == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	// Query closure
	closure := []string{}
	// Only query closure if we have a valid store path and it's not a dummy
	if storePath != "" && strings.HasPrefix(storePath, "/nix/store") {
		c, err := l.crawlClosure(lf, storePath)
		if err != nil {
			log.Printf("Warning: failed to resolve closure for %s: %v", storePath, err)
			// Fallback: just add the store path itself if possible?
			// But AddFlake requires closure list.
		} else {
			closure = c
		}
	}

	lf.AddFlake(label, drvHash, storePath, executable, env, deps, closure)
}

// resolveFlakeOutput runs 'nix show-derivation' and 'nix print-dev-env'.
func resolveFlakeOutput(c *config.Config, dir, nixpkgsOverride string) (string, string, map[string]string, error) {
	runNix := func(args ...string) ([]byte, error) {
		// Heuristic to finding 'nix' or 'nix-portable'
		// If we use 'findNixPortable', it returns a path found in runfiles or PATH.
		bin := findNixPortable(c)
		if bin == "" {
			return nil, fmt.Errorf("nix binary not found")
		}

		// If bin ends in "nix-portable", assumes it needs "nix" as first arg for some commands?
		// Actually for "show-derivation" and "print-dev-env" we need "nix".
		// If bin IS "nix", we run "nix args...".
		// If bin Is "nix-portable", we run "nix-portable nix args..."?
		// Based on my research: YES.

		realArgs := args
		if strings.Contains(filepath.Base(bin), "nix-portable") {
			realArgs = append([]string{"nix"}, args...)
		}

		cmd := exec.Command(bin, realArgs...)
		cmd.Dir = dir
		cmd.Env = os.Environ()

		out, err := cmd.Output()
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return out, fmt.Errorf("command %s %v failed: %w\nStderr: %s", bin, realArgs, err, exitErr.Stderr)
			}
			return out, fmt.Errorf("command %s %v failed: %w", bin, realArgs, err)
		}
		return out, nil
	}

	extraArgs := []string{"--extra-experimental-features", "nix-command flakes"}
	if nixpkgsOverride != "" {
		extraArgs = append(extraArgs, "--override-input", "nixpkgs", nixpkgsOverride)
	}

	// 1. Show Derivation
	args1 := append([]string{"derivation", "show"}, extraArgs...)
	args1 = append(args1, ".#default")

	drvOut, err := runNix(args1...)
	if err != nil {
		return "", "", nil, err
	}

	var drvData map[string]interface{}
	if err := json.Unmarshal(drvOut, &drvData); err != nil {
		return "", "", nil, fmt.Errorf("failed to parse derivation: %w", err)
	}

	var drvHash, storePath string
	for k, v := range drvData {
		drvHash = k
		outputs := v.(map[string]interface{})["outputs"].(map[string]interface{})
		if out, ok := outputs["out"]; ok {
			storePath = out.(map[string]interface{})["path"].(string)
		}
		break
	}

	// 2. Print Dev Env
	args2 := append([]string{"print-dev-env", "--json"}, extraArgs...)
	args2 = append(args2, ".#default")

	envOut, err := runNix(args2...)
	envMap := make(map[string]string)
	if err == nil {
		var envData struct {
			Variables map[string]struct {
				Type  string      `json:"type"`
				Value interface{} `json:"value"`
			} `json:"variables"`
		}
		if err := json.Unmarshal(envOut, &envData); err == nil {
			for k, v := range envData.Variables {
				if v.Type == "exported" {
					if strVal, ok := v.Value.(string); ok {
						envMap[k] = strVal
					}
				}
			}
		} else {
			log.Printf("Warning: failed to unmarshal env output: %v", err)
		}
	} else {
		log.Printf("Warning: failed to print-dev-env: %v", err)
	}

	return storePath, drvHash, envMap, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// crawlClosure recursively finds dependencies in the cache and populates StorePaths in lockfile.
func (l *nixLang) crawlClosure(lf *cache.LockFile, rootPath string) ([]string, error) {
	queue := []string{rootPath}
	visited := make(map[string]bool)
	var closure []string

	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]

		if visited[p] {
			continue
		}
		visited[p] = true
		closure = append(closure, p)

		// Lookup NarInfo
		hash := cache.StoreHash(p)
		info, err := l.cacheClient.LookupNarInfo(hash)
		if err != nil {
			log.Printf("Warning: error looking up %s: %v", p, err)
			continue
		}
		if info == nil {
			log.Printf("Warning: %s not found in cache", p)
			// TODO: Handle uncached paths (e.g. local build required)
			continue
		}

		// Add to LockFile
		l.mu.Lock()
		lf.AddStorePath(info)
		l.mu.Unlock()

		// Enqueue References
		// info.References are typically basenames in NarInfo from cache
		for _, ref := range info.References {
			fullRef := ref
			if len(ref) > 0 && ref[0] != '/' {
				fullRef = "/nix/store/" + ref
			}

			// Avoid self-ref loop if listed
			if fullRef != p && !visited[fullRef] {
				queue = append(queue, fullRef)
			}
		}
	}
	return closure, nil
}

// findNixPortable locates the nix-portable binary.
func findNixPortable(c *config.Config) string {
	r, err := runfiles.New()
	if err == nil {
		// http_file rule "@nix_portable" creates a target "@nix_portable//file"
		// which usually maps to "nix_portable/file/nix-portable" in runfiles (depending on mapping).
		// Try resolving likely labels.
		candidates := []string{
			"nix_portable/file/nix-portable",                     // Default module mapping
			"nix_bazel_via_bwrap/nix_portable/file/nix-portable", // If rooted in main repo
			// "nix_portable/nix-portable", // sometimes simplified?
		}

		for _, p := range candidates {
			loc, err := r.Rlocation(p)
			if err == nil {
				// Check simple stat
				if _, err := os.Stat(loc); err == nil {
					return loc
				}
			}
		}
	}

	// Fallback to system nix
	if p, err := exec.LookPath("nix"); err == nil {
		return p
	}
	log.Printf("Warning: nix not found in runfiles or path")
	return ""
}

// detectSingleBinary checks if the package produces a single executable.
// This is a heuristic - in practice we'd query the derivation or NAR listing.
func detectSingleBinary(c *config.Config, dir string) (bool, string) {
	// TODO: Implement actual detection by querying cache or derivation output
	// For now, use a simple heuristic based on package name
	name := filepath.Base(dir)
	knownSingleBinaries := map[string]bool{
		"hello": true,
		"curl":  true,
		"jq":    true,
	}
	return knownSingleBinaries[name], name
}
