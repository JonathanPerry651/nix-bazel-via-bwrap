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

# Java SSL fix for GitHub (Bazel 7+ / Java 11+)
BAZEL_OPTS="--host_jvm_args=-Dhttps.protocols=TLSv1.2"

# Java SSL fix: Bypass Bazel downloader for Gazelle
GAZELLE_TAR="$BIT_WORKSPACE_DIR/gazelle.tar.gz"
echo "Downloading Gazelle manually to $GAZELLE_TAR..."
# Unset LD_LIBRARY_PATH to avoid interference from Bazel's hermetic libs
unset LD_LIBRARY_PATH
# Try wget if curl failed (or use both as fallback)
if command -v wget >/dev/null; then
    wget --no-check-certificate -O "$GAZELLE_TAR" https://github.com/bazel-contrib/bazel-gazelle/releases/download/v0.47.0/bazel-gazelle-v0.47.0.tar.gz || exit 1
else
    curl -k -L -o "$GAZELLE_TAR" https://github.com/bazel-contrib/bazel-gazelle/releases/download/v0.47.0/bazel-gazelle-v0.47.0.tar.gz || exit 1
fi

# ==============================================================================
# Setup: MODULE.bazel & Initial State
# ==============================================================================
cat > MODULE.bazel <<EOF
module(name = "test_gazelle_update", version = "0.0.0")

bazel_dep(name = "nix_bazel_via_bwrap")
local_path_override(
    module_name = "nix_bazel_via_bwrap",
    path = "${REPO_ROOT}",
)

bazel_dep(name = "rules_go", version = "0.59.0")
bazel_dep(name = "gazelle", version = "0.47.0")
archive_override(
    module_name = "gazelle",
    urls = ["file://${GAZELLE_TAR}"],
    strip_prefix = "bazel-gazelle-v0.47.0",
)

bazel_dep(name = "rules_bazel_integration_test", version = "0.34.0")

# Nix Portable for testing updates
http_file = use_repo_rule("@bazel_tools//tools/build_defs/repo:http.bzl", "http_file")
http_file(
    name = "nix_portable",
    downloaded_file_path = "nix-portable",
    executable = True,
    sha256 = "b409c55904c909ac3aeda3fb1253319f86a89ddd1ba31a5dec33d4a06414c72a",
    urls = ["https://github.com/DavHau/nix-portable/releases/download/v012/nix-portable-x86_64"],
)

# Use local lockfile
nix_lock = use_extension("@nix_bazel_via_bwrap//:extensions.bzl", "nix_lock_ext")
nix_lock.from_file(
    lockfile = "//nix_deps:nix.lock",
    name = "repo_cache",
    nixpkgs_name = "repo_nixpkgs",
    nixpkgs_commit = "nixos-23.11",
)
use_repo(nix_lock, "repo_cache", "repo_nixpkgs", "repo_cache_gazelle_plugin")
EOF

# Setup initial Flake and Lock (Fake)
mkdir -p hello
cat > hello/flake.nix <<EOF
{
  outputs = { self, nixpkgs }: {
    packages.x86_64-linux.default = nixpkgs.legacyPackages.x86_64-linux.hello;
  };
}
EOF

# Copy real lockfile to serve as base
mkdir -p nix_deps
cp "$REPO_ROOT/nix_deps/nix.lock" nix_deps/nix.lock
cat > nix_deps/BUILD.bazel <<EOF
package(default_visibility = ["//visibility:public"])
exports_files(["nix.lock"])
EOF

# Gazelle Setup
cat > BUILD.bazel <<EOF
load("@gazelle//:def.bzl", "gazelle", "gazelle_binary")

# gazelle:nix_cache_name repo_cache

gazelle_binary(
    name = "gazelle_bin",
    languages = [
        "@gazelle//language/go",
        "@repo_cache_gazelle_plugin//:nix",
    ],
)

gazelle(
    name = "gazelle",
    gazelle = "gazelle_bin",
)
EOF

# Git init
git init
git config user.email "test@example.com"
git config user.name "Test User"
git add .
git commit -m "Initial commit" || echo "Nothing to commit"

# ==============================================================================
# Phase 1: Initial Generation
# ==============================================================================
echo "--- Phase 1: Running Gazelle ---"
bazel $BAZEL_OPTS run //:gazelle

echo "--- Verifying Phase 1 ---"
if grep -q 'nix_package(name = "hello")' hello/BUILD.bazel; then
    echo "Files found: hello/BUILD.bazel contains hello"
else
    echo "FAILURE: hello/BUILD.bazel does not contain expected target"
    cat hello/BUILD.bazel
    exit 1
fi

# ==============================================================================
# Phase 2: Update Lockfile (real update with nix-portable)
# ==============================================================================
echo "--- Phase 2: Updating Flake (Add figlet) ---"

# Modify flake.nix
cat > hello/flake.nix <<EOF
{
  outputs = { self, nixpkgs }: {
    packages.x86_64-linux.default = nixpkgs.legacyPackages.x86_64-linux.hello;
    packages.x86_64-linux.figlet = nixpkgs.legacyPackages.x86_64-linux.figlet;
  };
}
EOF

echo "--- Phase 2: Running Nix Update ---"

# Fetch Nix Portable
bazel $BAZEL_OPTS build @nix_portable//file
NP=$(bazel $BAZEL_OPTS cquery @nix_portable//file --output=files)
chmod +x "$NP"

# Update lockfile
# We need to run this from the root where nix_deps/nix.lock relates to?
# Wait. flake.nix is in hello/flake.nix.
# In Phase 1, we reused `nix_deps/nix.lock` from main repo.
# But `modules.bazel` points to `//nix_deps:nix.lock`.
# If I update `hello/flake.nix`, I need to generate `hello/flake.lock`?
# OR does my setup usage a unified lockfile?
# The `MODULE.bazel` config uses `nix_deps/nix.lock`.
# Gazelle plugin reads THAT lockfile.
# But `flake.nix` is in `hello/`.
# Usually `flake.nix` and `flake.lock` are together.
# If I update `hello/flake.nix`, I get `hello/flake.lock`.
# BUT my Gazelle plugin is configured to read `nix_deps/nix.lock`!
# So I must update `nix_deps/nix.lock`.
# BUT `flake.nix` is in `hello/`.
# This mismatch is tricky in the test.
# In `test_hello`, we manually copied `nix.lock` to `nix_deps/`.
# And `flake.nix` in `hello/` was just for detection?
#
# If I want to verify "Gazelle Update", I should probably align them.
# I'll create `flake.nix` in `nix_deps/` (or root) and use that.
#
# Let's move `flake.nix` to root `.` (where `nix_deps` folder is).
# Wait, `nix_deps` is a folder.
# If I put `flake.nix` in root.
# Then `default.lock` is generated in root.
# I can copy it to `nix_deps/nix.lock`.
#
# REVISED PLAN for Phase 2:
# 1. Modify `flake.nix` (create one in root).
# 2. Run `nix flake update` (generates flake.lock).
# 3. Copy `flake.lock` to `nix_deps/nix.lock`.
# 4. Run Gazelle.
#
# But for `nix flake update` to work, I need `flake.nix`.
# I'll generate a valid one.

cat > flake.nix <<EOF
{
  description = "Test Flake";
  inputs.nixpkgs.url = "nixpkgs";
  outputs = { self, nixpkgs }: let
    pkgs = nixpkgs.legacyPackages.x86_64-linux;
  in {
    packages.x86_64-linux.hello = pkgs.hello;
    packages.x86_64-linux.figlet = pkgs.figlet;
  };
}
EOF

# Run update
# standard nix portable usage: ./nix-portable flake update
export NP_RUNTIME=bwrap
"$NP" flake update

# Copy lock
cp flake.lock nix_deps/nix.lock

echo "--- Phase 2: Run Gazelle (Update) ---"
bazel $BAZEL_OPTS run //:gazelle

echo "--- Verifying Phase 2 ---"
# Where will figlet be generated?
# If flake packages are exposed.
# And lockfile has them.
# Gazelle iterates lockfile content.
# It should generate `nix_package(name="figlet")`.
# I'll check all BUILD files.

if grep -r 'nix_package(name = "figlet")' .; then
    echo "SUCCESS: Found figlet target"
else
    echo "FAILURE: Did not find figlet target"
    exit 1
fi

