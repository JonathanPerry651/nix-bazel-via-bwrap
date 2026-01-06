# Toolchain Implementation Guide: JDK 25 & Clang

This document outlines the strategy for implementing JDK 25 and Clang toolchains within the `nix-bazel-via-bwrap` workspace. The approach leverages the existing Nix integration (`nix_lock`, `nix_cache`) to provide hermetic, aligned toolchains for Bazel.

## 1. Prerequisites and Dependency Acquisition

Since the workspace is backed by Nix, the first step is to ensure that the required toolchain binaries are available in the Nix store and tracked by the project's lockfile.

### 1.1. Upgrading Nixpkgs
The current `MODULE.bazel` references `nixpkgs-23.11`, which is too old to contain JDK 25 (expected release late 2025/2026).
*   **Action**: Update `nixpkgs_commit` in `MODULE.bazel` (and the corresponding lock generator inputs) to a version of `nixpkgs` that contains `jdk25` (e.g., `nixos-unstable` or a specific future release 25.05/25.11).
*   **Alternative**: Use a Nix overlay or a direct URL fetch in the lock generator to pull a specific JDK 25 derivation if not in the pinned nixpkgs.

### 1.2. Updating `nix.lock`
Ensure the lockfile generator (integrated via Gazelle) includes the following packages:
*   `jdk25` (or `openjdk25`)
*   `clang` (via `llvmPackages_latest.clang` or specific version)

After running the update (e.g., `bazel run //:gazelle -- update-repos`), `nix_deps/nix.lock` will contain the store paths and hashes. The `nix_lock` extension will automatically expose these as targets in the `@nix_cache` repository (e.g., `@nix_cache//:s_<hash>`).

## 2. JDK 25 Toolchain

Bazel's `rules_java` provides robust support for custom runtimes.

### 2.1. Define Wrappers using `nix_flake_run_under`
Instead of manually constructing `nix_binary` targets with hardcoded paths, we should use the `nix_flake_run_under` rule (or a derived `nix_tool` rule). This rule leverages the `NixInfo` provider to automatically configure the sandbox mounts.

However, `nix_flake_run_under` currently expects a `startup_cmd`. To avoid hardcoding store paths (e.g., `/nix/store/<hash>.../bin/java`), we should enhance the rule or use a small wrapper that resolves the executable relative to the provided source's store path.

**Proposed Rule Usage:**
```starlark
load("@nix_bazel_via_bwrap//:rules.bzl", "nix_flake_run_under")

# Define a convenience wrapper or use nix_flake_run_under directly 
# if we add a 'relative_cmd' attribute or similar logic.
# For this doc, let's assume an enhanced nix_flake_run_under that supports finding the binary.

nix_flake_run_under(
    name = "java_bin",
    src = "@nix_cache//:s_jdk25_...", # The Nix package target
    startup_cmd = "bin/java",         # Relative path within the package
    env_paths = {
        "@nix_cache//:s_jdk25_...": "JAVA_HOME",
    },
)

nix_flake_run_under(
    name = "javac_bin",
    src = "@nix_cache//:s_jdk25_...",
    startup_cmd = "bin/javac",
    env_paths = {
         "@nix_cache//:s_jdk25_...": "JAVA_HOME",
    },
)

nix_flake_run_under(
    name = "jar_bin",
    src = "@nix_cache//:s_jdk25_...",
    startup_cmd = "bin/jar",
)
```

**Implementation Note**: We might need to slightly adjust `nix_flake_run_under` in `rules.bzl` to prepend the store path of `src` to `startup_cmd` if it's a relative path. This ensures we point to the correct executable inside the sandbox.

### 2.2. Define `java_runtime`
Now point the runtime to these wrapped executables. Note that `java_runtime` often assumes a directory structure (java_home), but can also accept individual tool targets.

```starlark
load("@rules_java//java:defs.bzl", "java_runtime", "java_toolchain")

java_runtime(
    name = "jdk25_runtime",
    java = ":java_bin",
    # java_home is still often required by some rules. 
    # We might need to construct a tree of symlinks that looks like a JDK but points to our wrappers
    # or rely on individual tool attributes if supported by the toolchain definition.
    # explicit_java_home = ...,
    version = 25,
)
```

### 2.3. Define `java_toolchain`
Define the toolchain that utilizes this runtime.

```starlark
java_toolchain(
    name = "jdk25_toolchain_impl",
    source_version = "25",
    target_version = "25",
    java_runtime = ":jdk25_runtime",
    # bootclasspath setup:
    # You might need to supply the bootclasspath JARs as a filegroup. 
    # These JARs can be read directly from the /nix/store mount if they are just files.
    bootclasspath = ["@nix_cache//:s_jdk25_..."], 
)

toolchain(
    name = "jdk25_toolchain",
    toolchain = ":jdk25_toolchain_impl",
    toolchain_type = "@bazel_tools//tools/jdk:toolchain_type",
)
```

### 2.3. Registration
Register the toolchain in `MODULE.bazel`:
```starlark
register_toolchains("//toolchains/jdk:jdk25_toolchain")
```

## 3. Clang Toolchain

Setting up a C++ toolchain is more involved due to the need for a `cc_toolchain_config` to handle system include paths and flags.

### 3.1. Toolchain Config Rule
You will need a Starlark rule (e.g., `cc_toolchain_config.bzl`) to generate the `CcToolchainConfigInfo` provider. This config must:
*   Point to the Clang binary.
*   Define system include paths (critical for hermeticity).
*   Set default linker flags (e.g., `-fuse-ld=lld` if using LLD).

### 3.2. Define `cc_toolchain`
Similarly for Clang, we define wrappers for the necessary tools (`clang`, `ld`, etc.).

```starlark
nix_flake_run_under(
    name = "clang_bin",
    src = "@nix_cache//:s_clang_...",
    startup_cmd = "bin/clang",
)
```

Then define the toolchain using these wrappers:

```starlark
load("@rules_cc//cc:defs.bzl", "cc_toolchain", "cc_toolchain_suite")
load(":cc_toolchain_config.bzl", "clang_toolchain_config")

filegroup(
    name = "clang_all_files",
    # Include the wrappers and the underlying Nix targets
    srcs = [":clang_bin", "@nix_cache//:s_clang_..."],
)

clang_toolchain_config(
    name = "clang_config",
    toolchain_identifier = "nix-clang-toolchain",
    cpu = "k8",
    compiler = "clang",
    toolchain_path_prefix = "", # Wrapper handles the path
)

cc_toolchain(
    name = "clang_toolchain_impl",
    all_files = ":clang_all_files",
    # ...
    toolchain_config = ":clang_config",
)
```

### 3.3. Handling Sysroots and Sandbox
Since `nix-bazel-via-bwrap` uses `bubblewrap`, ensure that the `clang` invocation within the sandbox can find:
1.  **Standard Libs**: The Nix Clang wrapper usually hardcodes paths to libc and standard includes. If these point to `/nix/store/...` paths that are *not* inputs to the action, the build will fail.
2.  **Mounts**: You might need to use the `nix_binary` or `nix_derivation` wrappers to ensure the Clang compiler *itself* has its runtime dependencies mounted (glibc, etc.).
3.  **Wrapper Script**: It is often robust to wrap the clang binary in a shell script that sets `C_INCLUDE_PATH`, `LIBRARY_PATH` or passes `--sysroot` if using a standalone sysroot.

## 4. Integration Steps

1.  **Add `rules_java` and `rules_cc`** to `MODULE.bazel` if not already present.
2.  **Create `//toolchains` package**.
3.  **Populate `BUILD.bazel` files** following the templates above.
4.  **Register toolchains** in `MODULE.bazel`.
5.  **Verification**:
    *   `bazel build //toolchains/jdk:jdk25_toolchain`
    *   `bazel coverage --toolchain_resolution_debug=.*\jdk.* //...` to verify selection.
