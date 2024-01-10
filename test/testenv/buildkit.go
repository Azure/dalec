package testenv

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/moby/buildkit/client"
)

type buildkitVersion struct {
	Major int
	Minor int
}

func (v buildkitVersion) String() string {
	return strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor)
}

var (
	minVersion = buildkitVersion{0, 12}
)

// supportsFrontendAsInput returns true if the buildkit instance allows you to pass LLB references as inputs to a solve request.
// This would be needed when testing custom frontends separate from the main one.
//
// More info:
// Buildkit treats the frontend ref (`#syntax=<ref>` or via the BUILDKIT_SYNTAX
// var) as a docker image ref.
// Buildkit will always check the remote registry for a new version of the image.
// As of buildkit v0.12 you can use named contexts to ovewrite the frontend ref
// with another type of ref.
// This can be another docker-image, an oci-layout, or even a frontend "input"
// (like feeding the output of a build into another build).
// Here we are checking the version of buildkit to determine what method we can
// use.
func supportsFrontendAsInput(info *client.Info) bool {
	majorStr, minorPatchStr, ok := strings.Cut(strings.TrimPrefix(info.BuildkitVersion.Version, "v"), ".")
	if !ok {
		return false
	}

	major, err := strconv.Atoi(majorStr)
	if err != nil {
		return false
	}

	if major < minVersion.Major {
		return false
	}

	if major > minVersion.Major {
		return true
	}

	minorStr, _, _ := strings.Cut(minorPatchStr, ".")

	minor, err := strconv.Atoi(minorStr)
	if err != nil {
		return false
	}

	return minor >= minVersion.Minor
}

// withGHCache adds the necessary cache export and import options to the solve request in order to use the GitHub Actions cache.
// It uses the test name as a scope for the cache. Each test will have its own scope.
// This means that caches are not shared between tests, but it also means that tests won't ovewrite each other's cache.
//
// Github Actions sets some specific environment variables that we'll look for to even determine if we should configure the cache or not.
//
// This is effectively what `docker build --cache-from=gha,scope=foo --cache-to=gha,mode=max,scope=foo` would do.
func withGHCache(t *testing.T, so *client.SolveOpt) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		// This is not running in GitHub Actions, so we don't need to configure the cache.
		return
	}

	// token and url are required for the cache to work.
	// These need to be exposed as environment variables in the GitHub Actions workflow.
	// See the crazy-max/ghaction-github-runtime@v3 action.
	token := os.Getenv("ACTIONS_RUNTIME_TOKEN")
	if token == "" {
		fmt.Fprintln(os.Stderr, "::warning::GITHUB_ACTIONS_RUNTIME_TOKEN is not set, skipping cache export")
		return
	}

	url := os.Getenv("ACTIONS_CACHE_URL")
	if url == "" {
		fmt.Fprintln(os.Stderr, "::warning::ACTIONS_CACHE_URL is not set, skipping cache export")
		return
	}

	scope := "test-integration-" + t.Name()
	so.CacheExports = append(so.CacheExports, client.CacheOptionsEntry{
		Type: "gha",
		Attrs: map[string]string{
			"scope": scope,
			"mode":  "max",
			"token": token,
			"url":   url,
		},
	})
	so.CacheImports = append(so.CacheImports, client.CacheOptionsEntry{
		Type: "gha",
		Attrs: map[string]string{
			"scope": scope,
			"token": token,
			"url":   url,
		},
	})
}
