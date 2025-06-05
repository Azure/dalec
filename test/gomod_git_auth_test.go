package test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/test/testenv"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	serverRoot     = "/git_server"
	repoDir        = "/user/private"
	repoMountpoint = serverRoot + repoDir

	host = "host.docker.internal"
	addr = "127.0.0.1"
)

func TestGomodGitAuthHTTPS(t *testing.T) {
	t.Parallel()

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

		// Private git repo
		modFile := fmt.Sprintf("module %s/user/private.git\n"+
			"\n"+
			"\n"+
			"go 1.23.5\n", host)

		repo := llb.Scratch().
			File(
				llb.Mkdir(repoDir, 0o755, llb.WithParents(true))).
			Dir(repoDir).
			File(
				llb.Mkfile("hello", 0o644, []byte("hello\n")).
					Mkfile("go.mod", 0o644, []byte(modFile)),
			)

		if err := runGitServer(ctx, t, c, repo, port, tag); err != nil {
			t.Fatal(err)
		}

		sr := newSolveRequest(
			withBuildTarget("debug/gomods"),
			withSpec(ctx, t, spec),
			withExtraHost(host, addr),
			withBuildContext(ctx, t, "gomod-worker", initGomodWorker(c, host, port)),
		)

		const outDirBase = host + "/user"
		res := solveT(ctx, t, c, sr)
		modDir := getDirName(ctx, t, res, outDirBase, "private.git@*")

		filename := filepath.Join(outDirBase, modDir, "hello")
		checkFile(ctx, t, filename, res, []byte("hello\n"))
	}, testenv.WithSecrets(testenv.KeyVal{
		K: "super-secret",
		V: "value",
	}), testenv.WithHostNetworking)
}

func getDirName(ctx context.Context, t *testing.T, res *gwclient.Result, base, dirPattern string) string {
	ref, err := res.SingleRef()
	if err != nil {
		t.Fatal(err)
	}

	stats, err := ref.ReadDir(ctx, gwclient.ReadDirRequest{
		Path:           base,
		IncludePattern: dirPattern,
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

func runGitServer(ctx context.Context, t *testing.T, client gwclient.Client, repo llb.State, port, tag string) error {

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
