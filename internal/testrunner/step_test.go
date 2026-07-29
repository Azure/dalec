package testrunner

import (
	"testing"

	"gotest.tools/v3/assert"

	"github.com/project-dalec/dalec"
)

func TestWithTestStepAppliesStepEnv(t *testing.T) {
	t.Parallel()

	step := dalec.TestStep{
		Command: "true",
		Env: map[string]string{
			"FOO":   "bar",
			"EMPTY": "",
		},
	}
	def := definitionFromStateOption(t, stepRunner.withTestStep(step, withTestFrontend()))
	exec := singleExecOp(t, def)

	assert.DeepEqual(t, exec.Meta.Env, []string{"EMPTY=", "FOO=bar"})
}

func TestWithTestStepNoEnv(t *testing.T) {
	t.Parallel()

	step := dalec.TestStep{
		Command: "true",
	}
	def := definitionFromStateOption(t, stepRunner.withTestStep(step, withTestFrontend()))
	exec := singleExecOp(t, def)

	assert.Assert(t, len(exec.Meta.Env) == 0)
}
