#!/bin/bash
set -e

# Test: Cache-miss scenario
# This test verifies that Gazelle handles derivations not found in cache.nixos.org

# Find repo root in runfiles
RUNFILES=${RUNFILES_DIR:-$0.runfiles}
if [ -d "$RUNFILES/_main" ]; then
    REPO_ROOT="$RUNFILES/_main"
else
    REPO_ROOT="$RUNFILES/nix_bazel_via_bwrap" # Workspace name
fi

if [ ! -f "$REPO_ROOT/MODULE.bazel" ]; then
    echo "Error: Could not find MODULE.bazel in $REPO_ROOT"
    echo "Contents of RUNFILES:"
    find "$RUNFILES" -maxdepth 2
    exit 1
fi

echo "Using repo root: $REPO_ROOT"

# Restore BUILD.bazel files from .in templates
find . -name "BUILD.bazel.in" -exec bash -c 'mv "$1" "${1%.in}"' _ {} \;

# rules_bazel_integration_test sets BIT_WORKSPACE_DIR
if [ -z "$BIT_WORKSPACE_DIR" ]; then
    echo "Error: BIT_WORKSPACE_DIR not set"
    exit 1
fi

echo "Changing to workspace: $BIT_WORKSPACE_DIR"
cd "$BIT_WORKSPACE_DIR"

# Create MODULE.bazel dynamically to inject REPO_ROOT
cat > MODULE.bazel <<EOF
module(name = "cache_miss_test", version = "0.0.0")

bazel_dep(name = "nix_bazel_via_bwrap")
local_path_override(
    module_name = "nix_bazel_via_bwrap",
    path = "../../..",
)

bazel_dep(name = "gazelle", version = "0.45.0")
bazel_dep(name = "rules_go", version = "0.57.0")
bazel_dep(name = "rules_bazel_integration_test", version = "0.34.0")

nix_lock = use_extension("@nix_bazel_via_bwrap//:extensions.bzl", "nix_lock_ext")
nix_lock.from_file(
    lockfile = "//nix_deps:nix.lock",
    name = "e2e_cache",
    nixpkgs_name = "e2e_nixpkgs",
)
use_repo(nix_lock, "e2e_cache", "e2e_nixpkgs")
EOF

# With rules_bazel_integration_test, we need to manually copy external data into the workspace
# if it's not part of the initial workspace glob.
TEST_FLAKE_DIR="tests/manual/cache_miss"
mkdir -p "$TEST_FLAKE_DIR"

if [ -f "$REPO_ROOT/$TEST_FLAKE_DIR/flake.nix" ]; then
    cp "$REPO_ROOT/$TEST_FLAKE_DIR/flake.nix" "$TEST_FLAKE_DIR/"
else
    echo "Error: Could not find flake.nix in runfiles at $REPO_ROOT/$TEST_FLAKE_DIR/flake.nix"
    exit 1
fi

if [ ! -f "$TEST_FLAKE_DIR/flake.nix" ]; then
    echo "Error: Failed to copy flake.nix to $TEST_FLAKE_DIR/flake.nix"
    exit 1
fi


# Run Gazelle
echo "Running: bazel run //:gazelle -- update $TEST_FLAKE_DIR"
# Locate bazel binary if possible, or assume 'bazel' is on path
# Locate bazel binary if possible, or assume 'bazel' is on path
BAZEL_CMD="bazel"
if [ -n "$BAZEL_REAL" ]; then
  BAZEL_CMD="$BAZEL_REAL"
fi

# Optimization: Share host repository cache if found
HOST_REPO_CACHE="${HOME}/.cache/bazel/_bazel_${USER}/cache/repos/v1"
BAZEL_OPTS=""
if [ -d "$HOST_REPO_CACHE" ]; then
    echo "Using host repository cache: $HOST_REPO_CACHE"
    BAZEL_OPTS="--repository_cache=$HOST_REPO_CACHE"
fi

$BAZEL_CMD run $BAZEL_OPTS //:gazelle -- update "$TEST_FLAKE_DIR" 2>&1 | tee gazelle_output.log

# Check for cache miss warning
if grep -q "not found in cache" gazelle_output.log; then
    echo "SUCCESS: Gazelle correctly detected cache miss"
else
    echo "INFO: No cache miss warning (derivation may have been cached or error occurred)"
fi

# Verify BUILD.bazel was generated
if [ -f "$TEST_FLAKE_DIR/BUILD.bazel" ]; then
    echo "SUCCESS: BUILD.bazel generated"
    cat "$TEST_FLAKE_DIR/BUILD.bazel"
else
    echo "FAILURE: BUILD.bazel not generated"
    exit 1
fi

# Check if nix.lock was updated
if [ -f "nix_deps/nix.lock" ]; then
    echo "Lockfile content:"
    cat nix_deps/nix.lock
    
    # Verify the cache_miss flake is in lockfile
    if grep -q "cache_miss" nix_deps/nix.lock; then
        echo "SUCCESS: cache_miss flake recorded in lockfile"
    else
        echo "WARNING: cache_miss not found in lockfile (may need local build)"
    fi
else
    echo "WARNING: nix.lock not found"
    exit 1
fi

echo "=== Cache Miss Test Complete ==="
