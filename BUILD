package(default_visibility = ["//visibility:public"])

load(":rules.bzl", "nix_derivation")

sh_binary(
    name = "bwrap_sandbox",
    srcs = ["bwrap_sandbox.sh"],
)

nix_derivation(
    name = "bootstrap_derivation",
    builder = "@bootstrap_busybox//file",
    args = ["sh", "-c", "echo 'Hello Pure Nix' > $out/hello"],
)
nix_derivation(
    name = "stage0_bootstrap_tools",
    builder = "@bootstrap_busybox//file",
    srcs = ["@bootstrap_tools//file"],
    # We unpack the tarball. 
    # $out is the output directory.
    # The tarball is mounted at /nix/store/bootstrap-tools.tar.xz (basename of the http_file output? actually http_file output is usually 'downloaded' or name of rule? 
    # Let's check what http_file produces. It produces a file named 'file' usually? Or 'downloaded'.
    # Note: `http_file` output name depends on `downloaded_file_path` attr, default is basename of URL if not specified.
    # So it should be `bootstrap-tools.tar.xz`.
    args = [
        "sh", 
        "-c", 
        "/nix/store/static-busybox unxz -c /nix/store/bootstrap-tools.tar.xz | /nix/store/static-busybox tar x -C $out"
    ],
)

