package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"github.com/pkg/errors"
)

type config struct {
	slowThreshold time.Duration
	modName       string
	verbose       bool
	stream        bool
	logDir        string
}

func main() {
	tmp, err := os.MkdirTemp("", "test2json2gha-")
	if err != nil {
		panic(err)
	}
	var cfg config

	flag.DurationVar(&cfg.slowThreshold, "slow", 500*time.Millisecond, "Threshold to mark test as slow")
	flag.BoolVar(&cfg.verbose, "verbose", false, "Enable verbose output")
	flag.BoolVar(&cfg.stream, "stream", false, "Enable streaming output")
	flag.StringVar(&cfg.logDir, "logdir", "", "Directory to store all test logs")

	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	slog.SetDefault(logger)

	info, _ := debug.ReadBuildInfo()
	if info != nil {
		cfg.modName = info.Main.Path
	}

	// Set TMPDIR so that [os.CreateTemp] can use an empty string as the dir
	// and wind up in our dir.
	os.Setenv("TMPDIR", tmp)

	cleanup := func() { os.RemoveAll(tmp) } //nolint:errcheck

	anyFail, err := do(os.Stdin, os.Stdout, cfg)
	cleanup()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%+v", err)
		os.Exit(1)
	}

	if anyFail {
		// In case pipefail is not enabled, make sure we exit non-zero
		os.Exit(2)
	}
}

func do(in io.Reader, out io.Writer, cfg config) (bool, error) {
	dec := json.NewDecoder(in)

	results := &resultsHandler{}
	defer results.Cleanup()

	defer func() {
		var wg waitGroup

		results.markUnfinishedAsTimeout()
		signalTimeout(results.Results())

		wg.Go(func() {
			var rf ResultsFormatter
			rf = &consoleFormatter{modName: cfg.modName, verbose: cfg.verbose}
			if err := rf.FormatResults(results.Results(), out); err != nil {
				slog.Error("Error writing annotations", "error", err)
			}

			rf = &errorAnnotationFormatter{}
			if err := rf.FormatResults(results.Results(), out); err != nil {
				slog.Error("Error writing error annotations", "error", err)
			}
		})

		wg.Go(func() {
			summary := getSummaryFile()
			formatter := &summaryFormatter{slowThreshold: cfg.slowThreshold}
			if err := formatter.FormatResults(results.Results(), getSummaryFile()); err != nil {
				slog.Error("Error writing summary", "error", err)
			}
			summary.Close()
		})

		wg.Wait()

		if cfg.logDir != "" {
			results.WriteLogs(cfg.logDir)
		}
	}()

	var anyFailed checkFailed
	handlers := []EventHandler{
		results,
		&anyFailed,
	}

	if cfg.stream {
		handlers = append(handlers, &outputStreamer{out: out})
	}

	te := &TestEvent{}
	for {
		*te = TestEvent{} // Reset the event struct to avoid reusing old data

		err := dec.Decode(te)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return false, errors.WithStack(err)
		}

		for _, h := range handlers {
			if err := h.HandleEvent(te); err != nil {
				slog.Error("Error handling event", "error", err)
			}
		}
	}

	return bool(anyFailed), nil
}

// signalTimeout writes test_timeout=true to GITHUB_OUTPUT if any test timed out.
// This allows subsequent CI steps to detect that a timeout occurred.
func signalTimeout(results iter.Seq[*TestResult]) {
	ghOutput := os.Getenv("GITHUB_OUTPUT")
	if ghOutput == "" {
		return
	}

	for r := range results {
		if r.timeout {
			f, err := os.OpenFile(ghOutput, os.O_WRONLY|os.O_APPEND, 0)
			if err != nil {
				slog.Error("Error opening GITHUB_OUTPUT", "error", err)
				return
			}
			if _, err := fmt.Fprintln(f, "test_timeout=true"); err != nil {
				slog.Error("Error writing timeout status to GITHUB_OUTPUT", "error", err)
			}
			f.Close()
			return
		}
	}
}

type waitGroup struct {
	sync.WaitGroup
}

func (wg *waitGroup) Go(f func()) {
	wg.Add(1)
	go func() {
		f()
		wg.Done()
	}()
}
