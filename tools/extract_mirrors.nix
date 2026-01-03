{ pkgs ? import <nixpkgs> {} }:
let
  mirrors = import (pkgs.path + "/pkgs/build-support/fetchurl/mirrors.nix");
in
  builtins.toJSON mirrors
