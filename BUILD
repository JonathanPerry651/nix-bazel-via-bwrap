load("@gazelle//:def.bzl", "gazelle", "gazelle_binary")
load(":rules.bzl", "nix_derivation")

package(default_visibility = ["//visibility:public"])

gazelle_binary(
    name = "gazelle_binary",
    languages = [
        "@gazelle//language/go",
        "//gazelle_nix/language/nix",
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
    srcs = glob([
        "*.bzl",
        "*.bazel",
        "BUILD",
        "*.mod",
        "*.sum",
        "*.lock",
        "bwrap_sandbox.sh",
        "nix-portable",
    ], exclude = ["bazel-*", ".*", "tests/**"]) + [
        "//cache:all_files",
        "//gazelle_nix/language/nix:all_files",
        "//cmd/nix_tool:all_files",
        "//sandbox:all_files",
        "//generator:all_files",
        "//nixpkgs:all_files",
        "//nix_sources:all_files",
    ],
    visibility = ["//visibility:public"],
)
