// Package dalec provides BuildKit frontend functionality for building
// system packages from declarative specifications.
package dalec

import (
	"fmt"
	"path/filepath"
	"strings"
)

// GomodEditScript returns a shell script snippet that applies go.mod replace and require
// directives defined in the spec's gomod generators. The snippet is intended to be executed
// from the root of the extracted source tree prior to running build steps so that the
// modifications persist for the actual build.
//
// Returns an error if any replace or require directives are malformed.
func GomodEditScript(spec *Spec) (string, error) {
	if spec == nil || !spec.HasGomods() {
		return "", nil
	}

	var builder strings.Builder

	sourceNames := SortMapKeys(spec.Sources)
	for _, sourceName := range sourceNames {
		src := spec.Sources[sourceName]
		if !src.IsDir() {
			continue
		}

		for _, generator := range src.Generate {
			gomod := generator.Gomod
			if gomod == nil {
				continue
			}

			if !gomod.HasEdits() {
				continue
			}

			basePath := sourceName
			if generator.Subpath != "" {
				basePath = filepath.Join(basePath, generator.Subpath)
			}

			paths := gomod.Paths
			if len(paths) == 0 {
				paths = []string{"."}
			}

			for _, p := range paths {
				rel := filepath.Join(basePath, p)
				rel = filepath.Clean(rel)
				if rel == "" {
					rel = "."
				}

				// Normalize to POSIX path separators for shell scripts.
				rel = filepath.ToSlash(rel)
				goModPath := filepath.ToSlash(filepath.Join(rel, "go.mod"))

				fmt.Fprintf(&builder, "if [ -f %q ]; then\n", goModPath)
				fmt.Fprintln(&builder, "  (")
				fmt.Fprintf(&builder, "    cd %q\n", rel)

				for _, replace := range gomod.GetReplace() {
					arg, err := replace.goModEditArg()
					if err != nil {
						return "", fmt.Errorf("invalid gomod replace configuration in source %q: %w", sourceName, err)
					}
					fmt.Fprintf(&builder, "    go mod edit -replace=%q\n", arg)
				}

				for _, require := range gomod.GetRequire() {
					arg, err := require.goModEditArg()
					if err != nil {
						return "", fmt.Errorf("invalid gomod require configuration in source %q: %w", sourceName, err)
					}
					fmt.Fprintf(&builder, "    go mod edit -require=%q\n", arg)
				}

				fmt.Fprintln(&builder, "    go mod tidy")
				fmt.Fprintln(&builder, "    go mod download")

				fmt.Fprintln(&builder, "  )")
				fmt.Fprintln(&builder, "fi")
				builder.WriteString("\n")
			}
		}
	}

	return builder.String(), nil
}
