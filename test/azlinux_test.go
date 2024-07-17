package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/test/testenv"
	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	moby_buildkit_v1_frontend "github.com/moby/buildkit/frontend/gateway/pb"
	"gotest.tools/v3/assert"
)

func TestMariner2(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		BuildTarget: "mariner2/container",
		SignTarget:  "mariner2/rpm",
		LicenseDir:  "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
	})
}

func TestAzlinux3(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)
	testLinuxDistro(ctx, t, testLinuxConfig{
		BuildTarget: "azlinux3/container",
		SignTarget:  "azlinux3/rpm",
		LicenseDir:  "/usr/share/licenses",
		SystemdDir: struct {
			Units   string
			Targets string
		}{
			Units:   "/usr/lib/systemd",
			Targets: "/etc/systemd/system",
		},
	})
}

type testLinuxConfig struct {
	BuildTarget string
	SignTarget  string
	LicenseDir  string
	SystemdDir  struct {
		Units   string
		Targets string
	}
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
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(testConfig.BuildTarget))
			sr.Evaluate = true
			_, err := gwc.Solve(ctx, sr)
			var xErr *moby_buildkit_v1_frontend.ExitError
			if !errors.As(err, &xErr) {
				t.Fatalf("expected exit error, got %T: %v", errors.Unwrap(err), err)
			}
		})
	})

	t.Run("should not have internet access during build", func(t *testing.T) {
		t.Parallel()
		spec := dalec.Spec{
			Name:        "test-no-internet-access",
			Version:     "0.0.1",
			Revision:    "1",
			License:     "MIT",
			Website:     "https://github.com/azure/dalec",
			Vendor:      "Dalec",
			Packager:    "Dalec",
			Description: "Should not have internet access during build",
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{"curl": {}},
			},
			Build: dalec.ArtifactBuild{
				Steps: []dalec.BuildStep{
					{
						Command: fmt.Sprintf("curl --head -ksSf %s > /dev/null", externalTestHost),
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(withSpec(ctx, t, &spec), withBuildTarget(testConfig.BuildTarget))
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
					},
				},
			},

			Artifacts: dalec.Artifacts{
				Binaries: map[string]dalec.ArtifactConfig{
					"src1":       {},
					"src2/file2": {},
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
						"/src1": {},
					},
					Steps: []dalec.TestStep{
						{Command: "/bin/bash -c 'test -L /src1'"},
						{Command: "/bin/bash -c 'test \"$(readlink /src1)\" = \"/usr/bin/src1\"'"},
						{Command: "/src1", Stdout: dalec.CheckOutput{Equals: "hello world\n"}, Stderr: dalec.CheckOutput{Empty: true}},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			sr := newSolveRequest(
				withSpec(ctx, t, &spec),
				withBuildTarget(testConfig.BuildTarget),
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
			withSpec(ctx, t, &spec)(&sr)

			if _, err := gwc.Solve(ctx, sr); err == nil {
				t.Fatal("expected test spec to run with error but got none")
			}
		})
	})

	runTest := func(t *testing.T, f testenv.TestFunc, opts ...testenv.TestRunnerOpt) {
		t.Helper()
		ctx := startTestSpan(baseCtx, t)
		testEnv.RunTest(ctx, t, f, opts...)
	}

	t.Run("test signing", func(t *testing.T) {
		newSpec := func() *dalec.Spec {
			return &dalec.Spec{
				Name:        "foo",
				Version:     "0.0.1",
				Description: "foo bar baz",
				Website:     "https://foo.bar.baz",
				Revision:    "1",
				License:     "MIT",
				PackageConfig: &dalec.PackageConfig{
					Signer: &dalec.PackageSigner{
						Frontend: &dalec.Frontend{
							Image: phonySignerRef,
						},
					},
				},
				Sources: map[string]dalec.Source{
					"foo": {
						Inline: &dalec.SourceInline{
							File: &dalec.SourceInlineFile{
								Contents: "#!/usr/bin/env bash\necho \"hello, world!\"\n",
							},
						},
					},
				},
				Build: dalec.ArtifactBuild{
					Steps: []dalec.BuildStep{
						{
							Command: "/bin/true",
						},
					},
				},
				Artifacts: dalec.Artifacts{
					Binaries: map[string]dalec.ArtifactConfig{
						"foo": {},
					},
				},
			}
		}

		t.Run("no args", func(t *testing.T) {
			t.Parallel()
			spec := newSpec()
			runTest(t, distroSigningTest(t, spec, testConfig.SignTarget))
		})

		t.Run("with args", func(t *testing.T) {
			t.Parallel()

			spec := newSpec()
			spec.PackageConfig.Signer.Args = map[string]string{
				"HELLO": "world",
				"FOO":   "bar",
			}
			runTest(t, distroSigningTest(t, spec, testConfig.SignTarget))
		})

		t.Run("with skip signing", func(t *testing.T) {
			t.Parallel()

			spec := newSpec()
			var found bool
			handleStatus := func(status *client.SolveStatus) {
				if found {
					return
				}
				for _, w := range status.Warnings {
					if strings.Contains(string(w.Short), "Signing disabled by build-arg") {
						found = true
						return
					}
				}
			}
			runTest(t, distroSkipSigningTest(t, spec, testConfig.SignTarget), testenv.WithSolveStatusFn(handleStatus))
			assert.Assert(t, found, "Signing disabled warning message not emitted")
		})
	})

	t.Run("test systemd unit", func(t *testing.T) {
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
`},
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
							Permissions: 0644,
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
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
						Permissions: 0644,
					},
					filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/simple.service"): {
						NotExist: true,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
						Permissions: 0644,
					},
					filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/phony.service"): {
						NotExist: true,
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
	# simple-socket.service
	[Unit]
	Description=Foo Service
	After=network.target foo.socket
	Requires=foo.socket

	[Service]
	Type=simple
	ExecStart=/usr/bin/foo
	ExecReload=/bin/kill -HUP $MAINPID
	StandardOutput=journal
	StandardError=journal
	`},

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
	Environment="KUBELET_KUBECONFIG_ARGS=--bootstrap-kubeconfig=/etc/kubernetes/bootstrap-kubelet.conf --kubeconfig=/etc/kubernetes/kubelet.conf"
									`,
								},
								"env.conf": {
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
							Permissions: 0644,
						},
						filepath.Join(testConfig.SystemdDir.Targets, "multi-user.target.wants/foo.service"): {
							NotExist: true,
						},
						filepath.Join(testConfig.SystemdDir.Targets, "sockets.target.wants/foo.socket"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Description=foo socket"}},
						},
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.service.d/foo.conf"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Environment"}},
							Permissions: 0644,
						},
						filepath.Join(testConfig.SystemdDir.Units, "system/foo.socket.d/env.conf"): {
							CheckOutput: dalec.CheckOutput{Contains: []string{"Environment"}},
							Permissions: 0644,
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
							Permissions: 0644,
						},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
									Permissions: 0o400,
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
									Permissions: 0o400,
								},
							},
						},
					},
				},
				"data_file": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents:    "This is a data file which should end up at /usr/share/data_file\n",
							Permissions: 0o400,
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
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
			res := solveT(ctx, t, client, req)

			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_dir", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_dir/nested_data_file", 0o400); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/subpath/another_data_dir/another_nested_data_file", 0o400); err != nil {
				t.Fatal(err)
			}
			if err := validatePathAndPermissions(ctx, ref, "/usr/share/data_file", 0o400); err != nil {
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
			sr := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
			sr.Evaluate = true
			solveT(ctx, t, client, sr)
		})
	})

	t.Run("docs and licenses are handled correctly", func(t *testing.T) {
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
			},
			Tests: []*dalec.TestSpec{
				{
					Name: "Doc files should be created in correct place",
					Files: map[string]dalec.FileCheckOutput{
						"/usr/share/doc/test-docs-handled/src1":                                        {},
						"/usr/share/doc/test-docs-handled/subpath/src2":                                {},
						filepath.Join(testConfig.LicenseDir, "test-docs-handled/src3"):                 {},
						filepath.Join(testConfig.LicenseDir, "test-docs-handled/license-subpath/src4"): {},
					},
				},
			},
		}

		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			sr := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
			req := newSolveRequest(withBuildTarget(testConfig.BuildTarget), withSpec(ctx, t, spec))
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
}

func validatePathAndPermissions(ctx context.Context, ref gwclient.Reference, path string, expected os.FileMode) error {
	stat, err := ref.StatFile(ctx, gwclient.StatRequest{Path: path})
	if err != nil {
		return err
	}

	got := os.FileMode(stat.Mode).Perm()

	if expected != got {
		return fmt.Errorf("expected permissions %v to equal expected %v", got, expected)
	}
	return nil
}
