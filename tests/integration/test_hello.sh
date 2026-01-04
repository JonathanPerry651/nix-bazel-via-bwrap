#!/bin/bash
set -e

# ==============================================================================
# Helper to locate resources in runfiles
# ==============================================================================
RUNFILES=${RUNFILES_DIR:-$0.runfiles}
if [ -d "$RUNFILES/_main" ]; then
    REPO_ROOT="$RUNFILES/_main"
else
    REPO_ROOT="$RUNFILES/nix_bazel_via_bwrap"
fi

if [ -z "$BIT_WORKSPACE_DIR" ]; then
    echo "Error: BIT_WORKSPACE_DIR not set"
    exit 1
fi
cd "$BIT_WORKSPACE_DIR"

# ==============================================================================
# Setup: MODULE.bazel & BUILD files
# ==============================================================================
cat > MODULE.bazel <<EOF
module(name = "test_hello", version = "0.0.0")

bazel_dep(name = "nix_bazel_via_bwrap")
local_path_override(
    module_name = "nix_bazel_via_bwrap",
    path = "${REPO_ROOT}",
)

bazel_dep(name = "rules_go", version = "0.59.0")
bazel_dep(name = "gazelle", version = "0.45.0")
bazel_dep(name = "rules_bazel_integration_test", version = "0.34.0")

# Reuse the main repo's lock/cache to avoid massive downloads
nix_lock = use_extension("@nix_bazel_via_bwrap//:extensions.bzl", "nix_lock_ext")
nix_lock.from_file(
    lockfile = "//nix_deps:nix.lock",
    name = "hello_cache",
    nixpkgs_name = "hello_nixpkgs",
    nixpkgs_commit = "nixos-23.11",
)
use_repo(nix_lock, "hello_cache", "hello_nixpkgs", "hello_cache_gazelle_plugin")
EOF

# Copy flake.nix to hello subdirectory for detection logic
mkdir -p hello
mkdir -p hello
cat > hello/flake.nix <<EOF
{
  description = "Minimal Hello World using cached Nixpkgs";
  inputs.nixpkgs.url = "nixpkgs";
  outputs = { self, nixpkgs }: let
    pkgs = nixpkgs.legacyPackages.x86_64-linux;
  in {
    packages.x86_64-linux.default = pkgs.hello;
  };
}
EOF

# Initialize git repo to satisfy Nix flake requirements
git init
git config user.email "test@example.com"
git config user.name "Test User"
git add .
git commit -m "Initial commit" || echo "Nothing to commit"

# Setup nix_deps for lockfile reuse
mkdir -p nix_deps
cp "$REPO_ROOT/nix_deps/nix.lock" nix_deps/
cat > nix_deps/BUILD.bazel <<EOF
package(default_visibility = ["//visibility:public"])
exports_files(["nix.lock"])
EOF

# Setup Root BUILD: Gazelle definitions
cat > BUILD.bazel <<EOF
load("@gazelle//:def.bzl", "gazelle", "gazelle_binary")

# gazelle:nix_cache_name hello_cache

gazelle_binary(
    name = "gazelle_bin",
    languages = [
        "@gazelle//language/go",
        "@hello_cache_gazelle_plugin//:nix",
    ],
)

gazelle(
    name = "gazelle",
    gazelle = "gazelle_bin",
)
EOF

# ==============================================================================
# Execution
# ==============================================================================
echo "--- Running Gazelle ---"
bazel run //:gazelle

echo "--- Building Hello Target ---"
# Expect generated target //hello:hello (from flake.nix in hello/ package)
# generate.go single binary detection matches "hello" directory name.
bazel build //hello:hello

echo "--- Running Hello Target ---"
# Debug: List runfiles before running
echo "DEBUG PRE-RUN: Listing bazel-bin/hello/hello.bash.runfiles"
find bazel-bin/hello/hello.bash.runfiles -name "*hello_cache*" || echo "Not found via find"

OUTPUT=$(bazel run //hello:hello)

echo "--- Verifying Output ---"
echo "Output: $OUTPUT"
if [[ "$OUTPUT" == *"Hello, world!"* ]]; then
    echo "SUCCESS"
else
    echo "FAILURE: Expected 'Hello, world!'"
    exit 1
fi
