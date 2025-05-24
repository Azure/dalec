package dalec

import (
	goerrors "errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

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

func (src *SourceInline) AsState(name string) (llb.State, error) {
	if src.File != nil {
		return llb.Scratch().With(src.File.PopulateAt(name)), nil
	}
	return llb.Scratch().With(src.Dir.PopulateAt("/")), nil
}

func (src *SourceInline) IsDir() bool {
	return src.Dir != nil
}

const (
	defaultFilePerms = 0o644
	defaultDirPerms  = 0o755
)

func (d *SourceInlineDir) PopulateAt(p string) llb.StateOption {
	return func(st llb.State) llb.State {
		perms := d.Permissions.Perm()
		if perms == 0 {
			perms = defaultDirPerms
		}

		st = st.File(llb.Mkdir(p, perms, llb.WithUIDGID(int(d.UID), int(d.GID))))

		sorted := SortMapKeys(d.Files)
		for _, k := range sorted {
			f := d.Files[k]
			st = st.With(f.PopulateAt(filepath.Join(p, k)))
		}

		return st
	}
}

func (f *SourceInlineFile) PopulateAt(p string) llb.StateOption {
	return func(st llb.State) llb.State {
		perms := f.Permissions.Perm()
		if perms == 0 {
			perms = defaultFilePerms
		}

		return st.File(
			llb.Mkfile(p, perms, []byte(f.Contents), llb.WithUIDGID(int(f.UID), int(f.GID))),
		)
	}
}

func (s *SourceInline) validate(opts fetchOptions) (retErr error) {
	var errs []error

	if s.File == nil && s.Dir == nil {
		errs = append(errs, errors.New("inline source is missing contents to inline"))
	}

	if s.File != nil && s.Dir != nil {
		errs = append(errs, errors.New("inline source variant cannot have both a file and dir set"))
	}

	if s.Dir != nil {
		if err := s.Dir.validate(); err != nil {
			errs = append(errs, err)
		}
	}

	if s.File != nil {
		if opts.Path != "" {
			errs = append(errs, errors.New("inline file source cannot have a path set"))
		}
		if opts.Includes != nil {
			errs = append(errs, errors.New("inline file source cannot have includes set"))
		}
		if opts.Excludes != nil {
			errs = append(errs, errors.New("inline file source cannot have excludes set"))
		}
		if err := s.File.validate(); err != nil {
			errs = append(errs, err)
		}
	}

	return goerrors.Join(errs...)
}

func (s *SourceInlineDir) validate() error {
	var errs []error

	if s.UID < 0 {
		errs = append(errs, errors.Errorf("uid %d must be non-negative", s.UID))
	}

	if s.GID < 0 {
		errs = append(errs, errors.Errorf("gid %d must be non-negative", s.GID))
	}

	for k, f := range s.Files {
		if strings.ContainsRune(k, os.PathSeparator) {
			errs = append(errs, errors.Wrapf(errSourceNamePathSeparator, "file %q", k))
		}
		if err := f.validate(); err != nil {
			errs = append(errs, errors.Wrapf(err, "file %q", k))
		}
	}
	return goerrors.Join(errs...)
}

func (s *SourceInlineFile) validate() error {
	var errs []error

	if s.UID < 0 {
		errs = append(errs, errors.Errorf("uid %d must be non-negative", s.UID))
	}

	if s.GID < 0 {
		errs = append(errs, errors.Errorf("gid %d must be non-negative", s.GID))
	}

	return goerrors.Join(errs...)
}

func (s *SourceInline) Doc(w io.Writer, name string) {
	if s.File != nil {
		s.File.Doc(w, name)
	}

	if s.Dir != nil {
		s.Dir.Doc(w, name)
	}
}

// Doc writes the information about the file to the writer.
//
//nolint:errcheck // ignore error check for simplicity, don't pass in a writer that can error on write.
func (s *SourceInlineFile) Doc(w io.Writer, name string) {
	fmt.Fprintln(w, `	cat << EOF > `+name+`
`+s.Contents+`
	EOF`)

	if s.UID != 0 {
		fmt.Fprintln(w, `	chown `+strconv.Itoa(s.UID)+" "+name) //nolint:errcheck
	}
	if s.GID != 0 {
		fmt.Fprintln(w, `	chgrp `+strconv.Itoa(s.GID)+" "+name)
	}

	perms := s.Permissions.Perm()
	if perms == 0 {
		perms = 0o644
	}
	fmt.Fprintf(w, "	chmod %o %s\n", perms.Perm(), name)
}

// Doc writes the information about the directory to the writer.
//
//nolint:errcheck // ignore error check for simplicity, don't pass in a writer that can error on write.
func (s *SourceInlineDir) Doc(w io.Writer, name string) {
	fmt.Fprintln(w, `	mkdir -p `+name)

	if s.UID != 0 {
		fmt.Fprintln(w, `	chown `+strconv.Itoa(s.UID)+" "+name)
	}
	if s.GID != 0 {
		fmt.Fprintln(w, `	chgrp `+strconv.Itoa(s.GID)+" "+name)
	}

	perms := s.Permissions.Perm()
	if perms == 0 {
		perms = 0o644
	}

	sorted := SortMapKeys(s.Files)
	for _, k := range sorted {
		v := s.Files[k]
		v.Doc(w, filepath.Join(name, k))
	}

	fmt.Fprintf(w, "	chmod %o %s\n", perms.Perm(), name)
}

func (s *SourceInline) toState(opts fetchOptions) llb.State {
	if s.File != nil {
		return llb.Scratch().With(s.File.toState(opts))
	}
	return s.Dir.toState(opts)
}

func (s *SourceInline) toMount(to string, opts fetchOptions, mountOpts ...llb.MountOption) llb.RunOption {
	if s.File != nil {
		return s.File.toMount(to, opts, mountOpts...)
	}
	return s.Dir.toMount(to, opts, mountOpts...)
}

func (s *SourceInlineFile) toState(opts fetchOptions) llb.StateOption {
	return func(in llb.State) llb.State {
		st := in.File(llb.Mkfile(opts.Rename, s.Permissions, []byte(s.Contents), llb.WithUIDGID(int(s.UID), int(s.GID))))
		return st
	}
}

func (s *SourceInlineFile) toMount(to string, opts fetchOptions, mountOpts ...llb.MountOption) llb.RunOption {
	mountOpts = append(mountOpts, llb.SourcePath(opts.Rename))
	st := llb.Scratch().With(s.toState(opts))
	return llb.AddMount(to, st, mountOpts...)
}

func (s *SourceInlineDir) toState(opts fetchOptions) llb.State {
	return s.baseState(opts).With(sourceFilters(opts))
}

func (s *SourceInlineDir) baseState(opts fetchOptions) llb.State {
	st := llb.Scratch().File(llb.Mkdir(opts.Rename, s.Permissions, llb.WithUIDGID(int(s.UID), int(s.GID))))
	sorted := SortMapKeys(s.Files)
	for _, k := range sorted {
		f := s.Files[k]
		opts := opts
		opts.Rename = k
		st = st.With(f.toState(opts))
	}
	return st
}

func (s *SourceInlineDir) toMount(to string, opts fetchOptions, mountOpts ...llb.MountOption) llb.RunOption {
	st := s.toState(opts).With(mountFilters(opts))
	mountOpts = append(mountOpts, llb.SourcePath(opts.Rename))
	return llb.AddMount(to, st, mountOpts...)
}
