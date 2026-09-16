package frontend

import (
	"fmt"
	"strconv"

	"github.com/project-dalec/dalec"
)

// KeyImageSourceLabel is a frontend input, deliberately not a build argument.
const KeyImageSourceLabel = "dalec.image-source-label"

func ImageConfigOpts(client BuildOpstGetter) ([]dalec.ImageConfigOpt, error) {
	value, ok := client.BuildOpts().Opts[KeyImageSourceLabel]
	if !ok {
		return nil, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", KeyImageSourceLabel, err)
	}
	if !enabled {
		return nil, nil
	}
	return []dalec.ImageConfigOpt{dalec.WithImageSourceLabel()}, nil
}
