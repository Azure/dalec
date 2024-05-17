//go:generate go run ./cmd/gen-jsonschema docs/spec.schema.json
package dalec

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"time"

	"github.com/opencontainers/go-digest"
)

// Spec is the specification for a package build.
type Spec struct {
	// Name is the name of the package.
	Name string `yaml:"name" json:"name" jsonschema:"required"`
	// Description is a short description of the package.
	Description string `yaml:"description" json:"description" jsonschema:"required"`
	// Website is the URL to store in the metadata of the package.
	Website string `yaml:"website" json:"website"`

	// Version sets the version of the package.
	Version string `yaml:"version" json:"version" jsonschema:"required"`
	// Revision sets the package revision.
	// This will generally get merged into the package version when generating the package.
	Revision string `yaml:"revision" json:"revision" jsonschema:"required,oneof_type=string;integer"`

	// Marks the package as architecture independent.
	// It is up to the package author to ensure that the package is actually architecture independent.
	// This is metadata only.
	NoArch bool `yaml:"noarch,omitempty" json:"noarch,omitempty"`

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
	Patches map[string][]PatchSpec `yaml:"patches,omitempty" json:"patches,omitempty"`

	// Build is the configuration for building the artifacts in the package.
	Build ArtifactBuild `yaml:"build,omitempty" json:"build,omitempty"`

	// Args is the list of arguments that can be used for shell-style expansion in (certain fields of) the spec.
	// Any arg supplied in the build request which does not appear in this list will cause an error.
	// Attempts to use an arg in the spec which is not specified here will assume to be a literal string.
	// The map value is the default value to use if the arg is not supplied in the build request.
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`

	// License is the license of the package.
	License string `yaml:"license" json:"license"`
	// Vendor is the vendor of the package.
	Vendor string `yaml:"vendor" json:"vendor"`
	// Packager is the name of the person,team,company that packaged the package.
	Packager string `yaml:"packager" json:"packager"`

	// Artifacts is the list of artifacts to include in the package.
	Artifacts Artifacts `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`

	// The list of distro targets to build the package for.
	Targets map[string]Target `yaml:"targets,omitempty" json:"targets,omitempty"`

	// Dependencies are the different dependencies that need to be specified in the package.
	// Dependencies are overwritten if specified in the target map for the requested distro.
	Dependencies *PackageDependencies `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`
	// PackageConfig is the configuration to use for artifact targets, such as
	// rpms, debs, or zip files containing Windows binaries
	PackageConfig *PackageConfig `yaml:"package_config,omitempty" json:"package_config,omitempty"`
	// Image is the image configuration when the target output is a container image.
	// This is overwritten if specified in the target map for the requested distro.
	Image *ImageConfig `yaml:"image,omitempty" json:"image,omitempty"`

	// Changelog is the list of changes to the package.
	Changelog []ChangelogEntry `yaml:"changelog,omitempty" json:"changelog,omitempty"`

	// Tests are the list of tests to run for the package that should work regardless of target OS
	// Each item in this list is run with a separate rootfs and cannot interact with other tests.
	// Each [TestSpec] is run with a separate rootfs, asyncronously from other [TestSpec].
	Tests []*TestSpec `yaml:"tests,omitempty" json:"tests,omitempty"`
}

// PatchSpec is used to apply a patch to a source with a given set of options.
// This is used in [Spec.Patches]
type PatchSpec struct {
	// Source is the name of the source that contains the patch to apply.
	Source string `yaml:"source" json:"source" jsonschema:"required"`
	// Strip is the number of leading path components to strip from the patch.
	// The default is 1 which is typical of a git diff.
	Strip *int `yaml:"strip,omitempty" json:"strip,omitempty"`
}

// ChangelogEntry is an entry in the changelog.
// This is used to generate the changelog for the package.
type ChangelogEntry struct {
	// Date is the date of the changelog entry.
	Date time.Time `yaml:"date" json:"date" jsonschema:"oneof_required=date"`
	// Author is the author of the changelog entry. e.g. `John Smith <john.smith@example.com>`
	Author string `yaml:"author" json:"author"`
	// Changes is the list of changes in the changelog entry.
	Changes []string `yaml:"changes" json:"changes"`
}

// Artifacts describes all the artifacts to include in the package.
// Artifacts are broken down into types, e.g. binaries, manpages, etc.
// This differentiation is used to determine where to place the artifact on install.
type Artifacts struct {
	// Binaries is the list of binaries to include in the package.
	Binaries map[string]ArtifactConfig `yaml:"binaries,omitempty" json:"binaries,omitempty"`
	// Manpages is the list of manpages to include in the package.
	Manpages map[string]ArtifactConfig `yaml:"manpages,omitempty" json:"manpages,omitempty"`
	// Directories is a list of various directories that should be created by the package.
	Directories *CreateArtifactDirectories `yaml:"createDirectories,omitempty" json:"createDirectories,omitempty"`
	// ConfigFiles is a list of files that should be marked as config files in the package.
	ConfigFiles map[string]ArtifactConfig `yaml:"configFiles,omitempty" json:"configFiles,omitempty"`
	// DocFiles is a list of doc files included in the package
	DocFiles []string `yaml:"docFiles,omitempty" json:"docFiles,omitempty"`
	// LicenseFiles is a list of doc files included in the package
	LicenseFiles []string `yaml:"licenseFiles,omitempty" json:"licenseFiles,omitempty"`
	// TODO: other types of artifacts (systtemd units, libexec, etc)
}

// CreateArtifactDirectories describes various directories that should be created on install.
// CreateArtifactDirectories represents different directory paths that are common to RPM systems.
type CreateArtifactDirectories struct {
	// Config is a list of directories the RPM should place under the system config directory (i.e. /etc)
	Config map[string]ArtifactDirConfig
	// State is a list of directories the RPM should place under the common directory for shared state and libs (i.e. /var/lib).
	State map[string]ArtifactDirConfig
}

// ArtifactDirConfig contains information about the directory to be created
type ArtifactDirConfig struct {
	// Mode is used to set the file permission bits of the final created directory to the specified mode.
	// Mode is the octal permissions to set on the dir.
	Mode fs.FileMode `yaml:"mode,omitempty" json:"mode,omitempty"`
}

// ArtifactConfig is the configuration for a given artifact type.
// This is used to customize where an artifact will be placed when installed.
type ArtifactConfig struct {
	// Subpath is the subpath to use in the package for the artifact type.
	//
	// As an example, binaries are typically placed in /usr/bin when installed.
	// If you want to nest them in a subdirectory, you can specify it here.
	SubPath string `yaml:"subpath,omitempty" json:"subpath,omitempty"`
	// Name is file or dir name to use for the artifact in the package.
	// If empty, the file or dir name from the produced artifact will be used.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// IsEmpty is used to determine if there are any artifacts to include in the package.
func (a *Artifacts) IsEmpty() bool {
	if len(a.Binaries) > 0 {
		return false
	}
	if len(a.Manpages) > 0 {
		return false
	}
	if a.Directories != nil && (len(a.Directories.Config) > 0 || len(a.Directories.State) > 0) {
		return false
	}
	if len(a.ConfigFiles) > 0 {
		return false
	}
	if len(a.DocFiles) > 0 {
		return false
	}
	return true
}

// PostInstall is the post install configuration for the image.
type PostInstall struct {
	// Symlinks is the list of symlinks to create in the container rootfs after the package(s) are installed.
	// The key is the path the symlink should point to.
	Symlinks map[string]SymlinkTarget `yaml:"symlinks,omitempty" json:"symlinks,omitempty"`
}

// SymlinkTarget specifies the properties of a symlink
type SymlinkTarget struct {
	// Path is the path where the symlink should be placed
	Path string `yaml:"path" json:"path" jsonschema:"required"`
}

type SourceDockerImage struct {
	Ref string   `yaml:"ref" json:"ref"`
	Cmd *Command `yaml:"cmd,omitempty" json:"cmd,omitempty"`
}

type SourceGit struct {
	URL        string `yaml:"url" json:"url"`
	Commit     string `yaml:"commit" json:"commit"`
	KeepGitDir bool   `yaml:"keepGitDir" json:"keepGitDir"`
}

// SourceHTTP is used to download a file from an HTTP(s) URL.
type SourceHTTP struct {
	// URL is the URL to download the file from.
	URL string `yaml:"url" json:"url"`
	// Digest is the digest of the file to download.
	// This is used to verify the integrity of the file.
	// Form: <algorithm>:<digest>
	Digest digest.Digest `yaml:"digest,omitempty" json:"digest,omitempty"`
}

// SourceContext is used to generate a source from a build context. The path to
// the build context is provided to the `Path` field of the owning `Source`.
type SourceContext struct {
	// Name is the name of the build context. By default, it is the magic name
	// `context`, recognized by Docker as the default context.
	Name string `yaml:"name,omitempty" json:"name,omitempty"`
}

// SourceInlineFile is used to specify the content of an inline source.
type SourceInlineFile struct {
	// Contents is the content.
	Contents string `yaml:"contents,omitempty" json:"contents,omitempty"`
	// Permissions is the octal file permissions to set on the file.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// UID is the user ID to set on the directory and all files and directories within it.
	// UID must be greater than or equal to 0
	UID int `yaml:"uid,omitempty" json:"uid,omitempty"`
	// GID is the group ID to set on the directory and all files and directories within it.
	// UID must be greater than or equal to 0
	GID int `yaml:"gid,omitempty" json:"gid,omitempty"`
}

// SourceInlineDir is used by by [SourceInline] to represent a filesystem directory.
type SourceInlineDir struct {
	// Files is the list of files to include in the directory.
	// The map key is the name of the file.
	//
	// Files with path separators in the key will be rejected.
	Files map[string]*SourceInlineFile `yaml:"files,omitempty" json:"files,omitempty"`
	// Permissions is the octal permissions to set on the directory.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`

	// UID is the user ID to set on the directory and all files and directories within it.
	// UID must be greater than or equal to 0
	UID int `yaml:"uid,omitempty" json:"uid,omitempty"`
	// GID is the group ID to set on the directory and all files and directories within it.
	// UID must be greater than or equal to 0
	GID int `yaml:"gid,omitempty" json:"gid,omitempty"`
}

// SourceInline is used to generate a source from inline content.
type SourceInline struct {
	// File is the inline file to generate.
	// File is treated as a literal single file.
	// [SourceIsDir] will return false when this is set.
	// This is mutally exclusive with [Dir]
	File *SourceInlineFile `yaml:"file,omitempty" json:"file,omitempty"`
	// Dir creates a directory with the given files and directories.
	// [SourceIsDir] will return true when this is set.
	// This is mutally exclusive with [File]
	Dir *SourceInlineDir `yaml:"dir,omitempty" json:"dir,omitempty"`
}

// Command is used to execute a command to generate a source from a docker image.
type Command struct {
	// Dir is the working directory to run the command in.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`

	// Mounts is the list of sources to mount into the build steps.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`

	// List of CacheDirs which will be used across all Steps
	CacheDirs map[string]CacheDirConfig `yaml:"cache_dirs,omitempty" json:"cache_dirs,omitempty"`

	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Steps is the list of commands to run to generate the source.
	// Steps are run sequentially and results of each step should be cached.
	Steps []*BuildStep `yaml:"steps" json:"steps" jsonschema:"required"`
}

// Source defines a source to be used in the build.
// A source can be a local directory, a git repositoryt, http(s) URL, etc.
type Source struct {
	// This is an embedded union representing all of the possible source types.
	// Exactly one must be non-nil, with all other cases being errors.
	//
	// === Begin Source Variants ===
	DockerImage *SourceDockerImage `yaml:"image,omitempty" json:"image,omitempty"`
	Git         *SourceGit         `yaml:"git,omitempty" json:"git,omitempty"`
	HTTP        *SourceHTTP        `yaml:"http,omitempty" json:"http,omitempty"`
	Context     *SourceContext     `yaml:"context,omitempty" json:"context,omitempty"`
	Build       *SourceBuild       `yaml:"build,omitempty" json:"build,omitempty"`
	Inline      *SourceInline      `yaml:"inline,omitempty" json:"inline,omitempty"`
	// === End Source Variants ===

	// Path is the path to the source after fetching it based on the identifier.
	Path string `yaml:"path,omitempty" json:"path,omitempty"`

	// Includes is a list of paths underneath `Path` to include, everything else is execluded
	// If empty, everything is included (minus the excludes)
	Includes []string `yaml:"includes,omitempty" json:"includes,omitempty"`
	// Excludes is a list of paths underneath `Path` to exclude, everything else is included
	Excludes []string `yaml:"excludes,omitempty" json:"excludes,omitempty"`

	// Generate is the list generators to run on the source.
	//
	// Generators are used to generate additional sources from this source.
	// As an example the `godmod` generator can be used to generate a go module cache from a go source.
	// How a genator operates is dependent on the actual generator.
	// Geneators may also cauuse modifications to the build environment.
	//
	// Currently only one generator is supported: "gomod"
	Generate []*SourceGenerator `yaml:"generate,omitempty" json:"generate,omitempty"`
}

// GeneratorGomod is used to generate a go module cache from go module sources
type GeneratorGomod struct {
}

// SourceGenerator holds the configuration for a source generator.
// This can be used inside of a [Source] to generate additional sources from the given source.
type SourceGenerator struct {
	// Subpath is the path inside a source to run the generator from.
	Subpath string `yaml:"subpath,omitempty" json:"subpath,omitempty"`

	// Gomod is the go module generator.
	Gomod *GeneratorGomod `yaml:"gomod" json:"gomod"`
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

	// Test lists any extra packages required for running tests
	// These packages are only installed for tests which have steps that require
	// running a command in the built container.
	// See [TestSpec] for more information.
	Test []string `yaml:"test,omitempty" json:"test,omitempty"`
}

// ArtifactBuild configures a group of steps that are run sequentially along with their outputs to build the artifact(s).
type ArtifactBuild struct {
	// Steps is the list of commands to run to build the artifact(s).
	// Each step is run sequentially and will be cached accordingly depending on the frontend implementation.
	Steps []BuildStep `yaml:"steps" json:"steps" jsonschema:"required"`
	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// BuildStep is used to execute a command to build the artifact(s).
type BuildStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string `yaml:"command" json:"command" jsonschema:"required"`
	// Env is the list of environment variables to set for the command.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// SourceMount is used to take a [Source] and mount it into a build step.
type SourceMount struct {
	// Dest is the destination directory to mount to
	Dest string `yaml:"dest" json:"dest" jsonschema:"required"`
	// Spec specifies the source to mount
	Spec Source `yaml:"spec" json:"spec" jsonschema:"required"`
}

// CacheDirConfig configures a persistent cache to be used across builds.
type CacheDirConfig struct {
	// Mode is the locking mode to set on the cache directory
	// values: shared, private, locked
	// default: shared
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty" jsonschema:"enum=shared,enum=private,enum=locked"`
	// Key is the cache key to use to cache the directory
	// default: Value of `Path`
	Key string `yaml:"key,omitempty" json:"key,omitempty"`
	// IncludeDistroKey is used to include the distro key as part of the cache key
	// What this key is depends on the frontend implementation
	// Example for Debian Buster may be "buster"
	//
	// An example use for this is with a Go(lang) build cache when CGO is included.
	// Go is unable to invalidate cgo and re-using the same cache across different distros may cause issues.
	IncludeDistroKey bool `yaml:"include_distro_key,omitempty" json:"include_distro_key,omitempty"`
	// IncludeArchKey is used to include the architecture key as part of the cache key
	// What this key is depends on the frontend implementation
	// Frontends SHOULD use the buildkit platform arch
	//
	// As with [IncludeDistroKey], this is useful for Go(lang) builds with CGO.
	IncludeArchKey bool `yaml:"include_arch_key,omitempty" json:"include_arch_key,omitempty"`
}

// Frontend encapsulates the configuration for a frontend to forward a build target to.
type Frontend struct {
	// Image specifies the frontend image to forward the build to.
	// This can be left unspecified *if* the original frontend has builtin support for the distro.
	//
	// If the original frontend does not have builtin support for the distro, this must be specified or the build will fail.
	// If this is specified then it MUST be used.
	Image string `yaml:"image,omitempty" json:"image,omitempty" jsonschema:"required,example=docker.io/my/frontend:latest"`
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

	// Tests are the list of tests to run which are specific to the target.
	// Tests are appended to the list of tests in the main [Spec]
	Tests []*TestSpec `yaml:"tests,omitempty" json:"tests,omitempty"`

	// PackageConfig is the configuration to use for artifact targets, such as
	// rpms, debs, or zip files containing Windows binaries
	PackageConfig *PackageConfig `yaml:"package_config,omitempty" json:"package_config,omitempty"`
}

// PackageConfig encapsulates the configuration for artifact targets
type PackageConfig struct {
	// Signer is the configuration to use for signing packages
	Signer *Frontend `yaml:"signer,omitempty" json:"signer,omitempty"`
}

// TestSpec is used to execute tests against a container with the package installed in it.
type TestSpec struct {
	// Name is the name of the test
	// This will be used to output the test results
	Name string `yaml:"name" json:"name" jsonschema:"required"`

	// Dir is the working directory to run the command in.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`

	// Mounts is the list of sources to mount into the build steps.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`

	// List of CacheDirs which will be used across all Steps
	CacheDirs map[string]CacheDirConfig `yaml:"cache_dirs,omitempty" json:"cache_dirs,omitempty"`

	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// Steps is the list of commands to run to test the package.
	Steps []TestStep `yaml:"steps" json:"steps" jsonschema:"required"`

	// Files is the list of files to check after running the steps.
	Files map[string]FileCheckOutput `yaml:"files,omitempty" json:"files,omitempty"`
}

// TestStep is a wrapper for [BuildStep] to include checks on stdio streams
type TestStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string `yaml:"command" json:"command" jsonschema:"required"`
	// Env is the list of environment variables to set for the command.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
	// Stdout is the expected output on stdout
	Stdout CheckOutput `yaml:"stdout,omitempty" json:"stdout,omitempty"`
	// Stderr is the expected output on stderr
	Stderr CheckOutput `yaml:"stderr,omitempty" json:"stderr,omitempty"`
	// Stdin is the input to pass to stdin for the command
	Stdin string `yaml:"stdin,omitempty" json:"stdin,omitempty"`
}

// CheckOutput is used to specify the exepcted output of a check, such as stdout/stderr or a file.
// All non-empty fields will be checked.
type CheckOutput struct {
	// Equals is the exact string to compare the output to.
	Equals string `yaml:"equals,omitempty" json:"equals,omitempty"`
	// Contains is the list of strings to check if they are contained in the output.
	Contains []string `yaml:"contains,omitempty" json:"contains,omitempty"`
	// Matches is the regular expression to match the output against.
	Matches string `yaml:"matches,omitempty" json:"matches,omitempty"`
	// StartsWith is the string to check if the output starts with.
	StartsWith string `yaml:"starts_with,omitempty" json:"starts_with,omitempty"`
	// EndsWith is the string to check if the output ends with.
	EndsWith string `yaml:"ends_with,omitempty" json:"ends_with,omitempty"`
	// Empty is used to check if the output is empty.
	Empty bool `yaml:"empty,omitempty" json:"empty,omitempty"`
}

// IsEmpty is used to determine if there are any checks to perform.
func (c CheckOutput) IsEmpty() bool {
	return c.Equals == "" && len(c.Contains) == 0 && c.Matches == "" && c.StartsWith == "" && c.EndsWith == "" && !c.Empty
}

// Check is used to check the output stream.
func (c CheckOutput) Check(dt string, p string) (retErr error) {
	if c.Empty {
		if dt != "" {
			return &CheckOutputError{Kind: "empty", Expected: "", Actual: dt, Path: p}
		}

		// Anything else would be nonsensical and it would make sense to return early...
		// But we'll check it anyway and it should fail since this would be an invalid CheckOutput
	}

	if c.Equals != "" && c.Equals != dt {
		return &CheckOutputError{Expected: c.Equals, Actual: dt, Path: p}
	}

	for _, contains := range c.Contains {
		if contains != "" && !strings.Contains(dt, contains) {
			return &CheckOutputError{Kind: "contains", Expected: contains, Actual: dt, Path: p}
		}
	}
	if c.Matches != "" {
		regexp, err := regexp.Compile(c.Matches)
		if err != nil {
			return err
		}

		if !regexp.Match([]byte(dt)) {
			return &CheckOutputError{Kind: "matches", Expected: c.Matches, Actual: dt, Path: p}
		}
	}

	if c.StartsWith != "" && !strings.HasPrefix(dt, c.StartsWith) {
		return &CheckOutputError{Kind: "starts_with", Expected: c.StartsWith, Actual: dt, Path: p}
	}

	if c.EndsWith != "" && !strings.HasSuffix(dt, c.EndsWith) {
		return &CheckOutputError{Kind: "ends_with", Expected: c.EndsWith, Actual: dt, Path: p}
	}

	return nil
}

// FileCheckOutput is used to specify the expected output of a file.
type FileCheckOutput struct {
	CheckOutput `yaml:",inline"`
	// Permissions is the expected permissions of the file.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// IsDir is used to set the expected file mode to a directory.
	IsDir bool `yaml:"is_dir,omitempty" json:"is_dir,omitempty"`
	// NotExist is used to check that the file does not exist.
	NotExist bool `yaml:"not_exist,omitempty" json:"not_exist,omitempty"`

	// TODO: Support checking symlinks
	// This is not currently possible with buildkit as it does not expose information about the symlink
}

// Check is used to check the output file.
func (c FileCheckOutput) Check(dt string, mode fs.FileMode, isDir bool, p string) error {
	if c.IsDir && !isDir {
		return &CheckOutputError{Kind: "mode", Expected: "ModeDir", Actual: "ModeFile", Path: p}
	}

	if !c.IsDir && isDir {
		return &CheckOutputError{Kind: "mode", Expected: "ModeFile", Actual: "ModeDir", Path: p}
	}

	if c.Permissions != 0 && c.Permissions != mode {
		return &CheckOutputError{Kind: "permissions", Expected: c.Permissions.String(), Actual: mode.String(), Path: p}
	}

	return c.CheckOutput.Check(dt, p)
}

// CheckOutputError is used to build an error message for a failed output check for a test case.
type CheckOutputError struct {
	Kind     string
	Expected string
	Actual   string
	Path     string
}

func (c *CheckOutputError) Error() string {
	return fmt.Sprintf("expected %q %s %q, got %q", c.Path, c.Kind, c.Expected, c.Actual)
}
