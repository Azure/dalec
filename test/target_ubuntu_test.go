package test

import (
	"fmt"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/targets/linux/deb/distro"
	"github.com/Azure/dalec/targets/linux/deb/ubuntu"
	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func withPackageOverride(oldPkg, newPkg string) func(cfg *testLinuxConfig) {
	return func(cfg *testLinuxConfig) {
		if cfg.Target.PackageOverrides == nil {
			cfg.Target.PackageOverrides = make(map[string]string)
		}

		cfg.Target.PackageOverrides[oldPkg] = newPkg
	}
}

func debLinuxTestConfigFor(targetKey string, cfg *distro.Config, opts ...func(*testLinuxConfig)) testLinuxConfig {
	tlc := testLinuxConfig{
		Target: targetConfig{
			Container: targetKey + "/testing/container",
			Package:   targetKey + "/deb",
			Worker:    targetKey + "/worker",
			FormatDepEqual: func(ver, rev string) string {
				return ver + "-" + cfg.VersionID + "u" + rev
			},
			ListExpectedSignFiles: debExpectedFiles(cfg.VersionID),
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
			ContextName: cfg.ContextRef,
			// /pkg1.deb ...
			CreateRepo:     ubuntuCreateRepo(cfg),
			SignRepo:       signRepoUbuntu,
			TestRepoConfig: ubuntuTestRepoConfig,
		},

		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
			{OS: "linux", Architecture: "arm", Variant: "v7"},
		},
		PackageOutputPath: debTargetOutputPath(cfg.VersionID),
	}

	for _, o := range opts {
		o(&tlc)
	}
	return tlc
}

func ubuntuCreateRepo(cfg *distro.Config) func(pkg llb.State, opts ...llb.StateOption) llb.StateOption {
	return func(pkg llb.State, opts ...llb.StateOption) llb.StateOption {
		repoFile := []byte(`
deb [trusted=yes] copy:/opt/repo/ /
`)
		return func(in llb.State) llb.State {
			withRepo := in.Run(
				dalec.ShArgs("apt-get update && apt-get install -y apt-utils gnupg2"),
				dalec.WithMountedAptCache(cfg.AptCachePrefix),
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
	}
}

func signRepoUbuntu(gpgKey llb.State) llb.StateOption {
	// key should be a state that has a public key under /public.key
	return func(in llb.State) llb.State {
		// assuming in is the state that has the repo files under / including
		// Release file
		return in.Run(
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

func ubuntuTestRepoConfig(name string) map[string]dalec.Source {
	return map[string]dalec.Source{
		"local.list": {
			Inline: &dalec.SourceInline{
				File: &dalec.SourceInlineFile{
					Contents: fmt.Sprintf(`deb [signed-by=/usr/share/keyrings/%s] copy:/opt/repo/ /`, name),
				},
			},
		},
	}
}

func debExpectedFiles(ver string) func(*dalec.Spec, ocispecs.Platform) []string {
	return func(spec *dalec.Spec, platform ocispecs.Platform) []string {
		base := fmt.Sprintf("%s_%s-%su%s", spec.Name, spec.Version, ver, spec.Revision)
		out := []string{
			fmt.Sprintf("%s_%s.deb", base, platform.Architecture),
		}
		return out
	}
}

func TestJammy(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, debLinuxTestConfigFor(ubuntu.JammyDefaultTargetKey, ubuntu.JammyConfig,
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", ""),
	))
}

func TestNoble(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, debLinuxTestConfigFor(ubuntu.NobleDefaultTargetKey, ubuntu.NobleConfig,
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", "bazel-bootstrap"),
	))
}

func TestFocal(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, debLinuxTestConfigFor(ubuntu.FocalDefaultTargetKey, ubuntu.FocalConfig,
		withPackageOverride("golang", "golang-1.22"),
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", ""),
	))
}

func TestBionic(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, debLinuxTestConfigFor(ubuntu.BionicDefaultTargetKey, ubuntu.BionicConfig,
		withPackageOverride("golang", "golang-1.18"),
		withPackageOverride("rust", "rust-all"),
		withPackageOverride("bazel", ""),
	))
}
