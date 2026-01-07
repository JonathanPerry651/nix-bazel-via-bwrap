package nix

import (
	"path/filepath"

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
	// LockPath is the absolute path to the lockfile.
	LockPath string
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
	// Root defaults handled in NewLanguage or below.

	var cfg *NixConfig
	if extra, ok := c.Exts[nixName]; ok {
		cfg = extra.(*NixConfig).Clone()
	} else {
		cfg = &NixConfig{
			Enabled:        true,
			ExecutableMode: "auto",
			CacheName:      l.cacheName,
			NixpkgsLabel:   l.nixpkgsLabel,
			LockPath:       l.lockPath,
		}
		if cfg.CacheName == "" {
			cfg.CacheName = "nix_cache"
		}
		if cfg.LockPath == "" {
			cfg.LockPath = filepath.Join(c.RepoRoot, "nix_deps", "nix.lock")
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
			case "nix_cache_name":
				cfg.CacheName = d.Value
			case "nix_lockfile":
				if filepath.IsAbs(d.Value) {
					cfg.LockPath = d.Value
				} else {
					cfg.LockPath = filepath.Join(c.RepoRoot, d.Value)
				}
			}
		}
	}
}
