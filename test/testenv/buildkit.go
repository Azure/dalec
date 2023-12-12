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

// isSupported returns true if the buildkit instance allows you to pass LLB references as inputs to a solve request.
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
func isSupported(info *client.Info) bool {
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

func withGHCache(t *testing.T, so *client.SolveOpt) {
	if os.Getenv("GITHUB_ACTIONS") != "true" {
		return
	}

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
