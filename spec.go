//go:generate go run ./cmd/gen-jsonschema docs/spec.schema.json
package dalec

import (
	"encoding/json"
	"io/fs"
	"strings"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"
	"github.com/moby/buildkit/client/llb"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
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
	Conflicts map[string]PackageConstraints `yaml:"conflicts,omitempty" json:"conflicts,omitempty"`
	// Replaces is the list of packages that are replaced by the generated package.
	Replaces map[string]PackageConstraints `yaml:"replaces,omitempty" json:"replaces,omitempty"`
	// Provides is the list of things that the generated package provides.
	// This can be used to satisfy dependencies of other packages.
	// As an example, the moby-runc package provides "runc", other packages could depend on "runc" and be satisfied by moby-runc.
	// This is an advanced use case and consideration should be taken to ensure that the package actually provides the thing it claims to provide.
	Provides map[string]PackageConstraints `yaml:"provides,omitempty" json:"provides,omitempty"`

	// Sources is the list of sources to use to build the artifact(s).
	// The map key is the name of the source and the value is the source configuration.
	// The source configuration is used to fetch the source and filter the files to include/exclude.
	// This can be mounted into the build using the "Mounts" field in the StepGroup.
	//
	// Sources can be embedded in the main spec as here or overridden in a build request.
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
	Vendor string `yaml:"vendor,omitempty" json:"vendor,omitempty"`
	// Packager is the name of the person,team,company that packaged the package.
	Packager string `yaml:"packager,omitempty" json:"packager,omitempty"`

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
	// Each [TestSpec] is run with a separate rootfs, asynchronously from other [TestSpec].
	Tests []*TestSpec `yaml:"tests,omitempty" json:"tests,omitempty"`

	extensions extensionFields `yaml:"-" json:"-"`

	decodeOpts []yaml.DecodeOption `yaml:"-" json:"-"`
}

type extensionFields map[string]rawYAML

// PatchSpec is used to apply a patch to a source with a given set of options.
// This is used in [Spec.Patches]
type PatchSpec struct {
	// Source is the name of the source that contains the patch to apply.
	Source string `yaml:"source" json:"source" jsonschema:"required"`
	// Strip is the number of leading path components to strip from the patch.
	// The default is 1 which is typical of a git diff.
	Strip *int `yaml:"strip,omitempty" json:"strip,omitempty"`
	// Optional subpath to the patch file inside the source
	// This is only useful for directory-backed sources.
	Path string `yaml:"path,omitempty" json:"path,omitempty"`
}

// ChangelogEntry is an entry in the changelog.
// This is used to generate the changelog for the package.
type ChangelogEntry struct {
	// Date is the date of the changelog entry.
	// Dates are formatted as YYYY-MM-DD
	Date Date `yaml:"date" json:"date" jsonschema:"oneof_required=date"`
	// Author is the author of the changelog entry. e.g. `John Smith <john.smith@example.com>`
	Author string `yaml:"author" json:"author"`
	// Changes is the list of changes in the changelog entry.
	Changes []string `yaml:"changes" json:"changes"`
}

type Date struct {
	time.Time
}

func (d Date) Compare(other Date) int {
	return d.Time.Compare(other.Time)
}

func (d Date) MarshalYAML() ([]byte, error) {
	return yaml.Marshal(d.String())
}

func (d *Date) UnmarshalYAML(dt []byte) error {
	var s string
	if err := yaml.Unmarshal(dt, &s); err != nil {
		return errors.Wrap(err, "error unmarshalling date to string")
	}
	parsedTime, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return err
	}
	d.Time = parsedTime
	return nil
}

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *Date) UnmarshalJSON(dt []byte) error {
	var s string
	if err := json.Unmarshal(dt, &s); err != nil {
		return err
	}
	parsedTime, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return err
	}
	d.Time = parsedTime
	return nil
}

func (d Date) String() string {
	return d.Format(time.DateOnly)
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
	//
	// Deprecated: This is here for backward compatibility. Use `Paths` instead.
	Path string `yaml:"path" json:"path" jsonschema:"oneof_required=path"`
	// Path is a list of `newpath`s that will all point to the same `oldpath`.
	Paths []string `yaml:"paths" json:"paths" jsonschema:"oneof_required=paths"`
	// User is the user name to set on the symlink.
	User string `yaml:"user,omitempty" json:"user,omitempty"`
	// Group is the group name to set on the symlink.
	Group string `yaml:"group,omitempty" json:"group,omitempty"`
}

type SourceDockerImage struct {
	Ref string   `yaml:"ref" json:"ref"`
	Cmd *Command `yaml:"cmd,omitempty" json:"cmd,omitempty"`
}

type SourceGit struct {
	URL        string  `yaml:"url" json:"url"`
	Commit     string  `yaml:"commit" json:"commit"`
	KeepGitDir bool    `yaml:"keepGitDir,omitempty" json:"keepGitDir,omitempty"`
	Auth       GitAuth `yaml:"auth,omitempty" json:"auth,omitempty"`
}

type GitAuth struct {
	// Header is the name of the secret which contains the git auth header.
	// when using git auth header based authentication.
	// Note: This should not have the *actual* secret value, just the name of
	// the secret which was specified as a build secret.
	Header string `yaml:"header,omitempty" json:"header,omitempty"`
	// Token is the name of the secret which contains a git auth token when using
	// token based authentication.
	// Note: This should not have the *actual* secret value, just the name of
	// the secret which was specified as a build secret.
	Token string `yaml:"token,omitempty" json:"token,omitempty"`
	// SSH is the name of the secret which contains the ssh auth into when using
	// ssh based auth.
	// Note: This should not have the *actual* secret value, just the name of
	// the secret which was specified as a build secret.
	SSH string `yaml:"ssh,omitempty" json:"ssh,omitempty"`
}

// LLBOpt returns an [llb.GitOption] which sets the auth header and token secret
// values in LLB if they are set.
func (a *GitAuth) LLBOpt() llb.GitOption {
	return gitOptionFunc(func(gi *llb.GitInfo) {
		if a == nil {
			return
		}

		if a.Header != "" {
			gi.AuthHeaderSecret = a.Header
		}

		if a.Token != "" {
			gi.AuthTokenSecret = a.Token
		}

		if a.SSH != "" {
			gi.MountSSHSock = a.SSH
		}
	})
}

// SourceHTTP is used to download a file from an HTTP(s) URL.
type SourceHTTP struct {
	// URL is the URL to download the file from.
	URL string `yaml:"url" json:"url"`
	// Digest is the digest of the file to download.
	// This is used to verify the integrity of the file.
	// Form: <algorithm>:<digest>
	Digest digest.Digest `yaml:"digest,omitempty" json:"digest,omitempty"`
	// Permissions is the octal file permissions to set on the file.
	Permissions fs.FileMode `yaml:"permissions,omitempty" json:"permissions,omitempty"`
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
	// This is mutually exclusive with [Dir]
	File *SourceInlineFile `yaml:"file,omitempty" json:"file,omitempty"`
	// Dir creates a directory with the given files and directories.
	// [SourceIsDir] will return true when this is set.
	// This is mutually exclusive with [File]
	Dir *SourceInlineDir `yaml:"dir,omitempty" json:"dir,omitempty"`
}

// Command is used to execute a command to generate a source from a docker image.
type Command struct {
	// Dir is the working directory to run the command in.
	Dir string `yaml:"dir,omitempty" json:"dir,omitempty"`

	// Mounts is the list of sources to mount into the build steps.
	Mounts []SourceMount `yaml:"mounts,omitempty" json:"mounts,omitempty"`

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
	// As an example the `gomod` generator can be used to generate a go module cache from a go source.
	// How a generator operates is dependent on the actual generator.
	// Generators may also cauuse modifications to the build environment.
	//
	// Currently only two generators are supported: "gomod" and "cargohome".
	// The "gomod" generator will generate a go module cache from the source.
	// The "cargohome" generator will generate a cargo home from the source.
	Generate []*SourceGenerator `yaml:"generate,omitempty" json:"generate,omitempty"`
}

// GeneratorGomod is used to generate a go module cache from go module sources
type GeneratorGomod struct {
	// Paths is the list of paths to run the generator on. Used to generate multi-module in a single source.
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`
}

// GeneratorCargohome is used to generate a cargo home from cargo sources
type GeneratorCargohome struct {
	// Paths is the list of paths to run the generator on. Used to generate multi-module in a single source.
	Paths []string `yaml:"paths,omitempty" json:"paths,omitempty"`
}

type GeneratorYarnNodeMod struct{}

// SourceGenerator holds the configuration for a source generator.
// This can be used inside of a [Source] to generate additional sources from the given source.
type SourceGenerator struct {
	// Subpath is the path inside a source to run the generator from.
	Subpath string `yaml:"subpath,omitempty" json:"subpath,omitempty"`

	// Gomod is the go module generator.
	Gomod *GeneratorGomod `yaml:"gomod" json:"gomod"`

	// Cargohome is the cargo home generator.
	Cargohome *GeneratorCargohome `yaml:"cargohome" json:"cargohome"`

	// YarnNodeMod is the yarn node module generator.
	YarnNodeMod *GeneratorYarnNodeMod `yaml:"yarnmod" json:"yarnmod"`
}

// ArtifactBuild configures a group of steps that are run sequentially along with their outputs to build the artifact(s).
type ArtifactBuild struct {
	// Steps is the list of commands to run to build the artifact(s).
	// Each step is run sequentially and will be cached accordingly depending on the frontend implementation.
	Steps []BuildStep `yaml:"steps" json:"steps" jsonschema:"required"`
	// Env is the list of environment variables to set for all commands in this step group.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`

	// NetworkMode sets the network mode to use during the build phase.
	// Accepted values: none, sandbox
	// Default: none
	NetworkMode string `yaml:"network_mode,omitempty" json:"network_mode,omitempty" jsonschema:"enum=none,enum=sandbox"`

	// Caches is the list of caches to use for the build.
	// These apply to all steps.
	Caches []CacheConfig `yaml:"caches,omitempty" json:"caches,omitempty"`
}

// BuildStep is used to execute a command to build the artifact(s).
type BuildStep struct {
	// Command is the command to run to build the artifact(s).
	// This will always be wrapped as /bin/sh -c "<command>", or whatever the equivalent is for the target distro.
	Command string `yaml:"command" json:"command" jsonschema:"required"`
	// Env is the list of environment variables to set for the command.
	Env map[string]string `yaml:"env,omitempty" json:"env,omitempty"`
}

// SourceMount wraps a [Source] with a target mount point.
type SourceMount struct {
	// Dest is the destination directory to mount to
	Dest string `yaml:"dest" json:"dest" jsonschema:"required"`
	// Spec specifies the source to mount
	Spec Source `yaml:"spec" json:"spec" jsonschema:"required"`
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

// PackageSigner is the configuration for defining how to sign a package
type PackageSigner struct {
	*Frontend `yaml:",inline" json:",inline"`
	// Args are passed along to the signer frontend as build args
	Args map[string]string `yaml:"args,omitempty" json:"args,omitempty"`
}

// PackageConfig encapsulates the configuration for artifact targets
type PackageConfig struct {
	// Signer is the configuration to use for signing packages
	Signer *PackageSigner `yaml:"signer,omitempty" json:"signer,omitempty"`
}

func (s *SystemdConfiguration) IsEmpty() bool {
	if s == nil {
		return true
	}

	if len(s.Units) == 0 {
		return true
	}

	return false
}

func (s *SystemdConfiguration) EnabledUnits() map[string]SystemdUnitConfig {
	if len(s.Units) == 0 {
		return nil
	}

	units := make(map[string]SystemdUnitConfig)
	for path, unit := range s.Units {
		if unit.Enable {
			units[path] = unit
		}
	}

	return units
}

type ExtDecodeConfig struct {
	AllowUnknownFields bool
}

var (
	ErrNodeNotFound  = errors.New("node not found")
	ErrInvalidExtKey = errors.New("extension keys must start with \"x-\"")
)

// Ext reads the extension field from the spec and unmarshals it into the target
// value.
func (s *Spec) Ext(key string, target interface{}, opts ...func(*ExtDecodeConfig)) error {
	v, ok := s.extensions[key]
	if !ok {
		return errors.Wrapf(ErrNodeNotFound, "extension field not found %q", key)
	}

	var yamlOpts []yaml.DecodeOption
	if len(opts) > 0 {
		var cfg ExtDecodeConfig
		for _, opt := range opts {
			opt(&cfg)
		}

		if !cfg.AllowUnknownFields {
			yamlOpts = append(yamlOpts, yaml.Strict())
		}
	}

	return yaml.UnmarshalWithOptions(v, target, yamlOpts...)
}

// WithExtension adds an extension field to the spec.
// If the value is set to a []byte, it is used as-is and is expected to already
// be in YAML format.
func (s *Spec) WithExtension(key string, value interface{}) error {
	if !strings.HasPrefix(key, "x-") && !strings.HasPrefix(key, "X-") {
		return errors.Wrap(ErrInvalidExtKey, key)
	}

	if s.extensions == nil {
		s.extensions = make(extensionFields)
	}

	dt, ok := value.([]byte)
	if ok {
		_, err := parser.ParseBytes(dt, parseModeIgnoreComments)
		if err != nil {
			return errors.Wrap(err, "extension value provided is a []byte but is not valid YAML")
		}
		s.extensions[key] = dt
		return nil
	}

	dt, err := yaml.Marshal(value)
	if err != nil {
		return errors.Wrapf(err, "failed to marshal extension field %q", key)
	}
	s.extensions[key] = dt
	return nil
}
