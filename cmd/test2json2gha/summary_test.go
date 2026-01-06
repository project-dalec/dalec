package main

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSummaryFormatter_FormatResults(t *testing.T) {
	results := []*TestResult{
		{pkg: "mypackage", name: "Test1", elapsed: 0.5, skipped: false, failed: false},
		{pkg: "mypackage", name: "Test2", elapsed: 1.5, skipped: false, failed: true},
		{pkg: "mypackage", name: "Test3", elapsed: 0.2, skipped: true, failed: false},
		{pkg: "mypackage", name: "Test4", elapsed: 2.0, skipped: false, failed: false},
	}

	expected := `## Test metrics
**Skipped:** 1 &nbsp;&nbsp;&nbsp;&nbsp; **Failed:** 1 &nbsp;&nbsp;&nbsp;&nbsp; **Total:** 4 &nbsp;&nbsp;&nbsp;&nbsp; **Elapsed:** 4.200s

` + "```" + `
[ min  max] cnt total%  sum (4 events)
[0.20 0.20] 1 25.00% 0.20 .........................
[0.50 0.50] 1 25.00% 0.50 .........................
[1.50 1.50] 1 25.00% 1.50 .........................
[2.00 2.00] 1 25.00% 2.00 .........................
` + "```" + `

## Slow tests

` + "```" + `
mypackage/Test2: 1.500s
mypackage/Test4: 2.000s
` + "```" + `

`

	formatter := &summaryFormatter{slowThreshold: 1 * time.Second}
	var output bytes.Buffer

	err := formatter.FormatResults(slices.Values(results), &output)
	assert.NoError(t, err)
	t.Log(output.String())
	assert.Equal(t, output.String(), expected)
}
