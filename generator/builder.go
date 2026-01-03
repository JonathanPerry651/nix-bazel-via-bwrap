package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bazelbuild/buildtools/build"
)

// BazelGenerator generates Bazel build files from a Nix Graph
type BazelGenerator struct {
	Indexer         *NixpkgsIndexer
	Copier          *SourceCopier
	NixPortablePath string
}

func NewBazelGenerator(indexer *NixpkgsIndexer, copier *SourceCopier, nixPortablePath string) *BazelGenerator {
	return &BazelGenerator{
		Indexer:         indexer,
		Copier:          copier,
		NixPortablePath: nixPortablePath,
	}
}

// GeneratedArtifacts holds the content of files to be written
type GeneratedArtifacts struct {
	BuildFile         []byte
	NixSourcesBuild   []byte
	NixDepsBzl        []byte
	NixDepsUseRepoBzl []byte
}

func (g *BazelGenerator) Generate(graph Graph, packageName string) (*GeneratedArtifacts, error) {
	// Map output paths to target names for dependency resolution
	storePathToTarget := make(map[string]string)
	// Map output paths to external labels (for http_file)
	storePathToExternalLabel := make(map[string]string)

	var httpFiles []HttpFileDef

	sortedDrvs := make([]string, 0, len(graph))
	for k := range graph {
		sortedDrvs = append(sortedDrvs, k)
	}
	sort.Strings(sortedDrvs)

	// First pass: register outputs to targets AND identify fetchurl
	for _, drvPath := range sortedDrvs {
		drv := graph[drvPath]
		name := strings.TrimSuffix(filepath.Base(drvPath), ".drv")

		if drv.Builder == "builtin:fetchurl" {
			httpFile := g.processFetchurl(name, drv)

			label := fmt.Sprintf("@%s//file", httpFile.Name)
			// Record outputs mapping to this external label
			for _, outDef := range drv.Outputs {
				storePathToExternalLabel[outDef.Path] = label
			}

			httpFiles = append(httpFiles, httpFile)
			continue // Skip adding to storePathToTarget
		}

		for _, outDef := range drv.Outputs {
			storePathToTarget[outDef.Path] = name
		}
	}

	// Prepare nix_sources directory mapping logic
	// We won't create directory here, we just need to track what to copy during rule generation if we were doing side effects.
	// But wait, the original code copied files WHILE generating rules.
	// We should probably keep that side effect in the generator for now or defer it.
	// The tasks says "Refactor", so let's keep side-effects but use the Copier.

	if err := os.MkdirAll("nix_sources", 0755); err != nil {
		return nil, fmt.Errorf("failed to create nix_sources: %v", err)
	}

	sourceFiles := make(map[string]bool)
	var rules []build.Expr

	rules = append(rules, &build.CallExpr{
		X: &build.Ident{Name: "load"},
		List: []build.Expr{
			&build.StringExpr{Value: "//:rules.bzl"},
			&build.StringExpr{Value: "nix_derivation"},
		},
	})

	for _, drvPath := range sortedDrvs {
		drv := graph[drvPath]

		if drv.Builder == "builtin:fetchurl" {
			continue
		}

		name := strings.TrimSuffix(filepath.Base(drvPath), ".drv")

		ruleExpr, err := g.generateRule(name, drv, storePathToTarget, storePathToExternalLabel, sourceFiles)
		if err != nil {
			return nil, err
		}
		rules = append(rules, ruleExpr)
	}

	// Add alias for the requested package
	if packageName != "" {
		aliasRule := g.generateAliasForPackage(graph, packageName, sortedDrvs)
		if aliasRule != nil {
			rules = append(rules, aliasRule)
		}
	}

	// Generate artifacts
	mainBuildFile := &build.File{Type: build.TypeBuild, Stmt: rules}

	nixSourcesBuild := g.generateNixSourcesBuild(sourceFiles)
	nixDepsBzl := g.generateNixDepsBzl(httpFiles)
	nixDepsUseRepo := g.generateNixDepsUseRepo(httpFiles)

	return &GeneratedArtifacts{
		BuildFile:         []byte(build.Format(mainBuildFile)),
		NixSourcesBuild:   nixSourcesBuild,
		NixDepsBzl:        nixDepsBzl,
		NixDepsUseRepoBzl: nixDepsUseRepo,
	}, nil
}

func (g *BazelGenerator) processFetchurl(name string, drv Derivation) HttpFileDef {
	var urls []string
	if url := drv.Env["url"]; url != "" {
		urls = append(urls, url)
	}
	if urlList, ok := drv.Env["urls"]; ok && urlList != "" {
		for _, u := range strings.Split(urlList, " ") {
			if u != "" {
				urls = append(urls, u)
			}
		}
	}

	// Deduplicate
	seen := make(map[string]bool)
	uniqueUrls := []string{}
	for _, u := range urls {
		if !seen[u] {
			seen[u] = true
			uniqueUrls = append(uniqueUrls, u)
		}
	}

	// Mirror logic expansion
	finalUrls := []string{}
	for _, u := range uniqueUrls {
		finalUrls = append(finalUrls, u)

		// GNU
		if strings.Contains(u, "ftp.gnu.org/pub/gnu/") || strings.Contains(u, "ftpmirror.gnu.org/") {
			suffix := ""
			if idx := strings.Index(u, "ftp.gnu.org/pub/gnu/"); idx != -1 {
				suffix = u[idx+len("ftp.gnu.org/pub/gnu/"):]
			} else if idx := strings.Index(u, "ftpmirror.gnu.org/"); idx != -1 {
				suffix = u[idx+len("ftpmirror.gnu.org/"):]
			}
			if suffix != "" {
				for _, m := range KnownMirrors["gnu"] {
					if strings.HasPrefix(m, "http") {
						finalUrls = append(finalUrls, m+suffix)
					}
				}
			}
		}

		// Savannah
		if strings.Contains(u, "download.savannah.gnu.org/releases/") {
			suffix := u[strings.Index(u, "download.savannah.gnu.org/releases/")+len("download.savannah.gnu.org/releases/"):]
			for _, m := range KnownMirrors["savannah"] {
				if strings.HasPrefix(m, "http") {
					finalUrls = append(finalUrls, m+suffix)
				}
			}
		}

		// Config.sub git fallback
		if strings.Contains(u, "git.savannah.gnu.org") && strings.HasPrefix(u, "https://") {
			finalUrls = append(finalUrls, strings.Replace(u, "https://", "http://", 1))
		}
	}

	// Re-deduplicate
	seenFinal := make(map[string]bool)
	uniqueFinalUrls := []string{}
	for _, u := range finalUrls {
		if !seenFinal[u] {
			seenFinal[u] = true
			uniqueFinalUrls = append(uniqueFinalUrls, u)
		}
	}
	sort.Strings(uniqueFinalUrls)

	url := ""
	if len(uniqueUrls) > 0 {
		url = uniqueUrls[0]
	}

	// Hash conversion
	hash := drv.Env["outputHash"]
	if hash != "" && !strings.HasPrefix(hash, "sha256-") && len(hash) == 52 {
		cmd := exec.Command(g.NixPortablePath, "nix", "hash", "to-sri", "--type", "sha256", hash)
		out, err := cmd.Output()
		if err == nil {
			hash = strings.TrimSpace(string(out))
		} else {
			log.Printf("Warning: Failed to convert hash %s for %s: %v", hash, name, err)
		}
	}

	// Patching config.sub/guess
	if strings.Contains(name, "config_sub") || strings.Contains(strings.Join(uniqueFinalUrls, " "), "config.sub") {
		wd, _ := os.Getwd()
		fallbackPath := filepath.Join(wd, "config.sub.fallback")
		log.Printf("PATCH: Overriding config.sub with local fallback: %s", fallbackPath)
		uniqueFinalUrls = []string{"file://" + fallbackPath}
		hash = "sha256-+AMuVyjE/DmpR2D6jkQBUetCaqYIa4C54W8HXXeo2Lk="
	} else if strings.Contains(name, "config_guess") || strings.Contains(strings.Join(uniqueFinalUrls, " "), "config.guess") {
		wd, _ := os.Getwd()
		fallbackPath := filepath.Join(wd, "config.guess.fallback")
		log.Printf("PATCH: Overriding config.guess with local fallback: %s", fallbackPath)
		uniqueFinalUrls = []string{"file://" + fallbackPath}
		hash = "sha256-xqOgjH1EwR9bBJ+SHNNm6VFeqjNVNj9ZtrvaqqSlXmk="
	}

	// Recursive hash prefetch logic (side effect!)
	mode := drv.Env["outputHashMode"]
	if mode == "recursive" {
		log.Printf("WARNING: Recursive hash detected for %s. Prefetching to calculate flat hash...", name)
		tmpFile, err := os.CreateTemp(os.Getenv("TMPDIR"), "prefetch-*")
		if err == nil {
			tmpName := tmpFile.Name()
			tmpFile.Close()
			defer os.Remove(tmpName)

			log.Printf("Downloading %s to %s", url, tmpName)
			dlCmd := exec.Command("curl", "-L", "-o", tmpName, url)
			if err := dlCmd.Run(); err != nil {
				log.Printf("Failed to download %s: %v. Keeping recursive hash (will likely fail).", url, err)
			} else {
				hashCmd := exec.Command(g.NixPortablePath, "nix", "hash", "file", "--sri", "--type", "sha256", tmpName)
				out, err := hashCmd.Output()
				if err == nil {
					newHash := strings.TrimSpace(string(out))
					log.Printf("Calculated flat hash for %s: %s (replacing %s)", name, newHash, hash)
					hash = newHash
				} else {
					log.Printf("Failed to calc hash: %v", err)
				}
			}
		}
	}

	filename := ""
	if url != "" {
		filename = filepath.Base(url)
	}
	if filename == "" || filename == "." || filename == "/" {
		if n, ok := drv.Env["name"]; ok {
			filename = n
		}
	}

	sanitized := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, filename)
	repoName := "nix_source_" + sanitized

	return HttpFileDef{
		Name:       repoName,
		URLs:       uniqueFinalUrls,
		Sha256:     hash,
		Path:       filename,
		Executable: drv.Env["executable"] == "1",
	}
}

func (g *BazelGenerator) generateRule(name string, drv Derivation, storePathToTarget, storePathToExternalLabel map[string]string, sourceFiles map[string]bool) (build.Expr, error) {
	args := []build.Expr{}
	for _, arg := range drv.Args {
		args = append(args, &build.StringExpr{Value: arg})
	}

	envKeys := make([]string, 0, len(drv.Env))
	for k := range drv.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	envList := []*build.KeyValueExpr{}
	for _, k := range envKeys {
		envList = append(envList, &build.KeyValueExpr{
			Key:   &build.StringExpr{Value: k},
			Value: &build.StringExpr{Value: drv.Env[k]},
		})
	}

	dependencies := make(map[string]bool)
	scanForStorePaths := func(s string) {
		parts := strings.Split(s, "/nix/store/")
		for i := 1; i < len(parts); i++ {
			pathPart := parts[i]
			end := strings.IndexAny(pathPart, "/ \t\n\"';")
			if end == -1 {
				end = len(pathPart)
			}
			fullPath := "/nix/store/" + pathPart[:end]
			dependencies[fullPath] = true
		}
	}
	scanForStorePaths(drv.Builder)
	for _, a := range drv.Args {
		scanForStorePaths(a)
	}
	for _, v := range drv.Env {
		scanForStorePaths(v)
	}
	for _, src := range drv.InputSrcs {
		dependencies[src] = true
	}

	srcs := []build.Expr{}
	sourceMappings := []*build.KeyValueExpr{}
	storeNames := []*build.KeyValueExpr{}

	resKeys := make([]string, 0, len(dependencies))
	for k := range dependencies {
		resKeys = append(resKeys, k)
	}
	sort.Strings(resKeys)

	for _, depPath := range resKeys {
		if targetName, ok := storePathToTarget[depPath]; ok {
			if targetName != name {
				srcs = append(srcs, &build.StringExpr{Value: ":" + targetName})
			}
		} else if extLabel, ok := storePathToExternalLabel[depPath]; ok {
			srcs = append(srcs, &build.StringExpr{Value: extLabel})
			sourceMappings = append(sourceMappings, &build.KeyValueExpr{
				Key:   &build.StringExpr{Value: extLabel},
				Value: &build.StringExpr{Value: depPath},
			})
		} else {
			// Source file logic common extracted?
			// Copying happens here
			hashName := filepath.Base(depPath)
			var cleanName string
			parts := strings.SplitN(hashName, "-", 2)
			if len(parts) == 2 && len(parts[0]) >= 32 {
				cleanName = parts[1]
			} else {
				cleanName = hashName
			}

			if relPath, found := g.Indexer.Find(cleanName); found {
				label := fmt.Sprintf("@nixpkgs//:%s", relPath)
				srcs = append(srcs, &build.StringExpr{Value: label})
				sourceMappings = append(sourceMappings, &build.KeyValueExpr{
					Key:   &build.StringExpr{Value: label},
					Value: &build.StringExpr{Value: depPath},
				})
			} else {
				isBinaryOrLib := false
				if strings.Contains(depPath, "-glibc-") ||
					strings.Contains(depPath, "-gcc-") ||
					strings.Contains(depPath, "-coreutils-") ||
					strings.Contains(depPath, "/lib/") ||
					strings.Contains(depPath, "/bin/") {
					if !strings.HasSuffix(depPath, ".patch") && !strings.HasSuffix(depPath, ".diff") {
						isBinaryOrLib = true
					}
				}

				if !isBinaryOrLib {
					strictName := strings.Map(func(r rune) rune {
						if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' {
							return r
						}
						return '_'
					}, hashName)

					destPath := filepath.Join("nix_sources", strictName)
					if _, err := os.Stat(destPath); os.IsNotExist(err) {
						if err := g.Copier.Copy(depPath, destPath); err != nil {
							log.Printf("Warning: Failed to copy source %s (not in nixpkgs): %v", depPath, err)
						}
					}

					sourceFiles[strictName] = true
					label := fmt.Sprintf("//nix_sources:%s", strictName)
					srcs = append(srcs, &build.StringExpr{Value: label})

					sourceMappings = append(sourceMappings, &build.KeyValueExpr{
						Key:   &build.StringExpr{Value: label},
						Value: &build.StringExpr{Value: depPath},
					})
				} else {
					log.Printf("Skipping copy of binary/system dependency: %s", depPath)
				}
			}
		}
	}

	outKeys := make([]string, 0, len(drv.Outputs))
	for k := range drv.Outputs {
		outKeys = append(outKeys, k)
	}
	sort.Strings(outKeys)
	for _, k := range outKeys {
		path := drv.Outputs[k].Path
		storeNames = append(storeNames, &build.KeyValueExpr{
			Key:   &build.StringExpr{Value: k},
			Value: &build.StringExpr{Value: filepath.Base(path)},
		})
	}

	return &build.CallExpr{
		X: &build.Ident{Name: "nix_derivation"},
		List: []build.Expr{
			&build.AssignExpr{LHS: &build.Ident{Name: "name"}, Op: "=", RHS: &build.StringExpr{Value: name}},
			&build.AssignExpr{LHS: &build.Ident{Name: "builder_path"}, Op: "=", RHS: &build.StringExpr{Value: drv.Builder}},
			&build.AssignExpr{LHS: &build.Ident{Name: "args"}, Op: "=", RHS: &build.ListExpr{List: args}},
			&build.AssignExpr{LHS: &build.Ident{Name: "env"}, Op: "=", RHS: &build.DictExpr{List: envList}},
			&build.AssignExpr{LHS: &build.Ident{Name: "srcs"}, Op: "=", RHS: &build.ListExpr{List: srcs}},
			&build.AssignExpr{LHS: &build.Ident{Name: "store_names"}, Op: "=", RHS: &build.DictExpr{List: storeNames}},
			&build.AssignExpr{LHS: &build.Ident{Name: "source_mappings"}, Op: "=", RHS: &build.DictExpr{List: sourceMappings}},
		},
	}, nil
}

func (g *BazelGenerator) generateNixSourcesBuild(sourceFiles map[string]bool) []byte {
	sortedSources := make([]string, 0, len(sourceFiles))
	for f := range sourceFiles {
		sortedSources = append(sortedSources, f)
	}
	sort.Strings(sortedSources)

	rule := &build.CallExpr{
		X: &build.Ident{Name: "exports_files"},
		List: []build.Expr{
			&build.ListExpr{List: func() []build.Expr {
				list := []build.Expr{}
				for _, f := range sortedSources {
					list = append(list, &build.StringExpr{Value: f})
				}
				return list
			}()},
		},
	}
	f := &build.File{Type: build.TypeBuild, Stmt: []build.Expr{rule}}
	return []byte(build.Format(f))
}

func (g *BazelGenerator) generateNixDepsBzl(httpFiles []HttpFileDef) []byte {
	depsDict := &build.DictExpr{}

	httpKeys := make([]string, 0, len(httpFiles))
	for _, h := range httpFiles {
		httpKeys = append(httpKeys, h.Name)
	}
	sort.Strings(httpKeys)

	// Build map to lookup def by name (redundant loop but ensures sort order safety)
	filesByName := make(map[string]HttpFileDef)
	for _, h := range httpFiles {
		filesByName[h.Name] = h
	}

	for _, name := range httpKeys {
		h := filesByName[name]
		urlList := []build.Expr{}
		for _, u := range h.URLs {
			urlList = append(urlList, &build.StringExpr{Value: u})
		}

		itemDict := &build.DictExpr{List: []*build.KeyValueExpr{
			{Key: &build.StringExpr{Value: "urls"}, Value: &build.ListExpr{List: urlList}},
		}}

		if strings.HasPrefix(h.Sha256, "sha256-") {
			itemDict.List = append(itemDict.List, &build.KeyValueExpr{Key: &build.StringExpr{Value: "integrity"}, Value: &build.StringExpr{Value: h.Sha256}})
		} else if h.Sha256 != "" {
			itemDict.List = append(itemDict.List, &build.KeyValueExpr{Key: &build.StringExpr{Value: "sha256"}, Value: &build.StringExpr{Value: h.Sha256}})
		}

		if h.Path != "" {
			itemDict.List = append(itemDict.List, &build.KeyValueExpr{Key: &build.StringExpr{Value: "downloaded_file_path"}, Value: &build.StringExpr{Value: h.Path}})
		}
		if h.Executable {
			itemDict.List = append(itemDict.List, &build.KeyValueExpr{Key: &build.StringExpr{Value: "executable"}, Value: &build.Ident{Name: "True"}})
		}

		depsDict.List = append(depsDict.List, &build.KeyValueExpr{
			Key:   &build.StringExpr{Value: h.Name},
			Value: itemDict,
		})
	}

	depsFile := &build.File{
		Type: build.TypeDefault,
		Stmt: []build.Expr{
			&build.AssignExpr{LHS: &build.Ident{Name: "NIX_DEPS"}, Op: "=", RHS: depsDict},
		},
	}
	return []byte(build.Format(depsFile))
}

func (g *BazelGenerator) generateNixDepsUseRepo(httpFiles []HttpFileDef) []byte {
	var useRepoLines []string
	useRepoLines = append(useRepoLines, "nix_deps = use_extension(\"//:extensions.bzl\", \"nix_deps_ext\")")

	httpKeys := make([]string, 0, len(httpFiles))
	for _, h := range httpFiles {
		httpKeys = append(httpKeys, h.Name)
	}
	sort.Strings(httpKeys)

	for _, name := range httpKeys {
		useRepoLines = append(useRepoLines, fmt.Sprintf("use_repo(nix_deps, \"%s\")", name))
	}
	return []byte(strings.Join(useRepoLines, "\n"))
}

// generateAliasForPackage generates an alias rule for the requested package.
// It finds the derivation whose pname matches the requested package and creates
// an alias from the short name to the full hash-prefixed target name.
func (g *BazelGenerator) generateAliasForPackage(graph Graph, packageName string, sortedDrvs []string) build.Expr {
	// Find the derivation with matching pname (the main output, not source tarballs etc.)
	for _, drvPath := range sortedDrvs {
		drv := graph[drvPath]

		// Skip fetchurl derivations
		if drv.Builder == "builtin:fetchurl" {
			continue
		}

		pname := drv.Env["pname"]
		if pname == packageName {
			// This is our package! Extract the target name
			targetName := strings.TrimSuffix(filepath.Base(drvPath), ".drv")

			// Generate the alias rule:
			// alias(
			//     name = "hello",
			//     actual = ":72pl0rs7xi7vsniia10p7q8vl7f36xaw-hello-2.12.2",
			// )
			return &build.CallExpr{
				X: &build.Ident{Name: "alias"},
				List: []build.Expr{
					&build.AssignExpr{LHS: &build.Ident{Name: "name"}, Op: "=", RHS: &build.StringExpr{Value: packageName}},
					&build.AssignExpr{LHS: &build.Ident{Name: "actual"}, Op: "=", RHS: &build.StringExpr{Value: ":" + targetName}},
				},
			}
		}
	}

	// Also check for version-suffixed pname (e.g., pname="hello", but name includes version)
	// This is already handled by the primary loop since we match on pname directly.

	log.Printf("Warning: Could not find derivation with pname='%s' to create alias", packageName)
	return nil
}
