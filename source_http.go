package dalec

import (
	stderrors "errors"
	"fmt"
	"io"
	"io/fs"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/frontend/dockerfile/shell"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

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

func (src *SourceHTTP) validate(opts fetchOptions) error {
	var errs []error

	if src.URL == "" {
		errs = append(errs, errors.New("http source must have a URL"))
	}
	if src.Digest != "" {
		if err := src.Digest.Validate(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(opts.Excludes) > 0 {
		errs = append(errs, errors.New("http source cannot be used with excludes"))
	}
	if len(opts.Includes) > 0 {
		errs = append(errs, errors.New("http source cannot be used with includes"))
	}
	if opts.Path != "" {
		errs = append(errs, errors.New("http source cannot be used with path"))
	}

	if len(errs) > 0 {
		return stderrors.Join(errs...)
	}

	return nil
}

func (src *SourceHTTP) AsState(name string, opts ...llb.ConstraintsOpt) (llb.State, error) {
	httpOpts := []llb.HTTPOption{withConstraints(opts)}
	httpOpts = append(httpOpts, llb.Filename(name))
	if src.Digest != "" {
		httpOpts = append(httpOpts, llb.Checksum(src.Digest))
	}

	if src.Permissions != 0 {
		httpOpts = append(httpOpts, llb.Chmod(src.Permissions))
	}

	st := llb.HTTP(src.URL, httpOpts...)
	return st, nil
}

func (src *SourceHTTP) toState(opts fetchOptions) llb.State {
	var httpOpts []llb.HTTPOption

	httpOpts = append(httpOpts, WithConstraints(opts.Constraints...))
	if src.Digest != "" {
		httpOpts = append(httpOpts, llb.Checksum(src.Digest))
	}

	if src.Permissions != 0 {
		httpOpts = append(httpOpts, llb.Chmod(src.Permissions))
	}

	if opts.Rename != "" {
		httpOpts = append(httpOpts, llb.Filename(opts.Rename))
	}
	return llb.HTTP(src.URL, httpOpts...)
}

func (src *SourceHTTP) IsDir() bool {
	return false
}

func (src *SourceHTTP) toMount(to string, opts fetchOptions, mountOpts ...llb.MountOption) llb.RunOption {
	st := src.toState(opts)
	mountOpts = append(mountOpts, llb.SourcePath(opts.Rename))
	return llb.AddMount(to, st, mountOpts...)
}

func (src *SourceHTTP) fillDefaults() {}

func (src *SourceHTTP) processBuildArgs(lex *shell.Lex, args map[string]string, allowArg func(key string) bool) error {
	updated, err := expandArgs(lex, src.URL, args, allowArg)
	if err != nil {
		return errors.Wrap(err, "failed to expand HTTP URL")
	}
	src.URL = updated
	return nil
}

func (src *SourceHTTP) doc(w io.Writer, name string) {
	fmt.Fprintln(w, "Generated from a http(s) source:")
	fmt.Fprintln(w, "	URL:", src.URL)
}
