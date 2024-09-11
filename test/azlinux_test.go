package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend/azlinux"
	"github.com/google/go-cmp/cmp"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"gotest.tools/v3/assert"
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
			CreateRepo:     azlinuxWithRepo,
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
			CreateRepo:     azlinuxWithRepo,
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

func azlinuxWithRepo(rpms llb.State, opts ...llb.StateOption) llb.StateOption {
	return func(in llb.State) llb.State {
		localRepo := []byte(`
[Local]
name=Local Repository
baseurl=file:///opt/repo
gpgcheck=0
priority=0
enabled=1
`)
		pg := dalec.ProgressGroup("Install local repo for test")
		withRepos := in.
			File(llb.Mkdir("/opt/repo/RPMS", 0o755, llb.WithParents(true)), pg).
			File(llb.Mkdir("/opt/repo/SRPMS", 0o755), pg).
			Run(dalec.ShArgs("tdnf install -y createrepo"), pg).
			File(llb.Mkfile("/etc/yum.repos.d/local.repo", 0o644, localRepo), pg).
			Run(
				llb.AddMount("/tmp/st", rpms, llb.Readonly),
				dalec.ShArgs("cp /tmp/st/RPMS/$(uname -m)/* /opt/repo/RPMS/ && cp /tmp/st/SRPMS/* /opt/repo/SRPMS"),
				pg,
			).
			Run(dalec.ShArgs("createrepo --compatibility /opt/repo"), pg).
			Root()

		for _, opt := range opts {
			withRepos = withRepos.With(opt)
		}

		return withRepos
	}
}

type workerConfig struct {
	// CreateRepo takes in a state which is the output of the sign target,
	// as well as optional state options for additional configuration.
	// the output [llb.StateOption] should install the repo into the worker image.
	CreateRepo func(llb.State, ...llb.StateOption) llb.StateOption
	SignRepo   func(llb.State) llb.StateOption
	// ContextName is the name of the worker context that the build target will use
	// to see if a custom worker is proivded in a context
	ContextName    string
	TestRepoConfig func(string) map[string]dalec.Source
	Constraints    constraintsSymbols
	Platform       *ocispecs.Platform
}

type constraintsSymbols struct {
	Equal string

	GreaterThan        string
	GreaterThanOrEqual string

	LessThan        string
	LessThanOrEqual string
}

type targetConfig struct {
	// Package is the target for creating a package.
	Package string
	// Container is the target for creating a container
	Container string
	// Target is the build target for creating the worker image.
	Worker string

	// FormatDepEqual, when set, alters the provided depenedency version to match
	// what is neccessary for the target distro to set a dependency for an equals
	// operator.
	FormatDepEqual func(ver, rev string) string

	// Given a spec, list all files (including the full path) that are expected
	// to be sent to be signed.
	ListExpectedSignFiles func(*dalec.Spec, ocispecs.Platform) []string
}

type testLinuxConfig struct {
	Target     targetConfig
	LicenseDir string
	SystemdDir struct {
		Units   string
		Targets string
	}
	Worker  workerConfig
	Release OSRelease
}

type OSRelease struct {
	ID        string
	VersionID string
}

func testLinuxDistro(ctx context.Context, t *testing.T, testConfig testLinuxConfig) {
	t.Run("Fail when non-zero exit code during build", func(t *testing.T) {
		t.Parallel()
		spec := dalec.Spec{
			Name:        "test-build-commands-fail",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing builds commands that fail cause the whole build to fail",
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: "exit 42",
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(testConfig.Target.Container))
			sr.Evaluate = true
			_, err := gwc.Solve(ctx, sr)
			var xErr *moby_buildkit_v1_frontend.ExitError
			if !errors.As(err, &xErr) {
				t.Fatalf("expected exit error, got %T: %v", errors.Unwrap(err), err)
			}
		})
	})

	t.Run("container", func(t *testing.T) {
		const src2Patch3File = "patch3"
		src2Patch3Content := []byte(`
diff --git a/file3 b/file3
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file3
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added another new file"
`)
		src2Patch3Context := llb.Scratch().File(
			llb.Mkfile(src2Patch3File, 0o600, src2Patch3Content),
		)
		src2Patch3ContextName := "patch-context"

		spec := dalec.Spec{
			Name:        "test-container-build",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing container target",
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"file1": {Contents: "file1 contents\n"},
							},
						},
					},
				},
				"src2-patch1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: `
diff --git a/file1 b/file1
index 84d55c5..22b9b11 100644
--- a/file1
+++ b/file1
@@ -1 +1 @@
-file1 contents
+file1 contents patched
`,
						},
					},
				},
				"src2-patch2": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"the-patch": {
									Contents: `
diff --git a/file2 b/file2
new file mode 100700
index 0000000..5260cb1
--- /dev/null
+++ b/file2
@@ -0,0 +1,3 @@
+#!/usr/bin/env bash
+
+echo "Added a new file"
`,
								},
							},
						},
					},
				},
				"src2-patch3": {
					Context: &dalec.SourceContext{
						Name: src2Patch3ContextName,
					},
				},
				"src3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho goodbye",
							Permissions: 0o700,
						},
					},
				},
			},
			Patches: map[string][]dalec.PatchSpec{
				"src2": {
					{Source: "src2-patch1"},
					{Source: "src2-patch2", Path: "the-patch"},
					{Source: "src2-patch3", Path: src2Patch3File},
				},
			},

			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{
					"bash":      {},
					"coreutils": {},
				},
			},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					// These are "build" steps where we aren't really building things just verifying
					// that sources are in the right place and have the right permissions and content
					{
						// file added by patch
						Command: "test -f ./src1",
					},
					{
						Command: "test -x ./src1",
					},
					{
						Command: "test ! -d ./src1",
					},
					{
						Command: "./src1 | grep 'hello world'",
					},
					{
						// file added by patch
						Command: "ls -lh ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -f ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -x ./src2/file2",
					},
					{
						Command: "grep 'Added a new file' ./src2/file2",
					},
					{
						// file added by patch
						Command: "test -f ./src2/file3",
					},
					{
						// file added by patch
						Command: "test -x ./src2/file3",
					},
					{
						Command: "grep 'Added another new file' ./src2/file3",
					},
					{
						// Test that a multiline command works with env vars
						Env: map[string]string{
							"FOO": "foo",
							"BAR": "bar",
						},
						Command: `
echo "${FOO}_0" > foo0.txt
echo "${FOO}_1" > foo1.txt
echo "$BAR" > bar.txt
`,
					},
				},
			},

			Image: &dalec.ImageConfig{
				Post: &dalec.PostInstall{
					Symlinks: map[string]dalec.SymlinkTarget{
						"/usr/bin/src1": {Path: "/src1"},
						"/usr/bin/src3": {Path: "/non/existing/dir/src3"},
					},
				},
			},

			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1":       {},
					"src2/file2": {},
					"src3":       {},
					// These are files we created in the build step
					// They aren't really binaries but we want to test that they are created and have the right content
					"foo0.txt": {},
					"foo1.txt": {},
					"bar.txt":  {},
				},
			},

			Tests: []*dalec.TestSpec{
				{
					Name: "Check that the binary artifacts execute and provide the expected output",
					Steps: []dalec.TestStep{
						{
							Command: "/usr/bin/src1",
							Stdout:  dalec.CheckOutput{Equals: "hello world\n"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
						{
							Command: "/usr/bin/file2",
							Stdout:  dalec.CheckOutput{Equals: "Added a new file\n"},
							Stderr:  dalec.CheckOutput{Empty: true},
						},
					},
				},
				{
					Name: "Check that multi-line command (from build step) with env vars propagates env vars to whole command",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/bin/foo0.txt": {CheckOutput: dalec.CheckOutput{StartsWith: "foo_0\n"}},
						"/usr/bin/foo1.txt": {CheckOutput: dalec.CheckOutput{StartsWith: "foo_1\n"}},
						"/usr/bin/bar.txt":  {CheckOutput: dalec.CheckOutput{StartsWith: "bar\n"}},
					},
				},
				{
					Name: "Post-install symlinks should be created",
					Files: map[string]dalec.FileCheckOutput{
						"/src1":                  {},
						"/non/existing/dir/src3": {},
					},
					Steps: []dalec.TestStep{
						{Command: "/bin/bash -c 'test -L /src1'"},
						{Command: "/bin/bash -c 'test \"$(readlink /src1)\" = \"/usr/bin/src1\"'"},
						{Command: "/src1", Stdout: dalec.CheckOutput{Equals: "hello world\n"}, Stderr: dalec.CheckOutput{Empty: true}},
						{Command: "/non/existing/dir/src3", Stdout: dalec.CheckOutput{Equals: "goodbye\n"}, Stderr: dalec.CheckOutput{Empty: true}},
					},
				},
				{
					Name: "Check /etc/os-release",
					Files: map[string]dalec.FileCheckOutput{
						"/etc/os-release": {
							CheckOutput: dalec.CheckOutput{
								Contains: []string{
									fmt.Sprintf("ID=%s\n", testConfig.Release.ID),
									// Note: the value of `VERSION_ID` needs to be quoted!
									// TODO: Something is stripping the quotes here...
									// fmt.Sprintf("VERSION_ID=%q\n", testConfig.Release.VersionID),
								},
							},
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(testConfig.Target.Container),
				withBuildContext(ctx, t, src2Patch3ContextName, src2Patch3Context),
			)
			sr.Evaluate = true

			solveT(ctx, t, gwc, sr)

			// Make sure the test framework was actually executed by the build target.
			// This appends a test case so that is expected to fail and as such cause the build to fail.
			spec.Tests = append(spec.Tests, &dalec.TestSpec{
				Name: "Test framework should be executed",
				Steps: []dalec.TestStep{
					{Command: "/bin/sh -c 'echo this command should fail; exit 42'"},
				},
			})

			// update the spec in the solve reuqest
			withSpec(ctx, t, &spec)(&newSolveRequestConfig{req: &sr})

			if _, err := gwc.Solve(ctx, sr); err == nil {
				t.Fatal("expected test spec to run with error but got none")
			}
		})
	})

	t.Run("signing", linuxSigningTests(ctx, testConfig))

	t.Run("test systemd unit single", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-systemd-unit",
			Description: "Test systemd unit",
			Website:     "https://www.github.com/Azure/dalec",
			Version:     "0.0.1",
			Revision:    "1",
			Vendor:      "Microsoft",
			License:     "Apache 2.0",
			Packager:    "Microsoft <support@microsoft.com>",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"simple.service": {
									Contents: `
[Unit]
Description=Phony Service
After=network.target

[Service]
Type=simple
ExecStart=/usr/bin/service
Restart=always

[Install]
WantedBy=multi-user.target
`,
								},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"src/simple.service": {
							Enable: true,
						},
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check service files",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(testConfig.SystemdDir.Units, "system/simple.service"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
							Permissions: 0o644,
						},
						// symlinked file in multi-user.target.wants should point to simple.service.
						filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/simple.service"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})

		// Test to ensure disabling works by default
		spec.Artifacts.Systemd = &dalec.SystemdConfiguration{
			Units: map[string]dalec.SystemdUnitConfig{
				"src/simple.service": {},
			},
		}
		spec.Tests = []*dalec.TestSpec{
			{
				Name: "Check service files",
				Files: map[string]dalec.FileCheckOutput{
					filepath.Join(testConfig.SystemdDir.Units, "system/simple.service"): {
						CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
						Permissions: 0o644,
					},
					filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/simple.service"): {
						NotExist: true,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})

		// Test to ensure unit can be installed under a different name
		spec.Artifacts.Systemd = &dalec.SystemdConfiguration{
			Units: map[string]dalec.SystemdUnitConfig{
				"src/simple.service": {
					Name: "phony.service",
				},
			},
		}

		spec.Tests = []*dalec.TestSpec{
			{
				Name: "Check service files",
				Files: map[string]dalec.FileCheckOutput{
					filepath.Join(testConfig.SystemdDir.Units, "system/phony.service"): {
						CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/service"}},
						Permissions: 0o644,
					},
					filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/phony.service"): {
						NotExist: true,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("test systemd unit multiple components", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-systemd-unit",
			Description: "Test systemd unit",
			Website:     "https://www.github.com/Azure/dalec",
			Version:     "0.0.1",
			Revision:    "1",
			Vendor:      "Microsoft",
			License:     "Apache 2.0",
			Packager:    "Microsoft <support@microsoft.com>",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"foo.service": {
									Contents: `
[Unit]
Description=Foo Service
After=network.target foo.socket
Requires=foo.socket

[Service]
Type=simple
ExecStart=/usr/bin/foo
Restart=always

[Install]
WantedBy=multi-user.target
`,
								},

								"foo.socket": {
									Contents: `
[Unit]
Description=foo socket
PartOf=foo.service

[Socket]
ListenStream=127.0.0.1:8080

[Install]
WantedBy=sockets.target
									`,
								},
								"foo.conf": {
									Contents: `
[Service]
Environment="FOO_ARGS=--some-foo-arg"
									`,
								},
								"env.conf": {
									Contents: `
[Service]
Environment="FOO_ARGS=--some-foo-args"
									`,
								},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Units: map[string]dalec.SystemdUnitConfig{
						"src/foo.service": {},
						"src/foo.socket": {
							Enable: true,
						},
					},
					Dropins: map[string]dalec.SystemdDropinConfig{
						"src/foo.conf": {
							Unit: "foo.service",
						},
						"src/env.conf": {
							Unit: "foo.socket",
						},
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check service files",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.service"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"ExecStart=/usr/bin/foo"}},
							Permissions: 0o644,
						},
						filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/foo.service"): {
							NotExist: true,
						},
						filepath.Join(testConfig.SystemdDir.Targets, "sockets.target.wants/foo.socket"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Description=foo socket"}},
						},
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.service.d/foo.conf"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Environment"}},
							Permissions: 0o644,
						},
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.socket.d/env.conf"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Environment"}},
							Permissions: 0o644,
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("test systemd with only config dropin", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-systemd-unit",
			Description: "Test systemd unit",
			Website:     "https://www.github.com/Azure/dalec",
			Version:     "0.0.1",
			Revision:    "1",
			Vendor:      "Microsoft",
			License:     "Apache 2.0",
			Packager:    "Microsoft <support@microsoft.com>",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"foo.conf": {
									Contents: `
[Service]
Environment="KUBELET_KUBECONFIG_ARGS=--bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf --kubeconfig=/etc/kubernetes/kubelet.conf"
								`,
								},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Systemd: &dalec.SystemdConfiguration{
					Dropins: map[string]dalec.SystemdDropinConfig{
						"src/foo.conf": {
							Unit: "foo.service",
						},
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check service files",
					Files: map[string]dalec.FileCheckOutput{
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.service.d/foo.conf"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Environment"}},
							Permissions: 0o644,
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("go module", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "test-build-with-gomod",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Testing container target",
			Sources: map[string]dalec.Source{
				"src": {
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{},
						},
					},
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"main.go": {Contents: gomodFixtureMain},
								"go.mod":  {Contents: gomodFixtureMod},
								"go.sum":  {Contents: gomodFixtureSum},
							},
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					// TODO: This works at least for now, but is distro specific and
					// could break on new distros (though that is still unlikely).
					"golang": {},
				},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{Command: "[ -d \"${GOMODCACHE}/github.com/cpuguy83/tar2go@v0.3.1\" ]"},
					{Command: "[ -d ./src ]"},
					{Command: "[ -f ./src/main.go ]"},
					{Command: "[ -f ./src/go.mod ]"},
					{Command: "[ -f ./src/go.sum ]"},
					{Command: "cd ./src && go build"},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			solveT(ctx, t, client, req)
		})
	})

	t.Run("test directory creation", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-directory-creation",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should Create Specified Directories",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{
					"curl": {},
				},
			},
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1": {},
				},
				Directories: &dalec.CreateArtifactDirectories{
					Config: map[string]dalec.ArtifactDirConfig{
						"test": {},
						"testWithPerms": {
							Mode: 0o700,
						},
					},
					State: map[string]dalec.ArtifactDirConfig{
						"one/with/slashes": {},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/etc/test", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/etc/testWithPerms", 0o700); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/var/lib/one/with/slashes", 0o755); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("test data file installation", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-data-file-installation",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should install specified data files",
			Sources: map[string]dalec.Source{
				"bin": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o700,
						},
					},
				},
				"data_dir": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"nested_data_file": {
									Contents:    "this is a file which should end up at the path /usr/share/data_dir/nested_data_file\n",
									Permissions: 0o644,
								},
							},
						},
					},
				},
				"another_data_dir": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"another_nested_data_file": {
									Contents:    "this is a file which should end up at the path /usr/share/data_dir/nested_data_file\n",
									Permissions: 0o644,
								},
							},
						},
					},
				},
				"data_file": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "This is a data file which should end up at /usr/share/data_file\n",
							Permissions: 0o644,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"bin": {},
				},
				DataDirs: map[string]dalec.ArtifactConfig{
					"data_dir": {},
					"another_data_dir": {
						SubPath: "subpath",
					},
					"data_file": {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_dir", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_dir/nested_data_file", 0o644); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/subpath/another_data_dir/another_nested_data_file", 0o644); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_file", 0o644); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("test libexec file installation", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "libexec-test",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should install specified data files",
			Sources: map[string]dalec.Source{
				"no_name_no_subpath": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
				"name_only": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
				"name_and_subpath": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
				"subpath_only": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
				"nested_subpath": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "#!/usr/bin/env bash\necho hello world",
							Permissions: 0o755,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"no_name_no_subpath": {},
				},
				Libexec: map[string]dalec.ArtifactConfig{
					"no_name_no_subpath": {},
					"name_only": {
						Name: "this_is_the_name_only",
					},
					"name_and_subpath": {
						SubPath: "subpath",
						Name:    "custom_name",
					},
					"subpath_only": dalec.ArtifactConfig{
						SubPath: "custom",
					},
					"nested_subpath": dalec.ArtifactConfig{
						SubPath: "libexec-test/abcdefg",
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/no_name_no_subpath", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/this_is_the_name_only", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/subpath/custom_name", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/custom/subpath_only", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/libexec/libexec-test/abcdefg/nested_subpath", 0o755); err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("test config files handled", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-config-files-work",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should Create Specified Directories",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{"curl": {}},
			},
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=goodbye",
							Permissions: 0o700,
						},
					},
				},
			},
			Build: dalec.ArtifactBuild{},
			Artifacts: dalec.Artifacts{
				ConfigFiles: map[string]dalec.ArtifactConfig{
					"src1": {},
					"src2": {
						SubPath: "sysconfig",
					},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Config Files Should Be Created in correct place",
					Files: map[string]dalec.FileCheckOutput{
						"/etc/src1":           {},
						"/etc/sysconfig/src2": {},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			sr.Evaluate = true
			solveT(ctx, t, client, sr)
		})
	})

	t.Run("docs and headers and licenses are handled correctly", func(t *testing.T) {
		t.Parallel()
		spec := &dalec.Spec{
			Name:        "test-docs-handled",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Docs should be placed",
			Sources: map[string]dalec.Source{
				"src1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src2": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src4": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "message=hello",
							Permissions: 0o700,
						},
					},
				},
				"src5": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"header.h": {
									Contents:    "message=hello",
									Permissions: 0o644,
								},
							},
						},
					},
				},
				"src6": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"header.h": {
									Contents:    "message=hello",
									Permissions: 0o644,
								},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"src1": {},
					"src2": {
						SubPath: "subpath",
					},
				},
				Licenses: map[string]dalec.ArtifactConfig{
					"src3": {},
					"src4": {
						SubPath: "license-subpath",
					},
				},
				Headers: map[string]dalec.ArtifactConfig{
					// Files with and without ArtifactConfig
					"src1": {
						Name:    "renamed-src1",
						SubPath: "header-subpath-src1",
					},
					"src2": {},
					// Directories with and without ArtifactConfig
					"src5": {
						Name:    "renamed-src5",
						SubPath: "header-subpath-src5",
					},
					"src6": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Doc and lib and header files should be created in correct place",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/share/doc/test-docs-handled/src1":                                        {},
						"/usr/share/doc/test-docs-handled/subpath/src2":                                {},
						filepath.Join(testConfig.LicenseDir, "test-docs-handled/src3"):                 {},
						filepath.Join(testConfig.LicenseDir, "test-docs-handled/license-subpath/src4"): {},
						"/usr/include/header-subpath-src1/renamed-src1":                                {},
						"/usr/include/src2": {},
						"/usr/include/header-subpath-src5/renamed-src5": {
							IsDir: true,
						},
						"/usr/include/src6": {
							IsDir: true,
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			sr.Evaluate = true
			solveT(ctx, t, client, sr)
		})
	})

	t.Run("meta package", func(t *testing.T) {
		// Ensure that packages that just install other packages give the expected output

		t.Parallel()
		ctx := startTestSpan(baseCtx, t)

		spec := &dalec.Spec{
			Name:        "some-meta-thing",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "meta test",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Runtime: map[string]dalec.PackageConstraints{
					"curl": {},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.Target.Container), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)
			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			_, err = ref.StatFile(ctx, gwclient.StatRequest{
				Path: "/usr/bin/curl",
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	})

	t.Run("custom worker", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testCustomLinuxWorker(ctx, t, testConfig.Target, testConfig.Worker)
	})

	t.Run("pinned build dependencies", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testPinnedBuildDeps(ctx, t, testConfig)
	})

	t.Run("custom repo", func(t *testing.T) {

		t.Parallel()

		ctx := startTestSpan(baseCtx, t)
		testCustomRepo(ctx, t, testConfig.Worker, testConfig.Target)
	})

	t.Run("test library artifacts", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testLinuxLibArtirfacts(ctx, t, testConfig)
	})
	t.Run("test symlink artifacts", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testLinuxSymlinkArtifacts(ctx, t, testConfig)
	})
	t.Run("test image configs", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testImageConfig(ctx, t, testConfig.Target.Container)
	})

	t.Run("test package tests cause build to fail", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testLinuxPackageTestsFail(ctx, t, testConfig)
	})

	t.Run("build network mode", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(baseCtx, t)
		testBuildNetworkMode(ctx, t, testConfig.Target)
	})
}

func testCustomLinuxWorker(ctx context.Context, t *testing.T, targetCfg targetConfig, workerCfg workerConfig) {
	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		// base package that will be used as a build dependency of the main package.
		depSpec := &dalec.Spec{
			Name:        "dalec-test-package-custom-worker-dep",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "A basic package for various testing uses",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"hello.txt": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "hello world!",
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"hello.txt": {},
				},
			},
		}

		// Main package, this should fail to build without a custom worker that has
		// the base package available.
		spec := &dalec.Spec{
			Name:        "test-dalec-custom-worker",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "Testing allowing custom worker images to be provided",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					depSpec.Name: {},
				},
				Runtime: map[string]dalec.PackageConstraints{
					depSpec.Name: {},
				},
			},
		}

		// Make sure the built-in worker can't build this package
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(targetCfg.Container))
		_, err := gwc.Solve(ctx, sr)
		if err == nil {
			t.Fatal("expected solve to fail")
		}

		var xErr *moby_buildkit_v1_frontend.ExitError
		if !errors.As(err, &xErr) {
			t.Fatalf("got unexpected error, expected error type %T: %v", xErr, err)
		}

		// Build the base package
		sr = newSolveRequest(withSpec(ctx, t, depSpec), withBuildTarget(targetCfg.Package))
		pkg := reqToState(ctx, gwc, sr, t)

		// Build the worker target, this will give us the worker image as an output.
		// Note: Currently we need to provide a dalec spec just due to how the router is setup.
		//       The spec can be nil, though, it just needs to be parsable by yaml unmarshaller.
		sr = newSolveRequest(withBuildTarget(targetCfg.Worker), withSpec(ctx, t, nil))
		worker := reqToState(ctx, gwc, sr, t)

		// Add the base package + repo to the worker
		// This should make it so when dalec installs build deps it can use the package
		// we built above.
		worker = worker.With(workerCfg.CreateRepo(pkg))

		// Now build again with our custom worker
		// Note, we are solving the main spec, not depSpec here.
		sr = newSolveRequest(withSpec(ctx, t, spec), withBuildContext(ctx, t, workerCfg.ContextName, worker), withBuildTarget(targetCfg.Container))
		res := solveT(ctx, t, gwc, sr)
		ref, err := res.SingleRef()
		if err != nil {
			t.Fatal(err)
		}

		// Since we also added the dep as a runtime dep, the file in the base package should be installed in the output container.
		_, err = ref.StatFile(ctx, gwclient.StatRequest{Path: "/usr/share/doc/" + depSpec.Name + "/hello.txt"})
		if err != nil {
			t.Fatal(err)
		}

		// TODO: we should have a test to make sure this also works with source policies.
		// Unfortunately it seems like there is an issue with the gateway client passing
		// in source policies.
	})
}

func testPinnedBuildDeps(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	pkgName := "dalec-test-package-pinned"

	getTestPackageSpec := func(version string) *dalec.Spec {
		depSpec := &dalec.Spec{
			Name:        pkgName,
			Version:     version,
			Revision:    "1",
			Description: "A basic package for various testing uses",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"version.txt": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "version: " + version,
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Docs: map[string]dalec.ArtifactConfig{
					"version.txt": {},
				},
			},
		}

		return depSpec
	}

	depSpecs := []*dalec.Spec{
		getTestPackageSpec("1.1.1"),
		getTestPackageSpec("1.2.0"),
		getTestPackageSpec("1.3.0"),
	}

	// getTestPinnedSpec returns a spec that has a build dependency on the package with the given constraints.
	// and with an included test in the build steps which ensures that the correct version of the
	// package was used.
	getPinnedTestSpec := func(constraints string, expectVersion string) *dalec.Spec {
		return &dalec.Spec{
			Name:        "dalec-test-pinned-build-deps",
			Version:     "0.0.1",
			Revision:    "1",
			Description: "Testing allowing custom worker images to be provided",
			License:     "MIT",
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					pkgName: {
						Version: []string{constraints},
					},
				},
			},

			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: fmt.Sprintf(`set -x; [ "$(cat /usr/share/doc/%s/version.txt)" = "version: %s" ]`, pkgName, expectVersion),
					},
				},
			},
		}
	}

	formatEqualForDistro := func(v, rev string) string {
		if cfg.Target.FormatDepEqual == nil {
			return v
		}
		return cfg.Target.FormatDepEqual(v, rev)
	}

	tests := []struct {
		name        string
		constraints string
		want        string
	}{
		{
			name:        "exact dep available",
			constraints: cfg.Worker.Constraints.Equal + " " + formatEqualForDistro("1.1.1", "1"),
			want:        "1.1.1",
		},

		{
			name:        "lt dep available",
			constraints: cfg.Worker.Constraints.LessThan + " 1.3.0",
			want:        "1.2.0",
		},

		{
			name:        "gt dep available",
			constraints: cfg.Worker.Constraints.GreaterThan + " 1.2.0",
			want:        "1.3.0",
		},
	}

	getWorker := func(ctx context.Context, t *testing.T, client gwclient.Client) llb.State {
		// Build the worker target, this will give us the worker image as an output.
		// Note: Currently we need to provide a dalec spec just due to how the router is setup.
		//       The spec can be nil, though, it just needs to be parsable by yaml unmarshaller.
		sr := newSolveRequest(withBuildTarget(cfg.Target.Worker), withSpec(ctx, t, nil))
		w := reqToState(ctx, client, sr, t)

		var pkgs []llb.State
		for _, depSpec := range depSpecs {
			sr := newSolveRequest(withSpec(ctx, t, depSpec), withBuildTarget(cfg.Target.Package))
			pkg := reqToState(ctx, client, sr, t)
			pkgs = append(pkgs, pkg)
		}
		return w.With(cfg.Worker.CreateRepo(llb.Merge(pkgs)))
	}

	for _, tt := range tests {
		spec := getPinnedTestSpec(tt.constraints, tt.want)

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				worker := getWorker(ctx, t, gwc)

				sr := newSolveRequest(withSpec(ctx, t, spec), withBuildContext(ctx, t, cfg.Worker.ContextName, worker), withBuildTarget(cfg.Target.Container), withPlatformPtr(cfg.Worker.Platform))
				res := solveT(ctx, t, gwc, sr)
				_, err := res.SingleRef()
				if err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func testLinuxLibArtirfacts(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	t.Run("file", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(ctx, t)

		spec := &dalec.Spec{
			Name:        "test-library-files",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing library files",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"lib1": {Contents: "this is lib1"},
								"lib2": {Contents: "this is lib2"},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{
					"src/lib1": {},
					"src/lib2": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check that lib files exist under package dir",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/lib/test-library-files/lib1": {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib1",
						}},
						"/usr/lib/test-library-files/lib2": {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib2",
						}},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			res := solveT(ctx, t, gwc, sr)
			_, err := res.SingleRef()
			assert.NilError(t, err)
		})
	})

	t.Run("dir", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(ctx, t)

		spec := &dalec.Spec{
			Name:        "test-library-files",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing library files",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"lib1": {Contents: "this is lib1"},
								"lib2": {Contents: "this is lib2"},
							},
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{
					"src/*": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check that lib files exist under package dir",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/lib/test-library-files/lib1": {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib1",
						}},
						"/usr/lib/test-library-files/lib2": {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib2",
						}},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			res := solveT(ctx, t, gwc, sr)
			_, err := res.SingleRef()
			assert.NilError(t, err)
		})
	})

	t.Run("mixed", func(t *testing.T) {
		t.Parallel()

		ctx := startTestSpan(ctx, t)

		spec := &dalec.Spec{
			Name:        "test-library-files",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing library files",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"src": {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"lib1": {Contents: "this is lib1"},
								"lib2": {Contents: "this is lib2"},
							},
						},
					},
				},
				"lib3": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "this is lib3",
						},
					},
				},
			},
			Artifacts: dalec.Artifacts{
				Libs: map[string]dalec.ArtifactConfig{
					"src/*": {},
					"lib3":  {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Check that lib files exist under package dir",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/lib/test-library-files/lib1": {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib1",
						}},
						"/usr/lib/test-library-files/lib2": {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib2",
						}},
						"/usr/lib/test-library-files/lib3": {CheckOutput: dalec.CheckOutput{
							Equals: "this is lib3",
						}},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			res := solveT(ctx, t, gwc, sr)
			_, err := res.SingleRef()
			assert.NilError(t, err)
		})
	})
}

func testLinuxSymlinkArtifacts(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	spec := &dalec.Spec{
		Name:        "test-symlinks",
		Version:     "0.0.1",
		Revision:    "42",
		Description: "Testing symlinks",
		License:     "MIT",

		Dependencies: &dalec.PackageDependencies{
			Runtime: map[string]dalec.PackageConstraints{
				"bash": {},
			},
		},

		Artifacts: dalec.Artifacts{
			Links: []dalec.ArtifactSymlinkConfig{
				{
					Source: "/bin/sh",
					Dest:   "/bin/dalecsh",
				},
			},
		},
		Tests: []*dalec.TestSpec{
			{
				Name: "Test symlink works",
				Steps: []dalec.TestStep{
					{
						Command: "/bin/dalecsh -c 'echo -n hello world'",
						Stdout:  dalec.CheckOutput{Equals: "hello world"},
					},
				},
			},
		},
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
		sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
		res := solveT(ctx, t, client, sr)
		_, err := res.SingleRef()
		assert.NilError(t, err)
	})
}

func testImageConfig(ctx context.Context, t *testing.T, target string, opts ...srOpt) {
	spec := &dalec.Spec{
		Name:        "test-image-config",
		Version:     "0.0.1",
		Revision:    "42",
		Description: "Test to make sure image configs are copied over",
		License:     "MIT",
		Image: &dalec.ImageConfig{
			Entrypoint: "some-entrypoint",
			Cmd:        "some-cmd",
			Env: []string{
				"ENV1=VAL1",
				"ENV2=VAL2",
			},
			Labels: map[string]string{
				"label.1": "value1",
				"label.2": "value2",
			},
			Volumes: map[string]struct{}{
				"/some/volume": {},
			},
			WorkingDir: "/some/work/dir",
			StopSignal: "SOME-SIG",
			User:       "some-user",
		},
	}

	envToMap := func(envs []string) map[string]string {
		out := make(map[string]string, len(envs))
		for _, env := range envs {
			k, v, _ := strings.Cut(env, "=")
			out[k] = v
		}
		return out
	}

	testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
		opts = append(opts, withSpec(ctx, t, spec))
		opts = append(opts, withBuildTarget(target))
		sr := newSolveRequest(opts...)
		res := solveT(ctx, t, gwc, sr)

		dt, ok := res.Metadata[exptypes.ExporterImageConfigKey]
		assert.Assert(t, ok, "missing image config in result metadata")

		var img dalec.DockerImageSpec
		err := json.Unmarshal(dt, &img)
		assert.NilError(t, err)

		assert.Check(t, cmp.Equal(strings.Join(img.Config.Entrypoint, " "), spec.Image.Entrypoint))
		assert.Check(t, cmp.Equal(strings.Join(img.Config.Cmd, " "), spec.Image.Cmd))

		// Envs are merged together with the base image
		// So we need to validate that the values we've set are what we expect
		// Often there will be at least one other env for `PATH` we won't check
		expectEnv := envToMap(spec.Image.Env)
		actualEnv := envToMap(img.Config.Env)
		for k, v := range expectEnv {
			assert.Check(t, cmp.Equal(actualEnv[k], v))
		}

		// Labels are merged with the base image
		// So we need to check that the labels we've set are added
		for k, v := range spec.Image.Labels {
			assert.Check(t, cmp.Equal(v, img.Config.Labels[k]))
		}

		// Volumes are merged with the base image
		// So we need to check that the volumes we've set are added
		for k := range spec.Image.Volumes {
			_, ok := img.Config.Volumes[k]
			assert.Check(t, ok, k)
		}

		assert.Check(t, cmp.Equal(img.Config.WorkingDir, spec.Image.WorkingDir))
		assert.Check(t, cmp.Equal(img.Config.StopSignal, spec.Image.StopSignal))
		assert.Check(t, cmp.Equal(img.Config.User, spec.Image.User))
	})
}

func testLinuxPackageTestsFail(ctx context.Context, t *testing.T, cfg testLinuxConfig) {
	t.Run("negative test", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := &dalec.Spec{
			Name:        "test-package-tests",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing package tests",
			License:     "MIT",
			Tests: []*dalec.TestSpec{
				{
					Name: "Test that tests fail the build",
					Files: map[string]dalec.FileCheckOutput{
						"/non-existing-file": {},
					},
				},
				{
					Name: "Test that permissions check fails the build",
					Files: map[string]dalec.FileCheckOutput{
						"/": {Permissions: 0o644, IsDir: true},
					},
				},
				{
					Name: "Test that dir check fails the build",
					Files: map[string]dalec.FileCheckOutput{
						"/": {IsDir: false},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
			_, err := client.Solve(ctx, sr)
			assert.ErrorContains(t, err, "lstat /non-existing-file: no such file or directory")
			assert.ErrorContains(t, err, "expected \"/\" permissions \"-rw-r--r--\", got \"-rwxr-xr-x\"")
			assert.ErrorContains(t, err, "expected \"/\" mode \"ModeFile\", got \"ModeDir\"")

			sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			_, err = client.Solve(ctx, sr)
			assert.ErrorContains(t, err, "lstat /non-existing-file: no such file or directory")
			assert.ErrorContains(t, err, "expected \"/\" permissions \"-rw-r--r--\", got \"-rwxr-xr-x\"")
			assert.ErrorContains(t, err, "expected \"/\" mode \"ModeFile\", got \"ModeDir\"")
		})
	})

	t.Run("positive test", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		spec := &dalec.Spec{
			Name:        "test-package-tests",
			Version:     "0.0.1",
			Revision:    "42",
			Description: "Testing package tests",
			License:     "MIT",
			Sources: map[string]dalec.Source{
				"test-file": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "hello world",
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Test: []string{
					"bash",
				},
			},
			Artifacts: dalec.Artifacts{
				DataDirs: map[string]dalec.ArtifactConfig{
					"test-file": {},
				},
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Test that tests fail the build",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/share/test-file": {},
						// Make sure dir permissions are chcked correctly.
						"/usr/share": {IsDir: true, Permissions: 0o755},
					},
				},
				{
					Name: "Test that test mounts work",
					Steps: []dalec.TestStep{
						{
							Command: "/bin/sh -c 'test -f /mount0'",
						},
						{
							Command: "/bin/sh -c 'test -d /mount1'",
						},
						{
							Command: `/bin/sh -c 'grep "some file" /mont1/some_file'`,
						},
						{
							Command: "/bin/sh -c 'test -f /mount2'",
						},
						{
							Command: `/bin/sh -c 'grep "some other file" /mount2'`,
						},
					},
					Mounts: []dalec.SourceMount{
						{
							Dest: "/mount0",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									File: &dalec.SourceInlineFile{
										Contents: "mount0",
									},
								},
							},
						},
						{
							Dest: "/mount1",
							Spec: dalec.Source{
								Inline: &dalec.SourceInline{
									Dir: &dalec.SourceInlineDir{
										Files: map[string]*dalec.SourceInlineFile{
											"some_file": &dalec.SourceInlineFile{
												Contents: "some file",
											},
										},
									},
								},
							},
						},
						{
							Dest: "/mount2",
							Spec: dalec.Source{
								Path: "another_file",
								Inline: &dalec.SourceInline{
									Dir: &dalec.SourceInlineDir{
										Files: map[string]*dalec.SourceInlineFile{
											"another_file": &dalec.SourceInlineFile{
												Contents: "some other file",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Package))
			res := solveT(ctx, t, client, sr)
			_, err := res.SingleRef()
			assert.NilError(t, err)

			sr = newSolveRequest(withSpec(ctx, t, spec), withBuildTarget(cfg.Target.Container))
			res = solveT(ctx, t, client, sr)
			_, err = res.SingleRef()
			assert.NilError(t, err)
		})
	})
}
