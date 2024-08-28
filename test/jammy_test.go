package test

import (
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend/jammy"
	"github.com/moby/buildkit/client/llb"
)

func TestJammy(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Container: "jammy/testing/container",
			Package:   "jammy/deb",
			Worker:    "jammy/worker",
			FormatDepEqual: func(ver, rev string) string {
				return ver + "-ubuntu22.04u" + rev
			},
		},
		LicenseDir: "/usr/share/doc",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Worker: workerConfig{
			ContextName: jammy.JammyWorkerContextName,
			CreateRepo: func(pkg llb.State) llb.StateOption {
				repoFile := []byte(`
deb [trusted=yes] copy:/opt/testrepo/ /
`)
				return func(in llb.State) llb.State {
					repo := in.Run(
						dalec.ShArgs("apt-get update && apt-get install -y apt-utils"),
						dalec.WithMountedAptCache(jammy.AptCachePrefix),
					).
						Run(
							llb.Dir("/opt/repo"),
							dalec.ShArgs("apt-ftparchive packages . | gzip -1 > Packages.gz"),
						).
						AddMount("/opt/repo", pkg)

					return in.
						File(llb.Copy(repo, "/", "/opt/testrepo")).
						File(llb.Mkfile("/etc/apt/sources.list.d/test-dalec-local-repo.list", 0o644, repoFile)).
						// This file prevents installation of things like docs in ubuntu
						// containers We don't want to exclude this because tests want to
						// check things for docs in the build container. But we also don't
						// want to remove this completely from the base worker image in the
						// frontend because we usually don't want such things in the build
						// environment. This is only needed because certain tests (which
						// are using this customized builder image) are checking for files
						// that are being excluded by this config file.
						File(llb.Rm("/etc/dpkg/dpkg.cfg.d/excludes", llb.WithAllowNotFound(true)))
				}
			},
			Constraints: constraintsSymbols{
				Equal:              "=",
				GreaterThan:        ">>",
				GreaterThanOrEqual: ">=",
				LessThan:           "<<",
				LessThanOrEqual:    "<=",
			},
		},
	})
}
