//go:generate go run . ../../.github/workflows/ci/matrix.json

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

func main() {
	flag.Parse()
	if flag.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "Usage: integration-matrix [output-file]")
		os.Exit(1)
	}

	// Find the module root directory
	modRoot, err := findModuleRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to find module root: %s\n", err)
		os.Exit(1)
	}

	// Run go test -list to find all TestDalecTarget.* tests
	cmd := exec.Command("go", "test", "-list", testTargetPrefix+".*", "./test")
	cmd.Dir = modRoot
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(os.Stderr, "go test -list failed: %s\n%s\n", err, exitErr.Stderr)
		} else {
			fmt.Fprintf(os.Stderr, "go test -list failed: %s\n", err)
		}
		os.Exit(1)
	}

	// Parse the test list output
	testNames, err := ParseTestList(strings.NewReader(string(output)))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse test list: %s\n", err)
		os.Exit(1)
	}

	// Build the matrix
	matrix, err := BuildMatrix(testNames)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build matrix: %s\n", err)
		os.Exit(1)
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(matrix)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to marshal matrix: %s\n", err)
		os.Exit(1)
	}

	// Determine output destination
	outF := os.Stdout
	if outPath := flag.Arg(0); outPath != "" {
		if err := os.MkdirAll(path.Dir(outPath), 0755); err != nil {
			fmt.Fprintf(os.Stderr, "failed to create output directory: %s\n", err)
			os.Exit(1)
		}
		outF, err = os.Create(outPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to create output file: %s\n", err)
			os.Exit(1)
		}
		defer outF.Close()
	}

	if _, err := fmt.Fprintln(outF, string(jsonData)); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write output: %s\n", err)
		os.Exit(1)
	}
}

// findModuleRoot finds the root directory of the Go module by looking for go.mod.
func findModuleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found")
		}
		dir = parent
	}
}
