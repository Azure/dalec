package dalec

import (
	"context"
	"testing"

	"gotest.tools/v3/assert"
)

func TestBaseImage_does_not_apply_source_filters(t *testing.T) {
	bi := BaseImage{
		Rootfs: Source{
			DockerImage: &SourceDockerImage{Ref: "example.invalid/base:latest"},
		},
	}

	sOpt := SourceOpts{
		SourceFilter: func() (SourceFilterConfig, error) {
			t.Fatal("source filter called for base image")
			return SourceFilterConfig{}, nil
		},
	}

	_, err := bi.ToState(sOpt).Marshal(context.Background())
	assert.NilError(t, err)
	assert.Assert(t, sOpt.SourceFilter != nil)
}
