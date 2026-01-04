{
  description = "Minimal Hello World using cached Nixpkgs";

  # Use the generic nixpkgs input, which will be overridden by Gazelle tooling
  inputs.nixpkgs.url = "nixpkgs";

  outputs = { self, nixpkgs }: let
    pkgs = nixpkgs.legacyPackages.x86_64-linux;
  in {
    packages.x86_64-linux.default = pkgs.writeScriptBin "hello" ''
      echo "Hello World"
    '';
  };
}
