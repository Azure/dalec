package test

import (
	"testing"

	"github.com/Azure/dalec/targets/linux/deb/debian"
)

func TestBookworm(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, debLinuxTestConfigFor(debian.BookwormDefaultTargetKey, debian.BookwormConfig, withPackageOverride("rust", "rust-all")))
}

func TestBullseye(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, debLinuxTestConfigFor(debian.BullseyeDefaultTargetKey, debian.BullseyeConfig, withPackageOverride("golang", "golang-1.19"), withPackageOverride("rust", "cargo-web")))
}
