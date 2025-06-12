package test

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	goerrors "errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/test/testenv"
	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerui"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/identity"
	"github.com/moby/buildkit/solver/pb"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

const (
	goVersion       = "1.23.5"
	usernameRoot    = "root"
	customScriptDir = "/tmp/dalec/internal/scripts"

	waitOnlineTimeout = 20 * time.Second
)

func TestGomodGitAuth(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)
	netHostBuildxEnv := testenv.NewWithNetHostBuildxInstance(ctx, t)
	sshID := "dalecssh"

	const sourcename = "gitauth"

	attr := GitServicesAttributes{
		ServerRoot:             "/",
		PrivateRepoPath:        "username/private",
		PublicRepoPath:         "username/public",
		HTTPServerPath:         "/usr/local/bin/git_http_server",
		Host:                   "host.docker.internal",
		Addr:                   "127.0.0.1",
		HTTPPort:               findRandomAvailablePort(t),
		SSHPort:                findRandomAvailablePort(t),
		HTTPServerBuildDir:     "/tmp/dalec/internal/dalec_coderoot",
		HTTPServeCodeLocalPath: "./test/cmd/git_repo",
		OutDir:                 "/tmp/dalec/internal/output",
	}

	dependingModfile := file{
		template: `
module {{ .Host }}/{{ .PublicRepoPath }}

go {{ .GoVersion }}

require {{ .Host }}/{{ .PrivateRepoPath }}.git {{ .Tag }}
`,
	}

	dependentModfile := file{
		location: "go.mod",
		template: `
module {{ .Host }}/{{ .PrivateRepoPath }}.git

go {{ .GoVersion }}
            `,
	}

	initStates := func(ts *TestState) (llb.State, llb.State, llb.State) {
		worker := initWorker(ts.client())
		repo := llb.Scratch().
			With(ts.customFile(dependentModfile)).
			With(ts.customFile(file{
				location: "foo",
				template: "bar\n",
			})).
			With(ts.initializeGitRepo(worker))

		// 3c. Create the hosting container by loading the git repo into it
		gitHost := worker.With(hostedRepo(repo, ts.attr.RepoAbsDir()))

		return worker, repo, gitHost
	}

	pubkey, privkey := generateKeyPair(t)

	t.Run("HTTP", func(t *testing.T) {
		t.Parallel()
		netHostBuildxEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			attr := attr.WithNewTag()
			testState := TestState{
				t:       t,
				ctx:     ctx,
				_client: client,
				attr:    *attr,
			}

			worker, _, gitHost := initStates(&testState)
			httpGitHost := gitHost.With(testState.updatedGitconfig())
			httpErrChan := testState.startHTTPServer(httpGitHost)

			dependingModfileContents := string(dependingModfile.inject(t, attr))
			spec := testState.generateSpec(dependingModfileContents, dalec.GomodGitAuth{
				Token: "super-secret",
			})

			sr := newSolveRequest(
				withBuildTarget("debug/gomods"),
				withSpec(ctx, t, spec),
				withExtraHost(testState.attr.Host, testState.attr.Addr),
				withBuildContext(ctx, t, "gomod-worker", worker.With(testState.updatedGitconfig())),
			)

			solveResultChan := make(chan *gwclient.Result)
			solveErrChan := make(chan error)
			solveTCh(ctx, t, testState.client(), sr, solveResultChan, solveErrChan)

			var res *gwclient.Result
			select {
			case err := <-httpErrChan:
				t.Fatalf("ssh server unexpededly failed: %s", err)
			case err := <-solveErrChan:
				t.Fatalf("solve failed: %s", err)
			case r := <-solveResultChan:
				res = r
			}

			filename := calculateFilename(ctx, t, attr, res)
			checkFile(ctx, t, filename, res, []byte("bar\n"))
		}, testenv.WithSecrets(testenv.KeyVal{
			K: "super-secret",
			V: "value",
		}), testenv.WithHostNetworking)
	})

	sockaddr, cleanup := getSocketAddr(t)
	agentErrChan := startSSHAgent(t, privkey, sockaddr)
	t.Run("SSH", func(t *testing.T) {
		t.Parallel()
		defer cleanup()

		netHostBuildxEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) {
			attr := attr.WithNewTag()
			testState := TestState{
				t:       t,
				ctx:     ctx,
				_client: client,
				attr:    *attr,
			}

			_, repo, gitHost := initStates(&testState)
			sshGitHost := gitHost.
				With(authorizedKey(pubkey, "/root")).
				With(bareRepo(repo, attr.RepoAbsDir()))

			const githostUsername = "root"
			sshErrChan := testState.startSSHServer(sshGitHost)
			dependingModfileContents := string(dependingModfile.inject(t, attr))

			spec := testState.generateSpec(dependingModfileContents, dalec.GomodGitAuth{
				SSH: &dalec.GomodGitAuthSSH{
					ID:       sshID,
					Username: githostUsername,
				},
			})
			sr := newSolveRequest(
				withBuildTarget("debug/gomods"),
				withSpec(ctx, t, spec),
				withExtraHost(testState.attr.Host, testState.attr.Addr),
			)

			solveResultChan := make(chan *gwclient.Result)
			solveErrChan := make(chan error)

			solveTCh(ctx, t, testState.client(), sr, solveResultChan, solveErrChan)

			var res *gwclient.Result
			select {
			case err := <-agentErrChan:
				t.Fatalf("ssh agent unexpededly failed: %s", err)
			case err := <-sshErrChan:
				t.Fatalf("ssh server unexpededly failed: %s", err)
			case err := <-solveErrChan:
				t.Fatalf("solve failed: %s", err)
			case r := <-solveResultChan:
				res = r
			}

			filename := calculateFilename(ctx, t, attr, res)
			checkFile(ctx, t, filename, res, []byte("bar\n"))
		}, testenv.WithSSHSocket(sshID, sockaddr), testenv.WithSecrets(testenv.KeyVal{
			K: "super-secret",
			V: "value",
		}), testenv.WithHostNetworking)
	})
}

func getSocketAddr(t *testing.T) (string, func()) {
	dir, err := os.MkdirTemp("/tmp", "dalec-ssh-agent")
	if err != nil {
		t.Fatal("could not create temporary directory for socket")
	}

	sockaddr := filepath.Join(dir, "ssh.agent.sock")
	return sockaddr, func() { _ = os.RemoveAll(dir) }
}

func calculateFilename(ctx context.Context, t *testing.T, attr *GitServicesAttributes, res *gwclient.Result) string {
	outDirBase := filepath.Join(attr.Host, filepath.Dir(attr.PrivateRepoPath))
	modDir := getDirName(ctx, t, res, outDirBase, "private.git@*")
	filename := filepath.Join(outDirBase, modDir, "foo")
	return filename
}

func copyAttr(g *GitServicesAttributes) GitServicesAttributes {
	gg := new(GitServicesAttributes)
	*gg = *g
	return *gg
}

// GitServicesAttributes are the basic pieces of information needed to host two git
// servers, one via SSH and one via HTTP
type GitServicesAttributes struct {
	// ServerRoot is the root filesystem path of the git server. URL paths
	// refer to repositories relative to this root
	ServerRoot string
	// PrivateRepoPath is the filesystem path, relative to `serverRoot`, where
	// the private git repository is hosted
	PrivateRepoPath string
	// privateRepoPath is the URI path in the name of the public go module;
	// this public go module has a private dependency on the repo at
	// `privateRepoPath`
	PublicRepoPath string
	// HTTP Server path is the filesystem path of the already-built HTTP
	// server, installed into its final location.
	HTTPServerPath string

	// Host is the hostname of the git server
	Host string
	// Addr is the IPv4 address to which the hostname resolves
	Addr string

	// HTTPPort is the port on which the http git server runs
	HTTPPort string
	// SSHPort is the port on which the ssh git server runs
	SSHPort string

	// HTTPServerBuildDir is the location at which the HTTP server program's
	// code will be loaded for building
	HTTPServerBuildDir string
	// HTTPServerBuildPath is the *local filesystem* location at which the HTTP server program's
	// code can be found
	HTTPServeCodeLocalPath string
	// OutDir is the location to which dalec will output files
	OutDir string

	// _tag is a private field and should not be accessed directly
	_tag string
}

func (g *GitServicesAttributes) PrivateRepoAbsPath() string {
	return filepath.Join(g.ServerRoot, g.PrivateRepoPath)
}

func (g *GitServicesAttributes) HTTPServerDir() string {
	return filepath.Dir(g.HTTPServerPath)
}

func (g *GitServicesAttributes) HTTPServerBase() string {
	return filepath.Base(g.HTTPServerPath)
}

type TestState struct {
	t       *testing.T
	ctx     context.Context
	_client gwclient.Client
	attr    GitServicesAttributes
}

func (ts *TestState) client() gwclient.Client {
	if ts._client == nil {
		ts.t.Fatal("TestState: called client() with nil client")
	}

	return ts._client
}

type file struct {
	location string
	template string
}

type script struct {
	basename string
	template string
}

func (s *script) absPath() string {
	return filepath.Join(customScriptDir, s.basename)
}

func (s *script) inject(t *testing.T, obj *GitServicesAttributes) []byte {
	tmpl := "#!/usr/bin/env sh\n" + s.template
	f := file{
		template: tmpl,
	}

	return f.inject(t, obj)
}

func (f *file) inject(t *testing.T, obj *GitServicesAttributes) []byte {
	cleaned := cleanWhitespace(f.template)

	if obj == nil {
		return []byte(cleaned)
	}

	tmpl, err := template.New("depending go mod").Parse(cleaned)
	if err != nil {
		t.Fatalf("could not parse template: %s", err)
	}

	type injector struct {
		*GitServicesAttributes
		GoVersion string
	}

	var contents bytes.Buffer
	tmpl.Execute(&contents, injector{
		GitServicesAttributes: obj,
		GoVersion:             goVersion,
	})

	return contents.Bytes()
}

func cleanWhitespace(s string) string {
	var b bytes.Buffer

	tb := bytes.NewBuffer([]byte(s))
	sc := bufio.NewScanner(tb)

	initial := true
	for sc.Scan() {
		t := strings.TrimSpace(sc.Text())

		if initial && t == "" {
			initial = false
			continue
		}

		b.WriteString(t)
		b.WriteRune('\n')
	}

	return b.String()
}

func (a *GitServicesAttributes) Tag() string {
	return a._tag
}

func (a *GitServicesAttributes) WithNewTag() *GitServicesAttributes {
	if a == nil {
		return &GitServicesAttributes{_tag: identity.NewID()}
	}

	b := *a
	b._tag = identity.NewID()

	return &b
}

func (a *GitServicesAttributes) RepoAbsDir() string {
	return filepath.Join(a.ServerRoot, a.PrivateRepoPath)
}

func (a *GitServicesAttributes) inPrivateGitRepo(basename string) string {
	return filepath.Join(a.RepoAbsDir(), basename)
}

// func (ts *TestState) createGitUser(user *string) llb.StateOption {
// 	s := script{
// 		basename: "create_git_user.sh",
// 		template: `
//                         #!/usr/bin/env sh
//                         adduser -D -u 9999 -h {{ .ServerRoot }} {{ .GitUsername }}
//                         chown -R {{ .GitUsername }} {{ .ServerRoot }}
//                         printf 'abc\nabc\n' | passwd git
//                     `,
// 	}

// 	if user != nil {
// 		*user = "git"
// 	}

// 	return func(worker llb.State) llb.State {
// 		return ts.runScriptOn(worker, s).Root().User(ts.attr.GitUsername)
// 	}
// }

func (ts *TestState) generateSpec(gomodContents string, auth dalec.GomodGitAuth) *dalec.Spec {
	const sourceName = "gitauth"
	var port string

	switch {
	case auth.Token != "":
		port = ts.attr.HTTPPort
	case auth.SSH != nil:
		port = ts.attr.SSHPort
	default:
		ts.t.Fatal("cannot tell which kind of spec is needed, aborting")
	}

	spec := &dalec.Spec{
		Name: "gomod-git-auth",
		Sources: map[string]dalec.Source{
			sourceName: {
				Inline: &dalec.SourceInline{
					Dir: &dalec.SourceInlineDir{
						Files: map[string]*dalec.SourceInlineFile{
							"go.mod": {
								Contents: string(gomodContents),
							},
						},
					},
				},
				Generate: []*dalec.SourceGenerator{
					{
						Gomod: &dalec.GeneratorGomod{
							Auth: map[string]dalec.GomodGitAuth{
								fmt.Sprintf("%s:%s", ts.attr.Host, port): auth,
							},
						},
					},
				},
			},
		},
	}
	return spec
}

func (ts *TestState) startHTTPServer(gitHost llb.State) chan error {
	t := ts.t

	serverScript := script{
		basename: "run_http_server.sh",
		template: `
            #!/usr/bin/env sh

            set -ex
            exec {{ .HTTPServerPath }} {{ .ServerRoot }} {{ .Addr }} {{ .HTTPPort }}
        `,
	}

	// This script attempts to connect to the http server. The `nc -z` flag
	// discconnects and exits with status 0 if a successful connection is made.
	// `nc -w5` gives up and exits with status 1 after a 5-second timeout.
	waitScript := script{
		basename: "wait_for_http.sh",
		template: `
            #!/usr/bin/env sh
            while ! nc -zw5 "{{ .Host }}" "{{ .HTTPPort }}"; do
                sleep 0.1
            done
        `,
	}

	httpServerBin := ts.getMainDockerContext().
		With(ts.buildHTTPServer(gitHost))

	gitHost = gitHost.
		With(ts.customScript(serverScript)).
		With(ts.customScript(waitScript)).
		File(
			llb.Copy(httpServerBin, "/", ts.attr.HTTPServerDir()),
		)

	cont := ts.newContainer(gitHost)

	env := ts.getStateEnv(gitHost)
	errChan := ts.runContainer(cont, env, serverScript)

	t.Log("waiting for http server to come online")

	timeout := waitOnlineTimeout
	ts.runWaitScript(cont, env, waitScript, timeout)
	// time.Sleep(time.Second * 86400)

	t.Logf("http server is online")

	return errChan
}

func (ts *TestState) buildHTTPServer(worker llb.State) llb.StateOption {
	s := script{
		basename: "build_http_server.sh",
		template: `
            #!/usr/bin/env sh
            set -ex
            cd {{ .HTTPServerBuildDir }}
            go build -o {{ .OutDir }}/git_http_server ./{{ .HTTPServeCodeLocalPath }}
        `,
	}

	return func(code llb.State) llb.State {
		return ts.runScriptOn(worker, s,
			llb.AddMount(ts.attr.HTTPServerBuildDir, code),
		).AddMount(ts.attr.OutDir, llb.Scratch())
	}
}

func (ts *TestState) getMainDockerContext() llb.State {
	var (
		t      = ts.t
		ctx    = ts.ctx
		client = ts.client()
	)

	dc, err := dockerui.NewClient(client)
	if err != nil {
		t.Fatalf("could not create dockerui client: %s", err)
	}

	gitServerProgramPtr, err := dc.MainContext(ctx)
	if err != nil {
		t.Fatalf("could not obtain main docker context: %s", err)
	}

	if gitServerProgramPtr == nil {
		t.Fatalf("main context is nil")
	}

	return *gitServerProgramPtr
}

type customMount struct {
	dst string
	st  llb.State
}

func (ts *TestState) newContainer(rootfs llb.State, extraMounts ...customMount) gwclient.Container {
	t := ts.t
	ctx := ts.ctx
	client := ts.client()
	attr := ts.attr

	mountCfgs := []customMount{
		{
			dst: "/",
			st:  rootfs,
		},
	}
	mountCfgs = append(mountCfgs, extraMounts...)

	mounts := make([]gwclient.Mount, 0, len(mountCfgs))
	for _, cm := range mountCfgs {
		mounts = append(mounts, gwclient.Mount{
			Dest: cm.dst,
			Ref:  ts.stateToRef(cm.st),
		})
	}

	cont, err := client.NewContainer(ctx, gwclient.NewContainerRequest{
		Mounts:  mounts,
		NetMode: pb.NetMode_HOST,
		ExtraHosts: []*pb.HostIP{
			{
				Host: attr.Host,
				IP:   attr.Addr,
			},
		},
	})
	if err != nil {
		t.Fatalf("could not create ssh server container: %s", err)
	}

	return cont
}

func (ts *TestState) customFile(f file) llb.StateOption {
	dir := filepath.Dir(f.location)

	return func(s llb.State) llb.State {
		return s.File(
			llb.Mkdir(dir, 0o777, llb.WithParents(true)).
				Mkfile(f.location, 0o666, f.inject(ts.t, &ts.attr)),
		)
	}
}

func (ts *TestState) customScript(s script) llb.StateOption {
	dir := customScriptDir
	absPath := filepath.Join(dir, s.basename)

	return func(worker llb.State) llb.State {
		return worker.File(
			llb.Mkdir(dir, 0o755, llb.WithParents(true)).
				Mkfile(absPath, 0o755, s.inject(ts.t, &ts.attr)),
		)
	}
}

// startSSHServer starts an sshd instance in a container hosting the git repo.
// It runs asynchonously and checks the connection after starting the server.
func (ts *TestState) startSSHServer(gitHost llb.State) chan error {
	t := ts.t

	// This script runs an ssh server. Rather than create a new user, we will
	// permit root login to simplify things. It is running in a container so
	// this should not be a security issue.
	serverScript := script{
		basename: "start_ssh_server.sh",
		template: `
            #!/usr/bin/env sh
            set -ex
            ssh-keygen -A
            exec /usr/sbin/sshd -o PermitRootLogin=yes -p {{ .SSHPort }} -D
        `,
	}

	// This script attempts to connect to the ssh server. The `nc -z` flag
	// discconnects and exits with status 0 if a successful connection is made.
	// `nc -w5` gives up and exits with status 1 after a 5-second timeout.
	waitScript := script{
		basename: "wait_for_ssh.sh",
		template: `
            #!/usr/bin/env sh
            while ! nc -zw5 "{{ .Host }}" "{{ .SSHPort }}"; do
                sleep 0.1
            done
`,
	}

	gitHost = gitHost.
		With(ts.customScript(serverScript)).
		With(ts.customScript(waitScript))

	cont := ts.newContainer(gitHost)
	env := ts.getStateEnv(gitHost)
	errChan := ts.runContainer(cont, env, serverScript)

	t.Log("waiting for ssh server to come online")

	timeout := waitOnlineTimeout
	ts.runWaitScript(cont, env, waitScript, timeout)

	t.Logf("ssh server is online")

	return errChan
}

type bufCloser struct {
	*bytes.Buffer
}

func (b *bufCloser) Close() error {
	return nil
}

func (ts *TestState) startContainer(cont gwclient.Container, env []string, s script) (gwclient.ContainerProcess, bufCloser, bufCloser) {
	var (
		t   = ts.t
		ctx = ts.ctx
	)
	stdout := bufCloser{bytes.NewBuffer(nil)}
	stderr := bufCloser{bytes.NewBuffer(nil)}

	cp, err := cont.Start(ctx, gwclient.StartRequest{
		Env:    env,
		Stdout: &stdout,
		Stderr: &stderr,
		Args:   []string{s.absPath()},
	})
	if err != nil {
		t.Fatalf("could not start server: %s\nstdout:\n%s\n===\nstderr:\n%s\n", err, stdout.String(), stderr.String())
	}

	return cp, stdout, stderr
}

func (ts *TestState) withTimeout(timeout time.Duration) (*TestState, func()) {
	if ts == nil {
		return ts, func() {}
	}

	ts2 := *ts

	var cancel func()
	ts2.ctx, cancel = context.WithTimeout(ts2.ctx, timeout)

	return &ts2, cancel
}

func (ts *TestState) runWaitScript(cont gwclient.Container, env []string, s script, timeout time.Duration) {
	ts2, cancel := ts.withTimeout(timeout)
	defer cancel()

	untilConnected, stdout, stderr := ts2.startContainer(cont, env, s)

	if err := untilConnected.Wait(); err != nil {
		if goerrors.Is(err, context.DeadlineExceeded) {
			ts2.t.Fatalf("Could not start server, timed out: %s", err)
		}

		ts2.t.Fatalf("could not check progress of server, container command failed: %s\nstdout:\n%s\n=====\nstderr:\n%s\n", err, stdout.String(), stderr.String())
	}
}

var (
	errContainerNoStart = goerrors.New("could not start server container")
	errContainerFailed  = goerrors.New("container process failed")
)

// runContainer runs a container in the background and sends errors to the returned channel
func (ts *TestState) runContainer(cont gwclient.Container, env []string, s script) chan error {

	var (
		ctx = ts.ctx
	)

	stdout := bufCloser{bytes.NewBuffer(nil)}
	stderr := bufCloser{bytes.NewBuffer(nil)}

	ts.t.Log("listening")
	cp, err := cont.Start(ctx, gwclient.StartRequest{
		Args:   []string{s.absPath()},
		Env:    env,
		Stdout: &stdout,
		Stderr: &stderr,
	})

	if err != nil {
		ts.t.Fatal(goerrors.Join(errContainerNoStart, err))
	}

	// Log but do not fail, since you cannot fail from within a goroutine
	ec := make(chan error)
	go func() {
		if err := cp.Wait(); err != nil {
			ec <- goerrors.Join(errContainerFailed, err, fmt.Errorf("stdout:\n%s\n=====\nstderr:\n%s\n", stdout.String(), stderr.String()))
		}
	}()

	return ec
}

func (ts *TestState) getStateEnv(st llb.State) []string {
	env, err := st.Env(ts.ctx)
	if err != nil {
		ts.t.Logf("unable to copy env: %s", err)
	}

	return env.ToArray()
}

func authorizedKey(pubkey ssh.PublicKey, homedir string) llb.StateOption {
	dir := filepath.Join(homedir, ".ssh")
	const basename = "authorized_keys"
	absPath := filepath.Join(dir, basename)

	pubkeyData := ssh.MarshalAuthorizedKey(pubkey)

	return func(s llb.State) llb.State {
		return s.File(
			llb.Mkdir(dir, 0o700, llb.WithParents(true)).
				Mkfile(absPath, 0o600, pubkeyData),
		)
	}
}

func hostedRepo(repo llb.State, mountpoint string) llb.StateOption {
	return func(worker llb.State) llb.State {
		return worker.File(
			llb.Mkdir(mountpoint, 0o755, llb.WithParents(true)).
				Copy(repo, "/", mountpoint),
		)
	}
}

func bareRepo(repo llb.State, mountpoint string) llb.StateOption {
	return func(worker llb.State) llb.State {
		bare := llb.Scratch().File(
			llb.Copy(repo, ".git", "/", &llb.CopyInfo{
				CopyDirContentsOnly: true,
			}),
		)

		bare = worker.Run(dalec.ShArgs("git init --bare")).AddMount(mountpoint, bare)

		return worker.User("git").File(
			llb.Rm(mountpoint).
				Mkdir(mountpoint, 0o755, llb.WithParents(true)).
				Copy(bare, "/", mountpoint),
		)
	}
}

func (ts *TestState) mountScript(s script) dalec.RunOptFunc {
	scriptDir := customScriptDir
	st := llb.Scratch().With(ts.customScript(s))

	return func(ei *llb.ExecInfo) {
		llb.AddMount(scriptDir, st).SetRunOption(ei)
	}
}

// `runScript` is a replacement for `llb.State.Run(...)`. It mounts the
// specified script in the custom script directory, then generates the llb to
// run the script on `worker`.
func (ts *TestState) runScriptOn(worker llb.State, s script, runopts ...llb.RunOption) llb.ExecState {
	worker = worker.With(ts.customScript(s))
	o := []llb.RunOption{
		llb.Args([]string{s.absPath()}),
	}

	o = append(o, runopts...)
	return worker.Run(o...)
}

// initializeGitRepo returns a stateOption that uses `worker` to create an
// initialized git repository from the base state.
func (ts *TestState) initializeGitRepo(worker llb.State) llb.StateOption {
	attr := ts.attr

	repoScript := script{
		basename: "git_init.sh",
		template: `
            #!/usr/bin/env sh

            set -ex
            export GIT_CONFIG_NOGLOBAL=true
            git init
            git config user.name foo
            git config user.email foo@bar.com

            git add -A
            git commit -m commit --no-gpg-sign
            git tag {{ .Tag }}
`,
	}

	return func(repo llb.State) llb.State {
		worker = worker.Dir(ts.attr.PrivateRepoAbsPath())

		return ts.runScriptOn(worker, repoScript).
			AddMount(attr.RepoAbsDir(), repo)
	}
}

func startSSHAgent(t *testing.T, privkey crypto.PrivateKey, sockaddr string) chan error {
	ec := make(chan error)
	t.Cleanup(func() {
		close(ec)
	})

	kr := agent.NewKeyring()
	kr.Add(agent.AddedKey{
		PrivateKey: privkey,
	})

	t.Logf("starting ssh agent on socket %s", sockaddr)
	listener, err := net.Listen("unix", sockaddr)
	if err != nil {
		t.Fatalf("can't listen on unix socket: %s", err)
	}

	go func() {
		for {
			c, err := listener.Accept()
			t.Log("connection accepted")
			if err != nil {
				ec <- fmt.Errorf("listener.Accept: %w", err)
				return
			}

			go func() {
				if err := agent.ServeAgent(kr, c); err != nil {
					if errors.Is(err, io.EOF) {
						return
					}

					ec <- fmt.Errorf("cannot serve agent: %w", err)
				}
			}()
		}
	}()

	return ec
}

func generateKeyPair(t *testing.T) (ssh.PublicKey, crypto.PrivateKey) {
	u, privkey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("could not generate ssh keypair: %s", err)
	}

	pubkey, err := ssh.NewPublicKey(u)
	if err != nil {
		t.Fatalf("could not parse ssh public key: %s", err)
	}

	return pubkey, privkey
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

func initWorker(c gwclient.Client) llb.State {
	worker := llb.Image("alpine:latest", llb.Platform(ocispecs.Platform{Architecture: runtime.GOARCH, OS: "linux"}), llb.WithMetaResolver(c)).
		Run(llb.Shlex("apk add --no-cache go git ca-certificates patch openssh netcat-openbsd")).Root()
	return worker
}

func (ts *TestState) updatedGitconfig() llb.StateOption {
	s := script{
		basename: "update_gitconfig.sh",
		template: `
            git config --global "url.http://{{ .Host }}:{{ .HTTPPort }}.insteadOf" "https://{{ .Host }}"
            git config --global credential."http://{{ .Host }}:{{ .HTTPPort }}.helper" "/usr/local/bin/frontend credential-helper --kind=token"
        `,
	}

	return func(st llb.State) llb.State {
		return ts.runScriptOn(st, s).Root()
	}
}

func (ts *TestState) stateToRef(st llb.State) gwclient.Reference {
	t := ts.t
	ctx := ts.ctx

	def, err := st.Marshal(ctx)
	if err != nil {
		t.Fatalf("could not marshal git repo llb: %s", err)
	}

	res, err := ts.client().Solve(ts.ctx, gwclient.SolveRequest{Definition: def.ToPB()})
	if err != nil {
		t.Fatalf("could not solve git repo llb %s", err)
	}

	ref, err := res.SingleRef()
	if err != nil {
		t.Fatalf("could not convert result to single ref %s", err)
	}
	return ref
}

func findRandomAvailablePort(t *testing.T) string {
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
