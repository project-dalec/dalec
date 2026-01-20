#!/bin/sh
set -e

# Ensure Go is in PATH
# On older Ubuntu versions, Go is installed in versioned directories like /usr/lib/go-1.18/bin
if ! command -v go >/dev/null 2>&1; then
  for godir in /usr/lib/go-*/bin; do
    if [ -d "$godir" ] && [ -x "$godir/go" ]; then
      export PATH="$godir:$PATH"
      break
    fi
  done
fi

# Environment variables expected:
# - PATCH_PATH: Output path for the patch file
# - EDIT_CMD: The go mod edit command to run
# - GIT_CONFIG_SCRIPT: Optional git configuration script
# - GOPRIVATE: Optional GOPRIVATE setting
# - GOINSECURE: Optional GOINSECURE setting
# - GOMOD_FILENAME: Filename for go.mod (default: go.mod)
# - GOSUM_FILENAME: Filename for go.sum (default: go.sum)
# - MODULE_PATHS: Colon-separated list of module paths to process

: "${PATCH_PATH:?PATCH_PATH is required}"
: "${EDIT_CMD:?EDIT_CMD is required}"
: "${GOMOD_FILENAME:=go.mod}"
: "${GOSUM_FILENAME:=go.sum}"
: "${MODULE_PATHS:=.}"

export GOMODCACHE="${TMP_GOMODCACHE}"
: > "$PATCH_PATH"
echo "Generating gomod patch with edits: $EDIT_CMD"
echo ''

# Setup git authentication if provided
if [ -n "$GIT_CONFIG_SCRIPT" ]; then
  echo "# Setup git authentication"
  eval "$GIT_CONFIG_SCRIPT"
fi

# Setup Go private/insecure settings if provided
if [ -n "$GOPRIVATE" ]; then
  export GOPRIVATE
fi
if [ -n "$GOINSECURE" ]; then
  export GOINSECURE
fi

# Process each module path
IFS=':'
for module_info in $MODULE_PATHS; do
  # Parse module info: rel_module_path|gomod_path|gosum_path|module_dir|rel_gomod_path|rel_gosum_path
  IFS='|' read -r rel_module_path gomod_path gosum_path module_dir rel_gomod_path rel_gosum_path <<EOF
$module_info
EOF
  IFS=':'

  echo "# Process $rel_module_path"
  if [ -f "$gomod_path" ]; then
    tmpdir=$(mktemp -d)
    cp "$gomod_path" "$tmpdir/$GOMOD_FILENAME"
    if [ -f "$gosum_path" ]; then
      cp "$gosum_path" "$tmpdir/$GOSUM_FILENAME"
    else
      : > "$tmpdir/$GOSUM_FILENAME"
    fi

    cd "$module_dir"
    eval "$EDIT_CMD"
    go mod tidy
    cd - > /dev/null

    if [ ! -f "$gosum_path" ]; then
      touch "$gosum_path"
    fi

    # Capture diffs and append to patch file
    diff -u --label "a/$rel_gomod_path" --label "b/$rel_gomod_path" "$tmpdir/$GOMOD_FILENAME" "$gomod_path" >> "$PATCH_PATH" || true
    if [ -f "$gosum_path" ] || [ -s "$tmpdir/$GOSUM_FILENAME" ]; then
      diff -u --label "a/$rel_gosum_path" --label "b/$rel_gosum_path" "$tmpdir/$GOSUM_FILENAME" "$gosum_path" >> "$PATCH_PATH" || true
    fi
    rm -rf "$tmpdir"
  fi
done

echo 'Gomod patch generation complete'
if [ -s "$PATCH_PATH" ]; then
  echo 'Patch file created with changes'
  wc -l "$PATCH_PATH"
else
  echo 'No changes detected - patch file is empty'
fi
