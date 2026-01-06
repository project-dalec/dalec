package main

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConsoleFormatter_FormatResults(t *testing.T) {
	handler := &resultsHandler{}

	for event := range readTestEvents(t) {
		err := handler.HandleEvent(event)
		assert.NoError(t, err)
	}

	var output bytes.Buffer
	formatter := &consoleFormatter{modName: "mypackage", verbose: false}

	err := formatter.FormatResults(handler.Results(), &output)
	assert.NoError(t, err)

	expected := "::group::some_package.TestGenFail\n" + testEventFailOutput + "::endgroup::\n"
	assert.Equal(t, output.String(), expected)
}

func TestErrorAnnotationFormatter_FormatResults(t *testing.T) {
	handler := &resultsHandler{results: make(map[string]*TestResult)}

	for event := range readTestEvents(t) {
		err := handler.HandleEvent(event)
		assert.NoError(t, err)
	}

	var output bytes.Buffer
	formatter := &errorAnnotationFormatter{}

	err := formatter.FormatResults(handler.Results(), &output)
	assert.NoError(t, err)

	expected := "::error file=foo_test.go,line=43::" + strings.ReplaceAll(testLogsAnnotation, "\n", "%0A") + "\n"
	assert.Equal(t, output.String(), expected)
}

func TestGetLastFileLine(t *testing.T) {
	input := "    file1_test.go:10: some error\n    file2_test.go:20: another error\n"
	rdr := strings.NewReader(input)
	file, line, err := getLastFileLine(rdr)
	assert.NoError(t, err)
	assert.Equal(t, file, "file2_test.go")
	assert.Equal(t, line, 20)
}

func TestGetTestOutputLoc(t *testing.T) {
	t.Run("ValidInput", func(t *testing.T) {
		file, line, ok := getTestOutputLoc("    file.go:123: some output")
		assert.True(t, ok)
		assert.Equal(t, file, "file.go")
		assert.Equal(t, line, "123")
	})

	t.Run("InvalidInput", func(t *testing.T) {
		_, _, ok := getTestOutputLoc("invalid input")
		assert.False(t, ok)
	})
}

func TestUrlEncodeNewlineReader(t *testing.T) {
	input := "line1\nline2\nline3"
	rdr := &urlEncodeNewlineReader{rdr: bufio.NewReader(strings.NewReader(input))}
	output, err := io.ReadAll(rdr)
	assert.NoError(t, err)
	assert.Equal(t, string(output), "line1%0Aline2%0Aline3")
}
