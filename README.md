# Nix Bazel via Bubblewrap

A Bazel rule set and Gazelle extension for integrating Nix flakes into Bazel builds, using `bubblewrap` for hermetic sandboxing.

## Overview

This project provides a way to consume Nix flake outputs (packages) as Bazel dependencies. It uses a custom Gazelle extension to automatically generate Bazel `BUILD` files from `flake.nix` inputs.

Key features:
- **Hermetic Execution**: Uses `bubblewrap` to run Nix commands and builders within a strict sandbox, ensuring purity and reproducibility.
- **Gazelle Integration**: Automatically generates `nix_package` or `sh_binary` targets from your `flake.nix`.
- **Caching**: Leverages a local disk cache for Nix store paths and metadata.

## Prerequisites

- **Bazel**: version 7.x+ (bzlmod enabled)
- **Bubblewrap (`bwrap`)**: Required for the sandbox execution.

## Setup

1. Add the dependency to your `MODULE.bazel`:

```starlark
bazel_dep(name = "nix_bazel_via_bwrap")
local_path_override(
    module_name = "nix_bazel_via_bwrap",
    path = "...",
)
```

2. Configure the Gazelle extension:

```starlark
nix_lock = use_extension("@nix_bazel_via_bwrap//:extensions.bzl", "nix_lock_ext")
nix_lock.from_file(
    lockfile = "//:nix.lock",
    name = "nix_deps",
)
use_repo(nix_lock, "nix_deps")
```

3. Enable the Gazelle language in your `BUILD` file:

```starlark
gazelle_binary(
    name = "gazelle_bin",
    languages = [
        "@gazelle//language/go",
        "@nix_bazel_via_bwrap//pkg/gazelle/language/nix",
    ],
)
```

### Example: Manual Reproduction Scaffold

A minimal example is available in `tests/manual_repro_scaffold`. You can use it as a template for your own projects.

```bash
cp -r tests/manual_repro_scaffold my_project
cd my_project
# Update MODULE.bazel to point to the correct repo path or use http_archive
bazel run //:gazelle
bazel build //hello:hello
```

## Running Tests

```bash
bazel test //...
```
