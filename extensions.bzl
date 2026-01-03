load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_file", "http_archive")
load("//nixpkgs:nix_deps.bzl", "NIX_DEPS")

def _nix_deps_impl(ctx):
    for name, attrs in NIX_DEPS.items():
        # Clean attrs: remove unknown attributes if needed, or pass selected ones
        # http_file supports: urls, sha256, integrity, netrc, downloaded_file_path, executable
        
        args = {
            "name": name,
            "urls": attrs.get("urls"),
            "sha256": attrs.get("sha256"),
            "integrity": attrs.get("integrity"),
            "downloaded_file_path": attrs.get("downloaded_file_path"),
            "executable": attrs.get("executable", False),
        }
        
        # Filter None values
        clean_args = {k: v for k, v in args.items() if v != None}
        
        http_file(**clean_args)

nix_deps_ext = module_extension(
    implementation = _nix_deps_impl,
)

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
        for path, info in lock_content["store_paths"].items():
            # Extract hash from path for target name
            # /nix/store/HASH-NAME
            basename = path.split("/")[-1]
            hash_part = basename.split("-")[0]
            target_name = "s_" + hash_part
            path_to_label[path] = "//:" + target_name
            
            # Download NAR
            nar_filename = "blobs/" + info["nar_hash"].replace(":", "_") + ".nar.xz"
            
            download_args = {
                "url": info["nar_url"],
                "output": nar_filename,
                "sha256": info["nar_hash"].replace("sha256:", ""),
            }
            # integrity field might be missing in StorePaths too if not added
            if info.get("integrity"):
                download_args["integrity"] = info["integrity"]
                
            ctx.download(**download_args)
            
            # Add unpack target
            root_build.append('nix_nar_unpack(name = "%s", src = "%s")' % (target_name, nar_filename))

    ctx.file("BUILD.bazel", "\n".join(root_build))

    # 2. Process Flakes (Packages)
    for label, flake in lock_content["flakes"].items():
        if not label.startswith("//"):
            continue
        
        # "tests/manual/hello"
        pkg_path = label[2:].split(":")[0] 
        
        build_content = ['load("@nix_bazel_via_bwrap//:rules.bzl", "nix_binary")']
        build_content.append('package(default_visibility = ["//visibility:public"])')
        
        # Determine closure labels
        closure_labels = []
        if flake.get("runtime_closure"):
            for p in flake["runtime_closure"]:
                if p in path_to_label:
                    closure_labels.append(path_to_label[p])
        
        # Determine strict output path for main executable
        # If Executable is bin/hello, and OutputStorePath is /nix/store/HASH-hello...
        # We need to know WHICH store path is the output.
        # flake["output_store_path"] -> Label
        output_label = ""
        if flake.get("output_store_path") and flake["output_store_path"] in path_to_label:
            output_label = path_to_label[flake["output_store_path"]]
        else:
             # Fallback or error?
             # For now, if missing component, we skip
             pass
        
        if flake.get("executable"):
            # Target name: "bin/hello"
            
            # Construct mounts dict: Label -> StorePath
            mounts_entries = []
            if flake.get("runtime_closure"):
                for p in flake["runtime_closure"]:
                    if p in path_to_label:
                        mounts_entries.append('        "%s": "%s",' % (path_to_label[p], p))
            
            exe_full_path = flake["output_store_path"] + "/" + flake["executable"]
            
            build_content.append('nix_binary(')
            build_content.append('    name = "%s",' % flake["executable"])
            build_content.append('    exe_path = "%s",' % exe_full_path)
            build_content.append('    mounts = {')
            build_content.extend(mounts_entries)
            build_content.append('    },')
            build_content.append(')')
        
        ctx.file(pkg_path + "/BUILD.bazel", "\n".join(build_content))

nix_cache_repo = repository_rule(
    implementation = _nix_cache_repo_impl,
    attrs = {
        "lockfile": attr.label(allow_single_file = True),
    },
)

def _nix_lock_impl(ctx):
    for mod in ctx.modules:
        for tag in mod.tags.from_file:
            nix_cache_repo(name = tag.name, lockfile = tag.lockfile)

            # Check for nixpkgs_commit and generate repo
            content = ctx.read(tag.lockfile)
            if content.strip():
                lock = json.decode(content)
                if lock.get("nixpkgs_commit"):
                    commit = lock["nixpkgs_commit"]
                    http_archive(
                        name = tag.nixpkgs_name,
                        urls = ["https://github.com/NixOS/nixpkgs/archive/%s.tar.gz" % commit],
                        strip_prefix = "nixpkgs-" + commit,
                        build_file_content = 'filegroup(name = "all_files", srcs = glob(["**"]), visibility = ["//visibility:public"])',
                    )

nix_lock_ext = module_extension(
    implementation = _nix_lock_impl,
    tag_classes = {
        "from_file": tag_class(attrs = {
            "lockfile": attr.label(),
            "name": attr.string(default = "nix_cache"),
            "nixpkgs_name": attr.string(default = "nixpkgs"),
        }),
    },
)
