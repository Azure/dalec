//go:generate go run ./cmd/gen-jsonschema ./spec.schema.json
package dalec

import (
	"fmt"

	"github.com/goccy/go-yaml"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
)

// Spec is the specification for a package build.
type Spec struct {
	// Name is the name of the package.
	Name string `yaml:"name" json:"name" jsonschema:"required"`
	// Description is a short description of the package.
	Description string `yaml:"description" json:"description" jsonschema:"required"`
	// Website is the URL to store in the metadata of the package.
	Website string `yaml:"website" json:"website"`

	Version  string `yaml:"version" json:"version" jsonschema:"required"`
	Revision string `yaml:"revision" json:"revision" jsonschema:"required"`

	// Marks the package as architecture independent.
	// It is up to the package author to ensure that the package is actually architecture independent.
	// This is metadata only.
	NoArch bool `yaml:"noarch" json:"noarch"`

	// Conflicts is the list of packages that conflict with the generated package.
	// This will prevent the package from being installed if any of these packages are already installed or vice versa.
	Conflicts map[string][]string `yaml:"conflicts,omitempty" json:"conflicts,omitempty"`
	// Replaces is the list of packages that are replaced by the generated package.
	Replaces map[string][]string `yaml:"replaces,omitempty" json:"replaces,omitempty"`
	// Provides is the list of things that the generated package provides.
	// This can be used to satisfy dependencies of other packages.
	// As an example, the moby-runc package provides "runc", other packages could depend on "runc" and be satisfied by moby-runc.
	// This is an advanced use case and consideration should be taken to ensure that the package actually provides the thing it claims to provide.
	Provides []string `yaml:"provides,omitempty" json:"provides,omitempty"`

	// Sources is the list of sources to use to build the artifact(s).
	// The map key is the name of the source and the value is the source configuration.
	// The source configuration is used to fetch the source and filter the files to include/exclude.
	// This can be mounted into the build using the "Mounts" field in the StepGroup.
	//
	// Sources can be embedded in the main spec as here or overriden in a build request.
	Sources map[string]Source `yaml:"sources,omitempty" json:"sources,omitempty"`

	// Patches is the list of patches to apply to the sources.
	// The map key is the name of the source to apply the patches to.
	// The value is the list of patches to apply to the source.
	// The patch must be present in the `Sources` map.
	// Each patch is applied in order and the result is used as the source for the build.
	Patches map[string][]string `yaml:"patches,omitempty" json:"patches,omitempty"`

	// Build is the configuration for building the artifacts in the package.
	Build ArtifactBuild `yaml:"build,omitempty" json:"build,omitempty"`

	// Args is the list of arguments that can be used for shell-style expansion in (certain fields of) the spec.
	// Any arg supplied in the build request which does not appear in this list will cause an error.
	// Attempts to use an arg in the spec which is not specified here will assume to be a literal string.
	// The map value is the default value to use if the arg is not supplied in the build request.
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`

	License  string `yaml:"license" json:"license"`
	Vendor   string `yaml:"vendor" json:"vendor"`
	Packager string `yaml:"packager" json:"packager"`

	// Artifacts is the list of artifacts to include in the package.
	Artifacts Artifacts `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// The list of distro targets to build the package for.
	Targets map[string]Target `yaml:"targets,omitempty" json:"targets,omitempty"`

	// Dependencies are the different dependencies that need to be specified in the package.
	// Dependencies are overwritten if specified in the target map for the requested distro.
	Dependencies *PackageDependencies `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	// Image is the image configuration when the target output is a container image.
	// This is overwritten if specified in the target map for the requested distro.
	Image *ImageConfig `yaml:"image,omitempty" json:"image,omitempty"`
}

type Artifacts struct {
	// NOTE: Using a struct as a map value for future expansion
	Binaries map[string]ArtifactConfig `yaml:"binaries" json:"binaries"`
	Manpages map[string]ArtifactConfig `yaml:"manpages" json:"manpages"`
	// TODO: other types of artifacts (systtemd units, libexec, configs, etc)
	// NOTE: When other artifact types are added, you must also update [ArtifactsConfig.IsEmpty]
}

type ArtifactConfig struct {
	// Subpath is the subpath to use in the package for the artifact type.
	//
	// As an example, binaries are typically placed in /usr/bin when installed.
	// If you want to nest them in a subdirectory, you can specify it here.
	SubPath string `yaml:"subpath" json:"subpath"`
	// Name is file or dir name to use for the artifact in the package.
	// If empty, the file or dir name from the produced artifact will be used.
	Name string `yaml:"name" json:"name"`
}

// IsEmpty is used to determine if there are any artifacts to include in the package.
func (a *Artifacts) IsEmpty() bool {
	if len(a.Binaries) > 0 {
		return false
	}
	if len(a.Manpages) > 0 {
		return false
	}
	return true
}

type ImageConfig struct {
	Entrypoint []string            `yaml:"entrypoint" json:"entrypoint"`
	Cmd        []string            `yaml:"cmd" json:"cmd"`
	Env        []string            `yaml:"env" json:"env"`
	Labels     map[string]string   `yaml:"labels" json:"labels"`
	Volumes    map[string]struct{} `yaml:"volumes" json:"volumes"`
	WorkingDir string              `yaml:"working_dir" json:"working_dir"`
	StopSignal string              `yaml:"stop_signal" json:"stop_signal"`
	// Base is the base image to use for the output image.
	// This only affects the output image, not the build image.
	Base string `yaml:"base" json:"base"`
}

// Source defines a source to be used in the build.
// A source can be a local directory, a git repositoryt, http(s) URL, etc.
type Source struct {
	// Ref is a unique identifier for the source.
	// example: "docker-image://busybox:latest", "https://github.com/moby/buildkit.git#master", "local://some/local/path
	Ref string `yaml:"ref" json:"ref" jsonschema:"required"`
	// Path is the path to the source after fetching it based on the identifier.
	Path string `yaml:"path" json:"path"`

	// Includes is a list of paths underneath `Path` to include, everything else is execluded
	// If empty, everything is included (minus the excludes)
	Includes []string `yaml:"includes" json:"includes"`
	// Excludes is a list of paths underneath `Path` to exclude, everything else is included
	Excludes []string `yaml:"excludes" json:"excludes"`

	// KeepGitDir is used to keep the .git directory after fetching the source for git references.
	KeepGitDir bool `yaml:"keep_git_dir" json:"keep_git_dir"`

	// Cmd is used to generate the source from a command.
	// This is used when `Ref` is "cmd://"
	// If ref is "cmd://", this is required.
	Cmd *CmdSpec `yaml:"cmd,omitempty" json:"cmd,omitempty"`

	// Build is used to generate source from a build.
	// This is used when [Ref]` is "build://"
	// The context for the build is assumed too be specified in after `build://` in the ref, e.g. `build://https://github.com/moby/buildkit.git#master`
	// When nothing is specified after `build://`, the context is assumed to be the current build context.
	Build *BuildSpec `yaml:"build,omitempty" json:"build,omitempty"`
}

// BuildSpec is used to generate source from a build.
// This is used when [Source.Ref] is "build://" to forward a build (aka a nested build) through to buildkit.
type BuildSpec struct {
	// Target specifies the build target to use.
	// If unset, the default target is determined by the frontend implementation (e.g. the dockerfile frontend uses the last build stage as the default).
	Target string `yaml:"target" json:"target"`
	// Args are the build args to pass to the build.
	Args map[string]string `yaml:"args" json:"args"`
	// File is the path to the build file in the b uild context
	// If not set the default is assumed by buildkit to be `Dockerfile` at the root of the context.
	// This is exclusive with [Inline]
	File string `yaml:"file" json:"file"`

	// Inline is an inline build spec to use.
	// This can be used to specify a dockerfile instead of using one in the build context
	// This is exclusive with [File]
	Inline string `yaml:"inline" json:"inline"`
}

// PackageDependencies is a list of dependencies for a package.
// This will be included in the package metadata so that the package manager can install the dependencies.
// It also includes build-time dedendencies, which we'll install before running any build steps.
type PackageDependencies struct {
	// Build is the list of packagese required to build the package.
	Build map[string][]string `yaml:"build,omitempty" json:"build,omitempty"`
	// Runtime is the list of packages required to install/run the package.
	Runtime map[string][]string `yaml:"runtime,omitempty" json:"runtime,omitempty"`
	// Recommends is the list of packages recommended to install with the generated package.
	// Note: Not all package managers support this (e.g. rpm)
	Recommends map[string][]string `yaml:"recommends,omitempty" json:"recommends,omitempty"`
}

// ArtifactBuild configures a group of steps that are run sequentially along with their outputs to build the artifact(s).
type ArtifactBuild struct {
	// Steps is the list of commands to run to build the artifact(s).
	// Each step is run sequentially and will be cached accordingly depending on the frontend implementation.
	Steps []BuildStep `yaml:"steps" json:"steps" jsonschema:"required"`
	// List of CacheDirs which will be used across all Steps
	CacheDirs map[string]CacheDirConfig `yaml:"cache_dirs,omitempty" json:"cache_dirs,omitempty"`
	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// BuildStep is used to execute a command to build the artifact(s).
type BuildStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string `yaml:"command" json:"command" jsonschema:"required"`
	// CacheDirs is the list of CacheDirs which will be used for this build step.
	// Note that this list will be merged with the list of CacheDirs from the StepGroup.
	CacheDirs map[string]CacheDirConfig `yaml:"cache_dirs,omitempty" json:"cache_dirs,omitempty"`
	// Env is the list of environment variables to set for the command.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

type SourceMount struct {
	// Path is the destination directory to mount to
	Path string `yaml:"path" json:"path" jsonschema:"required"`
	// Copy is used to copy the source into the destination directory rather than mount it
	Copy bool `yaml:"copy" json:"copy"`
	// Spec specifies the source to mount
	Spec Source `yaml:"spec" json:"spec" jsonschema:"required"`
}

type CmdSpec struct {
	// Dir is the working directory to run the command in.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`
	// Sources is the list of sources to mount into the build steps.
	Sources []SourceMount `yaml:"sources,omitempty" json:"sources,omitempty"`

	// List of CacheDirs which will be used across all Steps
	CacheDirs map[string]CacheDirConfig `yaml:"cache_dirs,omitempty" json:"cache_dirs,omitempty"`
	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	Steps []*BuildStep `yaml:"steps" json:"steps" jsonschema:"required"`
}

// CacheDirConfig configures a persistent cache to be used across builds.
type CacheDirConfig struct {
	// Mode is the locking mode to set on the cache directory
	// values: shared, private, locked
	// default: shared
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// Key is the cache key to use to cache the directory
	// default: Value of `Path`
	Key string `yaml:"key,omitempty" json:"key,omitempty"`
	// IncludeDistroKey is used to include the distro key as part of the cache key
	// What this key is depends on the frontend implementation
	// Example for Debian Buster may be "buster"
	IncludeDistroKey bool `yaml:"include_distro_key" json:"include_distro_key,omitempty"`
	// IncludeArchKey is used to include the architecture key as part of the cache key
	// What this key is depends on the frontend implementation
	// Frontends SHOULD use the buildkit platform arch
	IncludeArchKey bool `yaml:"include_arch_key" json:"include_arch_key,omitempty"`
}

func knownArg(key string) bool {
	switch key {
	case "BUILDKIT_SYNTAX":
		return true
	default:
		return false
	}
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
			if !knownArg(k) {
				return nil, fmt.Errorf("unknown arg %q", k)
			}
		}
		args[k] = v
	}

	for name, src := range spec.Sources {
		updated, err := lex.ProcessWordWithMap(src.Ref, args)
		if err != nil {
			return nil, fmt.Errorf("error performing shell expansion on source ref %q: %w", name, err)
		}
		src.Ref = updated
		if src.Cmd != nil {
			for i, smnt := range src.Cmd.Sources {
				updated, err := lex.ProcessWordWithMap(smnt.Spec.Ref, args)
				if err != nil {
					return nil, fmt.Errorf("error performing shell expansion on source ref %q: %w", name, err)
				}
				src.Cmd.Sources[i].Spec.Ref = updated
			}
			for k, v := range src.Cmd.Env {
				updated, err := lex.ProcessWordWithMap(v, args)
				if err != nil {
					return nil, fmt.Errorf("error performing shell expansion on env var %q for source %q: %w", k, name, err)
				}
				src.Cmd.Env[k] = updated
			}
			for i, step := range src.Cmd.Steps {
				for k, v := range step.Env {
					updated, err := lex.ProcessWordWithMap(v, args)
					if err != nil {
						return nil, fmt.Errorf("error performing shell expansion on env var %q for source %q: %w", k, name, err)
					}
					step.Env[k] = updated
					src.Cmd.Steps[i] = step
				}
			}
		}

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

	for k, v := range spec.Build.Env {
		updated, err := lex.ProcessWordWithMap(v, args)
		if err != nil {
			return nil, fmt.Errorf("error performing shell expansion on env var %q: %w", k, err)
		}
		spec.Build.Env[k] = updated
	}

	for name, step := range spec.Build.Steps {
		for k, v := range step.Env {
			updated, err := lex.ProcessWordWithMap(v, args)
			if err != nil {
				return nil, fmt.Errorf("error performing shell expansion on env var %q for step %q: %w", k, name, err)
			}
			step.Env[k] = updated
		}
	}
	return &spec, nil
}

// Frontend encapsulates the configuration for a frontend to forward a build target to.
type Frontend struct {
	// Image specifies the frontend image to forward the build to.
	// This can be left unspecified *if* the original frontend has builtin support for the distro.
	//
	// If the original frontend does not have builtin support for the distro, this must be specified or the build will fail.
	// If this is specified then it MUST be used.
	Image string `yaml:"image,omitempty" json:"image,omitempty" jsonschema:"required"`
	// CmdLine is the command line to use to forward the build to the frontend.
	// By default the frontend image's entrypoint/cmd is used.
	CmdLine string `yaml:"cmdline,omitempty" json:"cmdline,omitempty"`
}

// Target defines a distro-specific build target.
// This is used in [Spec] to specify the build target for a distro.
type Target struct {
	// Dependencies are the different dependencies that need to be specified in the package.
	Dependencies *PackageDependencies `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`

	// Image is the image configuration when the target output is a container image.
	Image *ImageConfig `yaml:"image,omitempty" json:"image,omitempty"`

	// Frontend is the frontend configuration to use for the target.
	// This is used to forward the build to a different, dalec-compatabile frontend.
	// This can be useful when testing out new distros or using a different version of the frontend for a given distro.
	Frontend *Frontend `yaml:"frontend,omitempty" json:"frontend,omitempty"`
}
