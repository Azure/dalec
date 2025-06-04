package test

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/frontend/pkg/bkfs"
	"github.com/Azure/dalec/test/testenv"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestGomodGitAuthHTTPS(t *testing.T) {
	t.Parallel()

	const host = "host.docker.internal"
	const addr = "127.0.0.1"

	ctx := startTestSpan(baseCtx, t)
	sourceName := "gitauth"

	tag := identity.NewID()
	netHostTestEnv := testenv.NewWithBuildxInstance(ctx, t)

	netHostTestEnv.RunTest(ctx, t, func(ctx context.Context, c gwclient.Client) {
		const gomodFmt = `module %[1]s/user/public

go 1.23.5

require %[1]s/user/private.git %[2]s
`

		gomodContents := fmt.Sprintf(gomodFmt, host, tag)
		port := getAvailablePort(t)

		spec := &dalec.Spec{
			Name: "gomod-git-auth",
			Sources: map[string]dalec.Source{
				sourceName: {
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"go.mod": {
									Contents: gomodContents,
								},
							},
						},
					},
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{
								Auth: map[string]dalec.GomodGitAuth{
									fmt.Sprintf("%s:%s", host, port): {
										Token: "super-secret",
									},
								},
							},
						},
					},
				},
			},
		}

		if err := runGitServer(ctx, t, c, addr, port, host, tag); err != nil {
			t.Fatal(err)
		}

		sr := newSolveRequest(
			withBuildTarget("debug/gomods"),
			withSpec(ctx, t, spec),
			withExtraHost(host, addr),
			withBuildContext(ctx, t, "gomod-worker", initGomodWorker(c, host, port)),
		)

		res := solveT(ctx, t, c, sr)
		modDir := getModuleDirName(ctx, t, res)
		filename := filepath.Join("host.docker.internal/user", modDir, "hello")

		checkFile(ctx, t, filename, res, []byte("hello\n"))
	}, testenv.WithSecrets(testenv.KeyVal{
		K: "super-secret",
		V: "value",
	}), testenv.WithHostNetworking)
}

func getModuleDirName(ctx context.Context, t *testing.T, res *gwclient.Result) string {
	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	stats, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{
		Path:           "host.docker.internal/user",
		IncludePattern: "private.git@*",
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(stats) == 0 {
		t.Fatalf("private go module directory not found")
	}

	return stats[0].Path
}

func initGomodWorker(c gwclient.Client, host, port string) llb.State {
	worker := llb.Image("alpine:latest", llb.Platform(ocispecs.Platform{Architecture: runtime.GOARCH, OS: "linux"}), llb.WithMetaResolver(c)).
		Run(llb.Shlex("apk add --no-cache go git ca-certificates patch openssh netcat-openbsd")).Root()

	run := func(cmd string) {
		// tell git to use the port along with the host
		worker = worker.Run(
			dalec.ShArgs(cmd),
			llb.AddEnv("HOST", host),
			llb.AddEnv("PORT", port),
		).Root()
	}

	run(`sh -c 'git config --global "url.http://${HOST}:${PORT}.insteadOf" "https://${HOST}"'`)
	run(`sh -c 'git config --global credential."http://${HOST}:${PORT}.helper" "/usr/local/bin/frontend credential-helper --kind=token"'`)

	return worker
}

func nullCharSplit(data []byte, atEOF bool) (int, []byte, error) {
	var (
		i   int
		err error
	)

	for i = 0; i < len(data); i++ {
		if data[i] == 0 {
			break
		}
	}

	if atEOF {
		err = bufio.ErrFinalToken
	}

	return i + 1, data[:i], err
}

func runGitServer(ctx context.Context, t *testing.T, client gwclient.Client, addr, port, host, tag string) error {
	const serverRoot = "/git_server"
	const repoDir = "/user/private"
	const repoMountpoint = serverRoot + repoDir

	var modFile bytes.Buffer
	fmt.Fprintf(&modFile, `module %s/user/private.git`, host)
	modFile.WriteRune('\n')
	modFile.WriteString("\n\ngo 1.23.5\n")

	repo := llb.Scratch().
		File(
			llb.Mkdir(repoDir, 0o755, llb.WithParents(true)).
				Mkfile(repoDir+"/hello", 0o644, []byte("hello\n")).
				Mkfile(repoDir+"/go.mod", 0o644, modFile.Bytes()),
		)

	worker := initGomodWorker(client, host, port)
	worker = worker.File(llb.Copy(repo, "/", serverRoot))
	worker = worker.Dir(repoMountpoint).Run(dalec.ShArgsf(`
set -ex
export GIT_CONFIG_NOGLOBAL=true
git init
git config user.name foo
git config user.email foo@bar.com

git add -A
git commit -m commit --no-gpg-sign
git tag %s
    `, tag)).Root()

	dc, err := dockerui.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}

	gitServerProgramPtr, err := dc.MainContext(ctx)
	if err != nil {
		t.Fatal(err)
	}

	gitServerProgramSt := *gitServerProgramPtr
	gitServerBinSt := worker.Run(
		dalec.ShArgs("cd /tmp/dalec/internal/dalec && go build -o /tmp/out/host ./test/cmd/git_repo"),
		llb.AddMount("/tmp/dalec/internal/dalec", gitServerProgramSt),
	).AddMount("/tmp/out", llb.Scratch())

	workerRef := stateToRef(ctx, t, client, worker)
	gitServerProgramRef := stateToRef(ctx, t, client, gitServerBinSt)

	cont, err := client.NewContainer(ctx, gwclient.NewContainerRequest{
		Mounts: []gwclient.Mount{
			{
				Dest:     "/",
				Ref:      workerRef,
				Readonly: true,
			},
			{
				Dest:     "/git_repo",
				Ref:      gitServerProgramRef,
				Readonly: true,
			},
		},
		NetMode: pb.NetMode_HOST,
		ExtraHosts: []*pb.HostIP{
			{
				Host: host,
				IP:   addr,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	env, err := worker.Env(ctx)
	if err != nil {
		t.Logf("unable to copy env: %s", err)
	}

	envArr := env.ToArray()
	envArr = append(envArr, "HOST="+host, "ADDR="+addr, "PORT="+port)

	cp, err := cont.Start(ctx, gwclient.StartRequest{
		Args:      []string{"/git_repo/host", serverRoot, addr, port},
		Env:       envArr,
		SecretEnv: []*pb.SecretEnv{},
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		if err := cp.Wait(); err != nil {
			t.Logf("unexpected server error: %s", err)
		}
	}()

	t.Log("waiting for git server to come online")
	ctxT, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()

	// netcat's -z will return 0 if a connection can be made, 1 if not
	// -w5 means timeout after 5 seconds
	untilConnected, err := cont.Start(ctxT, gwclient.StartRequest{
		Env: envArr,
		Args: []string{
			"sh", "-c", `
while ! nc -zw5 "$ADDR" "$PORT"; do
	sleep 0.1
done
			`,
		},
	})
	if err != nil {
		t.Fatalf("could not check progress of git server: %s", err)
	}

	if err := untilConnected.Wait(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Could not start git server: %s", err)
		}

		t.Fatalf("could not check progress of git server: %s", err)
	}

	t.Logf("git server is online")

	return nil
}

func stateToRef(ctx context.Context, t *testing.T, client gwclient.Client, st llb.State) gwclient.Reference {
	def, err := st.Marshal(ctx)
	if err != nil {
		t.Fatalf("could not marshal git repo llb: %s", err)
	}

	res, err := client.Solve(ctx, gwclient.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		t.Fatalf("could not solve git repo llb %s", err)
	}

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatalf("could not convert result to single ref %s", err)
	}
	return ref
}

func getAvailablePort(t *testing.T) string {
	addr, err := net.ResolveTCPAddr("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}

	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func(t *testing.T) {
		_ = l.Close() // if we got the port, ignore failure to close
	}(t)

	tcpa, ok := l.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("extpeccted return value of l.Addr() to be a (*net.TCPAddr)")
	}

	p := tcpa.Port
	return strconv.Itoa(p)
}

func withExtraHost(host string, ipv4 string) func(cfg *newSolveRequestConfig) {
	return func(cfg *newSolveRequestConfig) {
		const addHostsKey = "add-hosts"
		r := cfg.req

		if r.FrontendOpt == nil {
			r.FrontendOpt = make(map[string]string)
		}

		var prefix string
		if existing, ok := r.FrontendOpt[addHostsKey]; ok {
			prefix = existing + ","
		}

		r.FrontendOpt[addHostsKey] = fmt.Sprintf("%s%s=%s", prefix, host, ipv4)
	}
}

func getExtraHostRootful(t *testing.T) string {
	return "127.0.0.1"
	ifaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			t.Log(err)
			continue
		}

		for _, addr := range addrs {
			if ip := addr.(*net.IPNet).IP.To4(); ip != nil {
				if ip.IsLoopback() {
					continue
				}

				if ip.IsPrivate() {
					t.Log("IP ADDRESS", ip)
					return ip.String()
				}
			}
		}
	}

	t.FailNow()
	return "host-gateway"
}

func TestSourceCmd(t *testing.T) {
	t.Parallel()

	ctx := startTestSpan(baseCtx, t)

	sourceName := "checkcmd"
	testSpec := func() *dalec.Spec {
		return &dalec.Spec{
			Args: map[string]string{
				"BAR": "bar",
			},
			Name: "cmd-source-ref",
			Sources: map[string]dalec.Source{
				sourceName: {
					Path: "/output",
					DockerImage: &dalec.SourceDockerImage{
						Ref: "busybox:latest",
						Cmd: &dalec.Command{
							Steps: []*dalec.BuildStep{
								{
									Command: `mkdir -p /output; echo "$FOO $BAR" > /output/foo`,
									Env: map[string]string{
										"FOO": "foo",
										"BAR": "$BAR", // make sure args are passed through
									},
								},
								// make sure state is preserved for multiple steps
								{
									Command: `echo "hello" > /output/hello`,
								},
								{
									Command: `cat /output/foo | grep "foo bar"`,
								},

								// Make sure changes to the rootfs (as opposed to the output dir)
								// persist across steps.
								{
									Command: `echo "hello world" > /tmp/hello`,
								},
								{
									Command: `grep "hello world" /tmp/hello`,
								},
							},
						},
					},
				},
			},
		}
	}

	t.Run("base", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)

		testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
			spec := testSpec()
			req := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, spec))
			res := solveT(ctx, t, gwc, req)

			checkFile(ctx, t, filepath.Join(sourceName, "foo"), res, []byte("foo bar\n"))
			checkFile(ctx, t, filepath.Join(sourceName, "hello"), res, []byte("hello\n"))
		})
	})

	t.Run("with mounted file", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		t.Run("at root", func(t *testing.T) {
			t.Parallel()
			ctx := startTestSpan(ctx, t)
			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				spec := testSpec()
				spec.Sources[sourceName].DockerImage.Cmd.Steps = []*dalec.BuildStep{
					{
						Command: `grep 'foo bar' /foo`,
					},
					{
						Command: `mkdir -p /output; cp /foo /output/foo`,
					},
				}
				spec.Sources[sourceName].DockerImage.Cmd.Mounts = []dalec.SourceMount{
					{
						Dest: "/foo",
						Spec: dalec.Source{
							Inline: &dalec.SourceInline{
								File: &dalec.SourceInlineFile{
									Contents: "foo bar",
								},
							},
						},
					},
				}

				req := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, spec))
				res := solveT(ctx, t, gwc, req)

				checkFile(ctx, t, filepath.Join(sourceName, "foo"), res, []byte("foo bar"))
			})
		})
		t.Run("nested", func(t *testing.T) {
			t.Parallel()
			ctx := startTestSpan(ctx, t)
			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				spec := testSpec()
				spec.Sources[sourceName].DockerImage.Cmd.Steps = []*dalec.BuildStep{
					{
						Command: `grep 'foo bar' /tmp/foo`,
					},
					{
						Command: `mkdir -p /output; cp /tmp/foo /output/foo`,
					},
				}
				spec.Sources[sourceName].DockerImage.Cmd.Mounts = []dalec.SourceMount{
					{
						Dest: "/tmp/foo",
						Spec: dalec.Source{
							Inline: &dalec.SourceInline{
								File: &dalec.SourceInlineFile{
									Contents: "foo bar",
								},
							},
						},
					},
				}

				req := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, spec))
				res := solveT(ctx, t, gwc, req)

				checkFile(ctx, t, filepath.Join(sourceName, "foo"), res, []byte("foo bar"))
			})
		})
	})

	t.Run("with mounted dir", func(t *testing.T) {
		t.Parallel()
		ctx := startTestSpan(ctx, t)
		t.Run("at root", func(t *testing.T) {
			t.Parallel()
			ctx := startTestSpan(ctx, t)
			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				spec := testSpec()
				spec.Sources[sourceName].DockerImage.Cmd.Steps = []*dalec.BuildStep{
					{
						Command: `grep 'foo bar' /foo/bar`,
					},
					{
						Command: `mkdir -p /output; cp -r /foo /output/foo`,
					},
				}
				spec.Sources[sourceName].DockerImage.Cmd.Mounts = []dalec.SourceMount{
					{
						Dest: "/foo",
						Spec: dalec.Source{
							Inline: &dalec.SourceInline{
								Dir: &dalec.SourceInlineDir{
									Files: map[string]*dalec.SourceInlineFile{
										"bar": {Contents: "foo bar"},
									},
								},
							},
						},
					},
				}

				req := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, spec))
				res := solveT(ctx, t, gwc, req)

				checkFile(ctx, t, filepath.Join(sourceName, "foo/bar"), res, []byte("foo bar"))
			})
		})
		t.Run("nested", func(t *testing.T) {
			t.Parallel()
			ctx := startTestSpan(ctx, t)
			testEnv.RunTest(ctx, t, func(ctx context.Context, gwc gwclient.Client) {
				spec := testSpec()
				spec.Sources[sourceName].DockerImage.Cmd.Steps = []*dalec.BuildStep{
					{
						Command: `grep 'foo bar' /tmp/foo/bar`,
					},
					{
						Command: `mkdir -p /output; cp -r /tmp/foo /output/foo`,
					},
				}
				spec.Sources[sourceName].DockerImage.Cmd.Mounts = []dalec.SourceMount{
					{
						Dest: "/tmp/foo",
						Spec: dalec.Source{
							Inline: &dalec.SourceInline{
								Dir: &dalec.SourceInlineDir{
									Files: map[string]*dalec.SourceInlineFile{
										"bar": {Contents: "foo bar"},
									},
								},
							},
						},
					},
				}

				req := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, spec))
				res := solveT(ctx, t, gwc, req)

				checkFile(ctx, t, filepath.Join(sourceName, "foo/bar"), res, []byte("foo bar"))
			})
		})

	})
}

func TestSourceBuild(t *testing.T) {
	t.Parallel()

	doBuildTest := func(t *testing.T, subTest string, spec *dalec.Spec) {
		t.Run(subTest, func(t *testing.T) {
			t.Parallel()

			testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
				ro := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, spec))

				res := solveT(ctx, t, gwc, ro)
				checkFile(ctx, t, "test/hello", res, []byte("hello\n"))
			})
		})
	}

	const dockerfile = "FROM busybox\nRUN echo hello > /hello"

	newBuildSpec := func(p string, f func() dalec.Source) *dalec.Spec {
		return &dalec.Spec{
			Sources: map[string]dalec.Source{
				"test": {
					Path: "/hello",
					Build: &dalec.SourceBuild{
						DockerfilePath: p,
						Source:         f(),
					},
				},
			},
		}
	}

	t.Run("inline", func(t *testing.T) {
		t.Parallel()
		fileSrc := func() dalec.Source {
			return dalec.Source{
				Inline: &dalec.SourceInline{
					File: &dalec.SourceInlineFile{
						Contents: dockerfile,
					},
				},
			}
		}
		dirSrc := func(p string) func() dalec.Source {
			return func() dalec.Source {
				return dalec.Source{
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								p: {
									Contents: dockerfile,
								},
							},
						},
					},
				}
			}
		}

		t.Run("unspecified build file path", func(t *testing.T) {
			t.Parallel()
			doBuildTest(t, "file", newBuildSpec("", fileSrc))
			doBuildTest(t, "dir", newBuildSpec("", dirSrc("Dockerfile")))
		})

		t.Run("Dockerfile as build file path", func(t *testing.T) {
			t.Parallel()
			doBuildTest(t, "file", newBuildSpec("Dockerfile", fileSrc))
			doBuildTest(t, "dir", newBuildSpec("Dockerfile", dirSrc("Dockerfile")))
		})

		t.Run("non-standard build file path", func(t *testing.T) {
			t.Parallel()
			doBuildTest(t, "file", newBuildSpec("foo", fileSrc))
			doBuildTest(t, "dir", newBuildSpec("foo", dirSrc("foo")))
		})
	})
}

func TestSourceHTTP(t *testing.T) {
	t.Parallel()

	url := "https://raw.githubusercontent.com/Azure/dalec/0ae22acf69ab6ef0a0503affed1a8952c9dd1384/README.md"
	const badDigest = digest.Digest("sha256:000084c7170b4cfbad0690412259b5e252f84c0ccff79aaca023beb3f3ed0000")
	const goodDigest = digest.Digest("sha256:b0fa84c7170b4cfbad0690412259b5e252f84c0ccff79aaca023beb3f3ed6380")

	newSpec := func(url string, digest digest.Digest) *dalec.Spec {
		return &dalec.Spec{
			Sources: map[string]dalec.Source{
				"test": {
					HTTP: &dalec.SourceHTTP{
						URL:    url,
						Digest: digest,
					},
				},
			},
		}
	}

	testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
		bad := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, newSpec(url, badDigest)))
		bad.Evaluate = true
		_, err := gwc.Solve(ctx, bad)
		if err == nil {
			t.Fatal("expected digest mismatch, but received none")
		}

		if !strings.Contains(err.Error(), "digest mismatch") {
			t.Fatalf("expected digest mismatch, got: %v", err)
		}

		good := newSolveRequest(withBuildTarget("debug/sources"), withSpec(ctx, t, newSpec(url, goodDigest)))
		good.Evaluate = true
		solveT(ctx, t, gwc, good)
	})
}

// Create a very simple fake module with a limited dependency tree just to
// keep the test as fast/reliable as possible.
const gomodFixtureMain = `package main

import (
	"fmt"

	"github.com/cpuguy83/tar2go"
)

func main() {
	var i *tar2go.Index
	fmt.Println("Print something to use the i var", i)
}
`

const gomodFixtureMod = `module testgomodsource

go 1.20

require github.com/cpuguy83/tar2go v0.3.1
`

const gomodFixtureSum = `
github.com/cpuguy83/tar2go v0.3.1 h1:DMWlaIyoh9FBWR4hyfZSOEDA7z8rmCiGF1IJIzlTlR8=
github.com/cpuguy83/tar2go v0.3.1/go.mod h1:2Ys2/Hu+iPHQRa4DjIVJ7UAaKnDhAhNACeK3A0Rr5rM=
`

const alternativeGomodFixtureMain = `package main

import (
	"fmt"

	"github.com/stretchr/testify/assert"
)

func main() {
	msg := "This is a dummy test from module2"
	fmt.Println(msg)
	assert.True(nil, true, msg)
}
`

const alternativeGomodFixtureMod = `module example.com/module2

go 1.20

require github.com/stretchr/testify v1.7.0

require (
	github.com/davecgh/go-spew v1.1.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	gopkg.in/yaml.v3 v3.0.0-20200313102051-9f266ea9e77c // indirect
)
`

const alternativeGomodFixtureSum = `
github.com/davecgh/go-spew v1.1.0 h1:ZDRjVQ15GmhC3fiQ8ni8+OwkZQO4DARzQgrnXU1Liz8=
github.com/davecgh/go-spew v1.1.0/go.mod h1:J7Y8YcW2NihsgmVo/mv3lAwl/skON4iLHjSsI+c5H38=
github.com/pmezard/go-difflib v1.0.0 h1:4DBwDE0NGyQoBHbLQYPwSUPoCMWR5BEzIk/f1lZbAQM=
github.com/pmezard/go-difflib v1.0.0/go.mod h1:iKH77koFhYxTK1pcRnkKkqfTogsbg7gZNVY4sRDYZ/4=
github.com/stretchr/objx v0.1.0/go.mod h1:HFkY916IF+rwdDfMAkV7OtwuqBVzrE8GR6GFx+wExME=
github.com/stretchr/testify v1.7.0 h1:nwc3DEeHmmLAfoZucVR881uASk0Mfjw8xYJ99tb5CcY=
github.com/stretchr/testify v1.7.0/go.mod h1:6Fq8oRcR53rry900zMqJjRRixrwX3KX962/h/Wwjteg=
gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405 h1:yhCVgyC4o1eVCa2tZl7eS0r+SDo693bJlVdllGtEeKM=
gopkg.in/check.v1 v0.0.0-20161208181325-20d25e280405/go.mod h1:Co6ibVJAznAaIkqp8huTwlJQCZ016jof/cbN4VW5Yz0=
gopkg.in/yaml.v3 v3.0.0-20200313102051-9f266ea9e77c h1:dUUwHk2QECo/6vqA44rthZ8ie2QXMNeKRTHCNY2nXvo=
gopkg.in/yaml.v3 v3.0.0-20200313102051-9f266ea9e77c/go.mod h1:K4uyk7z7BCEPqu6E+C64Yfv1cQ7kz7rIZviUmN+EgEM=
`

func TestSourceWithGomod(t *testing.T) {
	t.Parallel()

	const downgradePatch = `diff --git a/go.mod b/go.mod
index 0c18614..8a3a0ee 100644
--- a/go.mod
+++ b/go.mod
@@ -2,4 +2,4 @@ module testgomodsource

 go 1.20

-require github.com/cpuguy83/tar2go v0.3.1
+require github.com/cpuguy83/tar2go v0.3.0
diff --git a/go.sum b/go.sum
index ea874f5..ba38f84 100644
--- a/go.sum
+++ b/go.sum
@@ -1,2 +1,2 @@
-github.com/cpuguy83/tar2go v0.3.1 h1:DMWlaIyoh9FBWR4hyfZSOEDA7z8rmCiGF1IJIzlTlR8=
-github.com/cpuguy83/tar2go v0.3.1/go.mod h1:2Ys2/Hu+iPHQRa4DjIVJ7UAaKnDhAhNACeK3A0Rr5rM=
+github.com/cpuguy83/tar2go v0.3.0 h1:SDNIJgmRrx5+6SnhjfxqeYfWhwo3/HlF0Cphqw2rewY=
+github.com/cpuguy83/tar2go v0.3.0/go.mod h1:2Ys2/Hu+iPHQRa4DjIVJ7UAaKnDhAhNACeK3A0Rr5rM=
`

	// Note: module here should be moduyle+version because this is checking the go module path on disk
	checkModule := func(ctx context.Context, gwc gwclient.Client, module string, spec *dalec.Spec) {
		t.Helper()
		res, err := gwc.Solve(ctx, newSolveRequest(withBuildTarget("debug/gomods"), withSpec(ctx, t, spec)))
		if err != nil {
			t.Fatal(err)
		}

		ref, err := res.SingleRef()
		if err != nil {
			t.Fatal(err)
		}

		stat, err := ref.StatFile(ctx, gwclient.StatRequest{
			Path: module,
		})
		if err != nil {
			t.Fatal(err)
		}

		if !fs.FileMode(stat.Mode).IsDir() {
			t.Fatal("expected directory")
		}
	}

	const srcName = "src1"

	baseSpec := func() *dalec.Spec {
		return &dalec.Spec{
			Sources: map[string]dalec.Source{
				srcName: {
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
		}
	}

	t.Run("no patch", func(t *testing.T) {
		t.Parallel()
		testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
			checkModule(ctx, gwc, "github.com/cpuguy83/tar2go@v0.3.1", baseSpec())
		})
	})

	t.Run("with patch", func(t *testing.T) {
		t.Parallel()
		t.Run("file", func(t *testing.T) {
			t.Parallel()
			testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
				spec := baseSpec()

				patchName := "patch"
				spec.Sources[patchName] = dalec.Source{
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: downgradePatch,
						},
					},
				}

				spec.Patches = map[string][]dalec.PatchSpec{
					srcName: {{Source: patchName}},
				}

				checkModule(ctx, gwc, "github.com/cpuguy83/tar2go@v0.3.0", spec)
			})
		})
		t.Run("dir", func(t *testing.T) {
			t.Parallel()
			testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
				spec := baseSpec()

				patchName := "patch"
				spec.Sources[patchName] = dalec.Source{
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"patch-file": {Contents: downgradePatch},
							},
						},
					},
				}

				spec.Patches = map[string][]dalec.PatchSpec{
					srcName: {{Source: patchName, Path: "patch-file"}},
				}

				checkModule(ctx, gwc, "github.com/cpuguy83/tar2go@v0.3.0", spec)
			})
		})
	})

	t.Run("multi-module", func(t *testing.T) {
		t.Parallel()
		/*
			dir/
				module1/
					go.mod
					go.sum
					main.go
				module2/
					go.mod
					go.sum
					main.go
		*/
		contextSt := llb.Scratch().File(llb.Mkdir("/dir", 0644)).
			File(llb.Mkdir("/dir/module1", 0644)).
			File(llb.Mkfile("/dir/module1/go.mod", 0644, []byte(alternativeGomodFixtureMod))).
			File(llb.Mkfile("/dir/module1/go.sum", 0644, []byte(alternativeGomodFixtureSum))).
			File(llb.Mkfile("/dir/module1/main.go", 0644, []byte(alternativeGomodFixtureMain))).
			File(llb.Mkdir("/dir/module2", 0644)).
			File(llb.Mkfile("/dir/module2/go.mod", 0644, []byte(gomodFixtureMod))).
			File(llb.Mkfile("/dir/module2/go.sum", 0644, []byte(gomodFixtureSum))).
			File(llb.Mkfile("/dir/module2/main.go", 0644, []byte(gomodFixtureMain)))

		const contextName = "multi-module"
		spec := &dalec.Spec{
			Name: "test-dalec-context-source",
			Sources: map[string]dalec.Source{
				"src": {
					Context: &dalec.SourceContext{Name: contextName},
					Generate: []*dalec.SourceGenerator{
						{
							Gomod: &dalec.GeneratorGomod{
								Paths: []string{"./dir/module1", "./dir/module2"},
							},
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					"golang": {
						Version: []string{},
					},
				},
			},
		}

		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildContext(ctx, t, contextName, contextSt), withBuildTarget("debug/gomods"))
			res := solveT(ctx, t, gwc, req)
			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}
			deps := []string{"github.com/cpuguy83/tar2go@v0.3.1", "github.com/stretchr/testify@v1.7.0"}
			for _, dep := range deps {
				stat, err := ref.StatFile(ctx, gwclient.StatRequest{
					Path: dep,
				})

				if err != nil {
					t.Fatal(err)
				}

				if !fs.FileMode(stat.Mode).IsDir() {
					t.Fatal("expected directory")
				}
			}
		})
	})
}

var (
	// Other existing fixtures...

	cargoFixtureToml = `
[package]
name = "cargo-test"
version = "0.1.0"
edition = "2021"

[dependencies]
once_cell = "1.18.0"  # Small crate with no dependencies

[lib]
path = "main.rs"
`

	cargoFixtureLock = `# This file is automatically @generated by Cargo.
# It is not intended for manual editing.
version = 3

[[package]]
name = "cargo-test"
version = "0.1.0"
dependencies = [
 "once_cell",
]

[[package]]
name = "once_cell"
version = "1.18.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
checksum = "dd8b5dd2ae5ed71462c540258bedcb51965123ad7e7ccf4b9a8cafaa4a63576d"
`

	cargoFixtureMain = `
fn main() {
    use once_cell::sync::Lazy;
    
    static GREETING: Lazy<String> = Lazy::new(|| "Hello from Rust with Cargo!".to_string());
    println!("{}", *GREETING);
}
`
)

func TestSourceWithCargohome(t *testing.T) {
	t.Parallel()

	const downgradePatch = `diff --git a/Cargo.toml b/Cargo.toml
--- a/Cargo.toml
+++ b/Cargo.toml
@@ -7,1 +7,1 @@
-once_cell = "1.18.0"  # Small crate with no dependencies
+once_cell = "1.17.0"  # Small crate with no dependencies
`
	// Helper function to check if a specific Cargo registry directory exists
	checkCargoRegistry := func(ctx context.Context, gwc gwclient.Client, registryPath string, spec *dalec.Spec) {
		t.Helper()
		res, err := gwc.Solve(ctx, newSolveRequest(withBuildTarget("debug/cargohome"), withSpec(ctx, t, spec)))
		if err != nil {
			t.Fatal(err)
		}

		ref, err := res.SingleRef()
		if err != nil {
			t.Fatal(err)
		}

		stat, err := ref.StatFile(ctx, gwclient.StatRequest{
			Path: filepath.Join("registry", registryPath),
		})
		if err != nil {
			t.Fatal(err)
		}

		if !fs.FileMode(stat.Mode).IsDir() {
			t.Fatal("expected directory")
		}
	}

	const srcName = "src1"

	baseSpec := func() *dalec.Spec {
		return &dalec.Spec{
			Sources: map[string]dalec.Source{
				srcName: {
					Generate: []*dalec.SourceGenerator{
						{
							Cargohome: &dalec.GeneratorCargohome{},
						},
					},
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"main.rs":    {Contents: cargoFixtureMain},
								"Cargo.toml": {Contents: cargoFixtureToml},
								"Cargo.lock": {Contents: cargoFixtureLock},
							},
						},
					},
				},
			},
		}
	}

	t.Run("no patch", func(t *testing.T) {
		t.Parallel()
		testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
			checkCargoRegistry(ctx, gwc, "index", baseSpec())
		})
	})

	t.Run("with patch", func(t *testing.T) {
		t.Parallel()
		t.Run("file", func(t *testing.T) {
			t.Parallel()
			testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
				spec := baseSpec()

				patchName := "patch"
				spec.Sources[patchName] = dalec.Source{
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: downgradePatch,
						},
					},
				}

				spec.Patches = map[string][]dalec.PatchSpec{
					srcName: {{Source: patchName}},
				}

				checkCargoRegistry(ctx, gwc, "index", spec)
			})
		})
		t.Run("dir", func(t *testing.T) {
			t.Parallel()
			testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
				spec := baseSpec()

				patchName := "patch"
				spec.Sources[patchName] = dalec.Source{
					Inline: &dalec.SourceInline{
						Dir: &dalec.SourceInlineDir{
							Files: map[string]*dalec.SourceInlineFile{
								"patch-file": {Contents: downgradePatch},
							},
						},
					},
				}

				spec.Patches = map[string][]dalec.PatchSpec{
					srcName: {{Source: patchName, Path: "patch-file"}},
				}

				checkCargoRegistry(ctx, gwc, "index", spec)
			})
		})
	})

	t.Run("multi-module", func(t *testing.T) {
		t.Parallel()

		// Create a context with multiple cargo modules
		contextSt := llb.Scratch().File(llb.Mkdir("/dir", 0644)).
			File(llb.Mkdir("/dir/module1", 0644)).
			File(llb.Mkfile("/dir/module1/Cargo.toml", 0644, []byte(cargoFixtureToml))).
			File(llb.Mkfile("/dir/module1/Cargo.lock", 0644, []byte(cargoFixtureLock))).
			File(llb.Mkfile("/dir/module1/main.rs", 0644, []byte(cargoFixtureMain))).
			File(llb.Mkdir("/dir/module2", 0644)).
			File(llb.Mkfile("/dir/module2/Cargo.toml", 0644, []byte(cargoFixtureToml))).
			File(llb.Mkfile("/dir/module2/Cargo.lock", 0644, []byte(cargoFixtureLock))).
			File(llb.Mkfile("/dir/module2/main.rs", 0644, []byte(cargoFixtureMain)))

		const contextName = "multi-cargo-module"
		spec := &dalec.Spec{
			Name: "test-dalec-cargo-context-source",
			Sources: map[string]dalec.Source{
				"src": {
					Context: &dalec.SourceContext{Name: contextName},
					Generate: []*dalec.SourceGenerator{
						{
							Cargohome: &dalec.GeneratorCargohome{
								Paths: []string{"./dir/module1", "./dir/module2"},
							},
						},
					},
				},
			},
			Dependencies: &dalec.PackageDependencies{
				Build: map[string]dalec.PackageConstraints{
					"rust": {
						Version: []string{},
					},
				},
			},
		}

		runTest(t, func(ctx context.Context, gwc gwclient.Client) {
			req := newSolveRequest(withSpec(ctx, t, spec), withBuildContext(ctx, t, contextName, contextSt), withBuildTarget("debug/cargohome"))
			res := solveT(ctx, t, gwc, req)
			ref, err := res.SingleRef()
			if err != nil {
				t.Fatal(err)
			}

			// Check that the registry directory exists
			stat, err := ref.StatFile(ctx, gwclient.StatRequest{
				Path: "registry/index",
			})
			if err != nil {
				t.Fatal(err)
			}
			if !fs.FileMode(stat.Mode).IsDir() {
				t.Fatal("expected directory")
			}
		})
	})
}

func TestSourceContext(t *testing.T) {
	t.Parallel()

	contextSt := llb.Scratch().
		File(llb.Mkfile("/base", 0o644, nil)).
		File(llb.Mkdir("/foo/bar", 0o755, llb.WithParents(true))).
		File(llb.Mkfile("/foo/file", 0o644, nil)).
		File(llb.Mkfile("/foo/bar/another", 0o644, nil))

	spec := &dalec.Spec{
		Name: "test-dalec-context-source",
		Sources: map[string]dalec.Source{
			"basic":         {Context: &dalec.SourceContext{}},
			"with-path":     {Path: "/foo/bar", Context: &dalec.SourceContext{}},
			"with-includes": {Includes: []string{"foo/**/*"}, Context: &dalec.SourceContext{}},
			"with-excludes": {Excludes: []string{"foo/**/*"}, Context: &dalec.SourceContext{}},
			"with-path-and-includes-excludes": {
				Path:     "/foo",
				Includes: []string{"file", "bar"},
				Excludes: []string{"bar/another"},
				Context:  &dalec.SourceContext{},
			},
		},
	}

	runTest(t, func(ctx context.Context, gwc gwclient.Client) {
		req := newSolveRequest(withSpec(ctx, t, spec), withBuildContext(ctx, t, "context", contextSt), withBuildTarget("debug/sources"))
		res := solveT(ctx, t, gwc, req)
		ref, err := res.SingleRef()
		if err != nil {
			t.Fatal(err)
		}

		dir := bkfs.FromRef(ctx, ref)

		existsNotDir := checkFileStatOpt{Exists: true}
		existsDir := checkFileStatOpt{Exists: true, IsDir: true}
		notExists := checkFileStatOpt{}

		checkFileStat(t, dir, "basic/base", existsNotDir)
		checkFileStat(t, dir, "basic/foo/bar", existsDir)
		checkFileStat(t, dir, "basic/foo/file", existsNotDir)
		checkFileStat(t, dir, "basic/foo/bar/another", existsNotDir)

		checkFileStat(t, dir, "with-path/base", notExists)
		checkFileStat(t, dir, "with-path/foo", notExists)
		checkFileStat(t, dir, "with-path/another", existsNotDir)

		checkFileStat(t, dir, "with-includes/base", notExists)
		checkFileStat(t, dir, "with-includes/foo/bar", existsDir)
		checkFileStat(t, dir, "with-includes/foo/file", existsNotDir)
		checkFileStat(t, dir, "with-includes/foo/bar/another", existsNotDir)

		checkFileStat(t, dir, "with-excludes/base", existsNotDir)
		checkFileStat(t, dir, "with-excludes/foo", existsDir)
		checkFileStat(t, dir, "with-excludes/foo/file", notExists)
		checkFileStat(t, dir, "with-excludes/foo/bar", notExists)

		checkFileStat(t, dir, "with-path-and-includes-excludes/base", notExists)
		checkFileStat(t, dir, "with-path-and-includes-excludes/foo", notExists)
		checkFileStat(t, dir, "with-path-and-includes-excludes/file", existsNotDir)
		checkFileStat(t, dir, "with-path-and-includes-excludes/bar", existsDir)
		checkFileStat(t, dir, "with-path-and-includes-excludes/bar/another", notExists)
	})
}

type checkFileStatOpt struct {
	IsDir  bool
	Exists bool
}

func checkFileStat(t *testing.T, dir fs.FS, p string, opt checkFileStatOpt) {
	t.Helper()

	stat, err := fs.Stat(dir, p)
	if err != nil && !os.IsNotExist(err) {
		// TODO: the error returned from the buildkit client is not giving us what we want here.
		// So we need to check the error string for now
		if !strings.Contains(err.Error(), "no such file or directory") {
			t.Error(err)
			return
		}

		if opt.Exists {
			t.Errorf("file %q should exist", p)
		}
		return
	}

	if !opt.Exists {
		t.Errorf("file %q should not exist", p)
		return
	}

	if stat == nil {
		return
	}

	if stat.IsDir() != opt.IsDir {
		t.Errorf("expected file %q isDir=%v, got %v", p, opt.IsDir, stat.IsDir())
	}
}

func TestPatchSources_MalformedPatch(t *testing.T) {
	t.Parallel()

	contextSt := llb.Scratch()

	testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
		spec := &dalec.Spec{
			Name:        "test-patch-sources",
			License:     "MIT",
			Version:     "1.0.0",
			Revision:    "1",
			Description: "This is a test package",
			Website:     "https://example.com",
			Patches: map[string][]dalec.PatchSpec{
				"source1": {
					{Source: "malformed_patch"},
				},
			},
			Sources: map[string]dalec.Source{
				"source1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "Hello World",
						},
					},
				},
				"malformed_patch": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "invalid patch content",
						},
					},
				},
			},
		}

		req := newSolveRequest(withBuildTarget("debug/patched-sources"), withBuildContext(ctx, t, "context", contextSt), withSpec(ctx, t, spec))
		req.Evaluate = true
		_, err := gwc.Solve(ctx, req)
		if err == nil {
			t.Fatal("expected error, got none")
		}
	})
}

func TestPatchSources_ConflictingPatches(t *testing.T) {
	t.Parallel()

	contextSt := llb.Scratch()

	testEnv.RunTest(baseCtx, t, func(ctx context.Context, gwc gwclient.Client) {
		spec := &dalec.Spec{
			Name:        "test-patch-sources",
			License:     "MIT",
			Version:     "1.0.0",
			Revision:    "1",
			Description: "This is a test package",
			Website:     "https://example.com",
			Patches: map[string][]dalec.PatchSpec{
				"source1": {
					{Source: "patch1"},
					{Source: "patch2"},
				},
			},
			Sources: map[string]dalec.Source{
				"source1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "Hello World",
						},
					},
				},
				"patch1": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "diff --git a/file.txt b/file.txt\nindex 123..456 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-Hello World\n+Hello Universe",
						},
					},
				},
				"patch2": {
					Inline: &dalec.SourceInline{
						File: &dalec.SourceInlineFile{
							Contents: "diff --git a/file.txt b/file.txt\nindex 123..789 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-Hello World\n+Hello Galaxy",
						},
					},
				},
			},
		}

		req := newSolveRequest(withBuildTarget("debug/patched-sources"), withBuildContext(ctx, t, "context", contextSt), withSpec(ctx, t, spec))
		req.Evaluate = true
		_, err := gwc.Solve(ctx, req)
		if err == nil {
			t.Fatal("expected error, got none")
		}
	})
}
