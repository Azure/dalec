package dalec

import (
	goerrors "errors"

	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/pkg/errors"
)

// Target defines a distro-specific build target.
// This is used in [Spec] to specify the build target for a distro.
type Target struct {
	// Dependencies are the different dependencies that need to be specified in the package.
	Dependencies *PackageDependencies `yaml:"dependencies,omitempty" json:"dependencies,omitempty"`

	// Image is the image configuration when the target output is a container image.
	Image *ImageConfig `yaml:"image,omitempty" json:"image,omitempty"`

	// Frontend is the frontend configuration to use for the target.
	// This is used to forward the build to a different, dalec-compatible frontend.
	// This can be useful when testing out new distros or using a different version of the frontend for a given distro.
	Frontend *Frontend `yaml:"frontend,omitempty" json:"frontend,omitempty"`

	// Tests are the list of tests to run which are specific to the target.
	// Tests are appended to the list of tests in the main [Spec]
	Tests []*TestSpec `yaml:"tests,omitempty" json:"tests,omitempty"`

	// PackageConfig is the configuration to use for artifact targets, such as
	// rpms, debs, or zip files containing Windows binaries
	PackageConfig *PackageConfig `yaml:"package_config,omitempty" json:"package_config,omitempty"`

	// Artifacts describes all of the artifact configurations to include for this specific target.
	Artifacts *Artifacts `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
}

func (t *Target) validate() error {
	var errs []error
	if err := t.Dependencies.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "dependencies"))
	}

	if err := t.Image.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "image"))
	}

	for _, test := range t.Tests {
		if err := test.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "test %s", test.Name))
		}
	}

	if err := t.Image.validate(); err != nil {
		errs = append(errs, errors.Wrap(err, "postinstall"))
	}

	return goerrors.Join(errs...)
}

func (t *Target) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(string) bool) error {
	var errs []error
	for _, tt := range t.Tests {
		if err := tt.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, err)
		}
	}

	if t.PackageConfig != nil {
		if err := t.PackageConfig.processBuildArgs(lex, args, allowArg); err != nil {
			errs = append(errs, errors.Wrap(err, "package config"))
		}
	}

	if err := t.Image.processBuildArgs(lex, args, allowArg); err != nil {
		errs = append(errs, errors.Wrap(err, "package config"))
	}

	if err := t.Dependencies.processBuildArgs(lex, args, allowArg); err != nil {
		errs = append(errs, errors.Wrap(err, "dependencies"))
	}

	return goerrors.Join(errs...)
}

func (t *Target) fillDefaults() {
	t.Dependencies.fillDefaults()
	t.Image.fillDefaults()
}
