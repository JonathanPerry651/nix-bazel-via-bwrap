def _browsers_test_impl(ctx):
    info = ctx.toolchains["//playwright_browsers:browsers_toolchain_type"]
    
    # Resolve target files
    nix_target = info.nix_target
    
    # Create runner script
    wrapper = ctx.actions.declare_file(ctx.label.name + "_runner.sh")
    
    # Construct env exports
    env_exports = []
    for k, v in info.env.items():
        # Expand $(location ...) if present? 
        # For prototype, let's assume raw string or we manually expand.
        # But wait, $(location :browsers) needs expansion contexts.
        # For simplicity, we'll assume the user passes the expanded path or relative path.
        # Re-reading plan: env = {"PLAYWRIGHT_BROWSERS_PATH": "$(location :browsers)"} 
        # Gazelle would generate that. But `nix_toolchain_info` doesn't automatically expand $(location).
        # We need `ctx.expand_location` in `nix_toolchain_info`! 
        # I missed that in step 1. I will fix `nix_toolchain_info` first or handle it here?
        # `nix_toolchain_info` does not have access to the *consumer's* location expansion context easily
        # but it DOES receive the `target` attribute. `ctx.expand_location` works if `target` is in `data` or `srcs`?
        # Actually `ctx.expand_location` takes a list of targets.
        
        # Let's pivot: Gazelle/User puts "$(location :browsers)" in the attribute.
        # `nix_toolchain_info` implementation should expand it using `[ctx.attr.target]`.
        env_exports.append("export {}='{}'".format(k, v))
        
    wrapper_content = """#!/bin/bash
{env_vars}
echo "Running test with environment:"
env | grep PLAYWRIGHT
exec /bin/bash "{test_script}" "$@"
""".format(
        env_vars = "\n".join(env_exports),
        test_script = ctx.file.src.short_path
    )
    
    ctx.actions.write(wrapper, wrapper_content, is_executable=True)
    
    # Runfiles
    runfiles = ctx.runfiles(files = [ctx.file.src])
    runfiles = runfiles.merge(ctx.runfiles(transitive_files = nix_target[DefaultInfo].files))
    
    return [DefaultInfo(
        executable = wrapper,
        runfiles = runfiles
    )]

browsers_test = rule(
    implementation = _browsers_test_impl,
    attrs = {
        "src": attr.label(allow_single_file = True, mandatory = True),
    },
    test = True,
    toolchains = ["//playwright_browsers:browsers_toolchain_type"],
)
