package dalec

import (
	"context"
	"io"
	"io/fs"
	"path"
	"strings"
	"sync"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/tonistiigi/fsutil/types"

	"github.com/tonistiigi/fsutil"

	"github.com/moby/buildkit/client/llb"
)

var _ fs.DirEntry = &stateRefDirEntry{}
var _ fs.ReadDirFS = new(StateRefFS)

// type FS interface {
// 	// Open opens the named file.
// 	//
// 	// When Open returns an error, it should be of type *PathError
// 	// with the Op field set to "open", the Path field set to name,
// 	// and the Err field describing the problem.
// 	//
// 	// Open should reject attempts to open names that do not satisfy
// 	// ValidPath(name), returning a *PathError with Err set to
// 	// ErrInvalid or ErrNotExist.
// 	Open(name string) (File, error)
// }

type StateRefFS struct {
	s       llb.State
	ctx     context.Context
	opts    []llb.ConstraintsOpt
	client  gwclient.Client
	res     *gwclient.Result
	o       sync.Once
	initErr error
}

func NewStateRefFS(s llb.State, ctx context.Context, client gwclient.Client) *StateRefFS {
	return &StateRefFS{
		s:      s,
		ctx:    ctx,
		client: client,
	}
}

func (fs *StateRefFS) fetchRef() (*gwclient.Result, error) {
	def, err := fs.s.Marshal(fs.ctx, fs.opts...)
	if err != nil {
		return nil, err
	}

	res, err := fs.client.Solve(fs.ctx, gwclient.SolveRequest{
		Definition: def.ToPB(),
	})
	if err != nil {
		return nil, err
	}

	return res, nil
}

func (fs *StateRefFS) Res() (*gwclient.Result, error) {
	fs.o.Do(func() {
		res, err := fs.fetchRef()
		fs.res = res
		fs.initErr = err
	})

	return fs.res, fs.initErr
}

func (fs *StateRefFS) Ref() (gwclient.Reference, error) {
	res, err := fs.Res()
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	return ref, nil
}

type stateRefDirEntry struct {
	stat *types.Stat
}

func (s *stateRefDirEntry) Name() string {
	return path.Base(s.stat.Path)
}

func (s *stateRefDirEntry) IsDir() bool {
	return s.stat.IsDir()
}

func (s *stateRefDirEntry) Type() fs.FileMode {
	return fs.FileMode(s.stat.Mode)
}

func (s *stateRefDirEntry) Info() (fs.FileInfo, error) {
	info := &fsutil.StatInfo{
		Stat: s.stat,
	}

	return info, nil
}

func (st *StateRefFS) ReadDir(name string) ([]fs.DirEntry, error) {
	ref, err := st.Ref()
	if err != nil {
		return nil, err
	}

	contents, err := ref.ReadDir(st.ctx, gwclient.ReadDirRequest{
		Path: name,
	})
	if err != nil {
		return nil, err
	}

	entries := []fs.DirEntry{}
	for _, stat := range contents {
		dirEntry := &stateRefDirEntry{stat: stat}
		entries = append(entries, dirEntry)
	}

	return entries, nil
}

func (st *StateRefFS) IsDir(name string) (bool, error) {
	ref, err := st.Ref()
	if err != nil {
		return false, err
	}
	_, err = ref.ReadDir(st.ctx, gwclient.ReadDirRequest{
		Path: name,
	})
	if err != nil {
		if strings.Contains(err.Error(), "not a directory") {
			return false, nil
		}
		return false, err
	}
	return true, err
}

type stateRefFile struct {
	eof    bool   // has file been read to EOF?
	path   string // the full path of the file from root
	ref    gwclient.Reference
	ctx    context.Context
	stat   *types.Stat
	offset int64
}

// close is a no-op
func (s *stateRefFile) Close() error {
	return nil
}

func (s *stateRefFile) Read(b []byte) (int, error) {
	if s.eof {
		return 0, io.EOF
	}

	// Could cache to avoid making read requests more than once
	segmentContents, err := s.ref.ReadFile(s.ctx, gwclient.ReadRequest{
		Filename: s.path,
		Range:    &gwclient.FileRange{Offset: int(s.offset), Length: len(b)},
	})
	if err != nil {
		return 0, err
	}

	s.offset += int64(len(segmentContents))
	if s.offset >= s.stat.Size_ {
		s.eof = true
		err = io.EOF
	}

	n := copy(b, segmentContents)
	return n, err
}

func (s *stateRefFile) Stat() (fs.FileInfo, error) {
	info := &fsutil.StatInfo{
		Stat: s.stat,
	}

	return info, nil
}

// TODO: handle malformed path and other error conditions
// according to conventions
func (st *StateRefFS) Open(name string) (fs.File, error) {
	ref, err := st.Ref()
	if err != nil {
		return nil, err
	}

	stat, err := ref.StatFile(st.ctx, gwclient.StatRequest{
		Path: name,
	})
	if err != nil {
		return nil, err
	}

	f := &stateRefFile{
		path:   name,
		ref:    ref,
		stat:   stat,
		ctx:    st.ctx,
		eof:    false,
		offset: 0,
	}

	return f, nil
}
