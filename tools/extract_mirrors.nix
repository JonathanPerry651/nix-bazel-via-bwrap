let
  pkgs = import <nixpkgs> {};
  mirrors = import (pkgs.path + "/pkgs/build-support/fetchurl/mirrors.nix");
in
  builtins.toJSON mirrors
