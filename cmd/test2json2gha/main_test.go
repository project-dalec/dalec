package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

const (
	testModuleName = "some/module/name"
)

func TestDo(t *testing.T) {
	passGroup := "::group::some_package.TestGenPass\n" + testEventPassOutput + "::endgroup::\n"
	failGroup := "::group::some_package.TestGenFail\n" + testEventFailOutput + "::endgroup::\n"
	timeoutGroup := "::group::some_package.TestGenTimeout\n::warning::Test timed out\n" + testEventTimeoutOutput + "::endgroup::\n"
	skipGroup := "::group::some_package.TestGenSkip\n" + testEventSkipOutput + "::endgroup::\n"

	timeoutAnnotation := strings.TrimPrefix(testEventTimeoutOutput, "=== RUN   TestGenTimeout\n")
	annotation := "::error file=foo_test.go,line=43::" + strings.ReplaceAll(testLogsAnnotation, "\n", "%0A") + "\n" + "::error file=foo_test.go,line=9::" + strings.ReplaceAll(timeoutAnnotation, "\n", "%0A") + "\n"

	t.Run("verbose=false", func(t *testing.T) {
		input := strings.NewReader(testEventJSON)
		var output bytes.Buffer

		cfg := config{
			slowThreshold: 200 * time.Millisecond,
			modName:       testModuleName,
			verbose:       false,
			stream:        false,
		}

		anyFail, err := do(input, &output, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		// Non-verbose output should only include the grouped fail events + error annotations
		expected := failGroup + timeoutGroup + annotation
		assert.Equal(t, expected, output.String())
	})

	t.Run("verbose=true", func(t *testing.T) {
		input := strings.NewReader(testEventJSON)
		var output bytes.Buffer

		cfg := config{
			slowThreshold: 200 * time.Millisecond,
			modName:       testModuleName,
			verbose:       true,
			stream:        false,
		}

		anyFail, err := do(input, &output, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		// verbose output should include grouped events for all test results + error annotations
		expected := failGroup + passGroup + skipGroup + timeoutGroup + annotation
		assert.Equal(t, output.String(), expected)
	})

	t.Run("stream=true", func(t *testing.T) {
		input := strings.NewReader(testEventJSON)
		var output bytes.Buffer

		cfg := config{
			slowThreshold: 200 * time.Millisecond,
			modName:       testModuleName,
			verbose:       false,
			stream:        true,
		}

		anyFail, err := do(input, &output, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		// Stream output should include all the raw events + the grouped fail events + the annotation
		expected := testEventPassOutput + testEventFailOutput + testEventSkipOutput + testEventTimeoutOutput + testEventPackageOutput + failGroup + timeoutGroup + annotation
		assert.Equal(t, output.String(), expected)
	})

	t.Run("summary", func(t *testing.T) {
		dir := t.TempDir()
		f, err := os.CreateTemp(dir, "summary")
		assert.NilError(t, err)
		defer f.Close()

		t.Setenv("GITHUB_STEP_SUMMARY", f.Name())

		input := strings.NewReader(testEventJSON)

		cfg := config{
			slowThreshold: 200 * time.Millisecond,
			modName:       testModuleName,
			verbose:       false,
			stream:        false,
		}

		anyFail, err := do(input, io.Discard, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		output, err := os.ReadFile(f.Name())
		assert.NilError(t, err)

		expect := `## Test metrics
**Skipped:** 1 &nbsp;&nbsp;&nbsp;&nbsp; **Failed:** 2 &nbsp;&nbsp;&nbsp;&nbsp; **Total:** 5 &nbsp;&nbsp;&nbsp;&nbsp; **Elapsed:** 0.250s

` + "```" + `
[ min  max] cnt total%  sum (total count: 5)
[0.00 0.00] 4 80.00% 0.00 ................................................................................
[0.25 0.25] 1 20.00% 0.25 ....................
` + "```" + `

## Slow tests

` + "```" + `
some_package: 0.250s
` + "```" + `

`

		assert.Equal(t, string(output), expect)
	})

	t.Run("LogDir", func(t *testing.T) {
		input := strings.NewReader(testEventJSON)
		var output bytes.Buffer

		logDir := t.TempDir()
		cfg := config{
			slowThreshold: 500,
			modName:       "github.com/project-dalec/dalec/cmd/test2json2gha",
			verbose:       false,
			stream:        false,
			logDir:        logDir,
		}

		anyFail, err := do(input, &output, cfg)
		assert.NilError(t, err)
		assert.Assert(t, anyFail, "expected anyFail to be true due to failed tests")

		// Validate that logs are written to the specified directory
		entries, err := os.ReadDir(logDir)
		assert.NilError(t, err)
		assert.Assert(t, len(entries) > 0, "expected log files to be written to the log directory")
	})
}
