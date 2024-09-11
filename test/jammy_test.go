package test

import (
	"fmt"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend/jammy"
	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func signRepoJammy(gpgKey llb.State) llb.StateOption {
	// key should be a state that has a public key under /public.key
	return func(in llb.State) llb.State {
		// assuming in is the state that has the repo files under / including
		// Release file
		return in.Run(
			dalec.ShArgs("gpg --import < /tmp/gpg/public.key"),
			dalec.ShArgs("gpg --import < /tmp/gpg/private.key"),
			llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
			dalec.ProgressGroup("Importing gpg key")).
			Run(
				dalec.ShArgs(`ID=$(gpg --list-keys --keyid-format LONG | grep -B 2 'test@example.com' | grep 'pub' | awk '{print $2}' | cut -d'/' -f2) && \
					gpg --list-keys --keyid-format LONG && \
					gpg --default-key $ID -abs -o /opt/repo/Release.gpg /opt/repo/Release && \
					gpg --default-key "$ID" --clearsign -o /opt/repo/InRelease /opt/repo/Release`),
				llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
				dalec.ProgressGroup("signing repo"),
			).Root()
	}
}

var jammyTestRepoConfig = map[string]dalec.Source{
	"local.list": {
		Inline: &dalec.SourceInline{
			File: &dalec.SourceInlineFile{
				Contents: `deb [signed-by=/usr/share/keyrings/public.key] copy:/opt/repo/ /`,
			},
		},
	},
}

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
			ListExpectedSignFiles: func(spec *dalec.Spec, platform ocispecs.Platform) []string {
				base := fmt.Sprintf("%s_%s-%su%s", spec.Name, spec.Version, "ubuntu22.04", spec.Revision)
				sourceBase := fmt.Sprintf("%s_%s.orig", spec.Name, spec.Version)

				out := []string{
					base + ".debian.tar.xz",
					base + ".dsc",
					fmt.Sprintf("%s_%s.deb", base, platform.Architecture),
					base + "_source.buildinfo",
					base + "_source.changes",
					sourceBase + ".tar.xz",
				}

				for src := range spec.Sources {
					out = append(out, fmt.Sprintf("%s-%s.tar.gz", sourceBase, src))
				}

				return out
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
			// /pkg1.deb ...
			CreateRepo: func(pkg llb.State, opts ...llb.StateOption) llb.StateOption {
				repoFile := []byte(`
deb [trusted=yes] copy:/opt/repo/ /
`)
				return func(in llb.State) llb.State {
					withRepo := in.Run(
						dalec.ShArgs("apt-get update && apt-get install -y apt-utils gnupg2"),
						dalec.WithMountedAptCache(jammy.AptCachePrefix),
					).File(llb.Copy(pkg, "/", "/opt/repo")).
						Run(
							llb.Dir("/opt/repo"),
							dalec.ShArgs("apt-ftparchive packages . > Packages"),
						).
						Run(
							llb.Dir("/opt/repo"),
							dalec.ShArgs("apt-ftparchive release . > Release"),
						).Root()

					for _, opt := range opts {
						withRepo = opt(withRepo)
					}

					return withRepo.
						File(llb.Mkfile("/etc/apt/sources.list.d/test-dalec-local-repo.list", 0o644, repoFile))
				}
			},
			SignRepo:       signRepoJammy,
			TestRepoConfig: jammyTestRepoConfig,
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
