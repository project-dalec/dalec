package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// Matrix represents the GitHub Actions matrix configuration.
type Matrix struct {
	Include []SuiteInfo `json:"include" yaml:"include"`
}

// SuiteInfo represents a single test suite entry in the matrix.
type SuiteInfo struct {
	// Name is the display name for the GitHub Actions UI (e.g., "Mariner2").
	Name string `json:"name" yaml:"name"`
	// Suite is the full test name passed to go test -run (e.g., "TestDalecTargetMariner2").
	Suite string `json:"suite" yaml:"suite"`
	// Skip is used for the "other" suite to skip all target-specific tests.
	Skip string `json:"skip,omitempty" yaml:"skip,omitempty"`
}

const testTargetPrefix = "TestDalecTarget"

// ParseTestList parses the output of `go test -list` and extracts test names
// that match the TestDalecTarget prefix.
func ParseTestList(r io.Reader) ([]string, error) {
	testNameRegex := regexp.MustCompile(`^` + testTargetPrefix + `\w+$`)
	var testNames []string

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if testNameRegex.MatchString(line) {
			testNames = append(testNames, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading test list: %w", err)
	}

	return testNames, nil
}

// BuildMatrix constructs a GitHub Actions matrix from a list of test names.
// It creates a suite entry for each test, plus an "other" suite that skips
// all target-specific tests.
func BuildMatrix(testNames []string) (*Matrix, error) {
	if len(testNames) == 0 {
		return nil, fmt.Errorf("no %s* tests found", testTargetPrefix)
	}

	// Sort test names for consistent output
	sorted := make([]string, len(testNames))
	copy(sorted, testNames)
	sort.Strings(sorted)

	var matrix Matrix

	for _, testName := range sorted {
		// Extract display name by removing the prefix
		displayName := strings.TrimPrefix(testName, testTargetPrefix)
		matrix.Include = append(matrix.Include, SuiteInfo{
			Name:  displayName,
			Suite: testName,
		})
	}

	// Add the "other" suite that skips all target-specific tests
	matrix.Include = append(matrix.Include, SuiteInfo{
		Name:  "other",
		Suite: "other",
		Skip:  strings.Join(sorted, "|"),
	})

	return &matrix, nil
}
