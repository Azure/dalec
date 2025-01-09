package dalec

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"reflect"
	"testing"

	"github.com/moby/buildkit/frontend/dockerui"
	"gotest.tools/v3/assert"
	"gotest.tools/v3/assert/cmp"
)

//go:embed test/fixtures/unmarshall/source-inline.yml
var sourceInlineTemplate []byte

func TestSourceValidation(t *testing.T) {
	cases := []struct {
		title     string
		src       Source
		expectErr bool
	}{
		{
			title:     "has no valid source variant",
			src:       Source{},
			expectErr: true,
		},
		{
			title: "has multiple non-nil source variants",
			src: Source{
				DockerImage: &SourceDockerImage{
					Ref: "nonempty:latest",
				},
				Git: &SourceGit{},
			},
			expectErr: true,
		},
		{
			title:     "has multiple source types in docker-image command mount",
			expectErr: true,
			src: Source{
				DockerImage: &SourceDockerImage{
					Ref: "nonempty:latest",
					Cmd: &Command{
						Mounts: []SourceMount{{
							Dest: "",
							Spec: Source{
								DockerImage: &SourceDockerImage{
									Ref: "",
									Cmd: &Command{
										Mounts: []SourceMount{
											{
												Spec: Source{
													Git:  &SourceGit{},
													HTTP: &SourceHTTP{},
												},
											},
										},
									},
								},
							},
						}},
					},
				},
			},
		},
		{
			title:     "has no non-nil source type in docker-image command mount",
			expectErr: true,
			src: Source{
				DockerImage: &SourceDockerImage{
					Ref: "nonempty:latest",
					Cmd: &Command{
						Mounts: []SourceMount{{
							Dest: "",
							Spec: Source{},
						}},
					},
				},
			},
		},
		{
			title:     "has recursive build sources",
			expectErr: true,
			src: Source{
				Build: &SourceBuild{
					Source: Source{
						Build: &SourceBuild{
							DockerfilePath: "/other/nonempty/Dockerfile/path",
							Source: Source{
								Git: &SourceGit{},
							},
						},
					},
					DockerfilePath: "/nonempty/Dockerfile/path",
				},
			},
		},
		{
			title:     "has invalid build subsource",
			expectErr: true,
			src: Source{
				Build: &SourceBuild{
					Source: Source{
						DockerImage: &SourceDockerImage{
							Ref: "",
						},
					},
					DockerfilePath: "/nonempty/Dockerfile/path",
				},
			},
		},
		{
			title:     "has multiple layers of recursion with an error at the bottom",
			expectErr: true,
			src: Source{
				Build: &SourceBuild{
					Source: Source{
						DockerImage: &SourceDockerImage{
							Ref: "nonempty:latest",
							Cmd: &Command{
								Mounts: []SourceMount{
									{
										Dest: "/nonempty",
										Spec: Source{
											DockerImage: &SourceDockerImage{
												Ref: "",
											},
										},
									},
								},
							},
						},
					},
					DockerfilePath: "/nonempty/Dockerfile/path",
				},
			},
		},
		{
			title:     "has inline file and files set",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{},
					Dir:  &SourceInlineDir{},
				},
			},
		},
		{
			title:     "has path separator in inline nested file name",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					Dir: &SourceInlineDir{
						Files: map[string]*SourceInlineFile{
							"file/with/slash": {},
						},
					},
				},
			},
		},
		{
			title:     "inline dir has negative UID",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					Dir: &SourceInlineDir{
						UID: -1,
					},
				},
			},
		},
		{
			title:     "inline dir has negative GID",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					Dir: &SourceInlineDir{
						GID: -1,
					},
				},
			},
		},
		{
			title:     "inline file has negative UID",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{
						UID: -1,
					},
				},
			},
		},
		{
			title:     "inline file has negative GID",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{
						GID: -1,
					},
				},
			},
		},
		{
			title:     "inline file has path set",
			expectErr: true,
			src: Source{
				Path: "subpath",
				Inline: &SourceInline{
					File: &SourceInlineFile{},
				},
			},
		},
		{
			title:     "has invalid genator config",
			expectErr: true,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{},
				},
				Generate: []*SourceGenerator{{}},
			},
		},
		{
			title:     "has valid genator",
			expectErr: false,
			src: Source{
				Inline: &SourceInline{
					File: &SourceInlineFile{},
				},
				Generate: []*SourceGenerator{{Gomod: &GeneratorGomod{}}},
			},
		},
		{
			title:     "docker images with cmd source must specify a path to extract",
			expectErr: true,
			src: Source{
				Path: "",
				DockerImage: &SourceDockerImage{
					Ref: "notexists:latest",
					Cmd: &Command{
						Steps: []*BuildStep{
							{Command: ":"},
						},
					},
				},
			},
		},
		{
			title:     "cmd source mount dest must not be /",
			expectErr: true,
			src: Source{
				Path: "/foo",
				DockerImage: &SourceDockerImage{
					Ref: "notexists:latest",
					Cmd: &Command{
						Steps: []*BuildStep{
							{Command: ":"},
						},
						Mounts: []SourceMount{
							{
								Dest: "/",
								Spec: Source{
									Inline: &SourceInline{
										File: &SourceInlineFile{},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			title:     "cmd source mount dest must not be a descendent of the extracted source path",
			expectErr: true,
			src: Source{
				Path: "/foo",
				DockerImage: &SourceDockerImage{
					Ref: "notexists:latest",
					Cmd: &Command{
						Mounts: []SourceMount{
							{
								Dest: "/foo",
								Spec: Source{
									Inline: &SourceInline{
										File: &SourceInlineFile{},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		title := fmt.Sprintf("source %s", tc.title)
		t.Run(title, func(tt *testing.T) {
			err := tc.src.validate()
			if tc.expectErr && err != nil {
				return
			}

			if err != nil {
				tt.Fatal(err)
			}

			if tc.expectErr {
				tt.Fatal("expected error, but received none")
			}
		})
	}
}

func TestSourceFillDefaults(t *testing.T) {
	cases := []struct {
		title  string
		before Source
		after  Source
	}{
		{
			title: "fills default context name when source type is context",
			before: Source{
				Context: &SourceContext{
					Name: "",
				},
				Path: ".",
			},
			after: Source{
				Context: &SourceContext{
					Name: "context",
				},
				Path: ".",
			},
		},
		{
			title: "sets nested defaults when source type is docker image",
			before: Source{
				DockerImage: &SourceDockerImage{
					Ref: "busybox:latest",
					Cmd: &Command{
						Dir: "/build",
						Mounts: []SourceMount{
							{
								Dest: "/build/test",
								Spec: Source{
									Context: &SourceContext{
										Name: "",
									},
									Path: ".",
								},
							},
						},
					},
				},
				Path: ".",
			},
			after: Source{
				DockerImage: &SourceDockerImage{
					Ref: "busybox:latest",
					Cmd: &Command{
						Dir: "/build",
						Mounts: []SourceMount{
							{
								Dest: "/build/test",
								Spec: Source{
									Context: &SourceContext{
										Name: "context",
									},
									Path: ".",
								},
							},
						},
					},
				},
				Path: ".",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		title := fmt.Sprintf("source %s", tc.title)
		t.Run(title, func(t *testing.T) {
			src := tc.before
			expected := tc.after

			if err := src.validate(); err != nil {
				t.Fatal(err)
			}
			spec := &Spec{
				Sources: map[string]Source{
					"test": src,
				},
			}

			spec.FillDefaults()
			filledSrc := spec.Sources["test"]

			if !reflect.DeepEqual(filledSrc, expected) {
				s, err := json.MarshalIndent(&src, "", "\t")
				if err != nil {
					t.Fatal(err)
				}

				e, err := json.MarshalIndent(&expected, "", "\t")
				if err != nil {
					t.Fatal(err)
				}

				t.Fatalf("\nactual: %s\n-------------\nexpected: %s", string(s), string(e))
			}

		})
	}
}

func TestSourceInlineUnmarshalling(t *testing.T) {
	// NOTE: not using text template yaml for this test
	// tabs seem to be illegal in yaml indentation
	// yaml unmarshalling with strict mode doesn't produce a great error message.
	spec, err := LoadSpec(sourceInlineTemplate)
	if err != nil {
		t.Fatal(err)
	}

	contents := "Hello world!"
	for k, v := range spec.Sources {
		t.Run(k, func(t *testing.T) {
			if v.Inline.File != nil {
				if v.Inline.File.Contents != contents {
					t.Fatalf("expected %s, got %s", contents, v.Inline.File.Contents)
				}

				expected := os.FileMode(0o644)
				if v.Inline.File.Permissions != expected {
					t.Fatalf("expected %O, got %O", expected, v.Inline.File.Permissions)
				}
			}

			if v.Inline.Dir != nil {
				expected := os.FileMode(0o755)
				if v.Inline.Dir.Permissions != expected {
					t.Fatalf("expected %O, got %O", expected, v.Inline.Dir.Permissions)
				}
			}
		})
	}
}

func TestSourceNameWithPathSeparator(t *testing.T) {
	spec := &Spec{
		Sources: map[string]Source{
			"forbidden/name": {
				Inline: &SourceInline{
					File: &SourceInlineFile{},
				},
			},
		},
	}

	err := spec.Validate()
	if err == nil {
		t.Fatal("expected error, but received none")
	}

	var expected *InvalidSourceError
	if !errors.As(err, &expected) {
		t.Fatalf("expected %T, got %T", expected, err)
	}

	if expected.Name != "forbidden/name" {
		t.Error("expected error to contain source name")
	}

	if !errors.Is(err, sourceNamePathSeparatorError) {
		t.Errorf("expected error to be sourceNamePathSeparatorError, got: %v", err)
	}
}

func TestUnmarshal(t *testing.T) {
	t.Run("x-fields are stripped from spec", func(t *testing.T) {
		dt := []byte(`
sources:
  test:
    inline:
      file:
        contents: "Hello world!"
x-some-field: "some value"
x-some-other-field: "some other value"
X-capitalized-other-field: "some other value capitalized X key"
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		src, ok := spec.Sources["test"]
		if !ok {
			t.Fatal("expected source to be present")
		}

		if src.Inline == nil {
			t.Fatal("expected inline source to be present")
		}

		if src.Inline.File == nil {
			t.Fatal("expected inline file to be present")
		}

		const xContents = "Hello world!"
		if src.Inline.File.Contents != xContents {
			t.Fatalf("expected %q, got %s", xContents, src.Inline.File.Contents)
		}
	})

	t.Run("unknown fields cause parse error", func(t *testing.T) {
		dt := []byte(`
sources:
  test:
    noSuchField: "some value"
`)

		_, err := LoadSpec(dt)
		if err == nil {
			t.Fatal("expected error, but received none")
		}
	})
}

func TestSpec_SubstituteBuildArgs(t *testing.T) {
	spec := &Spec{}
	assert.NilError(t, spec.SubstituteArgs(nil))

	env := map[string]string{}
	assert.NilError(t, spec.SubstituteArgs(env))

	// some values we'll be using throughout the test
	const (
		foo            = "foo"
		bar            = "bar"
		argWithDefault = "some default value"
		plainOleValue  = "some plain old value"
	)

	env["FOO"] = foo
	err := spec.SubstituteArgs(env)
	assert.ErrorIs(t, err, errUnknownArg, "args not defined in the spec should error out")

	// Now with the arg explicitly allowed as a passhtrough
	err = spec.SubstituteArgs(env, func(cfg *SubstituteConfig) {
		cfg.AllowArg = func(key string) bool {
			return key == "FOO"
		}
	})
	assert.NilError(t, err)

	spec.Args = map[string]string{}

	spec.Args["FOO"] = ""
	assert.NilError(t, spec.SubstituteArgs(env))

	pairs := map[string]string{
		"FOO":      "$FOO",
		"BAR":      "$BAR",
		"WHATEVER": "$VAR_WITH_DEFAULT",
		"REGULAR":  plainOleValue,
	}
	spec.PackageConfig = &PackageConfig{
		Signer: &PackageSigner{
			Args: maps.Clone(pairs),
		},
	}
	spec.Targets = map[string]Target{
		"t1": {}, // nil signer
		"t2": {
			PackageConfig: &PackageConfig{
				Signer: &PackageSigner{
					Args: maps.Clone(pairs),
				},
			},
		},
	}

	env["BAR"] = bar

	spec.Args["BAR"] = ""
	spec.Args["VAR_WITH_DEFAULT"] = argWithDefault

	assert.NilError(t, spec.SubstituteArgs(env))

	// Base package config
	assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["FOO"], foo))
	assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["BAR"], bar))
	assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["WHATEVER"], argWithDefault))
	assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["REGULAR"], plainOleValue))

	// targets
	assert.Check(t, cmp.Nil(spec.Targets["t1"].Frontend))
	assert.Check(t, cmp.Equal(spec.Targets["t2"].PackageConfig.Signer.Args["BAR"], bar))
	assert.Check(t, cmp.Equal(spec.Targets["t2"].PackageConfig.Signer.Args["WHATEVER"], argWithDefault))
	assert.Check(t, cmp.Equal(spec.Targets["t2"].PackageConfig.Signer.Args["REGULAR"], plainOleValue))

}

func TestCustomRepoFillDefaults(t *testing.T) {
	// In this case, the context source for the repo config and provided public key are not set,
	// so they should be set to the default context per source default-filling conventions.

	// Also, the env field should be set to all build stages, "build", "install", and "test", as it is
	// unspecified
	dt := []byte(`
dependencies: &deps
  extra_repos:
    - config:
        custom.repo:
          context: {}
      keys:
        public.gpg:
          context: {}
          path: "public.gpg"
targets:
  foo:
    dependencies: *deps
`)

	spec, err := LoadSpec(dt)
	if err != nil {
		t.Fatal(err)
	}

	err = spec.SubstituteArgs(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	extraRepo := spec.Dependencies.ExtraRepos[0]
	assert.Check(t, cmp.Equal(extraRepo.Config["custom.repo"].Context.Name,
		dockerui.DefaultLocalNameContext))

	assert.Check(t, cmp.Equal(extraRepo.Keys["public.gpg"].Context.Name,
		dockerui.DefaultLocalNameContext))

	assert.Check(t, cmp.DeepEqual(extraRepo.Envs, []string{"build", "install", "test"}))

	extraRepo = spec.Targets["foo"].Dependencies.ExtraRepos[0]
	assert.Check(t, cmp.Equal(extraRepo.Config["custom.repo"].Context.Name,
		dockerui.DefaultLocalNameContext))

	assert.Check(t, cmp.Equal(extraRepo.Keys["public.gpg"].Context.Name,
		dockerui.DefaultLocalNameContext))

	assert.Check(t, cmp.DeepEqual(extraRepo.Envs, []string{"build", "install", "test"}))

}

func TestBuildArgSubst(t *testing.T) {
	t.Run("value provided", func(t *testing.T) {
		dt := []byte(`
args:
  SOME_ARG:

version: 1.2.${SOME_ARG}
revision: ${SOME_ARG}ing

x-vars:
  img-src: &img-src
    path: /
    image:
      ref: whatever
      cmd:
        env:
          TEST: ${SOME_ARG}
  git-src: &git-src
    git:
      url: https://${SOME_ARG}
      commit: baddecaf${SOME_ARG}
  http-src: &http-src
    http:
      url: https://${SOME_ARG}
  context-src: &context-src
    context:
      name: ${SOME_ARG}
  build-src: &build-src
    build:
      dockerfile_path: /foo/bar/${SOME_ARG}
      source: *http-src

sources:
  img: *img-src
  git: *git-src
  http: *http-src
  context: *context-src
  build: *build-src

build:
  env:
    TEST_TOP: ${SOME_ARG}
  steps:
    - command: echo $TEST
      env:
        TEST: ${SOME_ARG}

tests: &tests
  - name: a test
    mounts:
      - dest: /a
        spec: *img-src
      - dest: /a
        spec: *git-src
      - dest: /a
        spec: *http-src
      - dest: /a
        spec: *context-src
      - dest: /a
        spec: *build-src
    files:
      foo: &check-output
        equals: ${SOME_ARG}
        contains:
          - ${SOME_ARG}
        starts_with: ${SOME_ARG}
        ends_with: ${SOME_ARG}
    steps:
      - command: hello
        stdout: *check-output
        stderr: *check-output
        stdin: ${SOME_ARG}

dependencies: &deps
  extra_repos:
    - keys:
        img: *img-src
        git: *git-src
        http: *http-src
        context: *context-src
        build: *build-src
      config:
        img: *img-src
        git: *git-src
        http: *http-src
        context: *context-src
        build: *build-src
      data:
        - dest: /a
          spec: *img-src
        - dest: /a
          spec: *git-src
        - dest: /a
          spec: *http-src
        - dest: /a
          spec: *context-src
        - dest: /a
          spec: *build-src

package_config: &pkg-config
  signer:
    args:
      FOO: ${SOME_ARG}

targets:
  foo:
    tests: *tests
    dependencies: *deps
    package_config: *pkg-config
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{
			"SOME_ARG": "test",
		})
		assert.NilError(t, err)

		assert.Check(t, cmp.Equal(spec.Version, "1.2.test"))
		assert.Check(t, cmp.Equal(spec.Revision, "testing"))
		assert.Check(t, cmp.Equal(spec.Sources["img"].DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(spec.Sources["git"].Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Sources["git"].Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(spec.Sources["http"].HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Sources["context"].Context.Name, "test"))
		assert.Check(t, cmp.Equal(spec.Sources["build"].Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(spec.Sources["build"].Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(spec.Build.Env["TEST_TOP"], "test"))
		assert.Check(t, cmp.Equal(spec.Build.Steps[0].Env["TEST"], "test"))

		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Keys["img"].DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Keys["git"].Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Keys["git"].Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Keys["http"].HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Keys["context"].Context.Name, "test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Keys["build"].Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Keys["build"].Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Config["img"].DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Config["git"].Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Config["git"].Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Config["http"].HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Config["context"].Context.Name, "test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Config["build"].Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Config["build"].Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Data[0].Spec.DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Data[1].Spec.Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Data[1].Spec.Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Data[2].Spec.HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Data[3].Spec.Context.Name, "test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Data[4].Spec.Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(spec.Dependencies.ExtraRepos[0].Data[4].Spec.Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(spec.Tests[0].Mounts[0].Spec.DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Mounts[1].Spec.Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Mounts[1].Spec.Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Mounts[2].Spec.HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Mounts[3].Spec.Context.Name, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Mounts[4].Spec.Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Mounts[4].Spec.Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(spec.Tests[0].Files["foo"].Equals, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Files["foo"].Contains[0], "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Files["foo"].StartsWith, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Files["foo"].EndsWith, "test"))

		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stdin, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stdout.Equals, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stdout.Contains[0], "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stdout.StartsWith, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stdout.EndsWith, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stderr.Equals, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stderr.Contains[0], "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stderr.StartsWith, "test"))
		assert.Check(t, cmp.Equal(spec.Tests[0].Steps[0].Stderr.EndsWith, "test"))

		assert.Check(t, cmp.Equal(spec.PackageConfig.Signer.Args["FOO"], "test"))

		// Now test the same things but for items defined under the targets section.
		target := spec.Targets["foo"]

		assert.Check(t, cmp.Equal(target.Tests[0].Mounts[0].Spec.DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Mounts[1].Spec.Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Mounts[1].Spec.Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(target.Tests[0].Mounts[2].Spec.HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Mounts[3].Spec.Context.Name, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Mounts[4].Spec.Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Mounts[4].Spec.Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(target.Tests[0].Files["foo"].Equals, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Files["foo"].Contains[0], "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Files["foo"].StartsWith, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Files["foo"].EndsWith, "test"))

		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stdin, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stdout.Equals, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stdout.Contains[0], "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stdout.StartsWith, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stdout.EndsWith, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stderr.Equals, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stderr.Contains[0], "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stderr.StartsWith, "test"))
		assert.Check(t, cmp.Equal(target.Tests[0].Steps[0].Stderr.EndsWith, "test"))

		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Keys["img"].DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Keys["git"].Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Keys["git"].Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Keys["http"].HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Keys["context"].Context.Name, "test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Keys["build"].Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Keys["build"].Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Config["img"].DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Config["git"].Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Config["git"].Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Config["http"].HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Config["context"].Context.Name, "test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Config["build"].Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Config["build"].Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Data[0].Spec.DockerImage.Cmd.Env["TEST"], "test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Data[1].Spec.Git.URL, "https://test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Data[1].Spec.Git.Commit, "baddecaftest"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Data[2].Spec.HTTP.URL, "https://test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Data[3].Spec.Context.Name, "test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Data[4].Spec.Build.DockerfilePath, "/foo/bar/test"))
		assert.Check(t, cmp.Equal(target.Dependencies.ExtraRepos[0].Data[4].Spec.Build.Source.HTTP.URL, "https://test"))

		assert.Check(t, cmp.Equal(target.PackageConfig.Signer.Args["FOO"], "test"))
	})

	t.Run("default value", func(t *testing.T) {
		dt := []byte(`
args:
  test: "test"

build:
  steps:
    - command: echo $TEST
      env:
        TEST: ${test}
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{})
		assert.NilError(t, err)

		assert.Check(t, cmp.Equal(spec.Build.Steps[0].Env["TEST"], "test"))
	})

	t.Run("build arg undeclared", func(t *testing.T) {
		dt := []byte(`
args:

build:
  steps:
    - command: echo $TEST
      env:
        TEST: ${test}
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{})
		assert.ErrorContains(t, err, `step index 0: env TEST=${test}: error performing variable expansion: build arg "test" not declared`)
	})

	t.Run("multiple undefined build args", func(t *testing.T) {
		dt := []byte(`
args:

sources:
  test1:
    git:
      url: phony.git
      commit: ${COMMIT1}
  test2:
    http:
      url: ${URL1}
build:
  steps:
    - command: echo ${COMMIT1}
      env:
        TEST: ${COMMIT1}
`)

		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{})

		// all occurrences of undefined build args should be reported
		assert.ErrorContains(t, err, `build arg "COMMIT1" not declared`)
		assert.ErrorContains(t, err, `build arg "URL1" not declared`)
		assert.ErrorContains(t, err, `build arg "COMMIT1" not declared`)
	})

	t.Run("builtin build arg", func(t *testing.T) {
		dt := []byte(`
args:

build:
  steps:
    - command: echo '$OS'
      env:
        OS: ${TARGETOS}
`)
		spec, err := LoadSpec(dt)
		if err != nil {
			t.Fatal(err)
		}

		err = spec.SubstituteArgs(map[string]string{})
		assert.ErrorContains(t, err,
			`opt-in arg "TARGETOS" not present in args`)
	})
}

func Test_validatePatch(t *testing.T) {
	type testCase struct {
		name     string
		patchSrc Source
		subpath  bool
	}

	// Create a test case for each source type.
	// For each type we need to specify if it should have a subpath or not.
	cases := []testCase{
		{
			name:     "ineline file",
			patchSrc: Source{Inline: &SourceInline{File: &SourceInlineFile{}}},
			subpath:  false,
		},
		{
			name:     "inline dir",
			patchSrc: Source{Inline: &SourceInline{Dir: &SourceInlineDir{}}},
			subpath:  true,
		},
		{
			name:     "git",
			patchSrc: Source{Git: &SourceGit{}},
			subpath:  true,
		},
		{
			name:     "image",
			patchSrc: Source{DockerImage: &SourceDockerImage{}},
			subpath:  true,
		},
		{
			name:     "HTTP",
			patchSrc: Source{HTTP: &SourceHTTP{}},
			subpath:  false,
		},
		{
			name:     "context",
			patchSrc: Source{Context: &SourceContext{}},
			subpath:  true,
		},
		{
			name:     "build",
			patchSrc: Source{Build: &SourceBuild{}},
			subpath:  true,
		},
	}

	// For each case generate 2 tests: 1 with a subpath and 1 without
	// Use the subpath field in the test case to determine if the validation
	// should return an error.
	//
	// If subpath is false in the testcase but the test is passing in a subpath then
	// an error is expected.
	// Likewise when subpath is true but no subpath is given.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("subpath=true", func(t *testing.T) {
				ps := PatchSpec{Path: "/test"}
				err := validatePatch(ps, tc.patchSrc)
				if tc.subpath {
					assert.NilError(t, err)
					return
				}
				assert.ErrorIs(t, err, errPatchFileNoSubpath)
			})
			t.Run("subpath=false", func(t *testing.T) {
				ps := PatchSpec{}
				err := validatePatch(ps, tc.patchSrc)
				if tc.subpath {
					assert.ErrorIs(t, err, errPatchRequiresSubpath)
					return
				}
				assert.NilError(t, err)
			})
		})
	}
}
