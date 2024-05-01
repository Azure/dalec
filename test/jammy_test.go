package test

import "testing"

func TestJammy(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		BuildTarget: "jammy/container",
		SignTarget:  "jammy/deb",
		LicenseDir:  "/usr/share/doc",
		SystemdDir:  "/lib/systemd",
	})
}
