package dalec

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

func TestDate(t *testing.T) {
	expect := "2023-10-01"
	expectTime, err := time.Parse(time.DateOnly, expect)
	assert.NilError(t, err)

	d := Date{Time: expectTime}
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(d.Format(time.DateOnly), expect))

	dtJSON, err := json.Marshal(d)
	assert.NilError(t, err)

	dtYAML, err := yaml.Marshal(d)
	assert.NilError(t, err)

	var d2 Date
	err = json.Unmarshal(dtJSON, &d2)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(d2.Format(time.DateOnly), expect))

	d3 := Date{}
	err = yaml.Unmarshal(dtYAML, &d3)
	assert.NilError(t, err)
	assert.Check(t, cmp.Equal(d3.Format(time.DateOnly), expect))
}
