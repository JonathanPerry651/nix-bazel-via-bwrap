// Package nix implements a Gazelle language extension for Nix flakes.
//
// It discovers flake.nix files in the workspace and generates appropriate
// Bazel BUILD rules for them.
package nix

import (
	"flag"
	"sync"

	"github.com/JonathanPerry651/nix-bazel-via-bwrap/cache"
	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

const nixName = "nix"

// nixLang implements language.Language for Nix flakes.
type nixLang struct {
	mu          sync.Mutex
	cacheClient *cache.Cache
	lockFile    *cache.LockFile
	lockPath    string
	initialized bool
}

// NewLanguage returns a new Nix language extension for Gazelle.
func NewLanguage() language.Language {
	return &nixLang{
		cacheClient: cache.New(""), // Use default cache URL
	}
}

// Name returns the name of the language.
func (*nixLang) Name() string { return nixName }

// Kinds returns the kinds of rules this language generates.
func (*nixLang) Kinds() map[string]rule.KindInfo {
	return map[string]rule.KindInfo{
		"nix_package": {
			MatchAny: false,
			NonEmptyAttrs: map[string]bool{
				"flake": true,
			},
			MergeableAttrs: map[string]bool{
				"deps": true,
			},
		},
		"sh_binary": {
			MatchAny: false,
			NonEmptyAttrs: map[string]bool{
				"srcs": true,
			},
			MergeableAttrs: map[string]bool{
				"deps": true,
			},
		},
	}
}

// Loads returns the load statements needed for rules this language generates.
func (*nixLang) Loads() []rule.LoadInfo {
	return []rule.LoadInfo{
		{
			Name:    "@rules_nix//:defs.bzl",
			Symbols: []string{"nix_package"},
		},
	}
}

// RegisterFlags allows the language to register command-line flags.
func (*nixLang) RegisterFlags(fs *flag.FlagSet, cmd string, c *config.Config) {}

// CheckFlags validates command-line flags.
func (*nixLang) CheckFlags(fs *flag.FlagSet, c *config.Config) error { return nil }

// KnownDirectives returns directives this language understands.
func (*nixLang) KnownDirectives() []string {
	return []string{
		"nix_flake",          // # gazelle:nix_flake enable/disable
		"nix_executable",     // # gazelle:nix_executable auto/force/disable
		"nix_nixpkgs_commit", // # gazelle:nix_nixpkgs_commit <sha>
		"nix_cache_name",
	}
}

// Fix is called to fix existing rules.
func (*nixLang) Fix(c *config.Config, f *rule.File) {}
