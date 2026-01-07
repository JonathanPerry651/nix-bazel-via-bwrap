load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

def _nix_cache_repo_impl(ctx):
    content = ctx.read(ctx.path(ctx.attr.lockfile))
    if not content.strip():
        lock_content = {"version": 1, "flakes": {}, "store_paths": {}}
    else:
        lock_content = json.decode(content)
    
    # Header for root BUILD file
    root_build = ['exports_files(glob(["blobs/**"]))', 'load("@nix_bazel_via_bwrap//:rules.bzl", "nix_nar_unpack")']
    root_build.append('package(default_visibility = ["//visibility:public"])')

    # 1. Process Store Paths (Dependencies)
    # Generate nix_nar_unpack targets in root package (or separate package?)
    # Root package is simplest for visibility.
    # Name scheme: "s_<hash>"
    
    # StorePath keys are full paths: /nix/store/HASH-NAME
    # We map path -> label string
    path_to_label = {}
    
    if "store_paths" in lock_content:
        # Pass 1: Map all paths to labels
        for path, info in lock_content["store_paths"].items():
            basename = path.split("/")[-1]
            hash_part = basename.split("-")[0]
            target_name = "s_" + hash_part
            path_to_label[path] = "//:" + target_name

        # Pass 2: Generate targets
        for path, info in lock_content["store_paths"].items():
            target_name = path_to_label[path].replace("//:", "") # Retrieve name
            
            # Download NAR
            nar_filename = "blobs/" + info["nar_hash"].replace(":", "_") + ".nar.xz"
            
            download_args = {
                "url": info["nar_url"],
                "output": nar_filename,
                "sha256": info["nar_hash"].replace("sha256:", ""),
            }
            if info.get("integrity"):
                download_args["integrity"] = info["integrity"]
                
            ctx.download(**download_args)
            
            # Resolve references for deps
            deps_labels = []
            for ref in info.get("references", []):
                if not ref.startswith("/"):
                     ref_path = "/nix/store/" + ref
                else:
                     ref_path = ref
                
                # Exclude self-references to avoid cycles
                if ref_path == path:
                    continue

                if ref_path in path_to_label:
                     deps_labels.append(path_to_label[ref_path])
            
            root_build.append('nix_nar_unpack(name = "%s", src = "%s", store_path = "%s", deps = %s)' % (target_name, nar_filename, path, deps_labels))

    # Symlink nix_sources if present (for reproducible source references)
    lock_path = ctx.path(ctx.attr.lockfile)
    nix_sources_path = lock_path.dirname.get_child("nix_sources")
    if nix_sources_path.exists:
        ctx.symlink(nix_sources_path, "nix_sources")

    build_files = {} # path -> list of lines
    build_files["BUILD.bazel"] = root_build

    # 2. Process Flakes (Packages)
    for label, flake in lock_content["flakes"].items():
        if not label.startswith("//"):
            continue
        
        # Parse package path from label
        # //foo/bar:target -> foo/bar
        # //:target -> ""
        if ":" in label:
             pkg_part = label[2:].split(":")[0]
        else:
             pkg_part = label[2:] # unlikely for flake label convention

        build_file_path = "BUILD.bazel"
        if pkg_part:
             build_file_path = pkg_part + "/BUILD.bazel"
        
        if build_file_path not in build_files:
             build_files[build_file_path] = [
                 'load("@nix_bazel_via_bwrap//:rules.bzl", "nix_binary")',
                 'package(default_visibility = ["//visibility:public"])'
             ]

        build_content = build_files[build_file_path]
        
        # Determine strict output path for main executable
        if flake.get("executable"):
            # Target name: "bin/hello"
            
            # Construct mounts dict: Label -> StorePath
            mounts_entries = []
            if flake.get("runtime_closure"):
                for p in flake["runtime_closure"]:
                    if p in path_to_label:
                        mounts_entries.append('        "%s": "%s",' % (path_to_label[p], p))
            
            # Output store path logic
            # Use 'output_store_path' if available, otherwise guess? 
            # Lockfile generator should populate 'output_store_path'
            if flake.get("output_store_path"):
                 exe_full_path = flake["output_store_path"] + "/" + flake["executable"]
            
                 build_content.append('nix_binary(')
                 build_content.append('    name = "%s",' % flake["executable"])
                 build_content.append('    exe_path = "%s",' % exe_full_path)
                 build_content.append('    mounts = {')
                 build_content.extend(mounts_entries)
                 build_content.append('    },')
                 build_content.append(')')
    
    # Write all build files
    for path, lines in build_files.items():
         ctx.file(path, "\n".join(lines))

# Repositories generated by this extension
nix_cache_repo = repository_rule(
    implementation = _nix_cache_repo_impl,
    attrs = {
        "lockfile": attr.label(allow_single_file = True),
    },
)

def _nix_plugin_repo_impl(ctx):
    nixpkgs = ctx.attr.nixpkgs
    pkg_name = ctx.attr.go_package_name
    
    # Calculate lockfile path relative to repo root
    # l.lockfile is a label. We assume it's in the main workspace for now or path relative to execution root.
    # NewLanguage needs a path that Gazelle can find relative to RepoRoot.
    # The attribute is 'lockfile'. 
    # Logic: if label package is empty -> at root. 
    # Ideally we pass "nix_deps/nix.lock" or similar string from an attribute, but here we have a label.
    # We will pass the package + name.
    
    lockfile_path = "nix_deps/nix.lock" # Default fallback
    if ctx.attr.lockfile:
         # Best effort: package + / + name
         l = ctx.attr.lockfile
         if l.package:
             lockfile_path = l.package + "/" + l.name
         else:
             lockfile_path = l.name

    ctx.file("plugin.go", """
package nix

import (
    "github.com/JonathanPerry651/nix-bazel-via-bwrap/pkg/gazelle/language/nix"
    "github.com/bazelbuild/bazel-gazelle/language"
)

func NewLanguage() language.Language {
    return nix.NewLanguage()
}
""")

    # Generate BUILD.bazel
    ctx.file("BUILD.bazel", """
load("@rules_go//go:def.bzl", "go_library")

go_library(
    name = "nix",
    srcs = ["plugin.go"],
    importpath = "github.com/JonathanPerry651/nix-bazel-via-bwrap/plugin_generated/%s",
    visibility = ["//visibility:public"],
    deps = [
        "@nix_bazel_via_bwrap//pkg/gazelle/language/nix",
        "@gazelle//language",
    ],
    data = ["%s", "@nix_portable//file"],
)
""" % (pkg_name, str(nixpkgs)))

nix_plugin_repo = repository_rule(
    implementation = _nix_plugin_repo_impl,
    attrs = {
        "nixpkgs": attr.string(mandatory = True),
        "go_package_name": attr.string(mandatory = True),
        "cache_name": attr.string(default = "nix_cache"),
        "lockfile": attr.label(),
    },
)

def _nix_lock_impl(ctx):
    seen_repos = {}
    for mod in ctx.modules:
        for tag in mod.tags.from_file:
            if tag.name in seen_repos:
                continue
            seen_repos[tag.name] = True
            nix_cache_repo(name = tag.name, lockfile = tag.lockfile)

            # Check for nixpkgs_commit and generate repo
            commit = tag.nixpkgs_commit
            content = ctx.read(tag.lockfile)
            if content.strip():
                lock = json.decode(content)
                lock_commit = lock.get("nixpkgs_commit")
                # Fail if lockfile has a commit but it differs from the tag
                if lock_commit and lock_commit != commit:
                     fail("nixpkgs_commit mismatch: MODULE.bazel specifies '%s' but lockfile '%s' has '%s'. Please re-run Gazelle to update the lockfile." % (commit, tag.lockfile, lock_commit))

            if commit:
                http_archive(
                    name = tag.nixpkgs_name,
                    urls = ["https://github.com/NixOS/nixpkgs/archive/%s.tar.gz" % commit],
                    strip_prefix = "nixpkgs-" + commit,
                    build_file_content = """
filegroup(
    name = "all_files",
    srcs = glob(["**"]),
    visibility = ["//visibility:public"],
)
exports_files(glob(["**"]))
""",
                )
            
            # Always generate gazelle plugin
            plugin_name = tag.gazelle_plugin_name
            if not plugin_name:
                plugin_name = tag.name + "_gazelle_plugin"
            
            nix_plugin_repo(
                name = plugin_name,
                nixpkgs = "@%s//:flake.nix" % tag.nixpkgs_name,
                go_package_name = plugin_name,
                cache_name = tag.name,
                lockfile = tag.lockfile,
            )

nix_lock_ext = module_extension(
    implementation = _nix_lock_impl,
    tag_classes = {
        "from_file": tag_class(attrs = {
            "lockfile": attr.label(),
            "name": attr.string(default = "nix_cache"),
            "nixpkgs_name": attr.string(default = "nixpkgs"),
            "nixpkgs_commit": attr.string(mandatory = True),
            "gazelle_plugin_name": attr.string(),
        }),
    },
)
