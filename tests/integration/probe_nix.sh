#!/bin/bash
echo "=== Nix Probe ==="
echo "PATH: $PATH"
which nix || echo "nix not found in PATH"
if command -v nix; then
    echo "Nix version:"
    nix --version
    echo "Nix conf:"
    nix show-config || echo "failed to show config"
fi
echo "Environment:"
env
