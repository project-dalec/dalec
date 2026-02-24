package main

import (
	"strings"
	"testing"
)

func TestParseTestList(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name: "parses valid test names",
			input: `TestDalecTargetMariner2
TestDalecTargetAzlinux3
TestDalecTargetJammy
ok  	github.com/project-dalec/dalec/test	0.003s
`,
			expected: []string{
				"TestDalecTargetMariner2",
				"TestDalecTargetAzlinux3",
				"TestDalecTargetJammy",
			},
		},
		{
			name: "ignores non-matching lines",
			input: `TestDalecTargetMariner2
TestSomethingElse
TestDalecTargetJammy
TestTarget
ok  	github.com/project-dalec/dalec/test	0.003s
`,
			expected: []string{
				"TestDalecTargetMariner2",
				"TestDalecTargetJammy",
			},
		},
		{
			name:     "handles empty input",
			input:    "",
			expected: nil,
		},
		{
			name: "handles whitespace",
			input: `  TestDalecTargetMariner2  
	TestDalecTargetJammy	
`,
			expected: []string{
				"TestDalecTargetMariner2",
				"TestDalecTargetJammy",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseTestList(strings.NewReader(tt.input))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d results, got %d: %v", len(tt.expected), len(result), result)
			}

			for i, expected := range tt.expected {
				if result[i] != expected {
					t.Errorf("result[%d] = %q, expected %q", i, result[i], expected)
				}
			}
		})
	}
}

func TestBuildMatrix(t *testing.T) {
	tests := []struct {
		name           string
		testNames      []string
		expectedSuites []SuiteInfo
		expectError    bool
	}{
		{
			name:      "builds matrix from test names",
			testNames: []string{"TestDalecTargetMariner2", "TestDalecTargetJammy"},
			expectedSuites: []SuiteInfo{
				{Name: "Jammy", Suite: "TestDalecTargetJammy"},
				{Name: "Mariner2", Suite: "TestDalecTargetMariner2"},
				{Name: "other", Suite: "other", Skip: "TestDalecTargetJammy|TestDalecTargetMariner2"},
			},
		},
		{
			name:      "sorts test names alphabetically",
			testNames: []string{"TestDalecTargetZebra", "TestDalecTargetAlpha", "TestDalecTargetMiddle"},
			expectedSuites: []SuiteInfo{
				{Name: "Alpha", Suite: "TestDalecTargetAlpha"},
				{Name: "Middle", Suite: "TestDalecTargetMiddle"},
				{Name: "Zebra", Suite: "TestDalecTargetZebra"},
				{Name: "other", Suite: "other", Skip: "TestDalecTargetAlpha|TestDalecTargetMiddle|TestDalecTargetZebra"},
			},
		},
		{
			name:        "returns error for empty input",
			testNames:   []string{},
			expectError: true,
		},
		{
			name:        "returns error for nil input",
			testNames:   nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matrix, err := BuildMatrix(tt.testNames)

			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(matrix.Include) != len(tt.expectedSuites) {
				t.Fatalf("expected %d suites, got %d", len(tt.expectedSuites), len(matrix.Include))
			}

			for i, expected := range tt.expectedSuites {
				actual := matrix.Include[i]
				if actual.Name != expected.Name {
					t.Errorf("suite[%d].Name = %q, expected %q", i, actual.Name, expected.Name)
				}
				if actual.Suite != expected.Suite {
					t.Errorf("suite[%d].Suite = %q, expected %q", i, actual.Suite, expected.Suite)
				}
				if actual.Skip != expected.Skip {
					t.Errorf("suite[%d].Skip = %q, expected %q", i, actual.Skip, expected.Skip)
				}
			}
		})
	}
}
