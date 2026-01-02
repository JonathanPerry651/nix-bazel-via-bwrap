NixInfo = provider(
    fields = ["outputs", "out_hash", "closure", "store_paths"],
)

def _nix_derivation_impl(ctx):
    primary_name = ctx.label.name
    all_outputs = []
    store_names = ctx.attr.store_names
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
            print("DEBUG: Mapping string %s to Label %s" % (k, lbl))
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
            print("DEBUG: Checking src %s" % src.label)
            for f in src.files.to_list():
                matched_p = None
                
                if src.label in resolved_source_mappings:
                    matched_p = resolved_source_mappings[src.label]
                elif f.basename in resolved_source_mappings:
                    matched_p = resolved_source_mappings[f.basename]
                
                if matched_p:
                    print("DEBUG: matched f=%s to p=%s" % (f.path, matched_p))
                    store_paths[f] = matched_p
                else:
                    print("DEBUG: no match for f=%s (basename=%s)" % (f.path, f.basename))

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
    
    return [
        DefaultInfo(files = depset(all_outputs)),
        NixInfo(outputs = fake_outputs, out_hash = "todo", closure = full_closure, store_paths = store_paths),
    ]

nix_derivation = rule(
    implementation = _nix_derivation_impl,
    attrs = {
        "builder": attr.label(
            executable = True,
            cfg = "exec",
            mandatory = False, 
        ),
        "builder_path": attr.string(
            doc = "Path to builder inside sandbox, if not using 'builder' label",
        ),
        "srcs": attr.label_list(
            allow_files = True,
        ),
        "args": attr.string_list(),
        "env": attr.string_dict(),
        "store_names": attr.string_dict(
            doc = "Map of output names to their store directory names (basename of /nix/store path)",
        ),
        "source_hashes": attr.string_dict(
            doc = "Map of source filenames to expected 'hash:algo' for deferred verification",
        ),
        "source_mappings": attr.string_dict(
            doc = "Map of labels or basenames to /nix/store paths",
        ),
        "_tool": attr.label(
            default = "//sandbox:runner",
            executable = True,
            cfg = "exec",
        ),
    },
)
