package test

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"testing"

	"github.com/Azure/dalec"
	"github.com/stretchr/testify/assert"

	"github.com/moby/buildkit/client/llb"
	gwclient "github.com/moby/buildkit/frontend/gateway/client"
)

func TestStateWrapper_Open(t *testing.T) {
	st := llb.Scratch().
		File(llb.Mkfile("/foo", 0644, []byte("hello world")))

	testEnv.RunTest(context.Background(), t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		fs := dalec.NewStateRefFS(st, context.Background(), gwc)
		f, err := fs.Open("/foo")
		if err != nil {
			t.Fatal(err)
		}

		b := make([]byte, 11)
		n, err := f.Read(b)
		assert.Nil(t, err)
		assert.Equal(t, n, 11)

		return fs.Res()
	})
}

func TestStateWrapper_Stat(t *testing.T) {
	st := llb.Scratch().File(llb.Mkfile("/foo", 0755, []byte("contents")))
	testEnv.RunTest(context.Background(), t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		rfs := dalec.NewStateRefFS(st, context.Background(), gwc)
		f, err := rfs.Open("/foo")
		assert.Nil(t, err)

		info, err := f.Stat()
		assert.Nil(t, err)

		assert.Equal(t, info.IsDir(), false)
		assert.Equal(t, info.Mode(), fs.FileMode(0755))
		assert.Equal(t, info.Size(), int64(len([]byte("contents"))))
		assert.Equal(t, info.Name(), "foo")

		return rfs.Res()
	})
}

func TestStateWrapper_ReadDir(t *testing.T) {
	st := llb.Scratch().File(llb.Mkdir("/bar", 0644)).
		File(llb.Mkfile("/bar/foo", 0644, []byte("file contents"))).
		File(llb.Mkdir("/bar/baz", 0644))
	var expectInfo = map[string]struct {
		perms    fs.FileMode
		isDir    bool
		contents []byte
	}{
		"/bar/foo": {
			perms:    0644,
			isDir:    false,
			contents: []byte("file contents"),
		},

		"/bar/baz": {
			perms: fs.ModeDir | 0644,
			isDir: true,
		},
	}

	testEnv.RunTest(context.Background(), t, func(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
		rfs := dalec.NewStateRefFS(st, ctx, c)
		root := "/bar"
		entries, err := rfs.ReadDir(root)
		assert.Nil(t, err)

		fmt.Println(entries)
		for _, e := range entries {
			p := path.Join(root, e.Name())
			want, ok := expectInfo[p]
			assert.True(t, ok)

			info, err := e.Info()
			assert.Nil(t, err)

			assert.Equal(t, want.perms, info.Mode())
			assert.Equal(t, want.perms.String(), info.Mode().String())
			assert.Equal(t, want.isDir, info.IsDir())
		}

		return rfs.Res()
	})

}

func TestStateWrapper_Walk(t *testing.T) {
	// create a simple test file structure like so:
	/*
		dir/
			a/
				b/
					ab.txt
			c/
				d/
					e/
						f123.txt
			h.exe
	*/
	st := llb.Scratch().File(llb.Mkdir("/dir", 0644)).
		File(llb.Mkdir("/dir/a", 0644)).
		File(llb.Mkdir("/dir/a/b", 0644)).
		File(llb.Mkfile("/dir/a/b/ab.txt", 0644, []byte("ab.txt contents"))).
		File(llb.Mkdir("/dir/c", 0644)).
		File(llb.Mkdir("/dir/c/d", 0644)).
		File(llb.Mkdir("/dir/c/d/e", 0644)).
		File(llb.Mkfile("/dir/c/d/e/f123.txt", 0644, []byte("f123.txt contents"))).
		File(llb.Mkfile("h.exe", 0755, []byte("h.exe contents")))

	var expectInfo = map[string]struct {
		perms    fs.FileMode
		isDir    bool
		contents []byte
	}{
		"/dir": {
			perms: fs.ModeDir | 0644,
			isDir: true,
		},
		"/dir/a": {
			perms: fs.ModeDir | 0644,
			isDir: true,
		},
		"/dir/a/b": {
			isDir: true,
			perms: fs.ModeDir | 0644,
		},
		"/dir/a/b/ab.txt": {
			isDir:    false,
			perms:    0644,
			contents: []byte("ab.txt contents"),
		},
		"/dir/c": {
			isDir: true,
			perms: fs.ModeDir | 0644,
		},
		"/dir/c/d": {
			isDir: true,
			perms: fs.ModeDir | 0644,
		},
		"/dir/c/d/e": {
			isDir: true,
			perms: fs.ModeDir | 0644,
		},
		"/dir/c/d/e/f123.txt": {
			isDir:    false,
			perms:    0644,
			contents: []byte("f123.txt contents"),
		},
		"/h.exe": {
			isDir:    false,
			perms:    0755,
			contents: []byte("h.exe contents"),
		},
	}

	testEnv.RunTest(context.Background(), t, func(ctx context.Context, gwc gwclient.Client) (*gwclient.Result, error) {
		rfs := dalec.NewStateRefFS(st, context.Background(), gwc)
		err := fs.WalkDir(rfs, "/", func(path string, d fs.DirEntry, err error) error {
			if path == "/" {
				return nil
			}

			if err != nil {
				return err
			}
			fmt.Println(path)

			expect, ok := expectInfo[path]
			assert.True(t, ok)

			info, err := d.Info()
			assert.Nil(t, err)
			assert.Equal(t, expect.perms, info.Mode())
			assert.Equal(t, expect.isDir, info.IsDir())

			if !d.IsDir() {
				fmt.Println("opening ", path)
				fmt.Println(d.Name())
				f, err := rfs.Open(path)
				assert.Nil(t, err)

				contents, err := io.ReadAll(f)
				if err != nil {
					return err
				}
				assert.Equal(t, contents, expect.contents)
				fmt.Println(contents)
			}

			return nil
		})

		assert.Nil(t, err)
		return rfs.Res()
	})

}

func TestStateWrapper_ReadPartial(t *testing.T) {
	contents := []byte(`
		This is a
		multline
		file
	`)
	st := llb.Scratch().File(llb.Mkfile("/foo", 0644, contents))

	testEnv.RunTest(context.Background(), t, func(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
		rfs := dalec.NewStateRefFS(st, ctx, c)
		f, err := rfs.Open("/foo")
		assert.Nil(t, err)

		// read 10 bytes
		b := make([]byte, 10)
		n, err := f.Read(b)
		assert.Equal(t, err, nil)
		assert.Equal(t, n, 10)
		assert.Equal(t, b, contents[0:10])

		// read 8 bytes
		b = make([]byte, 8)
		n, err = f.Read(b)
		assert.Equal(t, err, nil)
		assert.Equal(t, n, 8)
		assert.Equal(t, b, contents[10:18])

		// purposefully exceed length of remainder of file to
		// read the rest of the contents (14 bytes)
		b = make([]byte, 40)
		n, err = f.Read(b)
		assert.Equal(t, n, 14)
		assert.Equal(t, b[:14], contents[18:])

		// the rest of the buffer should be an unfilled 26 bytes
		assert.Equal(t, b[14:], make([]byte, 26))
		assert.Equal(t, err, io.EOF)

		// subsequent read of any size should return io.EOF
		b = make([]byte, 1)
		n, err = f.Read(b)
		assert.Equal(t, n, 0)
		assert.Equal(t, b, []byte{0x0})
		assert.Equal(t, err, io.EOF)

		return nil, nil
	})
}

func TestStateWrapper_ReadAll(t *testing.T) {
	// purposefully exceed initial length of io.ReadAll buffer (512)
	b := make([]byte, 520)
	for i := 0; i < 520; i++ {
		b[i] = byte(i % 256)
	}

	st := llb.Scratch().File(llb.Mkfile("/file", 0644, b))

	testEnv.RunTest(context.Background(), t, func(ctx context.Context, c gwclient.Client) (*gwclient.Result, error) {
		rfs := dalec.NewStateRefFS(st, ctx, c)
		f, err := rfs.Open("/file")
		assert.Nil(t, err)

		contents, err := io.ReadAll(f)
		assert.Nil(t, err)
		assert.Equal(t, contents, b)

		return rfs.Res()
	})
}
