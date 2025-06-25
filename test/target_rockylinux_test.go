package test

import (
	"testing"

	"github.com/Azure/dalec/targets/linux/rpm/rockylinux"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestRockylinux9(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Package:   "rockylinux9/rpm",
			Container: "rockylinux9/container",
			Worker:    "rockylinux9/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			ListExpectedSignFiles: azlinuxListSignFiles("el9"),
			PackageOverrides: map[string]string{
				"rust":         "rust cargo",
				"bazel":        noPackageAvailable,
				"python3-venv": "python3-virtualenv",
			},
		},
		LicenseDir: "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Libdir: "/usr/lib64",
		Worker: workerConfig{
			ContextName:    rockylinux.ConfigV9.ContextRef,
			CreateRepo:     createYumRepo(rockylinux.ConfigV9),
			SignRepo:       signRepoAzLinux,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "rocky",
			VersionID: "9",
		},
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
		},
		PackageOutputPath: rpmTargetOutputPath("el9"),
	})
}

func TestRockylinux8(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Package:   "rockylinux8/rpm",
			Container: "rockylinux8/container",
			Worker:    "rockylinux8/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			ListExpectedSignFiles: azlinuxListSignFiles("el8"),
			PackageOverrides: map[string]string{
				"rust":         "rust cargo",
				"bazel":        noPackageAvailable,
				"python3-venv": "python3-virtualenv",
			},
		},
		LicenseDir: "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
		Libdir: "/usr/lib64",
		Worker: workerConfig{
			ContextName:    rockylinux.ConfigV8.ContextRef,
			CreateRepo:     createYumRepo(rockylinux.ConfigV8),
			SignRepo:       signRepoAzLinux,
			TestRepoConfig: azlinuxTestRepoConfig,
		},
		Release: OSRelease{
			ID:        "rocky",
			VersionID: "8",
		},
		SkipStripTest: true,
		Platforms: []ocispecs.Platform{
			{OS: "linux", Architecture: "amd64"},
			{OS: "linux", Architecture: "arm64"},
		},
		PackageOutputPath: rpmTargetOutputPath("el8"),
	})
}
