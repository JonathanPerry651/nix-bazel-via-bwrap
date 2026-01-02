
load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_file")
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
