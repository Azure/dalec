package test

import (
	"testing"

	"github.com/Azure/dalec/targets/linux/rpm/almalinux"
)

func TestAlmalinux9(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Package:   "almalinux9/rpm",
			Container: "almalinux9/container",
			Worker:    "almalinux9/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			ListExpectedSignFiles: azlinuxListSignFiles("el9"),
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
			ContextName:    almalinux.ConfigV9.ContextRef,
			CreateRepo:     createYumRepo(almalinux.ConfigV9),
			SignRepo:       signRepoAzLinux,
			TestRepoConfig: azlinuxTestRepoConfig,
			Constraints:    azlinuxConstraints,
		},
		Release: OSRelease{
			ID:        "almalinux",
			VersionID: "9",
		},
	})
}

func TestAlmalinux8(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		Target: targetConfig{
			Package:   "almalinux8/rpm",
			Container: "almalinux8/container",
			Worker:    "almalinux8/worker",
			FormatDepEqual: func(v, _ string) string {
				return v
			},
			ListExpectedSignFiles: azlinuxListSignFiles("el8"),
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
			ContextName:    almalinux.ConfigV8.ContextRef,
			CreateRepo:     createYumRepo(almalinux.ConfigV8),
			SignRepo:       signRepoAzLinux,
			TestRepoConfig: azlinuxTestRepoConfig,
			Constraints:    azlinuxConstraints,
		},
		Release: OSRelease{
			ID:        "almalinux",
			VersionID: "8",
		},
		SkipStripTest: true,
	})
}
