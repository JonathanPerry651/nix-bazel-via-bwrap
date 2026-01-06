package nix

import (
	"log"
	"path/filepath"

	"github.com/JonathanPerry651/nix-bazel-via-bwrap/cache"
	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// NixConfig holds Nix-specific configuration.
type NixConfig struct {
	// Enabled indicates whether Nix flake processing is enabled.
	Enabled bool
	// ExecutableMode controls how single-binary packages are handled.
	// "auto" = detect and generate sh_binary
	// "force" = always generate sh_binary
	// "disable" = always generate nix_package
	// ExecutableMode controls how single-binary packages are handled.
	// "auto" = detect and generate sh_binary
	// "force" = always generate sh_binary
	// "disable" = always generate nix_package
	ExecutableMode string
	// NixpkgsCommit specifies the commit hash of nixpkgs to use.
	NixpkgsCommit string
	// CacheName specifies the name of the repository where cached artifacts are stored.
	// Defaults to "nix_cache".
	CacheName string
	// NixpkgsLabel specifies a Bazel label (e.g. @nixpkgs//:src) pointing to the nixpkgs source.
	// If set, this is resolved to an absolute path via runfiles and passed to nix show-derivation.
	NixpkgsLabel string
}

func (c *NixConfig) Clone() *NixConfig {
	newConfig := *c
	return &newConfig
}

// GetNixConfig returns the NixConfig for a given config.Config.
func GetNixConfig(c *config.Config) *NixConfig {
	if cfg, ok := c.Exts[nixName].(*NixConfig); ok {
		return cfg
	}
	return &NixConfig{
		Enabled:        true,
		ExecutableMode: "auto",
	}
}

// Configure implements language.Language.Configure.
func (l *nixLang) Configure(c *config.Config, rel string, f *rule.File) {
	if rel == "" {
		l.mu.Lock()
		if !l.initialized {
			if l.lockPath == "" {
				// Fallback if not provided (though it should be mandatory from Bzlmod)
				l.lockPath = filepath.Join(c.RepoRoot, "nix_deps", "nix.lock")
			} else if !filepath.IsAbs(l.lockPath) {
				l.lockPath = filepath.Join(c.RepoRoot, l.lockPath)
			}
			lf, err := cache.LoadLockFile(l.lockPath)
			if err != nil {
				// Don't fail hard, just warn.
				log.Printf("Warning: failed to load %s: %v", l.lockPath, err)
			}
			l.lockFile = lf
			l.initialized = true
		}
		l.mu.Unlock()
	}

	var cfg *NixConfig
	if extra, ok := c.Exts[nixName]; ok {
		cfg = extra.(*NixConfig).Clone()
	} else {
		cfg = &NixConfig{
			Enabled:        true,
			ExecutableMode: "auto",
			CacheName:      l.cacheName,
			NixpkgsLabel:   l.nixpkgsLabel,
		}
		if cfg.CacheName == "" {
			cfg.CacheName = "nix_cache"
		}
	}
	c.Exts[nixName] = cfg

	// Process directives
	if f != nil {
		for _, d := range f.Directives {
			switch d.Key {
			case "nix_flake":
				cfg.Enabled = d.Value != "disable"
			case "nix_executable":
				cfg.ExecutableMode = d.Value
			case "nix_nixpkgs_commit":
				cfg.NixpkgsCommit = d.Value
			case "nix_nixpkgs_label":
				cfg.NixpkgsLabel = d.Value
			}
		}
	}
}
