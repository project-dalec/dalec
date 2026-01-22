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
# - EDIT_ARGS: Newline-separated list of -replace= arguments for go mod edit
# - GIT_CONFIG_SCRIPT: Optional git configuration script
# - GOPRIVATE: Optional GOPRIVATE setting
# - GOINSECURE: Optional GOINSECURE setting
# - GOMOD_FILENAME: Filename for go.mod (default: go.mod)
# - GOSUM_FILENAME: Filename for go.sum (default: go.sum)
# - MODULE_PATHS: Colon-separated list of module paths to process

: "${PATCH_PATH:?PATCH_PATH is required}"
: "${EDIT_ARGS:?EDIT_ARGS is required}"
: "${GOMOD_FILENAME:=go.mod}"
: "${GOSUM_FILENAME:=go.sum}"
: "${MODULE_PATHS:=.}"

export GOMODCACHE="${TMP_GOMODCACHE}"
: > "$PATCH_PATH"
echo "Generating gomod patch with edit args:"
printf '%s\n' "$EDIT_ARGS"
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
  # Parse module info: rel_module_path|gomod_path|gosum_path|module_dir|rel_gomod_path|rel_gosum_path|orig_module_dir
  IFS='|' read -r rel_module_path gomod_path gosum_path module_dir rel_gomod_path rel_gosum_path orig_module_dir <<EOF
$module_info
EOF
  IFS=':'

  echo "# Process $rel_module_path"
  if [ -f "$gomod_path" ]; then
    vendor_path="$module_dir/vendor"
    orig_vendor_path="$orig_module_dir/vendor"
    orig_gomod_path="$orig_module_dir/$GOMOD_FILENAME"
    orig_gosum_path="$orig_module_dir/$GOSUM_FILENAME"
    rel_vendor_path="$rel_module_path/vendor"
    # Handle empty rel_module_path (root module)
    if [ -z "$rel_module_path" ]; then
      rel_vendor_path="vendor"
    fi

    # Check if vendor directory exists (use orig path for existence check)
    vendor_existed=false
    if [ -d "$orig_vendor_path" ]; then
      vendor_existed=true
    fi

    # Apply edits using go mod edit with each argument separately
    cd "$module_dir"

    # Read EDIT_ARGS line by line and pass each as a separate argument to go mod edit
    # Using a here-document instead of pipe to avoid subshell (ensures set -e works)
    while IFS= read -r arg; do
      if [ -n "$arg" ]; then
        go mod edit "$arg"
      fi
    done <<-EOF
	$EDIT_ARGS
	EOF

    go mod tidy

    # If go.work exists and go mod tidy bumped the go version, sync go.work
    gowork_path="$module_dir/go.work"
    if [ -f "$gowork_path" ]; then
      # Extract go version from go.mod (e.g., "go 1.24.0")
      go_version=$(grep "^go " "$gomod_path" | head -1)
      if [ -n "$go_version" ]; then
        # Update go.work to match the go.mod version
        # This prevents version mismatch errors when Kubernetes build scripts unset GOWORK
        sed -i "s/^go [0-9.]*/$go_version/" "$gowork_path"
        echo "  Updated go.work to: $go_version"
      fi
    fi

    # Only run go mod vendor if a vendor directory already existed
    if [ "$vendor_existed" = "true" ]; then
      # If go.work exists, use 'go work vendor' instead of 'go mod vendor'
      # because 'go mod vendor' cannot be run in workspace mode
      if [ -f "$gowork_path" ]; then
        echo "  Running go work vendor to sync workspace vendor directory"
        go work vendor
      else
        echo "  Running go mod vendor to sync existing vendor directory"
        go mod vendor
      fi
    fi

    cd - > /dev/null

    if [ ! -f "$gosum_path" ]; then
      touch "$gosum_path"
    fi

    # Capture diffs for go.mod and go.sum using read-only original mount
    diff -u --label "a/$rel_gomod_path" --label "b/$rel_gomod_path" "$orig_gomod_path" "$gomod_path" >> "$PATCH_PATH" || true
    if [ -f "$gosum_path" ] || [ -f "$orig_gosum_path" ]; then
      # Handle case where original go.sum doesn't exist
      if [ -f "$orig_gosum_path" ]; then
        diff -u --label "a/$rel_gosum_path" --label "b/$rel_gosum_path" "$orig_gosum_path" "$gosum_path" >> "$PATCH_PATH" || true
      else
        diff -u --label "a/$rel_gosum_path" --label "b/$rel_gosum_path" /dev/null "$gosum_path" >> "$PATCH_PATH" || true
      fi
    fi

    # Capture go.work diff if it exists and was modified
    gowork_path="$module_dir/go.work"
    orig_gowork_path="$orig_module_dir/go.work"
    rel_gowork_path="$rel_module_path/go.work"
    if [ -z "$rel_module_path" ]; then
      rel_gowork_path="go.work"
    fi
    if [ -f "$gowork_path" ] && [ -f "$orig_gowork_path" ]; then
      diff -u --label "a/$rel_gowork_path" --label "b/$rel_gowork_path" "$orig_gowork_path" "$gowork_path" >> "$PATCH_PATH" || true
    fi

    # Capture vendor directory diff only if vendor existed before
    if [ "$vendor_existed" = "true" ]; then
      echo "  Generating vendor directory diff"
      # Generate diff using read-only original mount and fix paths for patch format
      diff -Nur "$orig_vendor_path" "$vendor_path" | \
        sed "s@--- $orig_vendor_path@--- a/$rel_vendor_path@g; s@+++ $vendor_path@+++ b/$rel_vendor_path@g" >> "$PATCH_PATH" || true
    fi
  fi
done

echo 'Gomod patch generation complete'
if [ -s "$PATCH_PATH" ]; then
  echo 'Patch file created with changes'
  wc -l "$PATCH_PATH"
else
  echo 'No changes detected - patch file is empty'
fi
