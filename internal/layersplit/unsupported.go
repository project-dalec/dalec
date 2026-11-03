//go:build !linux

package layersplit

import "errors"

var errUnsupported = errors.New("layer splitting is only supported on linux")

// Copy is only supported on linux.
func Copy(string, map[string]int, []string, int64) error {
	return errUnsupported
}

// Entry describes one filesystem entry for comparison.
type Entry struct{}

// Diff describes a difference between two trees.
type Diff struct{}

func (Diff) String() string { return "" }

// Tree is only supported on linux.
func Tree(string) (map[string]Entry, error) {
	return nil, errUnsupported
}

// Compare is only supported on linux.
func Compare(map[string]Entry, map[string]Entry) []Diff {
	return nil
}
