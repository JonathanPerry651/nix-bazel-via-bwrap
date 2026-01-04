load("@gazelle//:def.bzl", "gazelle", "gazelle_binary")
load(":rules.bzl", "nix_derivation")

package(default_visibility = ["//visibility:public"])

gazelle_binary(
    name = "gazelle_binary",
    languages = [
        "@gazelle//language/go",
        "//pkg/gazelle/language/nix",
    ],
)

gazelle(
    name = "gazelle",
    gazelle = "gazelle_binary",
    prefix = "github.com/JonathanPerry651/nix-bazel-via-bwrap",
    visibility = ["//visibility:public"],
)

sh_binary(
    name = "bwrap_sandbox",
    srcs = ["bwrap_sandbox.sh"],
)

filegroup(
    name = "distribution",
    srcs = glob(
        [
            "*.bzl",
            "*.bazel",
            "BUILD",
            "*.mod",
            "*.sum",
            "*.lock",
            "bwrap_sandbox.sh",
            "nix-portable",
        ],
        exclude = [
            "bazel-*",
            ".*",
            "tests/**",
        ],
    ) + [
        "//cache:all_files",
        "//cmd/nix_tool:all_files",
        "//nix_deps:all_files",
        "//nix_deps/nix_sources:all_files",
        "//pkg/gazelle/language/nix:all_files",
        "//pkg/sandbox:all_files",
        "//tests/integration:all_files",
    ],
    visibility = ["//visibility:public"],
)
