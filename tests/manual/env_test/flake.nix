{
  description = "Test flake for environment variables";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-23.11";

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
    in
    {
      devShells.${system}.default = pkgs.mkShell {
        buildInputs = [ pkgs.hello ];
        env = {
            MY_VAR = "foo";
            JAVA_HOME = "${pkgs.hello}"; # dummy path for testing
        };
        shellHook = ''
          export HOOK_VAR="bar"
        '';
      };
    };
}
