package nix

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/JonathanPerry651/nix-bazel-via-bwrap/cache"
	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// GenerateArgs contains arguments for rule generation.
type GenerateArgs = language.GenerateArgs

// GenerateResult contains the result of rule generation.
type GenerateResult = language.GenerateResult

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

	// Determine if this is a single-executable package
	isSingleBinary, binaryName := detectSingleBinary(args.Config, args.Dir)

	// Update lockfile with NixpkgsCommit if specified
	if cfg.NixpkgsCommit != "" && l.lockFile != nil {
		l.mu.Lock()
		if l.lockFile.NixpkgsCommit != cfg.NixpkgsCommit {
			l.lockFile.NixpkgsCommit = cfg.NixpkgsCommit
			l.lockFile.Save(l.lockPath)
		}
		l.mu.Unlock()
	}

	// Resolve derivation to get output hash
	storePath, drvHash, err := resolveFlakeOutput(args.Config, args.Dir, cfg.NixpkgsCommit)
	if err != nil {
		log.Printf("Warning: failed to resolve derivation for %s: %v", args.Dir, err)
	} else {
		// Update lockfile
		label := "//" + args.Rel + ":default"
		if args.Rel == "" {
			label = "//:default"
		}

		var exePath string
		if isSingleBinary {
			exePath = "bin/" + binaryName
		}

		l.updateLockfile(label, storePath, drvHash, deps, exePath)
	}

	var rules []*rule.Rule
	if isSingleBinary && cfg.ExecutableMode != "disable" {
		// Generate sh_binary for single-executable packages
		r := rule.NewRule("sh_binary", binaryName)
		r.SetAttr("srcs", []string{"@" + cfg.CacheName + "//" + args.Rel + ":" + "bin/" + binaryName})
		if len(deps) > 0 {
			r.SetAttr("deps", deps)
		}
		rules = append(rules, r)
	} else {
		// Generate nix_package for libraries/multi-binary packages
		r := rule.NewRule("nix_package", "default")
		r.SetAttr("flake", "flake.nix")
		if len(deps) > 0 {
			r.SetAttr("deps", deps)
		}
		rules = append(rules, r)
	}

	imports := make([]interface{}, len(rules))
	for i := range rules {
		imports[i] = nil
	}

	return GenerateResult{
		Gen:     rules,
		Imports: imports,
	}
}

// resolveFlakeOutput runs nix show-derivation to find the output store path and derivation hash.
func resolveFlakeOutput(c *config.Config, dir string, nixpkgsCommit string) (storePath string, drvHash string, err error) {
	nixPortable := findNixPortable(c)
	if nixPortable == "" {
		return "", "", nil
	}

	// We default to the 'default' package for now
	args := []string{
		"nix",
		"--extra-experimental-features", "nix-command flakes",
		"show-derivation", dir + "#default",
	}
	if nixpkgsCommit != "" {
		args = append(args, "--override-input", "nixpkgs", "github:NixOS/nixpkgs/"+nixpkgsCommit)
	}

	cmd := exec.Command(nixPortable, args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			log.Printf("Warning: nix show-derivation failed: %s\nStderr: %s", err, exitErr.Stderr)
		}
		return "", "", err
	}

	// Parse map[string]Derivation
	var drvs map[string]struct {
		Outputs map[string]struct {
			Path string `json:"path"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(out, &drvs); err != nil {
		return "", "", err
	}

	// There should be one root derivation (or we take the first one)
	for path, drv := range drvs {
		if out, ok := drv.Outputs["out"]; ok {
			return out.Path, cache.StoreHash(path), nil
		}
	}
	return "", "", nil
}

// updateLockfile queries the cache and updates the lockfile.
func (l *nixLang) updateLockfile(label, storePath, drvHash string, deps []string, executable string) {
	if l.lockFile == nil {
		return
	}

	// Crawl closure
	closure, err := l.crawlClosure(storePath)
	if err != nil {
		log.Printf("Warning: failed to resolve closure for %s: %v", storePath, err)
		// Fallback: just add the store path itself if possible?
		// But AddFlake requires closure list.
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	l.lockFile.AddFlake(label, drvHash, storePath, executable, deps, closure)

	if err := l.lockFile.Save(l.lockPath); err != nil {
		log.Printf("Warning: failed to save lockfile: %v", err)
	}
}

// crawlClosure recursively finds dependencies in the cache and populates StorePaths in lockfile.
func (l *nixLang) crawlClosure(rootPath string) ([]string, error) {
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
		l.lockFile.AddStorePath(info)
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
	// Try common locations
	candidates := []string{
		filepath.Join(c.RepoRoot, "nix-portable"),
		filepath.Join(c.RepoRoot, "bazel-bin/external/nix_portable/file/nix-portable"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Fallback to system nix
		return p
	}
	log.Printf("Warning: nix not found in candidates or path")
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
