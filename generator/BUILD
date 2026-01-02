load("@rules_go//go:def.bzl", "go_binary", "go_library", "go_test")

# gazelle:resolve go github.com/bazelbuild/buildtools/build @buildtools//build


go_library(
    name = "generator_lib",
    srcs = [
        "builder.go",
        "copier.go",
        "generator.go",
        "indexer.go",
        "loader.go",
        "types.go",
    ],
    importpath = "github.com/jonathanp/nix-bazel-via-bwrap/generator",
    visibility = ["//visibility:private"],
    data = [
        "@nix_portable//file",
        "@nixpkgs//:all_files",
    ],
    deps = [
        "@buildtools//build",
        "@rules_go//go/runfiles",
    ],
)

go_test(
    name = "generator_test",
    srcs = ["generator_test.go"],
    embed = [":generator_lib"],
    data = glob(["testdata/**"]),
)

go_binary(
    name = "generator",
    embed = [":generator_lib"],
    visibility = ["//visibility:public"],
)
