package frontend

import (
	"context"
	"io"
	"io/fs"
	"path"
	"sync"

	gwclient "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/tonistiigi/fsutil/types"

	"github.com/tonistiigi/fsutil"

	"github.com/moby/buildkit/client/llb"
)

var _ fs.DirEntry = &stateRefDirEntry{}
var _ fs.ReadDirFS = &StateRefFS{}
var _ io.ReaderAt = &stateRefFile{}

type StateRefFS struct {
	s         llb.State
	ctx       context.Context
	opts      []llb.ConstraintsOpt
	client    gwclient.Client
	clientRes *gwclient.Result
	o         sync.Once
	initErr   error
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
		fs.clientRes = res
		fs.initErr = err
	})

	return fs.clientRes, fs.initErr
}

func (fs *StateRefFS) ref() (gwclient.Reference, error) {
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
	ref, err := st.ref()
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

func (s *stateRefFile) ReadAt(b []byte, off int64) (int, error) {
	if off < 0 {
		return 0, &fs.PathError{Op: "read", Path: s.path, Err: fs.ErrInvalid}
	}

	if off >= s.stat.Size_ {
		return 0, io.EOF
	}

	segmentContents, err := s.ref.ReadFile(s.ctx, gwclient.ReadRequest{
		Filename: s.path,
		Range:    &gwclient.FileRange{Offset: int(off), Length: len(b)},
	})
	if err != nil {
		return 0, err
	}

	n := copy(b, segmentContents)

	// ReaderAt is supposed to return a descriptive error when the number of bytes read is less than
	// the length of the input buffer
	if n < len(b) {
		err = io.EOF
	}

	return n, err
}

// invariant: s.offset is the offset of the next byte to be read
func (s *stateRefFile) Read(b []byte) (int, error) {
	n, err := s.ReadAt(b, s.offset)
	s.offset += int64(n)

	return n, err
}

func (s *stateRefFile) Stat() (fs.FileInfo, error) {
	info := &fsutil.StatInfo{
		Stat: s.stat,
	}

	return info, nil
}

func (st *StateRefFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, fs.ErrInvalid
	}

	ref, err := st.ref()
	if err != nil {
		return nil, err
	}

	stat, err := ref.StatFile(st.ctx, gwclient.StatRequest{
		Path: name,
	})
	if err != nil {
		return nil, &fs.PathError{Err: err, Op: "open", Path: name}
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
