package dalec

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
)

func TestDate(t *testing.T) {
	expect := "2023-10-01"
	expectTime, err := time.Parse(time.DateOnly, expect)
	assert.NoError(t, err)

	d := Date{Time: expectTime}
	assert.Equal(t, d.Format(time.DateOnly), expect)

	dtJSON, err := json.Marshal(d)
	assert.NoError(t, err)

	dtYAML, err := yaml.Marshal(d)
	assert.NoError(t, err)

	var d2 Date
	err = json.Unmarshal(dtJSON, &d2)
	assert.NoError(t, err)
	assert.Equal(t, d2.Format(time.DateOnly), expect)

	d3 := Date{}
	err = yaml.Unmarshal(dtYAML, &d3)
	assert.NoError(t, err)
	assert.Equal(t, d3.Format(time.DateOnly), expect)
}
