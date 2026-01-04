NixInfo = provider(
    fields = ["outputs", "out_hash", "closure", "store_paths", "env"],
)

# ... (skip to nix_package)

def _nix_package_impl(ctx):
    # Simulates a package/environment definition.
    # It can bundle dependencies and export environment variables.
    
    # Collect transitive closures and store paths from deps
    closure = []
    store_paths = {}
    
    # Normally nix_package would involve an action to build the derivation?
    # But here we assume it acts as a provider aggregator for Gazelle results.
    # Or in the repro, it just wraps deps.
    # The 'srcs' might be the output of the derivation built elsewhere?
    # For now, let's treat it as a wrapper around deps that adds env info.
    
    all_files = []
    
    for dep in ctx.attr.deps:
        if NixInfo in dep:
             closure.append(dep[NixInfo].closure)
             store_paths.update(dep[NixInfo].store_paths)
        all_files.append(dep[DefaultInfo].files)

    # Generate manifests for this package's environment
    # Env manifest
    env_content = "{\n"
    sorted_envs = sorted(ctx.attr.env.items())
    for i, (k, v) in enumerate(sorted_envs):
        env_content += '  "%s": "%s"' % (k, v)
        if i < len(sorted_envs) - 1:
             env_content += ",\n"
    env_content += "\n}"
    
    env_file = ctx.actions.declare_file(ctx.label.name + ".nix-env.json")
    ctx.actions.write(env_file, env_content)
    all_files.append(depset([env_file]))
    
    # Mounts manifest (if we had direct mounts, but here we aggregate deps)
    # The runner walks runfiles. It will find deps' manifests.
    # But if this package introduces new mounts (not just deps), we would need one.
    # For now, we rely on deps' manifests.
    
    runfiles = ctx.runfiles(files = [env_file])
    for dep in ctx.attr.deps:
        runfiles = runfiles.merge(dep[DefaultInfo].default_runfiles)

    return [
       DefaultInfo(files = depset(transitive=all_files), runfiles = runfiles),
       NixInfo(
           outputs = {}, 
           out_hash = "",
           closure = depset(transitive=closure),
           store_paths = store_paths,
           env = ctx.attr.env,
       )
    ]

nix_package = rule(
    implementation = _nix_package_impl,
    attrs = {
        "deps": attr.label_list(),
        "srcs": attr.label_list(allow_files = True),
        "env": attr.string_dict(doc = "Environment variables exposed by this package"),
        "flake": attr.label(allow_single_file = True),
    }
)

def _nix_derivation_impl(ctx):
    primary_name = ctx.label.name
    all_outputs = []
    store_names = ctx.attr.store_names
    if not store_names:
        store_names = {"out": primary_name}
    fake_outputs = {}
    primary_fake = None
    
    # Map of Bazel File -> /nix/store/... path
    store_paths = {}

    for out_name, store_basename in store_names.items():
        out_dir = ctx.actions.declare_directory(primary_name + ".outputs/" + store_basename)
        all_outputs.append(out_dir)
        fake_outputs[out_name] = out_dir
        store_paths[out_dir] = "/nix/store/" + store_basename
        if out_name == "out" or not primary_fake:
            primary_fake = out_dir

    env = dict(ctx.attr.env)
    
    builder_inputs = []
    if ctx.attr.builder:
        builder_exe = ctx.executable.builder
        builder_inputs.append(builder_exe)
        builder_path = builder_exe.path
    else:
        builder_path = ctx.attr.builder_path

    # Collect transitive closure and mount points
    closure_parts = []
    mount_args = []

    # Resolve source_mappings keys into Label objects
    resolved_source_mappings = {}
    for k, v in ctx.attr.source_mappings.items():
        if ":" in k or k.startswith("/") or k.startswith("@"):
            lbl = ctx.label.relative(k)

            resolved_source_mappings[lbl] = v
        else:
            resolved_source_mappings[k] = v

    for src in ctx.attr.srcs:
        if NixInfo in src:
            closure_parts.append(src[NixInfo].closure)
            for f, p in src[NixInfo].store_paths.items():
                store_paths[f] = p
        else:
            closure_parts.append(src.files)
            # For files from nix_sources or external sources, use source_mappings
            # We match by label (Canonical Label comparison) or basename

            for f in src.files.to_list():
                matched_p = None
                
                if src.label in resolved_source_mappings:
                    matched_p = resolved_source_mappings[src.label]
                elif f.basename in resolved_source_mappings:
                    matched_p = resolved_source_mappings[f.basename]
                
                if matched_p:

                    store_paths[f] = matched_p
                else:
                    pass
    full_closure = depset(
        direct = all_outputs + ctx.files.srcs,
        transitive = closure_parts
    )

    # Usage: runner <builder> <realOutDirBase> [--mount host:sandbox...] -- [args...]
    args = ctx.actions.args()
    args.add(builder_path)
    args.add(primary_fake.dirname)
    
    # Add mount points
    for f, p in store_paths.items():
        if f not in all_outputs: # Only mount inputs
            args.add("--mount", "%s:%s" % (f.path, p))

    # Clean --output flags
    for out_name, store_basename in store_names.items():
        real_store_path = "/nix/store/" + store_basename
        args.add("--output", "%s:%s" % (out_name, real_store_path))

    args.add("--")
    for a in ctx.attr.args:
        args.add(a)

    ctx.actions.run(
        outputs = all_outputs,
        inputs = depset(direct = builder_inputs, transitive = closure_parts),
        executable = ctx.executable._tool,
        arguments = [args],
        env = env,
        mnemonic = "NixDerivation",
        use_default_shell_env = True,
        execution_requirements = {
            "no-sandbox": "1",
            "no-remote": "1", # Also unlikely to work remotely as-is
            "local": "1",
            "supports-path-mapping": "1",
        }
    )
    
    # Generate mounts manifest for runfiles auto-discovery
    manifest_content = "{\n"
    sorted_paths = sorted(store_paths.items(), key=lambda x: x[0].short_path)
    for i, (f, p) in enumerate(sorted_paths):
        # We use short_path as the key (relative to runfiles root)
        # JSON formatting: key quoted, value quoted
        manifest_content += '  "%s": "%s"' % (f.short_path, p)
        if i < len(sorted_paths) - 1:
            manifest_content += ",\n"
    manifest_content += "\n}"
    
    manifest_file = ctx.actions.declare_file(ctx.label.name + ".nix-mounts.json")
    ctx.actions.write(manifest_file, manifest_content)
    all_outputs.append(manifest_file)
    
    # Generate env manifest
    env_content = "{\n"
    sorted_envs = sorted(ctx.attr.env.items())
    for i, (k, v) in enumerate(sorted_envs):
        env_content += '  "%s": "%s"' % (k, v)
        if i < len(sorted_envs) - 1:
            env_content += ",\n"
    env_content += "\n}"
    
    env_file = ctx.actions.declare_file(ctx.label.name + ".nix-env.json")
    ctx.actions.write(env_file, env_content)
    all_outputs.append(env_file)

    return [
        DefaultInfo(files = depset(all_outputs)),
        NixInfo(outputs = fake_outputs, out_hash = "todo", closure = full_closure, store_paths = store_paths, env = {}),
    ]

nix_derivation = rule(
    implementation = _nix_derivation_impl,
    doc = "Defines a Nix derivation build using a custom builder or script.",
    attrs = {
        "builder": attr.label(
            executable = True,
            cfg = "exec",
            mandatory = False,
            doc = "The builder executable label (e.g. //sandbox:builder).",
        ),
        "builder_path": attr.string(
            doc = "Absolute path to builder executable inside the sandbox (if not using 'builder' label).",
        ),
        "srcs": attr.label_list(
            allow_files = True,
            doc = "Input files and dependencies for the build.",
        ),
        "args": attr.string_list(
            doc = "Arguments passed to the builder.",
        ),
        "env": attr.string_dict(
            doc = "Environment variables set during the build.",
        ),
        "store_names": attr.string_dict(
            doc = "Map of output names (e.g. 'out') to their store directory names (basename of /nix/store path).",
        ),
        "source_hashes": attr.string_dict(
            doc = "Map of source filenames to expected 'hash:algo' for deferred verification (optional).",
        ),
        "source_mappings": attr.string_dict(
            doc = "Map of labels or basenames to /nix/store paths for input mapping.",
        ),
        "_tool": attr.label(
            default = "//cmd/nix_builder",
            executable = True,
            cfg = "exec",
            doc = "Internal reference to the nix_builder tool.",
        ),
    },
)

def _nix_nar_unpack_impl(ctx):
    out = ctx.actions.declare_directory(ctx.attr.name)
    
    args = ctx.actions.args()
    args.add("-src", ctx.file.src)
    args.add("-dest", out.path)
    args.add("-compression", ctx.attr.compression)
    
    ctx.actions.run(
        outputs = [out],
        inputs = [ctx.file.src],
        executable = ctx.executable._tool,
        arguments = [args],
        mnemonic = "NixNarUnpack",
        progress_message = "Unpacking NAR %s" % ctx.file.src.short_path,
        use_default_shell_env = True,
    )

    # Collect transitive closure and store paths
    closure = []
    store_paths = {}
    
    # Collect transitive closure and store paths from deps

    for dep in ctx.attr.deps:
        if NixInfo in dep:
             closure.append(dep[NixInfo].closure)
             store_paths.update(dep[NixInfo].store_paths)
    
    # Store path for self
    if ctx.attr.store_path:
        store_paths[out] = ctx.attr.store_path

    files_depset = depset([out], transitive = closure)
    return [
        DefaultInfo(
            files = files_depset,
            runfiles = ctx.runfiles(transitive_files = files_depset)
        ),
        NixInfo(
            outputs = {},
            out_hash = "",
            closure = files_depset,
            store_paths = store_paths,
            env = {}
        )
    ]

nix_nar_unpack = rule(
    implementation = _nix_nar_unpack_impl,
    attrs = {
        "src": attr.label(allow_single_file = True, mandatory = True),
        "deps": attr.label_list(providers = [NixInfo]),
        "store_path": attr.string(doc = "Absolute /nix/store path this output corresponds to"),
        "compression": attr.string(default = "xz", values = ["xz", "bzip2", "none"]),
        "_tool": attr.label(
            default = Label("@nix_bazel_via_bwrap//cmd/nix_tool"),
            executable = True,
            cfg = "exec",
        ),
    },
)

def _nix_binary_impl(ctx):
    out = ctx.actions.declare_file(ctx.label.name)
    config_out = ctx.actions.declare_file(ctx.label.name + ".nix-runner.json")
    
    config_mounts = {}
    
    # Calculate mounts from targets
    for target, mount_path in ctx.attr.mounts.items():
        # Heuristic: pick the first file that is likely the output directory
        # Avoiding .json metadata files
        selected_file = None
        for f in target[DefaultInfo].files.to_list():
            if f.extension != "json":
                selected_file = f
                break
        if not selected_file and len(target[DefaultInfo].files.to_list()) > 0:
             selected_file = target[DefaultInfo].files.to_list()[0]
        
        if selected_file:
            config_mounts[selected_file.short_path] = mount_path
        else:
            print("WARNING: No files found for mount target %s" % target.label)

    config = {
        "mounts": config_mounts,
        "env": ctx.attr.env,
        "command": ctx.attr.exe_path,
        "args": ctx.attr.args,
        "work_dir": "",
        "impure": ctx.attr.impure,
    }

    ctx.actions.write(config_out, json.encode(config))

    # Create symlink to runner
    ctx.actions.symlink(
        output = out,
        target_file = ctx.executable._runner,
        is_executable = True,
    )

    # Runfiles
    runfiles = ctx.runfiles(files = [config_out, ctx.executable._runner])
    for target in ctx.attr.mounts:
        runfiles = runfiles.merge(target[DefaultInfo].default_runfiles)
        # Also ensure the files themselves are in runfiles (transitive_files)
        # default_runfiles usually includes them, but explicit add is safe
        runfiles = runfiles.merge(ctx.runfiles(transitive_files = target[DefaultInfo].files))

    return [DefaultInfo(executable = out, runfiles = runfiles)]

nix_binary = rule(
    implementation = _nix_binary_impl,
    attrs = {
        "mounts": attr.label_keyed_string_dict(allow_files = True),
        "env": attr.string_dict(),
        "exe_path": attr.string(),
        "impure": attr.bool(default = False, doc = "If True, mounts host system libraries (/bin, /lib, etc)."),
        "_runner": attr.label(default = Label("//cmd/nix_runner"), executable = True, cfg = "target"),
    },
    executable = True,
)






def _nix_flake_run_under_impl(ctx):
    out = ctx.actions.declare_file(ctx.label.name)
    config_out = ctx.actions.declare_file(ctx.label.name + ".nix-runner.json")
    
    # Provider-based configuration
    pkg_info = ctx.attr.src[NixInfo]
    
    # Construct config
    mounts = {}
    for f, p in pkg_info.store_paths.items():
        mounts[f.short_path] = p

    env = {}
    for k, v in pkg_info.env.items():
        env[k] = v

    config = {
        "mounts": mounts,
        "env": env,
        "command": ctx.attr.startup_cmd,
        "args": [],
        "work_dir": "", # Default
        "impure": False,
    }

    ctx.actions.write(config_out, json.encode(config))

    # Create symlink to runner
    ctx.actions.symlink(
        output = out,
        target_file = ctx.executable._runner,
        is_executable = True,
    )

    # Merge runfiles
    runfiles = ctx.runfiles(files = [config_out, ctx.executable._runner])
    runfiles = runfiles.merge(ctx.attr.src[DefaultInfo].default_runfiles)
    
    return [DefaultInfo(executable = out, runfiles = runfiles)]

nix_flake_run_under = rule(
    implementation = _nix_flake_run_under_impl,
    attrs = {
        "src": attr.label(mandatory = True, providers = [NixInfo]),
        "startup_cmd": attr.string(doc = "Optional command to execute on startup (before args)."),
        "_runner": attr.label(default = Label("//cmd/nix_runner"), executable = True, cfg = "target"),
    },
    executable = True,
)

def _debug_rule_impl(ctx):
    runfiles = ctx.runfiles()
    for dep in ctx.attr.deps:
        runfiles = runfiles.merge(dep[DefaultInfo].default_runfiles)
    
    executable = ctx.actions.declare_file(ctx.label.name + ".sh")
    ctx.actions.write(executable, "#!/bin/bash\necho Runfiles Debug", is_executable=True)
    return [DefaultInfo(executable=executable, runfiles=runfiles)]

debug_rule = rule(implementation=_debug_rule_impl, attrs={"deps": attr.label_list()}, executable=True)


