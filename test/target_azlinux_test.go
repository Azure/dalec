package test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/targets/linux/rpm/azlinux"
	"github.com/moby/buildkit/client/llb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

var azlinuxConstraints = constraintsSymbols{
	Equal:              "==",
	GreaterThan:        ">",
	GreaterThanOrEqual: ">=",
	LessThan:           "<",
	LessThanOrEqual:    "<=",
}

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
	testLinuxDistro(ctx, t, testLinuxConfig{
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
			Constraints:    azlinuxConstraints,
		},
		Release: OSRelease{
			ID:        "mariner",
			VersionID: "2.0",
		},
	})
}

func TestAzlinux3(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
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
			Constraints:    azlinuxConstraints,
		},
		Release: OSRelease{
			ID:        "azurelinux",
			VersionID: "3.0",
		},
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
