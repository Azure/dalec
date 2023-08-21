package frontend

import (
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	spb "github.com/moby/buildkit/sourcepolicy/pb"
)

// Spec is the specification for a package build.
type Spec struct {
	// Name is the name of the package.
	Name string
	// Description is a short description of the package.
	Description string
	// Website is the URL to store in the metadata of the package.
	Website string

	Version  string
	Revision string

	// Dependencies are the different dependencies that need to be specified in the package.
	Dependencies PackageDependencies

	// Conflicts is the list of packages that conflict with the generated package.
	// This will prevent the package from being installed if any of these packages are already installed or vice versa.
	Conflicts map[string][]string
	// Replaces is the list of packages that are replaced by the generated package.
	Replaces map[string][]string
	// Provides is the list of things that the generated package provides.
	// This can be used to satisfy dependencies of other packages.
	// As an example, the moby-runc package provides "runc", other packages could depend on "runc" and be satisfied by moby-runc.
	// This is an advanced use case and consideration should be taken to ensure that the package actually provides the thing it claims to provide.
	Provides []string

	// Sources is the list of sources to use to build the artifact(s).
	// The map key is the name of the source and the value is the source configuration.
	// The source configuration is used to fetch the source and filter the files to include/exclude.
	// This can be mounted into the build using the "Mounts" field in the StepGroup.
	//
	// Sources can be embedded in the main spec as here or overriden in a build request.
	Sources map[string]Source

	// BuildSteps is the list of build steps to run to build the artifact(s).
	// Each entry may be run in parallel and will not share state with each other.
	BuildSteps []StepGroup

	// SourcePolicy is used to approve/deny/rewrite sources used by a build.
	SourcePolicy *spb.Policy

	// Args is the list of arguments that can be used for shell-style expansion in (certain fields of) the spec.
	// Any arg supplied in the build request which does not appear in this list will cause an error.
	// Attempts to use an arg in the spec which is not specified here will assume to be a literal string.
	// The map value is the default value to use if the arg is not supplied in the build request.
	Args map[string]string

	License  string
	Vendor   string
	Packager string
}

// Source defines a source to be used in the build.
// A source can be a local directory, a git repositoryt, http(s) URL, etc.
type Source struct {
	// Ref is a unique identifier for the source.
	// example: "docker-image://busybox:latest", "https://github.com/moby/moby.git#master", "local://some/local/path
	Ref string
	// Path is the path to the source after fetching it based on the identifier.
	Path string
	// Filters is used to filter the files to include/exclude from beneath "Path".
	Filters
	// Satisfies is the list of build dependencies that this source satisfies.
	// This needs to match the name of the dependency in the
	// [PackageDependencies.Build] list.  You can specify multiple dependencies
	// that are satisfied by this source.  This will cause the output packaging
	// spec to elide the dependency from the package metadata but should include
	// the dependency in the build source.
	Satisfies []string
	// KeepGitDir is used to keep the .git directory after fetching the source for git references.
	KeepGitDir bool `yaml:"keep_git_dir"`
}

// PackageDependencies is a list of dependencies for a package.
// This will be included in the package metadata so that the package manager can install the dependencies.
// It also includes build-time dedendencies, which we'll install before running any build steps.
type PackageDependencies struct {
	// Build is the list of packagese required to build the package.
	Build map[string][]string
	// Runtime is the list of packages required to install/run the package.
	Runtime map[string][]string
	// Recommends is the list of packages recommended to install with the generated package.
	// Note: Not all package managers support this (e.g. rpm)
	Recommends map[string][]string
}

// StepGroup configures a group of steps that are run sequentially along with their outputs to build the artifact(s).
type StepGroup struct {
	// Steps is the list of commands to run to build the artifact(s).
	// Each step is run sequentially and will be cached accordingly.
	Steps []BuildStep
	// List of CacheDirs which will be used across all Steps
	CacheDirs map[string]CacheDirConfig
	// Outputs is the list of artifacts to be extracted after running the steps.
	Outputs map[string]ArtifactConfig
	// Mounts is the list of sources to mount into the build.
	// The map key is the name of the source to mount and the value is the path to mount it to.
	Mounts map[string]string
	// Workdir specifies the working directory that each new command will run in within this step group
	WorkDir string
	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string
}

// BuildStep is used to execute a command to build the artifact(s).
type BuildStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string
	// CacheDirs is the list of CacheDirs which will be used for this build step.
	// Note that this list will be merged with the list of CacheDirs from the StepGroup.
	CacheDirs map[string]CacheDirConfig
	// Env is the list of environment variables to set for the command.
	Env map[string]string
}

// CacheDirConfig configures a persistent cache to be used across builds.
type CacheDirConfig struct {
	// Mode is the locking mode to set on the cache directory
	// values: shared, private, locked
	// default: shared
	Mode string
	// Key is the cache key to use to cache the directory
	// default: Value of `Path`
	Key string
	// IncludeDistroKey is used to include the distro key as part of the cache key
	// What this key is depends on the frontend implementation
	// Example for Debian Buster may be "buster"
	IncludeDistroKey bool
	// IncludeArchKey is used to include the architecture key as part of the cache key
	// What this key is depends on the frontend implementation
	// Frontends SHOULD use the buildkit platform arch
	IncludeArchKey bool
}

type ArtifactType string

var (
	// ArtifactTypeExecutable is used to install a binary
	ArtifactTypeExecutable ArtifactType = "exe"
	// ArtifactTypeManpage is used to install a manpage document
	ArtifactTypeManpage ArtifactType = "manpage"
	// ArtifactTypeSystemdUnit is used to install a systemd unit file
	ArtifactTypeSystemdUnit ArtifactType = "systemd-unit"
	// ArtifactTypePreInst is used to run a script after installing the package
	ArtifactTypePostInst ArtifactType = "postinst"
	// ArtifactTypeContrib is used to install a contrib file
	ArtifactTypeContrib ArtifactType = "contrib"
	// TODO: others TBD, eg config files, licenses, notices, libexec, etc
)

// ArtifactConfig is used to configure how to extract an artifact and whatit is
type ArtifactConfig struct {
	// ArtifactType defines the type of artifact this is and will determine how to handle it in the package
	Type ArtifactType
	Filters
}

// Filters is used to filter the files to include/exclude from a directory.
type Filters struct {
	// Includes is a list of paths underneath `Path` to include, everything else is execluded
	// If empty, everything is included (minus the excludes)
	Includes []string
	// Excludes is a list of paths underneath `Path` to exclude, everything else is included
	Excludes []string
}

// LoadSpec loads a spec from the given data.
// env is a map of environment variables to use for shell-style expansion in the spec.
func LoadSpec(dt []byte, env map[string]string) (*Spec, error) {
	var spec Spec
	if err := yaml.Unmarshal(dt, &spec); err != nil {
		return nil, fmt.Errorf("error unmarshalling spec: %w", err)
	}

	lex := shell.NewLex('\\')

	args := make(map[string]string)
	for k, v := range spec.Args {
		args[k] = v
	}
	for k, v := range env {
		if _, ok := args[k]; !ok {
			return nil, fmt.Errorf("unknown arg %q", k)
		}
		args[k] = v
	}

	for name, src := range spec.Sources {
		updated, err := lex.ProcessWordWithMap(src.Ref, env)
		if err != nil {
			return nil, fmt.Errorf("error performing shell expansion on source ref %q: %w", name, err)
		}
		src.Ref = updated
		spec.Sources[name] = src
	}

	updated, err := lex.ProcessWordWithMap(spec.Version, args)
	if err != nil {
		return nil, fmt.Errorf("error performing shell expansion on version: %w", err)
	}
	spec.Version = updated

	updated, err = lex.ProcessWordWithMap(spec.Revision, args)
	if err != nil {
		return nil, fmt.Errorf("error performing shell expansion on revision: %w", err)
	}
	spec.Revision = updated

	return &spec, nil
}
