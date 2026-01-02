package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/bazelbuild/rules_go/go/runfiles"
)

// NixpkgsIndexer indexes files within the nixpkgs repository for mapping
type NixpkgsIndexer struct {
	Index map[string]string
}

func NewNixpkgsIndexer() *NixpkgsIndexer {
	indexer := &NixpkgsIndexer{
		Index: make(map[string]string),
	}
	indexer.index()
	return indexer
}

// Find looks up a filename in the index and returns its relative path if found
func (idx *NixpkgsIndexer) Find(filename string) (string, bool) {
	rel, ok := idx.Index[filename]
	return rel, ok
}

func (idx *NixpkgsIndexer) index() {
	r, err := runfiles.New()
	if err != nil {
		log.Printf("Warning: Failed to load runfiles: %v", err)
		return
	}

	// Try multiple potential locations for nixpkgs
	var nixpkgsPath string
	candidates := []string{"nixpkgs", "external/nixpkgs", "nix_bazel_via_bwrap/external/nixpkgs"}

	valid := false
	for _, cand := range candidates {
		path, err := r.Rlocation(cand)
		if err == nil {
			// Check if exists/is dir
			info, sErr := os.Stat(path)
			if sErr == nil && info.IsDir() {
				nixpkgsPath = path
				valid = true
				break
			}
		}
	}

	if !valid {
		// Try hardcoded relative just in case
		// In bazel run, runfiles are usually at <binary>.runfiles/
		pathToCheck := "external/nixpkgs"
		if _, err := os.Stat(pathToCheck); err == nil {
			nixpkgsPath = pathToCheck
			valid = true
		}
	}

	if !valid {
		log.Printf("Warning: Failed to find nixpkgs in runfiles. Tried: %v", candidates)
		return
	}

	log.Printf("Indexing nixpkgs at: %s", nixpkgsPath)

	filepath.Walk(nixpkgsPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(nixpkgsPath, path)
		if err != nil {
			return nil
		}

		name := filepath.Base(path)
		idx.Index[name] = rel
		return nil
	})

	log.Printf("Indexed %d files in nixpkgs", len(idx.Index))

	// DEBUG: Dump index to file for inspection
	// f, _ := os.Create("nixpkgs_index.txt")
	// defer f.Close()
	// for k, v := range idx.Index {
	// 	fmt.Fprintf(f, "%s -> %s\n", k, v)
	// }
}
