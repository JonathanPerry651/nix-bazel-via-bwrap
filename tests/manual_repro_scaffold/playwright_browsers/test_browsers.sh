#!/bin/bash
set -e

echo "Running browser test verification..."
echo "PWD: $(pwd)"
echo "Listing files:"
ls -R

echo "Env check:"
env | grep HELLO_RUNNER || echo "HELLO_RUNNER not found in env"

echo "Arg check:"
echo "Arg count: $#"

# Attempt to find runner
RUNNER=$(find . -name "hello_runner.bash" | head -n 1)

if [ -z "$RUNNER" ]; then
    echo "ERROR: Could not find hello_runner.bash"
    # Try just hello_runner
    RUNNER=$(find . -name "hello_runner" | head -n 1)
fi

if [ -z "$RUNNER" ]; then
    echo "ERROR: Could not find hello_runner (any variation)"
    exit 1
fi

echo "Found runner at: $RUNNER"

echo "Invoking hello_runner: $RUNNER"
OUTPUT=$("$RUNNER" -c "cat /nix/store/hello-output/bin/hello")

echo "Output received: $OUTPUT"

if [[ "$OUTPUT" == "Hello from Nix Builder" ]]; then
    echo "SUCCESS: Found expected content from Nix Builder artifact."
else
    echo "FAILURE: Expected 'Hello from Nix Builder', got '$OUTPUT'"
    exit 1
fi
