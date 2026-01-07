{
  description = "Nix Integration for Bazel via Bubblewrap";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-23.11";

  outputs = { self, nixpkgs }: {
    lib.mkNakedShell = pkgs: { name, packages ? [], env ? {}, ... }:
      pkgs.runCommand name (env // {
        inherit packages;
        PATH = pkgs.lib.makeBinPath packages;
      }) ''
        mkdir -p $out/bin
        for p in $packages; do
          if [ -d "$p/bin" ]; then
            ln -s $p/bin/* $out/bin/
          fi
        done
      '';

    # Provide a default devShell for the repo itself if needed
    devShells.x86_64-linux.default = nixpkgs.legacyPackages.x86_64-linux.mkShell {
      buildInputs = with nixpkgs.legacyPackages.x86_64-linux; [
        bazel_7
        go
        nix
      ];
    };
  };
}
