package test

import (
	"testing"

	_ "github.com/Azure/dalec/frontend/jammy"
)

func TestJammy(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Container: "jammy/testing/container",
			Package:   "jammy/deb",
		},
		LicenseDir: "/usr/share/doc",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/lib/systemd",
			Targets: "/etc/systemd/system",
		},
	})
}
