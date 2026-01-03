package nix

import (
	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/repo"
	"github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// Imports implements language.Language.Imports.
// Returns imports from a rule that need to be resolved.
func (*nixLang) Imports(c *config.Config, r *rule.Rule, f *rule.File) []resolve.ImportSpec {
	// nix_package rules don't have cross-language imports to resolve
	// Dependencies are discovered from flake inputs during generation
	return nil
}

// Embeds implements language.Language.Embeds.
func (*nixLang) Embeds(r *rule.Rule, from label.Label) []label.Label {
	return nil
}

// Resolve implements language.Language.Resolve.
// Resolves imports to Bazel labels.
func (*nixLang) Resolve(c *config.Config, ix *resolve.RuleIndex, rc *repo.RemoteCache, r *rule.Rule, imports interface{}, from label.Label) {
	// Dependencies are already resolved as labels during generation
	// No cross-language resolution needed
}
