package testenv

import (
	"strconv"
	"strings"

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
