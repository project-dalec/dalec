package main

import (
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

const (
	testEventJSON = `
{"Time":"2025-03-31T09:46:20.078814-07:00","Action":"start","Package":"some_package"}
{"Time":"2025-03-31T09:46:20.327453-07:00","Action":"run","Package":"some_package","Test":"TestGenPass"}
{"Time":"2025-03-31T09:46:20.327531-07:00","Action":"output","Package":"some_package","Test":"TestGenPass","Output":"=== RUN   TestGenPass\n"}
{"Time":"2025-03-31T09:46:20.327583-07:00","Action":"output","Package":"some_package","Test":"TestGenPass","Output":"    foo_test.go:38: some log\n"}
{"Time":"2025-03-31T09:46:20.32762-07:00","Action":"output","Package":"some_package","Test":"TestGenPass","Output":"--- PASS: TestGenPass (0.00s)\n"}
{"Time":"2025-03-31T09:46:20.327654-07:00","Action":"pass","Package":"some_package","Test":"TestGenPass","Elapsed":0}
{"Time":"2025-03-31T09:46:20.327706-07:00","Action":"run","Package":"some_package","Test":"TestGenFail"}
{"Time":"2025-03-31T09:46:20.327713-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"=== RUN   TestGenFail\n"}
{"Time":"2025-03-31T09:46:20.327757-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"    foo_test.go:42: some error\n"}
{"Time":"2025-03-31T09:46:20.327758-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"    build.go.go:42: some build message\n"}
{"Time":"2025-03-31T09:46:20.327773-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"    foo_test.go:43: some fatal error\n"}
{"Time":"2025-03-31T09:46:20.327918-07:00","Action":"output","Package":"some_package","Test":"TestGenFail","Output":"--- FAIL: TestGenFail (0.00s)\n"}
{"Time":"2025-03-31T09:46:20.327934-07:00","Action":"fail","Package":"some_package","Test":"TestGenFail","Elapsed":0}
{"Time":"2025-03-31T09:46:20.327943-07:00","Action":"run","Package":"some_package","Test":"TestGenSkip"}
{"Time":"2025-03-31T09:46:20.327948-07:00","Action":"output","Package":"some_package","Test":"TestGenSkip","Output":"=== RUN   TestGenSkip\n"}
{"Time":"2025-03-31T09:46:20.327954-07:00","Action":"output","Package":"some_package","Test":"TestGenSkip","Output":"    foo_test.go:47: some skip reason\n"}
{"Time":"2025-03-31T09:46:20.327971-07:00","Action":"output","Package":"some_package","Test":"TestGenSkip","Output":"--- SKIP: TestGenSkip (0.00s)\n"}
{"Time":"2025-03-31T09:46:20.327977-07:00","Action":"skip","Package":"some_package","Test":"TestGenSkip","Elapsed":0}
{"Time":"2025-06-04T09:51:33.180898-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"=== RUN   TestGenTimeout\n"}
{"Time":"2025-06-04T09:51:33.180943-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"    foo_test.go:9: some log message\n"}
{"Time":"2025-06-04T09:51:34.183903-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"panic: test timed out after 1s\n"}
{"Time":"2025-06-04T09:51:34.183948-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\trunning tests:\n"}
{"Time":"2025-06-04T09:51:34.183955-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t\tTestGenTimeout (1s)\n"}
{"Time":"2025-06-04T09:51:34.18396-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\n"}
{"Time":"2025-06-04T09:51:34.183965-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"goroutine 23 [running]:\n"}
{"Time":"2025-06-04T09:51:34.18397-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"testing.(*M).startAlarm.func1()\n"}
{"Time":"2025-06-04T09:51:34.183974-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:2484 +0x308\n"}
{"Time":"2025-06-04T09:51:34.184331-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"created by time.goFunc\n"}
{"Time":"2025-06-04T09:51:34.184344-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/time/sleep.go:215 +0x38\n"}
{"Time":"2025-06-04T09:51:34.18435-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\n"}
{"Time":"2025-06-04T09:51:34.184355-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"goroutine 1 [chan receive]:\n"}
{"Time":"2025-06-04T09:51:34.184359-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"testing.(*T).Run(0x14000103500, {0x104c8448d?, 0x14000269b38?}, 0x10500a490)\n"}
{"Time":"2025-06-04T09:51:34.184365-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:1859 +0x388\n"}
{"Time":"2025-06-04T09:51:34.184493-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"testing.runTests.func1(0x14000103500)\n"}
{"Time":"2025-06-04T09:51:34.184503-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:2279 +0x40\n"}
{"Time":"2025-06-04T09:51:34.184509-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"testing.tRunner(0x14000103500, 0x14000269c68)\n"}
{"Time":"2025-06-04T09:51:34.184534-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:1792 +0xe4\n"}
{"Time":"2025-06-04T09:51:34.184543-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"testing.runTests(0x14000129410, {0x1056b7400, 0x19, 0x19}, {0x140002da760?, 0x7?, 0x1056c5420?})\n"}
{"Time":"2025-06-04T09:51:34.184708-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:2277 +0x3ec\n"}
{"Time":"2025-06-04T09:51:34.184716-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"testing.(*M).Run(0x140002f66e0)\n"}
{"Time":"2025-06-04T09:51:34.184721-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:2142 +0x588\n"}
{"Time":"2025-06-04T09:51:34.184726-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"main.main()\n"}
{"Time":"2025-06-04T09:51:34.18473-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t_testmain.go:93 +0x90\n"}
{"Time":"2025-06-04T09:51:34.184735-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\n"}
{"Time":"2025-06-04T09:51:34.18474-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"goroutine 22 [sleep]:\n"}
{"Time":"2025-06-04T09:51:34.184871-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"time.Sleep(0x12a05f200)\n"}
{"Time":"2025-06-04T09:51:34.184927-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/runtime/time.go:338 +0x158\n"}
{"Time":"2025-06-04T09:51:34.18495-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"github.com/project-dalec/dalec.TestGenTimeout(0x140001036c0)\n"}
{"Time":"2025-06-04T09:51:34.184965-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/Users/cpuguy83/dev/dalec/gen_test.go:10 +0x58\n"}
{"Time":"2025-06-04T09:51:34.18497-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"testing.tRunner(0x140001036c0, 0x10500a490)\n"}
{"Time":"2025-06-04T09:51:34.184978-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:1792 +0xe4\n"}
{"Time":"2025-06-04T09:51:34.184984-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"created by testing.(*T).Run in goroutine 1\n"}
{"Time":"2025-06-04T09:51:34.184988-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"\t/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:1851 +0x374\n"}
{"Time":"2025-06-04T09:51:34.185052-07:00","Action":"output","Package":"some_package","Test":"TestGenTimeout","Output":"exit status 2\n"}
{"Time":"2025-03-31T09:46:20.328007-07:00","Action":"output","Package":"some_package","Output":"FAIL\n"}
{"Time":"2025-03-31T09:46:20.328475-07:00","Action":"output","Package":"some_package","Output":"FAIL\tsome_package\t0.249s\n"}
{"Time":"2025-03-31T09:46:20.32851-07:00","Action":"fail","Package":"some_package","Elapsed":0.25}
`

	testEventPassOutput    = "=== RUN   TestGenPass\n    foo_test.go:38: some log\n--- PASS: TestGenPass (0.00s)\n"
	testEventFailOutput    = "=== RUN   TestGenFail\n    foo_test.go:42: some error\n    build.go.go:42: some build message\n    foo_test.go:43: some fatal error\n--- FAIL: TestGenFail (0.00s)\n"
	testEventSkipOutput    = "=== RUN   TestGenSkip\n    foo_test.go:47: some skip reason\n--- SKIP: TestGenSkip (0.00s)\n"
	testEventTimeoutOutput = `=== RUN   TestGenTimeout
    foo_test.go:9: some log message
panic: test timed out after 1s
	running tests:
		TestGenTimeout (1s)

goroutine 23 [running]:
testing.(*M).startAlarm.func1()
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:2484 +0x308
created by time.goFunc
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/time/sleep.go:215 +0x38

goroutine 1 [chan receive]:
testing.(*T).Run(0x14000103500, {0x104c8448d?, 0x14000269b38?}, 0x10500a490)
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:1859 +0x388
testing.runTests.func1(0x14000103500)
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:2279 +0x40
testing.tRunner(0x14000103500, 0x14000269c68)
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:1792 +0xe4
testing.runTests(0x14000129410, {0x1056b7400, 0x19, 0x19}, {0x140002da760?, 0x7?, 0x1056c5420?})
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:2277 +0x3ec
testing.(*M).Run(0x140002f66e0)
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:2142 +0x588
main.main()
	_testmain.go:93 +0x90

goroutine 22 [sleep]:
time.Sleep(0x12a05f200)
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/runtime/time.go:338 +0x158
github.com/project-dalec/dalec.TestGenTimeout(0x140001036c0)
	/Users/cpuguy83/dev/dalec/gen_test.go:10 +0x58
testing.tRunner(0x140001036c0, 0x10500a490)
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:1792 +0xe4
created by testing.(*T).Run in goroutine 1
	/opt/homebrew/Cellar/go/1.24.3/libexec/src/testing/testing.go:1851 +0x374
exit status 2
`
	testEventPackageOutput = "FAIL\nFAIL\tsome_package\t0.249s\n"

	// This is for the error annotations handler which skips build logs so as not to overflow the github annotation.
	testLogsAnnotation = "    foo_test.go:42: some error\n    foo_test.go:43: some fatal error\n"

	testPackageName = "some_package"
)

func mockTestResults(t *testing.T) iter.Seq[*TestResult] {
	h := &resultsHandler{results: make(map[string]*TestResult)}
	t.Cleanup(func() {
		h.Cleanup()
	})

	return h.Results()
}

func readTestEvents(t *testing.T) iter.Seq[*TestEvent] {
	dec := json.NewDecoder(strings.NewReader(testEventJSON))
	te := &TestEvent{}

	return func(yield func(*TestEvent) bool) {
		t.Helper()

		for {
			*te = TestEvent{}
			err := dec.Decode(te)
			if errors.Is(err, io.EOF) {
				break
			}
			assert.NilError(t, err)
			if !yield(te) {
				break
			}
		}
	}
}

func TestOutputStreamer(t *testing.T) {
	var output strings.Builder
	streamer := &outputStreamer{out: &output}

	for event := range readTestEvents(t) {
		err := streamer.HandleEvent(event)
		assert.NilError(t, err)
	}

	expectedOutput := testEventPassOutput + testEventFailOutput + testEventSkipOutput + testEventTimeoutOutput + testEventPackageOutput
	assert.Equal(t, expectedOutput, output.String())
}

func TestOutputStreamer_HandleEvent(t *testing.T) {
	var output strings.Builder
	streamer := &outputStreamer{out: &output}

	for event := range readTestEvents(t) {
		err := streamer.HandleEvent(event)
		assert.NilError(t, err)
	}

	expectedOutput := testEventPassOutput + testEventFailOutput + testEventSkipOutput + testEventTimeoutOutput + testEventPackageOutput
	assert.Equal(t, output.String(), expectedOutput)
}

func TestResultsHandler(t *testing.T) {
	// Validate specific results
	for r := range mockTestResults(t) {
		assert.Check(t, cmp.Equal(r.pkg, testPackageName))
		switch r.name {
		case "TestGenPass":
			assert.Check(t, !r.failed)
			assert.Check(t, !r.skipped)
			assert.Check(t, !r.timeout)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventPassOutput))
		case "TestGenFail":
			assert.Check(t, r.failed)
			assert.Check(t, !r.skipped)
			assert.Check(t, !r.timeout)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventFailOutput))
		case "TestGenSkip":
			assert.Check(t, !r.failed)
			assert.Check(t, r.skipped)
			assert.Check(t, !r.timeout)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventSkipOutput))
		case "TestGenTimeout":
			assert.Check(t, !r.failed)
			assert.Check(t, !r.skipped)
			assert.Check(t, r.timeout)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventTimeoutOutput))
		case "":
			assert.Check(t, r.failed)
			output, err := io.ReadAll(r.Reader())
			assert.NilError(t, err)
			assert.Check(t, cmp.Equal(string(output), testEventPackageOutput))
		default:
			t.Fatalf("unexpected test result: %s", r.name)
		}
	}
}

func TestCheckFailed(t *testing.T) {
	var failed checkFailed

	for event := range readTestEvents(t) {
		err := failed.HandleEvent(event)
		assert.NilError(t, err)
	}

	assert.Assert(t, bool(failed), "expected checkFailed to be true after a fail event")

	t.Run("HandleMultipleEvents_NoFailures", func(t *testing.T) {
		var failed checkFailed
		events := []*TestEvent{
			{Action: "pass", Package: "mypackage", Test: "Test1", Output: "file1.go:10: Test1 passed\n"},
			{Action: "skip", Package: "mypackage", Test: "Test3", Output: "file3.go:30: Test3 skipped\n"},
		}

		for _, event := range events {
			err := failed.HandleEvent(event)
			assert.NilError(t, err)
		}

		assert.Assert(t, !bool(failed), "expected checkFailed to be false when no fail events are present")
	})
}

func TestWriteLogs(t *testing.T) {
	logDir := t.TempDir()
	handler := &resultsHandler{results: make(map[string]*TestResult)}
	defer handler.Cleanup()
	events := readTestEvents(t)

	for event := range events {
		err := handler.HandleEvent(event)
		assert.NilError(t, err)
	}

	results := handler.Results()

	handler.WriteLogs(logDir)

	// Validate that logs are written to the expected file names
	for result := range results {
		expectedFileName := filepath.Join(logDir, result.pkg, strings.ReplaceAll(result.name, "/", "_")+".txt")
		content, err := os.ReadFile(expectedFileName)
		assert.NilError(t, err)
		output, err := io.ReadAll(result.Reader())
		assert.NilError(t, err)
		assert.Equal(t, string(content), string(output), "log file content does not match expected output")
	}
}
