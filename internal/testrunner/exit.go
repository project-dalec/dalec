package testrunner

import "os"

// exit is overridden in tests to prevent os.Exit from terminating the test process.
var exit = os.Exit
