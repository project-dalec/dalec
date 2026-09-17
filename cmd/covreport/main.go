// Command covreport reads the textual output of `go tool cover -func` and
// renders a markdown section listing the functions with the least coverage.
//
// It is meant to be appended to a GitHub Actions job summary so contributors
// can see what is missing coverage without downloading the full profile.
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
)

func main() {
	cfg := parseFlags(os.Args[1:])

	in := os.Stdin
	if cfg.input != "" {
		f, err := os.Open(cfg.input)
		if err != nil {
			fmt.Fprintf(os.Stderr, "covreport: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}

	data, err := io.ReadAll(in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "covreport: %v\n", err)
		os.Exit(1)
	}

	if err := render(os.Stdout, string(data), cfg.top); err != nil {
		fmt.Fprintf(os.Stderr, "covreport: %v\n", err)
		os.Exit(1)
	}
}

type config struct {
	input string
	top   int
}

func parseFlags(args []string) config {
	cfg := config{top: 20}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-input", "--input":
			if i+1 < len(args) {
				i++
				cfg.input = args[i]
			}
		case "-top", "--top":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil {
					cfg.top = n
				}
			}
		default:
			if v, ok := strings.CutPrefix(args[i], "-input="); ok {
				cfg.input = v
			} else if v, ok := strings.CutPrefix(args[i], "--input="); ok {
				cfg.input = v
			} else if v, ok := strings.CutPrefix(args[i], "-top="); ok {
				if n, err := strconv.Atoi(v); err == nil {
					cfg.top = n
				}
			} else if v, ok := strings.CutPrefix(args[i], "--top="); ok {
				if n, err := strconv.Atoi(v); err == nil {
					cfg.top = n
				}
			}
		}
	}
	return cfg
}

type funcCoverage struct {
	location string
	function string
	percent  float64
}

// parse extracts per-function coverage entries from `go tool cover -func`
// output. The trailing "total:" line and any lines that do not match the
// expected shape are ignored.
func parse(out string) []funcCoverage {
	var entries []funcCoverage
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if fields[0] == "total:" {
			continue
		}

		pctField := fields[len(fields)-1]
		if !strings.HasSuffix(pctField, "%") {
			continue
		}
		pct, err := strconv.ParseFloat(strings.TrimSuffix(pctField, "%"), 64)
		if err != nil {
			continue
		}

		entries = append(entries, funcCoverage{
			location: strings.TrimSuffix(fields[0], ":"),
			function: fields[len(fields)-2],
			percent:  pct,
		})
	}
	return entries
}

// missing returns the entries below 100% coverage sorted ascending by
// percentage (least covered first), with deterministic tie-breaks.
func missing(entries []funcCoverage) []funcCoverage {
	var out []funcCoverage
	for _, e := range entries {
		if e.percent < 100.0 {
			out = append(out, e)
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].percent != out[j].percent {
			return out[i].percent < out[j].percent
		}
		if out[i].location != out[j].location {
			return out[i].location < out[j].location
		}
		return out[i].function < out[j].function
	})

	return out
}

func render(w io.Writer, out string, top int) error {
	below := missing(parse(out))

	var buf strings.Builder
	buf.WriteString("## Missing coverage\n\n")

	if len(below) == 0 {
		buf.WriteString("All tracked functions are fully covered.\n")
		_, err := io.WriteString(w, buf.String())
		return err
	}

	total := len(below)
	shown := below
	if top > 0 && total > top {
		shown = below[:top]
	}

	summary := fmt.Sprintf("%d least-covered functions (below 100%%)", len(shown))
	if len(shown) < total {
		summary = fmt.Sprintf("%d of %d functions below 100%% coverage", len(shown), total)
	}

	buf.WriteString("<details>\n")
	fmt.Fprintf(&buf, "<summary>%s</summary>\n\n", summary)
	buf.WriteString("| Coverage | Function | Location |\n")
	buf.WriteString("| --- | --- | --- |\n")
	for _, e := range shown {
		fmt.Fprintf(&buf, "| %.1f%% | %s | %s |\n", e.percent, e.function, e.location)
	}
	buf.WriteString("\n</details>\n")

	_, err := io.WriteString(w, buf.String())
	return err
}
