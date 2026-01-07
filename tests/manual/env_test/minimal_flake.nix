{
  description: "Minimal Shell Test";
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-23.11";
  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
      mkNakedShell = { name, buildInputs ? [], ... } @ attrs:
        derivation (attrs // {
          inherit name;
          inherit system;
          builder = "${pkgs.bash}/bin/bash";
          args = [ "-c" "exit 0" ];
          PATH = pkgs.lib.makeBinPath buildInputs;
          # nix print-dev-env looks for these
          outputs = [ "out" ];
        });
    in {
      devShells.${system}.naked = mkNakedShell {
        name = "naked-shell";
        buildInputs = [ pkgs.hello ];
        GREETING = "Naked Hello";
      };
    };
}
