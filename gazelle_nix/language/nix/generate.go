package nix

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
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
	path := label
	if len(path) > 0 && path[0] == '@' {
		path = path[1:]
	}
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

	storePath, drvHash, err := resolveFlakeOutput(args.Config, args.Dir, nixpkgsOverride)
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
func resolveFlakeOutput(c *config.Config, dir string, nixpkgsOverride string) (storePath string, drvHash string, err error) {
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
	if nixpkgsOverride != "" {
		args = append(args, "--override-input", "nixpkgs", nixpkgsOverride)
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
					// log.Printf("DEBUG: Found nix-portable at %s", loc)
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
