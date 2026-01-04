def _browsers_test_impl(ctx):
    info = ctx.toolchains["//playwright_browsers:browsers_toolchain_type"]
    
    # Resolve target files
    nix_target = info.nix_target
    
    # Create runner script
    wrapper = ctx.actions.declare_file(ctx.label.name + "_runner.sh")
    
    # Construct env exports
    env_exports = []
    for k, v in info.env.items():
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
