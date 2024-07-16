package test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/test/testenv"
	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/stretchr/testify/assert"
)

func runTest(t *testing.T, f testenv.TestFunc, opts ...testenv.TestRunnerOpt) {
	t.Helper()
	ctx := startTestSpan(baseCtx, t)
	testEnv.RunTest(ctx, t, f, opts...)
}

func newSpec() *dalec.Spec {
	spec := fillMetadata("foo", &dalec.Spec{
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
	})

	return spec
}

func linuxSigningTests(ctx context.Context, testConfig testLinuxConfig) func(*testing.T) {
	return func(t *testing.T) {
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

		t.Run("root config", func(t *testing.T) {
			t.Parallel()
			spec := newSpec()
			runTest(t, distroSigningTest(t, spec, testConfig.SignTarget))
		})

		t.Run("with target config", func(t *testing.T) {
			t.Parallel()
			spec := newSpec()
			first, _, _ := strings.Cut(testConfig.SignTarget, "/")
			spec.Targets = map[string]dalec.Target{
				first: {
					PackageConfig: &dalec.PackageConfig{
						Signer: spec.PackageConfig.Signer,
					},
				},
			}
			spec.PackageConfig.Signer = nil

			runTest(t, distroSigningTest(t, spec, testConfig.SignTarget))
		})

		t.Run("target config takes precedence when root config is there", func(t *testing.T) {
			t.Parallel()
			spec := newSpec()

			first, _, _ := strings.Cut(testConfig.SignTarget, "/")
			spec.Targets = map[string]dalec.Target{
				first: {
					PackageConfig: &dalec.PackageConfig{
						Signer: &dalec.PackageSigner{
							Frontend: &dalec.Frontend{
								Image: spec.PackageConfig.Signer.Image,
							},
						},
					},
				},
			}

			spec.PackageConfig.Signer.Image = "notexist"
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

		t.Run("with path build arg and build context", func(t *testing.T) {
			spec := newSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().File(llb.Mkfile("/unusual_place.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.SignTarget,
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/unusual_place.yml"),
			))
		})

		t.Run("path build arg takes precedence over spec config", func(t *testing.T) {
			spec := newSpec()
			spec.PackageConfig.Signer.Frontend.Image = "notexist"

			signConfig := llb.Scratch().File(llb.Mkfile("/unusual_place.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.SignTarget,
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/unusual_place.yml"),
			))
		})

		t.Run("with path build arg and build context", func(t *testing.T) {
			spec := newSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().File(llb.Mkfile("/unusual_place.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.SignTarget,
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "/unusual_place.yml"),
			))
		})

		t.Run("with no build context and config path build arg", func(t *testing.T) {
			spec := newSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().
				File(llb.Mkdir("/test/fixtures/signer/", 0o755, llb.WithParents(true))).
				File(llb.Mkfile("/test/fixtures/signer/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.SignTarget,
				withMainContext(ctx, t, signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "test/fixtures/signer/sign_config.yml"),
			))
		})

		t.Run("local context with config path takes precedence over spec", func(t *testing.T) {
			spec := newSpec()
			spec.PackageConfig.Signer.Frontend.Image = "notexist"

			signConfig := llb.Scratch().
				File(llb.Mkdir("/test/fixtures/signer/", 0o755, llb.WithParents(true))).
				File(llb.Mkfile("/test/fixtures/signer/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSigningTest(
				t,
				spec,
				testConfig.SignTarget,
				withMainContext(ctx, t, signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "test/fixtures/signer/sign_config.yml"),
			))
		})

		t.Run("skip signing", func(t *testing.T) {
			t.Parallel()

			spec := newSpec()
			runTest(t, distroSkipSigningTest(t, spec, testConfig.SignTarget))
		})

		t.Run("skip signing takes precedence over custom context", func(t *testing.T) {
			t.Parallel()

			spec := newSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().
				File(llb.Mkdir("/test/fixtures/signer/", 0o755, llb.WithParents(true))).
				File(llb.Mkfile("/test/fixtures/signer/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSkipSigningTest(
				t,
				spec,
				testConfig.SignTarget,
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "test/fixtures/signer/sign_config.yml"),
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
			))
			runTest(t, distroSkipSigningTest(t, spec, testConfig.SignTarget))
		})

		t.Run("skip signing takes precedence over local context", func(t *testing.T) {
			t.Parallel()

			spec := newSpec()
			spec.PackageConfig.Signer = nil

			signConfig := llb.Scratch().
				File(llb.Mkdir("/test/fixtures/signer/", 0o755, llb.WithParents(true))).
				File(llb.Mkfile("/test/fixtures/signer/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runTest(t, distroSkipSigningTest(
				t,
				spec,
				testConfig.SignTarget,
				withMainContext(ctx, t, signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "test/fixtures/signer/sign_config.yml"),
			))
			runTest(t, distroSkipSigningTest(t, spec, testConfig.SignTarget))
		})
	}
}

func windowsSigningTests(t *testing.T) {
	t.Run("target spec config", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSpec()
			spec.Targets = map[string]dalec.Target{
				"windowscross": {
					PackageConfig: &dalec.PackageConfig{
						Signer: &dalec.PackageSigner{
							Frontend: &dalec.Frontend{
								Image: phonySignerRef,
							},
						},
					},
				},
			}

			runBuild(ctx, t, gwc, spec)
		})
	})

	t.Run("root spec config", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSpec()
			spec.PackageConfig = &dalec.PackageConfig{
				Signer: &dalec.PackageSigner{
					Frontend: &dalec.Frontend{
						Image: phonySignerRef,
					},
				},
			}

			runBuild(ctx, t, gwc, spec)
		})
	})

	t.Run("with path arg and build context", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSpec()

			signConfig := llb.Scratch().File(llb.Mkfile("/unusual_place.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runBuild(
				ctx,
				t,
				gwc,
				spec,
				withBuildArg("DALEC_SIGNING_CONFIG_CONTEXT_NAME", "dalec_signing_config"),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "unusual_place.yml"),
				withBuildContext(ctx, t, "dalec_signing_config", signConfig),
			)
		})
	})

	t.Run("with path arg and local context", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSpec()

			signConfig := llb.Scratch().
				File(llb.Mkdir("/test/fixtures/signer/", 0o755, llb.WithParents(true))).
				File(llb.Mkfile("/test/fixtures/signer/sign_config.yml", 0o400, []byte(`
signer:
  image: `+phonySignerRef+`
  cmdline: /signer
`)))

			runBuild(ctx,
				t,
				gwc,
				spec,
				withMainContext(ctx, t, signConfig),
				withBuildArg("DALEC_SIGNING_CONFIG_PATH", "test/fixtures/signer/sign_config.yml"),
			)
		})

	})

	t.Run("test skipping windows signing", func(t *testing.T) {
		t.Parallel()
		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			spec := newSpec()
			st := prepareSigningState(ctx, t, gwc, spec, withBuildArg("DALEC_SKIP_SIGNING", "1"))

			def, err := st.Marshal(ctx)
			if err != nil {
				t.Fatal(err)
			}

			res := solveT(ctx, t, gwc, gwclient.SolveRequest{
				Definition: def.ToPB(),
			})

			if _, err := maybeReadFile(ctx, "/target", res); err == nil {
				t.Fatalf("signing took place even though signing was disabled")
			}

			if _, err = maybeReadFile(ctx, "/config.json", res); err == nil {
				t.Fatalf("signing took place even though signing was disabled")
			}
		})
	})
}
func distroSigningTest(t *testing.T, spec *dalec.Spec, buildTarget string, extraSrOpts ...srOpt) testenv.TestFunc {
	return func(ctx context.Context, gwc gwclient.Client) {
		topTgt, _, _ := strings.Cut(buildTarget, "/")

		srOpts := []srOpt{
			withSpec(ctx, t, spec),
			withBuildTarget(buildTarget),
		}
		srOpts = append(srOpts, extraSrOpts...)

		sr := newSolveRequest(srOpts...)
		res := solveT(ctx, t, gwc, sr)

		tgt := readFile(ctx, t, "/target", res)
		cfg := readFile(ctx, t, "/config.json", res)

		if string(tgt) != topTgt {
			t.Fatal(fmt.Errorf("target incorrect; either not sent to signer or not received back from signer"))
		}

		if !strings.Contains(string(cfg), "linux") {
			t.Fatal(fmt.Errorf("configuration incorrect"))
		}

		if spec.PackageConfig != nil && spec.PackageConfig.Signer != nil {
			for k, v := range spec.PackageConfig.Signer.Args {
				dt := readFile(ctx, t, "/env/"+k, res)
				assert.Equal(t, v, string(dt))
			}
		}
	}
}

func distroSkipSigningTest(t *testing.T, spec *dalec.Spec, buildTarget string, extraSrOpts ...srOpt) testenv.TestFunc {
	return func(ctx context.Context, gwc gwclient.Client) {
		srOpts := []srOpt{withSpec(ctx, t, spec), withBuildTarget(buildTarget), withBuildArg("DALEC_SKIP_SIGNING", "1")}
		srOpts = append(srOpts, extraSrOpts...)
		sr := newSolveRequest(srOpts...)

		res := solveT(ctx, t, gwc, sr)

		if _, err := maybeReadFile(ctx, "/target", res); err == nil {
			t.Fatalf("signer signed even though signing was disabled")
		}
		if _, err := maybeReadFile(ctx, "/config.json", res); err == nil {
			t.Fatalf("signer signed even though signing was disabled")
		}
	}
}
