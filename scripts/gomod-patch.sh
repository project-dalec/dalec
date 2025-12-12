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

export GOMODCACHE="${TMP_GOMODCACHE}"
: > "{{.PatchPath}}"
echo 'Generating gomod patch with edits: {{.EditCmd}}'
echo ''

{{- if .GitConfig}}
# Setup git authentication
{{.GitConfig}}
{{- end}}

{{- if .GoPrivate}}
export GOPRIVATE={{.GoPrivate}}
export GOINSECURE={{.GoInsecure}}

{{- end}}

{{- range .Modules}}
# Process {{.RelModulePath}}
if [ -f "{{.GoModPath}}" ]; then
  tmpdir=$(mktemp -d)
  cp "{{.GoModPath}}" "$tmpdir/{{$.GoModFilename}}"
  if [ -f "{{.GoSumPath}}" ]; then cp "{{.GoSumPath}}" "$tmpdir/{{$.GoSumFilename}}"; else : > "$tmpdir/{{$.GoSumFilename}}"; fi
  cd "{{.ModuleDir}}"
  {{$.EditCmd}}
  go mod tidy
  cd -
  if [ ! -f "{{.GoSumPath}}" ]; then touch "{{.GoSumPath}}"; fi

  # Capture diffs and append to patch file
  diff -u --label a/{{.RelGoModPath}} --label b/{{.RelGoModPath}} "$tmpdir/{{$.GoModFilename}}" "{{.GoModPath}}" >> "{{$.PatchPath}}" || true
  if [ -f "{{.GoSumPath}}" ] || [ -s "$tmpdir/{{$.GoSumFilename}}" ]; then
    diff -u --label a/{{.RelGoSumPath}} --label b/{{.RelGoSumPath}} "$tmpdir/{{$.GoSumFilename}}" "{{.GoSumPath}}" >> "{{$.PatchPath}}" || true
  fi
  rm -rf "$tmpdir"
fi

{{- end}}

echo 'Gomod patch generation complete'
if [ -s "{{.PatchPath}}" ]; then
  echo 'Patch file created with changes'
  wc -l "{{.PatchPath}}"
else
  echo 'No changes detected - patch file is empty'
fi
