package distro

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestLocalPackageVersionOrder(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("requires bash")
	}
	if _, err := exec.LookPath("dpkg"); err != nil {
		t.Skip("requires native Debian comparison")
	}

	helpers, _, found := strings.Cut(installLocalScript, "\nset -euo pipefail\n")
	assert.Assert(t, found)

	cmd := exec.Command(bash, "-c", helpers+`
set -eu
versions=(unchanged)
sort_rank_versions 1.0~rc1-1 1.0-2 2:0.1-1 1.0-1 1.0-01 1.0-2
[ "${versions[*]}" = unchanged ]
[ -z "$(sort_rank_versions)" ]
`)
	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Equal(t, string(out), "2:0.1-1\n1.0-2\n1.0-1\n1.0~rc1-1\n")
}

func TestLocalPackageInstallerInvocation(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("requires bash")
	}

	for _, tc := range []struct {
		name        string
		fail        string
		provision   bool
		aptSuccess  bool
		emptyRows   bool
		cleanupFail bool
		failCode    int
	}{
		{name: "apt success", aptSuccess: true},
		{name: "fallback"},
		{name: "provision aptitude", provision: true},
		{name: "cleanup failure", provision: true, cleanupFail: true, failCode: 64},
		{name: "resolver failure", fail: "install", provision: true},
		{name: "resolver failure survives cleanup failure", fail: "install", provision: true, cleanupFail: true},
		{name: "versions failure", fail: "versions"},
		{name: "versions query or cache error", fail: "versions", failCode: 255},
		{name: "dpkg failure", fail: "dpkg"},
		{name: "no direct unmet versions", emptyRows: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.failCode == 0 {
				tc.failCode = 42
			}
			dir := t.TempDir()
			for _, file := range []string{"anchor.deb", "second.deb"} {
				assert.NilError(t, os.WriteFile(filepath.Join(dir, file), nil, 0o600))
			}

			// Stub native tools, not dependency solving. Exercise argv boundaries,
			// explicit failures, and cleanup without touching the host APT state.
			stubs := `
configure_apt_proxy() { :; }
cleanup_apt_proxy() { echo proxy-cleaned; }
rm() { :; }
command() {
	if [[ "$*" = "-v aptitude" ]]; then
		[[ "$PROVISION" = false ]]
	else
		builtin command "$@"
	fi
}
apt() {
	printf 'APT: <%s>\n' "$@"
	if [[ "$1" = install && "$3" != aptitude ]]; then
		[[ "$APT_SUCCESS" = true ]]
	elif [[ "$1" = remove ]]; then
		[[ "$CLEANUP_FAIL" != true ]] || return 64
	else
		return 0
	fi
}
dpkg() {
	printf 'DPKG: <%s>\n' "$@"
	[[ "$FAIL" != dpkg ]] || return 42
}
dpkg-deb() {
	local name="${2##*/}"
	case "$3" in
		Package) echo "${name%.deb}";;
		Version) echo 1;;
		Architecture)
			if [[ "$name" = anchor.deb ]]; then echo amd64; else echo all; fi;;
	esac
}
aptitude() {
	printf 'APTITUDE: <%s>\n' "$@" >&2
	[[ "$FAIL" != "$1" ]] || return "$FAIL_CODE"
	case "$1" in
		versions)
			if [[ "$EMPTY_ROWS" = false ]]; then
				printf 'provider 2+ all\nnative 2+ amd64\nforeign:arm64 2+ arm64\n'
			else
				return 2
			fi;;
	esac
	return 0
}
`

			cmd := exec.Command(bash, "-c", stubs+installLocalScript, "test-local", dir)
			cmd.Env = append(os.Environ(), "FAIL="+tc.fail,
				"FAIL_CODE="+strconv.Itoa(tc.failCode),
				"PROVISION="+strconv.FormatBool(tc.provision),
				"CLEANUP_FAIL="+strconv.FormatBool(tc.cleanupFail),
				"APT_SUCCESS="+strconv.FormatBool(tc.aptSuccess),
				"EMPTY_ROWS="+strconv.FormatBool(tc.emptyRows), "DALEC_UPGRADE=true")

			out, err := cmd.CombinedOutput()
			if tc.fail != "" || tc.cleanupFail {
				assert.ErrorType(t, err, (*exec.ExitError)(nil))
				assert.Equal(t, err.(*exec.ExitError).ExitCode(), tc.failCode)
			} else {
				assert.NilError(t, err, string(out))
			}
			for _, want := range []string{
				"proxy-cleaned", "APT: <dist-upgrade>",
				"APT: <" + filepath.Join(dir, "anchor.deb") + ">",
				"APT: <" + filepath.Join(dir, "second.deb") + ">",
			} {
				assert.Assert(t, strings.Contains(string(out), want), string(out))
			}
			assert.Equal(t, strings.Contains(string(out), "APT: <remove>"), tc.provision)
			if tc.aptSuccess {
				assert.Assert(t, !strings.Contains(string(out), "APTITUDE:"))
				assert.Assert(t, !strings.Contains(string(out), "DPKG:"))
				return
			}
			if tc.fail != "" || tc.cleanupFail {
				return
			}
			for _, want := range []string{
				"DPKG: <" + filepath.Join(dir, "anchor.deb") + ">",
				"DPKG: <" + filepath.Join(dir, "second.deb") + ">",
				"APTITUDE: <?broken-reverse-depends(?and(?installed,?or(?and(?exact-name(anchor),?architecture(amd64)),?and(?exact-name(second),?architecture(all)))))>",
				"APTITUDE: <install>",
				"APTITUDE: <Aptitude::ProblemResolver::Hints::=reject ?and(?exact-name(anchor),?architecture(amd64)) :UNINST>",
				"APTITUDE: <Aptitude::ProblemResolver::Hints::=reject ?and(?exact-name(second),?architecture(all)) :UNINST>",
				"APTITUDE: <Aptitude::ProblemResolver::Hints::=reject ?and(?exact-name(anchor),?architecture(amd64)) <1>",
				"APTITUDE: <Aptitude::ProblemResolver::Hints::=reject ?and(?exact-name(anchor),?architecture(amd64)) >1>",
				"APTITUDE: <Aptitude::ProblemResolver::Hints::=reject ?and(?exact-name(second),?architecture(all)) <1>",
				"APTITUDE: <Aptitude::ProblemResolver::Hints::=reject ?and(?exact-name(second),?architecture(all)) >1>",
			} {
				assert.Assert(t, strings.Contains(string(out), want), string(out))
			}
			for _, target := range []string{
				"?and(?exact-name(provider),?architecture(all))",
				"?and(?exact-name(native),?architecture(amd64))",
				"?and(?exact-name(foreign),?architecture(arm64))",
			} {
				want := "APTITUDE: <Aptitude::ProblemResolver::Hints::=-1 " + target + " 2+>"
				assert.Equal(t, strings.Contains(string(out), want), !tc.emptyRows, string(out))
			}
			assert.Assert(t, !strings.Contains(string(out), "provider=2+"))
			assert.Assert(t, !strings.Contains(string(out), "Aptitude::ProblemResolver::SolutionCost="))
		})
	}
}

func TestConfigureAptProxyWritesConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	conf := filepath.Join(t.TempDir(), "apt.conf")
	cmd := exec.Command("/bin/sh", "-c", aptProxyConfigScript+"\nconfigure_apt_proxy\nprintf '%s' \"${APT_CONFIG}\"\n")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy.example:3128",
		"HTTPS_PROXY=https://proxy.example:8443",
		"DALEC_APT_PROXY_CONFIG=" + conf,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Equal(t, string(out), conf)

	dt, err := os.ReadFile(conf)
	assert.NilError(t, err)

	aptConf := string(dt)
	assert.Assert(t, strings.Contains(aptConf, `Acquire::http::Proxy "http://proxy.example:3128";`), aptConf)
	assert.Assert(t, strings.Contains(aptConf, `Acquire::https::Proxy "https://proxy.example:8443";`), aptConf)

	info, err := os.Stat(conf)
	assert.NilError(t, err)
	assert.Equal(t, info.Mode().Perm(), os.FileMode(0o600))
}

func TestConfigureAptProxyDoesNotTraceProxyValues(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	conf := filepath.Join(t.TempDir(), "apt.conf")
	cmd := exec.Command("/bin/sh", "-c", "set -x\n"+aptProxyConfigScript+"\nconfigure_apt_proxy\n")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://user:secret@proxy.example:3128",
		"DALEC_APT_PROXY_CONFIG=" + conf,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Assert(t, !strings.Contains(string(out), "secret"), string(out))
}

func TestCleanupAptProxyRemovesConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	conf := filepath.Join(t.TempDir(), "apt.conf")
	cmd := exec.Command("/bin/sh", "-c", aptProxyConfigScript+"\nconfigure_apt_proxy\ncleanup_apt_proxy\nif [ -n \"${APT_CONFIG:-}\" ] || [ -e \"${DALEC_APT_PROXY_CONFIG}\" ]; then exit 1; fi\n")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_APT_PROXY_CONFIG=" + conf,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}

func TestCleanupAptProxyRestoresAptConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	dir := t.TempDir()
	conf := filepath.Join(dir, "apt.conf")
	original := filepath.Join(dir, "original.conf")
	cmd := exec.Command("/bin/sh", "-c", aptProxyConfigScript+"\nconfigure_apt_proxy\ncleanup_apt_proxy\nprintf '%s' \"${APT_CONFIG:-}\"\n")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"APT_CONFIG=" + original,
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_APT_PROXY_CONFIG=" + conf,
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
	assert.Equal(t, string(out), original)
}

func TestConfigureAptProxyDisabled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a POSIX shell")
	}

	conf := filepath.Join(t.TempDir(), "apt.conf")
	cmd := exec.Command("/bin/sh", "-c", aptProxyConfigScript+"\nconfigure_apt_proxy\nif [ -n \"${APT_CONFIG:-}\" ] || [ -e \"${DALEC_APT_PROXY_CONFIG}\" ]; then exit 1; fi\n")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HTTP_PROXY=http://proxy.example:3128",
		"DALEC_APT_PROXY_CONFIG=" + conf,
		"DALEC_DISABLE_PROXY_CONFIG=1",
	}

	out, err := cmd.CombinedOutput()
	assert.NilError(t, err, string(out))
}
