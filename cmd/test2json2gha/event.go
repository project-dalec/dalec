package main

import (
	"io"
	"iter"
	"log/slog"
	"math"
	"os"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/project-dalec/dalec"
)

const (
	pass = "pass"
	fail = "fail"
	skip = "skip"
)

// TestEvent is the go test2json event data structure we receive from `go test`
// This is defined in https://pkg.go.dev/cmd/test2json#hdr-Output_Format
type TestEvent struct {
	Time    time.Time
	Action  string
	Package string
	Test    string
	Elapsed float64 // seconds
	Output  string
}

type EventHandler interface {
	HandleEvent(te *TestEvent) error
}

type ResultsFormatter interface {
	FormatResults(results iter.Seq[*TestResult], out io.Writer) error
}

// outputStreamer is an [EventHandler] that writes the test output to the console
// This allows receiving the test output in real time
type outputStreamer struct {
	out io.Writer
}

func (h *outputStreamer) HandleEvent(te *TestEvent) error {
	if te.Output != "" {
		_, err := h.out.Write([]byte(te.Output))
		return err
	}
	return nil
}

// resultsHandler is an [EventHandler] that gathers all the results from every event
// handled.
type resultsHandler struct {
	results map[string]*TestResult
}

func (h *resultsHandler) getOutputStream(te *TestEvent) (*TestResult, error) {
	key := path.Join(te.Package, te.Test)
	tr := h.results[key]
	if tr != nil {
		return tr, nil
	}

	f, err := os.CreateTemp("", strings.ReplaceAll(key, "/", "-"))
	if err != nil {
		return nil, errors.WithStack(err)
	}
	tr = &TestResult{output: f, pkg: te.Package, name: te.Test}
	if h.results == nil {
		h.results = make(map[string]*TestResult)
	}
	h.results[key] = tr
	return tr, nil
}

// markUnfinishedAsTimeout marks all tests that have not been marked as pass, skip, or fail
// as a timeout.
// Call this after all events have been handled to ensure that any tests that were not completed
// are marked appropriately.
// This would typically occur when there is a test timeout (e.g. `go test -timeout 30s`, and it took more than 30 seconds to run).
func (h *resultsHandler) markUnfinishedAsTimeout() {
	for _, tr := range h.results {
		if !tr.pass && !tr.skipped && !tr.failed {
			tr.timeout = true
		}
	}
}

func (h *resultsHandler) HandleEvent(te *TestEvent) error {
	tr, err := h.getOutputStream(te)
	if err != nil {
		return err
	}

	switch te.Action {
	case fail:
		tr.failed = true
	case skip:
		tr.skipped = true
	case pass:
		tr.pass = true
	}

	tr.elapsed = te.Elapsed

	if te.Output != "" {
		_, err := tr.output.WriteString(te.Output)
		if err != nil {
			return errors.Wrap(err, "error collecting test event output")
		}
	}

	return nil
}

func (h *resultsHandler) Results() iter.Seq[*TestResult] {
	keys := dalec.SortMapKeys(h.results)
	return func(yield func(*TestResult) bool) {
		for _, key := range keys {
			tr := h.results[key]
			if !yield(tr) {
				break
			}
		}
	}
}

func (h *resultsHandler) Close() {
	for _, tr := range h.results {
		tr.Close()
	}
}

// WriteLogs writes the test logs to the specified directory
func (h *resultsHandler) WriteLogs(dir string) {
	for r := range h.Results() {
		name := strings.ReplaceAll(r.name, "/", "_") + ".txt"
		fullPath := path.Join(dir, r.pkg, name)

		log, err := os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				slog.Error("Error opening test log file", "error", err)
				continue
			}
			if err := os.MkdirAll(path.Dir(fullPath), 0755); err != nil {
				slog.Error("Error creating test log directory", "error", err)
				continue
			}
			log, err = os.OpenFile(fullPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
			if err != nil {
				slog.Error("Error opening test log file", "error", err)
				continue
			}
		}

		// Here will intentionally use the original *os.File instead of calling r.Reader()
		// This allows potential optimizations in `io.Copy` to avoid actually copying data in userspace.
		rdr, err := os.Open(r.output.Name())
		if err != nil {
			slog.Error("Error opening test log file", "error", err)
			log.Close()
			continue
		}

		_, err = io.Copy(log, rdr)
		log.Close()
		rdr.Close()
		if err != nil {
			slog.Error("Error writing test log file", "error", err)
			continue
		}
	}
}

func (h *resultsHandler) Cleanup() {
	h.Close()
	for _, tr := range h.results {
		if err := os.Remove(tr.output.Name()); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			slog.Error("Error removing test log file", "error", err)
		}
	}
}

// TestResult is where we collect all the data about a test
type TestResult struct {
	output  *os.File
	pkg     string
	name    string
	failed  bool
	skipped bool
	elapsed float64
	pass    bool
	timeout bool // true if the test was not completed within the timeout period
}

// [Close] closes the underlying output file and invalidates any readers
// created from [TestResult.Reader].
func (r *TestResult) Close() {
	r.output.Close()
}

// Reader creates a new reader that contains all the test output
// Calling [TestResult.Reader] multiple times will return a new, independent reader
// each time.
//
// Calling [TestResult.Close] will close the underlying file, any readers created before or
// after will be invalid after that and should return an [io.EOF] error on read.
func (r *TestResult) Reader() *io.SectionReader {
	return io.NewSectionReader(r.output, 0, math.MaxInt64)
}

// checkFailed is an [EventHandler] that checks if any test has failed
// and sets the [checkFailed] to true if so.
type checkFailed bool

func (c *checkFailed) HandleEvent(te *TestEvent) error {
	if te.Action == "fail" {
		*c = true
	}
	return nil
}
