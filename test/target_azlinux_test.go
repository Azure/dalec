package test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/targets/linux/rpm/azlinux"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

var azlinuxTestRepoConfig = func(keyPath string) map[string]dalec.Source {
	return map[string]dalec.Source{
		"local.repo": {
			Inline: &dalec.SourceInline{
				File: &dalec.SourceInlineFile{
					Contents: fmt.Sprintf(`[Local]
name=Local Repository
baseurl=file:///opt/repo
repo_gpgcheck=1
priority=0
enabled=1
gpgkey=file:///etc/pki/rpm-gpg/%s
	`, keyPath),
				},
			},
		},
	}
}

func TestMariner2(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Package:   "mariner2/rpm",
			Container: "mariner2/container",
			Worker:    "mariner2/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			ListExpectedSignFiles: azlinuxListSignFiles("cm2"),
		},
		LicenseDir: "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Worker: workerConfig{
			ContextName:    azlinux.Mariner2WorkerContextName,
			CreateRepo:     createYumRepo(azlinux.Mariner2Config),
			SignRepo:       signRepoAzLinux,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "mariner",
			VersionID: "2.0",
		},
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
		},
		PackageOutputPath: rpmTargetOutputPath("cm2"),
	}

	testLinuxDistro(ctx, t, cfg)
	testAzlinuxExtra(ctx, t, cfg)
}

func TestAzlinux3(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	cfg := testLinuxConfig{
		Target: targetConfig{
			Package:               "azlinux3/rpm",
			Container:             "azlinux3/container",
			Worker:                "azlinux3/worker",
			ListExpectedSignFiles: azlinuxListSignFiles("azl3"),
		},
		LicenseDir: "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Worker: workerConfig{
			ContextName:    azlinux.Azlinux3WorkerContextName,
			CreateRepo:     createYumRepo(azlinux.Azlinux3Config),
			SignRepo:       signRepoAzLinux,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "azurelinux",
			VersionID: "3.0",
		},
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
		},
		PackageOutputPath: rpmTargetOutputPath("azl3"),
	}
	testLinuxDistro(ctx, t, cfg)
	testAzlinuxExtra(ctx, t, cfg)

	t.Run("ca-certs override", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testAzlinuxCaCertsOverride(ctx, t, cfg.Target)
	})
}

func testAzlinuxExtra(ctx context.Context, t *testing.T, cfg testLinuxConfig) {

	t.Run("base deps", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		testAzlinuxBaseDeps(ctx, t, cfg.Target)
	})
}

func testAzlinuxCaCertsOverride(ctx context.Context, t *testing.T, target targetConfig) {
	spec := newSimpleSpec()
	spec.Dependencies = &dalec.PackageDependencies{
		Runtime: map[string]dalec.PackageConstraints{
			"ca-certificates": {},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(target.Container))
		solveT(ctx, t, client, req)
	})
}

func azlinuxListSignFiles(ver string) func(*dalec.Spec, ocispecs.Platform) []string {
	return func(spec *dalec.Spec, platform ocispecs.Platform) []string {
		base := fmt.Sprintf("%s-%s-%s.%s", spec.Name, spec.Version, spec.Revision, ver)

		var arch string
		switch platform.Architecture {
		case "amd64":
			arch = "x86_64"
		case "arm64":
			arch = "aarch64"
		default:
			arch = platform.Architecture
		}

		return []string{
			filepath.Join("SRPMS", fmt.Sprintf("%s.src.rpm", base)),
			filepath.Join("RPMS", arch, fmt.Sprintf("%s.%s.rpm", base, arch)),
		}
	}
}

func signRepoAzLinux(gpgKey llb.State) llb.StateOption {
	// key should be a state that has a public key under /public.key
	return func(in llb.State) llb.State {
		return in.Run(
			dalec.ShArgs("gpg --import < /tmp/gpg/private.key"),
			llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
			dalec.ProgressGroup("Importing gpg key")).
			Run(
				dalec.ShArgs(`ID=$(gpg --list-keys --keyid-format LONG | grep -B 2 'test@example.com' | grep 'pub' | awk '{print $2}' | cut -d'/' -f2) && \
					gpg --list-keys --keyid-format LONG && \
					gpg --detach-sign --default-key "$ID" --armor --yes /opt/repo/repodata/repomd.xml`),
				llb.AddMount("/tmp/gpg", gpgKey, llb.Readonly),
			).Root()
	}
}

func testAzlinuxBaseDeps(ctx context.Context, t *testing.T, cfg targetConfig) {
	spec := newSimpleSpec()
	spec.Tests = []*dalec.TestSpec{
		{
			Name: "validate no shell",
			Files: map[string]dalec.FileCheckOutput{
				"/bin/sh/": {
					NotExist: true,
				},
				"/bin/bash/": {
					NotExist: true,
				},
				"/etc/pki": {
					IsDir: true,
				},
				"/etc/localtime": {},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		req := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Container))
		solveT(ctx, t, client, req)
	})
}
