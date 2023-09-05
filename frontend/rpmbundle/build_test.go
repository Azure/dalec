package rpmbundle

import (
	"testing"

	"github.com/azure/dalec/frontend"
	"gotest.tools/v3/assert"

	_ "embed"
)

type testWriter struct {
	t *testing.T
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.t.Log(string(p))
	return len(p), nil
}

var (
	//go:embed test/fixtures/moby-runc.yml
	mobyRunc []byte
)

func TestLoadSpec(t *testing.T) {
	_, err := frontend.LoadSpec(mobyRunc, nil)
	assert.NilError(t, err)
}
