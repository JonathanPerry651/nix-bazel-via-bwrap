#!/bin/bash
set -e

# Path to the current repository
REPO_ROOT=$(pwd)

# Create a temporary directory
WORK_DIR=$(mktemp -d)
echo "Working directory: $WORK_DIR"

# Copy scaffolding
cp -r tests/manual_repro_scaffold/* "$WORK_DIR"
# Also need nix_deps dir which might be empty
mkdir -p "$WORK_DIR/nix_deps"
touch "$WORK_DIR/nix_deps/nix.lock"
echo 'exports_files(["nix.lock"])' > "$WORK_DIR/nix_deps/BUILD.bazel"

# Switch to working directory
cd "$WORK_DIR"

# Replace PLACEHOLDER_REPO_ROOT with absolute path
sed -i "s|PLACEHOLDER_REPO_ROOT|$REPO_ROOT|g" MODULE.bazel

# Configure host repo cache optimization
HOST_REPO_CACHE="${HOME}/.cache/bazel/_bazel_${USER}/cache/repos/v1"
BAZEL_OPTS=""
if [ -d "$HOST_REPO_CACHE" ]; then
    echo "Using host repository cache: $HOST_REPO_CACHE"
    BAZEL_OPTS="--repository_cache=$HOST_REPO_CACHE --action_env=PATH --action_env=HOME"
else
    BAZEL_OPTS="--action_env=PATH --action_env=HOME"
fi

echo "Current PATH: $PATH"
echo "Running: bazel run $BAZEL_OPTS //:gazelle"
bazel run $BAZEL_OPTS //:gazelle

# Simple validation of content
if grep -q "nixpkgs_commit" nix_deps/nix.lock; then
    echo "SUCCESS: nix.lock generated with expected content."
else
    echo "Error: nix.lock does not seem to contain expected content"
    cat nix_deps/nix.lock
    exit 1
fi

# Build Hello
echo "Building Hello..."
bazel build $BAZEL_OPTS //hello:hello

# Run Browser Toolchain Test
echo "Running Browser Test..."
if ! bazel test $BAZEL_OPTS //playwright_browsers:browser_test; then
    echo "Test Failed! dumping log:"
    cat bazel-testlogs/playwright_browsers/browser_test/test.log
    exit 1
fi

echo "Success!"
