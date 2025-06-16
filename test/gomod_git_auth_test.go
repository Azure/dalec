package test

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/Azure/dalec"
	"github.com/Azure/dalec/test/cmd/git_repo/passwd"
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
	usernameRoot    = "root"
	customScriptDir = "/tmp/dalec/internal/scripts"

	waitOnlineTimeout = 20 * time.Second
)

func TestGomodGitAuth(t *testing.T) {
	t.Parallel()
	ctx := startTestSpan(baseCtx, t)

	// In order to perform these tests, we need to run auxiliary services to
	// act as git servers that host private git modules. Rather than create a
	// separate virtual network, we are using host networking to accomplish
	// this. In order to do so, the buildx instance we use to run the tests
	// needs to have host networking enabled; otherwise, the solve request will
	// fail.
	netHostBuildxEnv := testenv.NewWithNetHostBuildxInstance(ctx, t)

	secretName := "super-secret"
	sshID := "dalecssh"

	attr := GitServicesAttributes{
		ServerRoot:             "/",
		PrivateRepoPath:        "username/private",
		DependingRepoPath:      "username/public",
		HTTPServerPath:         "/usr/local/bin/git_http_server",
		PrivateGomoduleHost:    "host.docker.internal",
		GitRemoteAddr:          "127.0.0.1",
		HTTPPort:               findRandomAvailablePort(t),
		SSHPort:                findRandomAvailablePort(t),
		HTTPServerBuildDir:     "/tmp/dalec/internal/dalec_coderoot",
		HTTPServeCodeLocalPath: "./test/cmd/git_repo",
		OutDir:                 "/tmp/dalec/internal/output",
		ModFileGoVersion:       "1.23.5",
	}

	// This is the go.mod file of the go module that *depends on our private go
	// module*. We call it the "depending" go module.
	dependingModfile := file{
		template: `
module {{ .PrivateGomoduleHost }}/{{ .DependingRepoPath }}

go {{ .ModFileGoVersion }}

require {{ .PrivateGomoduleHost }}/{{ .PrivateRepoPath }}.git {{ .PrivateGoModuleGitTag }}
`,
	}

	// This is the go.mod file of the *private go module*.
	dependentModfile := file{
		location: "go.mod",
		template: `
module {{ .PrivateGomoduleHost }}/{{ .PrivateRepoPath }}.git

go {{ .ModFileGoVersion }}
            `,
	}

	// This closure creates the necessary llb states representing:
	//   - the `worker`, which will be the worker on which the `debug/gomods`
	//     target is run
	//   - the `repo`, which is the initialized git repository containing the
	//     private go module
	//  - the `gitHost`, which is essentially the worker plus the repo: an OS
	//    capable of acting as a git server to host the private repo, which is
	//    copied in.
	initStates := func(ts *TestState) (llb.State, llb.State, llb.State) {
		worker := initWorker(ts.client())
		repo := llb.Scratch().
			With(ts.customFile(dependentModfile)).
			With(ts.customFile(file{
				location: "foo",
				template: "bar\n",
			})).
			With(ts.initializeGitRepo(worker))

		gitHost := worker.With(hostedRepo(repo, ts.attr.RepoAbsDir()))

		return worker, repo, gitHost
	}

	// This generates the contents of the depending mod file, using `t.Fatal`
	// in the case of failures and using the git tag and other attributes from
	// `attr`.
	depModfileContents := func(t *testing.T, attr *GitServicesAttributes) string {
		return string(dependingModfile.inject(t, attr))
	}

	t.Run("HTTP", func(t *testing.T) {
		t.Parallel()
		netHostBuildxEnv.RunTestOptsFirst(ctx, t, []testenv.TestRunnerOpt{
			// This gives buildkit access to a secret with the name
			// `secretName` and the value `passwd.Password`. On the worker that
			// fetches the gomod dependencies, a file will be mounted at the
			// location `/run/secrets/super-secret` with the contents
			// `passwd.Password`.
			testenv.WithSecrets(secretName, passwd.Password),
			// Host Networking MUST also be requested on the individual solve
			// requests. It is necessary but not sufficient for the buildx
			// instance to have host networking enabled.
			testenv.WithHostNetworking,
		}, func(ctx context.Context, client gwclient.Client) {
			// This MUST be called at the  start of each test. Because we
			// persist the go mod cache between runs, the git tag of the
			// private go module needs to be unique. Within a single run of the
			// tests, the http and ssh tests cannot have the same tag or we
			// risk a false positive due to caching.
			attr := attr.WithNewPrivateGoModuleGitTag()

			testState := TestState{
				t:       t,
				ctx:     ctx,
				_client: client,
				attr:    *attr,
			}

			worker, _, gitHost := initStates(&testState)
			httpGitHost := gitHost.With(testState.updatedGitconfig())
			httpErrChan := testState.startHTTPGitServer(httpGitHost)

			// Generate a basic spec file with the *depending* go module's
			// go.mod file inlined, and the name of the secret corresponding to
			// the password required to authenticate to the git repository.
			spec := testState.generateSpec(depModfileContents(t, attr), dalec.GomodGitAuth{
				Token: secretName,
			})

			sr := newSolveRequest(
				withBuildTarget("debug/gomods"),
				withSpec(ctx, t, spec),
				withExtraHost(testState.attr.PrivateGomoduleHost, testState.attr.GitRemoteAddr),
				// We need to provide a custom worker to the gomod generator.
				// The reason is described in the documentation to
				// `updatedGitconfig`
				withBuildContext(ctx, t, "gomod-worker", worker.With(testState.updatedGitconfig())),
			)

			solveResultChan := make(chan *gwclient.Result)
			solveErrChan := make(chan error)
			solveTCh(ctx, t, testState.client(), sr, solveResultChan, solveErrChan)

			var res *gwclient.Result
			select {
			case err := <-httpErrChan:
				t.Fatalf("http server unexpectedly failed: %s", err)
			case err := <-solveErrChan:
				t.Fatalf("solve failed: %s", err)
			case r := <-solveResultChan:
				res = r
			}

			filename := calculateFilename(ctx, t, attr, res)
			checkFile(ctx, t, filename, res, []byte("bar\n"))
		})
	})

	t.Run("SSH", func(t *testing.T) {
		t.Parallel()

		sockaddr := getSocketAddr(t)

		// In order to simulate real-life SSH auth scenarios, we generate a
		// keypair. Buildkit handles SSH auth by forwarding an SSH agent socket
		// to privide the private key upon request. We start an agent here to
		// serve the private key.
		pubkey, privkey := generateKeyPair(t)
		agentErrChan := startSSHAgent(t, privkey, sockaddr)

		netHostBuildxEnv.RunTestOptsFirst(ctx, t, []testenv.TestRunnerOpt{
			// This tells buildkit to forward the SSH Agent socket, giving the
			// gomod generator worker access to the private key
			testenv.WithSSHSocket(sshID, sockaddr),
			testenv.WithHostNetworking,
		}, func(ctx context.Context, client gwclient.Client) {
			// This MUST be called at the  start of each test. Because we
			// persist the go mod cache between runs, the git tag of the
			// private go module needs to be unique. Within a single run of the
			// tests, the http and ssh tests cannot have the same tag or we
			// risk a false positive due to caching.
			attr := attr.WithNewPrivateGoModuleGitTag()

			testState := TestState{
				t:       t,
				ctx:     ctx,
				_client: client,
				attr:    *attr,
			}

			_, repo, gitHost := initStates(&testState)
			sshGitHost := gitHost.
				// The SSH host has to know the public key that corresponds to
				// the client's private key; otherwise it will deny access.
				With(authorizedKey(pubkey, "/root")).
				// In order to host a git repo over SSH, it needs to be hosted
				// as a "bare" git repo. An existing git repo can be made into
				// a bare repo by hoisting the contents of the `.git` directory
				// up a level, removing everything else, and calling `git init
				// --bare`. That bare repo can then be used as a git remote
				// over SSH.
				With(bareRepo(repo, attr.RepoAbsDir()))

			const githostUsername = "root"
			sshErrChan := testState.startSSHServer(sshGitHost)

			spec := testState.generateSpec(depModfileContents(t, attr), dalec.GomodGitAuth{
				SSH: &dalec.GomodGitAuthSSH{
					ID:       sshID,
					Username: githostUsername,
				},
			})
			sr := newSolveRequest(
				withBuildTarget("debug/gomods"),
				withSpec(ctx, t, spec),
				// The extra host in fquestion here is the
				withExtraHost(testState.attr.PrivateGomoduleHost, testState.attr.GitRemoteAddr),
			)

			solveResultChan := make(chan *gwclient.Result)
			solveErrChan := make(chan error)

			solveTCh(ctx, t, testState.client(), sr, solveResultChan, solveErrChan)

			var res *gwclient.Result
			select {
			case err := <-agentErrChan:
				t.Fatalf("ssh agent unexpededly failed: %s", err)
			case err := <-sshErrChan:
				t.Fatalf("ssh server unexpectedly failed: %s", err)
			case err := <-solveErrChan:
				t.Fatalf("solve failed: %s", err)
			case r := <-solveResultChan:
				res = r
			}

			filename := calculateFilename(ctx, t, attr, res)
			checkFile(ctx, t, filename, res, []byte("bar\n"))
		})
	})
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
	DependingRepoPath string
	// HTTP Server path is the filesystem path of the already-built HTTP
	// server, installed into its final location.
	HTTPServerPath string

	// PrivateGomoduleHost is the hostname of the git server
	PrivateGomoduleHost string
	// GitRemoteAddr is the IPv4 address to which the hostname resolves
	GitRemoteAddr string

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

	// ModFileGoVersion is the go version for the go modules
	ModFileGoVersion string

	// _tag is a private field and should not be accessed directly
	_tag string
}

func getSocketAddr(t *testing.T) string {
	dir := t.TempDir()
	sockaddr := filepath.Join(dir, "ssh.agent.sock")

	return sockaddr
}

// The go module, once downloaded, will have a randomized filename so we need
// to find out what it is before we can check the contents of the file inside.
func calculateFilename(ctx context.Context, t *testing.T, attr *GitServicesAttributes, res *gwclient.Result) string {
	outDirBase := filepath.Join(attr.PrivateGomoduleHost, filepath.Dir(attr.PrivateRepoPath))
	modDir := getDirName(ctx, t, res, outDirBase, "private.git@*")
	filename := filepath.Join(outDirBase, modDir, "foo")
	return filename
}

// `PrivateRepoAbsPath` returns the resolved absolute filepath of the private
// git repo in the `gitHost` container.
func (g *GitServicesAttributes) PrivateRepoAbsPath() string {
	return filepath.Join(g.ServerRoot, g.PrivateRepoPath)
}

func (g *GitServicesAttributes) HTTPServerDir() string {
	return filepath.Dir(g.HTTPServerPath)
}

func (g *GitServicesAttributes) HTTPServerBase() string {
	return filepath.Base(g.HTTPServerPath)
}

// `TestState` is a bundle of stuff that the tests need access to in order to do their work.
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

// Wrapper types to make templating and injecting files into llb states
type file struct {
	location string
	template string
}

// Wrapper types to make templating and injecting files into llb states.
// Scripts will typically be copied into `customScriptDir`
type script struct {
	basename string
	template string
}

func (s *script) absPath() string {
	return filepath.Join(customScriptDir, s.basename)
}

// Completes a template and adds a shebang to a script.
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

	var contents bytes.Buffer
	if err := tmpl.Execute(&contents, obj); err != nil {
		t.Fatalf("could not inject values into template: %s", err)
	}

	return contents.Bytes()
}

// Removes unnecessary whitespace so that scripts run properly and don't have a
// messed-up shebang.
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

func (a *GitServicesAttributes) PrivateGoModuleGitTag() string {
	if a._tag == "" {
		panic("PrivateGoModuleGitTag() called with empty tag")
	}
	return a._tag
}

func (a *GitServicesAttributes) WithNewPrivateGoModuleGitTag() *GitServicesAttributes {
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

// Dalec spec boilerplate
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
								fmt.Sprintf("%s:%s", ts.attr.PrivateGomoduleHost, port): auth,
							},
						},
					},
				},
			},
		},
	}
	return spec
}

// `startHTTPGitServer` starts a git HTTP server to serve the private go module
// as a git repo.
func (ts *TestState) startHTTPGitServer(gitHost llb.State) <-chan error {
	t := ts.t

	serverScript := script{
		basename: "run_http_server.sh",
		template: `
            #!/usr/bin/env sh

            set -ex
            exec {{ .HTTPServerPath }} {{ .ServerRoot }} {{ .GitRemoteAddr }} {{ .HTTPPort }}
        `,
	}

	// This script attempts to connect to the http server. The `nc -z` flag
	// discconnects and exits with status 0 if a successful connection is made.
	// `nc -w5` gives up and exits with status 1 after a 5-second timeout.
	waitScript := script{
		basename: "wait_for_http.sh",
		template: `
            #!/usr/bin/env sh
            while ! nc -zw5 "{{ .PrivateGomoduleHost }}" "{{ .HTTPPort }}"; do
                sleep 0.1
            done
        `,
	}

	// The Git HTTP server is coded in a separate program at test/cmd/git_repo.
	// We need to build and inject it.
	httpServerBin := ts.getMainDockerContext().
		With(ts.buildHTTPGitServer(gitHost))

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

	t.Logf("http server is online")

	return errChan
}

// `buildHTTPGitServer` builds the Git HTTP server helper program.
func (ts *TestState) buildHTTPGitServer(worker llb.State) llb.StateOption {
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
				Host: attr.PrivateGomoduleHost,
				IP:   attr.GitRemoteAddr,
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
func (ts *TestState) startSSHServer(gitHost llb.State) <-chan error {
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
            while ! nc -zw5 "{{ .PrivateGomoduleHost }}" "{{ .SSHPort }}"; do
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

// Return a new TestState with the context set to a context with a deadline.
func (ts *TestState) withTimeout(timeout time.Duration) (*TestState, func()) {
	if ts == nil {
		return ts, func() {}
	}

	ts2 := *ts

	var cancel func()
	ts2.ctx, cancel = context.WithTimeout(ts2.ctx, timeout)

	return &ts2, cancel
}

// `runWaitScript` runs a script checking on the just-started server (http or ssh).
func (ts *TestState) runWaitScript(cont gwclient.Container, env []string, s script, timeout time.Duration) {
	ts2, cancel := ts.withTimeout(timeout)
	defer cancel()

	untilConnected, stdout, stderr := ts2.startContainer(cont, env, s)

	if err := untilConnected.Wait(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			ts2.t.Fatalf("Could not start server, timed out: %s", err)
		}

		ts2.t.Fatalf("could not check progress of server, container command failed: %s\nstdout:\n%s\n=====\nstderr:\n%s\n", err, stdout.String(), stderr.String())
	}
}

var (
	errContainerNoStart = errors.New("could not start server container")
	errContainerFailed  = errors.New("container process failed")
)

// runContainer runs a container in the background and sends errors to the returned channel
func (ts *TestState) runContainer(cont gwclient.Container, env []string, s script) <-chan error {

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
		ts.t.Fatal(errors.Join(errContainerNoStart, err))
	}

	// Log but do not fail, since you cannot fail from within a goroutine
	ec := make(chan error)
	go func() {
		if err := cp.Wait(); err != nil {
			ec <- errors.Join(errContainerFailed, err, fmt.Errorf("stdout:\n%s\n=====\nstderr:\n%s\n", stdout.String(), stderr.String()))
		}
	}()

	return ec
}

// Returns the list of env vars from an llb.State
func (ts *TestState) getStateEnv(st llb.State) []string {
	env, err := st.Env(ts.ctx)
	if err != nil {
		ts.t.Logf("unable to copy env: %s", err)
	}

	return env.ToArray()
}

// Overwrites/creates the `.ssh/authorized_keys` file in the specified
// `homedir` with the provided `pubkey` in the proper format.
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

// Copies the repo to its mountpoint.
func hostedRepo(repo llb.State, mountpoint string) llb.StateOption {
	return func(worker llb.State) llb.State {
		return worker.File(
			llb.Mkdir(mountpoint, 0o755, llb.WithParents(true)).
				Copy(repo, "/", mountpoint),
		)
	}
}

// `bareRepo` initializes a bare git repo from `repo`.
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
            git tag {{ .PrivateGoModuleGitTag }}
`,
	}

	return func(repo llb.State) llb.State {
		worker = worker.Dir(ts.attr.PrivateRepoAbsPath())

		return ts.runScriptOn(worker, repoScript).
			AddMount(attr.RepoAbsDir(), repo)
	}
}

// `startSSHAgent` creates an SSH agent on `sockaddr`, loaded up with
// `privkey`. The agent is started in the background and errors will be
// provided on the returned channel.
func startSSHAgent(t *testing.T, privkey crypto.PrivateKey, sockaddr string) <-chan error {
	ec := make(chan error)
	t.Cleanup(func() {
		close(ec)
	})

	kr := agent.NewKeyring()
	if err := kr.Add(agent.AddedKey{
		PrivateKey: privkey,
	}); err != nil {
		t.Fatalf("could not add private key to agent keyring: %s", err)
	}

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

// This is a helper function to find the filename of the download go module
// file. The containing directory will have a randomized filename.
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

// `updatedGitconfig` updatesd the gitconfig on the gomod worker. This is
// convoluted, but necessary. The `go` tool uses `git` under the hood to
// download go modules in response to an invocation of `go mod download`.
// Making such an invocation will cause go to open the `go.mod` file in the
// current directory and build a dependency graph of modules to download.
//
// A go module cannot have a URI that includes a port number. Go uses the
// standard HTTP/HTTPS/SSH ports of 80, 443, and 22 respenctively, to attempt
// to fetch a module. Root privileges would be required to bind to those port
// numbers, so we run our HTTP and SSH servers on nonstandard ports.
//
// Modifying the gitconfig as below will tell git to substitute
// http://host.com:port/ when it receives a request for a repository at
// http://host.com/ . That way, when go sees a module with URI path
// `host.com/module/name`, it will call `git` to look up the repository there.
// Git will first consult the gitconfig to see if there are any subsittutions,
// and will then make a request instead to http://host.com:<portnumber>/module/name .
func (ts *TestState) updatedGitconfig() llb.StateOption {
	s := script{
		basename: "update_gitconfig.sh",
		template: `
            git config --global "url.http://{{ .PrivateGomoduleHost }}:{{ .HTTPPort }}.insteadOf" "https://{{ .PrivateGomoduleHost }}"
            git config --global credential."http://{{ .PrivateGomoduleHost }}:{{ .HTTPPort }}.helper" "/usr/local/bin/frontend credential-helper --kind=token"
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
