{
  description = "A flake that exposes a derivation";

  outputs = { self }: {
    packages.x86_64-linux.default = derivation {
      name = "simple-drv";
      system = "x86_64-linux";
      builder = "/bin/sh";
      args = ["-c" "echo hello > $out"];
    };
  };
}
