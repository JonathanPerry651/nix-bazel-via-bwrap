#!/bin/bash
set -e

# Path to the runner binary when run via bazel test
RUNNER_PATH="./sandbox/runner_/runner"

if [ ! -f "$RUNNER_PATH" ]; then
    echo "Runner not found at $RUNNER_PATH"
    exit 1
fi

# Create a temporary directory for the test
TEST_DIR=$(mktemp -d)
trap 'rm -rf "$TEST_DIR"' EXIT

REAL_OUT_DIR="$TEST_DIR/out"
mkdir -p "$REAL_OUT_DIR"

# Define the output store path
OUT_HASH="00000000000000000000000000000000"
OUT_NAME="test-output"
STORE_PATH="/nix/store/$OUT_HASH-$OUT_NAME"

# Create a builder script that creates a dangling symlink if not handled correctly
BUILDER_SCRIPT="$TEST_DIR/builder.sh"
cat > "$BUILDER_SCRIPT" <<EOF
#!/bin/sh
mkdir -p \$out
echo "content" > \$out/file
# Create a symlink pointing to the absolute path of the file within the store
ln -s \$out/file \$out/link
EOF
chmod +x "$BUILDER_SCRIPT"

# Run the runner
echo "Running runner..."
export out="$STORE_PATH"
"$RUNNER_PATH" \
    /bin/sh \
    "$REAL_OUT_DIR" \
    --mount "/lib:/lib" \
    --mount "/lib64:/lib64" \
    --mount "/usr/lib:/usr/lib" \
    --mount "/bin:/bin" \
    --mount "/usr/bin:/usr/bin" \
    --mount "$TEST_DIR:$TEST_DIR" \
    --output "out:$STORE_PATH" \
    -- \
    "$BUILDER_SCRIPT"

# Check if the symlink in the output directory is valid or if it was dereferenced
LINK_PATH="$REAL_OUT_DIR/$OUT_HASH-$OUT_NAME/link"
if [ -L "$LINK_PATH" ]; then
    TARGET=$(readlink "$LINK_PATH")
    echo "Symlink created. Target: $TARGET"
    if [ -e "$LINK_PATH" ]; then
        echo "SUCCESS: Symlink is valid."
    else
        echo "FAILURE: Symlink is dangling (does not exist)."
        exit 1
    fi
elif [ -f "$LINK_PATH" ]; then
    echo "SUCCESS: Symlink was dereferenced to a regular file."
else
    echo "FAILURE: Symlink/File not created."
    exit 1
fi
