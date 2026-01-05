load("@rules_bazel_integration_test//bazel_integration_test:defs.bzl", "bazel_integration_test", "integration_test_utils")

def nix_integration_test(name, test_runner, test_flake = None, workspace_path = None, **kwargs):
    if not workspace_path:
        workspace_path = name + "_workspace"

    workspace_files = integration_test_utils.glob_workspace_files(workspace_path) + [
        "//:distribution",
        "//nix_deps:all_files",
    ]
    
    if test_flake:
        workspace_files.append(test_flake)

    bazel_integration_test(
        name = name,
        test_runner = test_runner,
        workspace_path = workspace_path,
        workspace_files = workspace_files,
        bazel_version = "7.4.1",
        # Tags for local execution (hermetic but needs sandbox access)
        tags = kwargs.pop("tags", []) + ["local", "requires-network"],
        **kwargs
    )
