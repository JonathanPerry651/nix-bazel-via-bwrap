#!/bin/bash
set -e

# ==============================================================================
# Helper to locate resources in runfiles
# ==============================================================================
RUNFILES=${RUNFILES_DIR:-$0.runfiles}
if [ -d "$RUNFILES/_main" ]; then
    REPO_ROOT="$RUNFILES/_main"
elif [ -d "$RUNFILES/nix_bazel_via_bwrap" ]; then
    REPO_ROOT="$RUNFILES/nix_bazel_via_bwrap"
else
    # Fallback for manual execution
    REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
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

http_file = use_repo_rule("@bazel_tools//tools/build_defs/repo:http.bzl", "http_file")
http_file(
    name = "nix_portable",
    downloaded_file_path = "nix-portable",
    executable = True,
    sha256 = "b409c55904c909ac3aeda3fb1253319f86a89ddd1ba31a5dec33d4a06414c72a",
    urls = ["https://github.com/DavHau/nix-portable/releases/download/v012/nix-portable-x86_64"],
)
EOF

# Copy flake.nix to hello subdirectory for detection logic
mkdir -p hello

mkdir -p hello
cat > hello/flake.nix <<EOF
{
  description = "Env Test";
  inputs.nixpkgs.url = "nixpkgs";
  outputs = { self, nixpkgs }: let
    pkgs = nixpkgs.legacyPackages.x86_64-linux;
  in {
    packages.x86_64-linux.default = pkgs.mkShell {
      packages = [ pkgs.hello ];
      env.GREETING = "Hello from Nix Environment";
    };
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

echo "--- Appending Manual Target ---"
# Gazelle only generates 'nix_package'. Users define their own nix_flake_run_under.
# We need to add the load statement since Gazelle doesn't include it anymore.
cat >> hello/BUILD.bazel <<'EOF'

load("@nix_bazel_via_bwrap//:rules.bzl", "nix_flake_run_under")

nix_flake_run_under(
    name = "manual_hello",
    src = ":default",
    startup_cmd = "hello",
)
EOF

echo "--- Building Manual Target ---"
bazel build //hello:manual_hello

echo "--- Running Manual Target ---"
OUTPUT=$(bazel run //hello:manual_hello)

echo "--- Verifying Output ---"
echo "Output: $OUTPUT"

# hello prints "Hello, world!"
# We also expect GREETING to be set.
# But 'hello' doesn't print env vars.
# Wait, I need a command that prints GREETING.
# 'hello' just prints "Hello, world!".
# I should verify startup_cmd="hello" works (it calls the binary).
# AND verify GREETING is set.
# I can use startup_cmd="bash" and args?
# Or use `sh -c 'echo $GREETING'`?
# startup_cmd resolution works on "sh" too (from path).
# Let's try running a shell command to print GREETING.

cat >> hello/BUILD.bazel <<'EOF'

nix_flake_run_under(
    name = "print_env",
    src = ":default",
    startup_cmd = "bash",
    args = ["-c", "echo $$GREETING"],
)
EOF

echo "--- Generated hello/BUILD.bazel ---"
cat hello/BUILD.bazel

OUTPUT_ENV=$(bazel run //hello:print_env)
echo "Env Output: $OUTPUT_ENV"

if [[ "$OUTPUT" == *"Hello, world!"* ]]; then
    echo "Binary Execution: SUCCESS"
else
    echo "Binary Execution: FAILURE"
    exit 1
fi

if [[ "$OUTPUT_ENV" == *"Hello from Nix Environment"* ]]; then
    echo "Env Propagation: SUCCESS"
else
    echo "Env Propagation: FAILURE"
    exit 1
fi
