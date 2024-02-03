package dalec

import (
	goerrors "errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/moby/buildkit/client/llb"
	"github.com/pkg/errors"
)

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

func (s *SourceInline) validate(subpath string) (retErr error) {
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
		if subpath != "" {
			errs = append(errs, errors.New("inline file source cannot have a path set"))
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
			errs = append(errs, errors.Errorf("file name %q must not contain path separator", k))
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

func (s *SourceInlineFile) Doc(w io.Writer, name string) {
	fmt.Fprintln(w, `	cat << EOF > `+name+`
`+s.Contents+`
	EOF`)

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
	fmt.Fprintf(w, "	chmod %o %s\n", perms.Perm(), name)
}

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
