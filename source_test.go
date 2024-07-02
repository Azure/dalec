package dalec

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/frontend/dockerfile/dockerfile2llb"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	v1 "github.com/opencontainers/image-spec/specs-go/v1"
	"gotest.tools/v3/assert"
)

func TestSourceGitSSH(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// buildkit's llb.Git currently directly runs an ssh keyscan (connects to the specified host and port to get the host key)
	// This is not ideal for our test setup here because we need to use a real server.
	// Thankfully when there is an error it is ignored so we don't actually need to setup a full SSH server here.
	addr := stubListener(t)

	src := Source{
		Git: &SourceGit{
			URL:    fmt.Sprintf("user@%s:test.git", addr),
			Commit: t.Name(),
		},
	}

	ops := getSourceOp(ctx, t, src)
	checkGitOp(t, ops, &src)

	t.Run("with subdir", func(t *testing.T) {
		src := src
		src.Path = "subdir"
		ops2 := getSourceOp(ctx, t, src)
		checkGitOp(t, ops2, &src)

		// git ops require extra filtering to get the correct subdir, so we should have an extra op
		if len(ops2) != len(ops)+1 {
			t.Fatalf("expected %d ops, got %d", len(ops)+1, len(ops2))
		}

		checkFilter(t, ops2[1].GetFile(), &src)
	})

	t.Run("with include-exclude", func(t *testing.T) {
		src := src
		src.Includes = []string{"foo", "bar"}
		src.Excludes = []string{"baz"}
		ops2 := getSourceOp(ctx, t, src)
		checkGitOp(t, ops2, &src)

		// git ops require extra filtering to get the correct subdir, so we should have an extra op
		if len(ops2) != len(ops)+1 {
			t.Fatalf("expected %d ops, got %d", len(ops)+1, len(ops2))
		}

		checkFilter(t, ops2[1].GetFile(), &src)
	})

	t.Run("with include-exclude and subpath", func(t *testing.T) {
		src := src
		src.Includes = []string{"foo", "bar"}
		src.Excludes = []string{"baz"}
		src.Path = "subdir"

		ops2 := getSourceOp(ctx, t, src)
		checkGitOp(t, ops2, &src)

		// git ops require extra filtering to get the correct subdir, so we should have an extra op
		if len(ops2) != len(ops)+1 {
			t.Fatalf("expected %d ops, got %d", len(ops)+1, len(ops2))
		}

		checkFilter(t, ops2[1].GetFile(), &src)
	})
}

func TestSourceGitHTTP(t *testing.T) {
	t.Parallel()

	src := Source{
		Git: &SourceGit{
			URL:    "https://localhost/test.git",
			Commit: t.Name(),
		},
	}

	ctx := context.Background()
	ops := getSourceOp(ctx, t, src)
	checkGitOp(t, ops, &src)

	t.Run("with subdir", func(t *testing.T) {
		src := src
		src.Path = "subdir"
		ops2 := getSourceOp(ctx, t, src)
		checkGitOp(t, ops2, &src)

		// git ops require extra filtering to get the correct subdir, so we should have an extra op
		if len(ops2) != len(ops)+1 {
			t.Fatalf("expected %d ops, got %d", len(ops)+1, len(ops2))
		}

		checkFilter(t, ops2[1].GetFile(), &src)
	})

	t.Run("with include-exclude", func(t *testing.T) {
		src := src
		src.Includes = []string{"foo", "bar"}
		src.Excludes = []string{"baz"}
		ops2 := getSourceOp(ctx, t, src)
		checkGitOp(t, ops2, &src)

		// git ops require extra filtering to get the correct subdir, so we should have an extra op
		if len(ops2) != len(ops)+1 {
			t.Fatalf("expected %d ops, got %d", len(ops)+1, len(ops2))
		}

		checkFilter(t, ops2[1].GetFile(), &src)
	})

	t.Run("with include-exclude and subpath", func(t *testing.T) {
		src := src
		src.Includes = []string{"foo", "bar"}
		src.Excludes = []string{"baz"}
		src.Path = "subdir"

		ops2 := getSourceOp(ctx, t, src)
		checkGitOp(t, ops2, &src)

		// git ops require extra filtering to get the correct subdir, so we should have an extra op
		if len(ops2) != len(ops)+1 {
			t.Fatalf("expected %d ops, got %d", len(ops)+1, len(ops2))
		}

		checkFilter(t, ops2[1].GetFile(), &src)
	})
}

func TestSourceHTTP(t *testing.T) {
	src := Source{
		HTTP: &SourceHTTP{
			URL: "https://localhost/test.tar.gz",
		},
	}

	ctx := context.Background()
	ops := getSourceOp(ctx, t, src)

	op := ops[0].GetSource()

	xID := "https://localhost/test.tar.gz"
	if op.Identifier != xID {
		t.Errorf("expected identifier %q, got %q", xID, op.Identifier)
	}

	if len(op.Attrs) != 1 {
		t.Errorf("expected 1 attribute, got %d", len(op.Attrs))
	}

	// For http sources we expect the filename to be the name of the source not the filename in the URL.
	xFilename := "test"
	const httpFilename = "http.filename"
	if op.Attrs[httpFilename] != "test" {
		t.Errorf("expected http.filename %q, got %q", xFilename, op.Attrs[httpFilename])
	}

	t.Run("with digest", func(t *testing.T) {
		dgst := digest.Canonical.FromBytes(nil)
		src.HTTP.Digest = dgst

		ops := getSourceOp(ctx, t, src)
		op := ops[0].GetSource()

		if len(op.Attrs) != 2 {
			t.Errorf("expected 2 attribute, got %d", len(op.Attrs))
		}

		if op.Attrs[httpFilename] != "test" {
			t.Errorf("expected http.filename %q, got %q", xFilename, op.Attrs[httpFilename])
		}

		const httpChecksum = "http.checksum"
		if op.Attrs[httpChecksum] != dgst.String() {
			t.Errorf("expected http.checksum %q, got %q", dgst.String(), op.Attrs[httpChecksum])
		}
	})
}

func toImageRef(ref string) string {
	return "docker-image://" + ref
}

var (
	noMountCheck = []expectMount{}
	rootMount    = expectMount{dest: "/", selector: "", typ: pb.MountType_BIND}
)

func TestSourceDockerImage(t *testing.T) {
	imgRef := "localhost:0/does/not/exist:latest"
	src := Source{
		DockerImage: &SourceDockerImage{
			Ref: imgRef,
		},
	}
	ctx := context.Background()
	ops := getSourceOp(ctx, t, src)

	op := ops[0].GetSource()

	xID := toImageRef(imgRef)
	if op.Identifier != xID {
		t.Errorf("expected identifier %q, got %q", xID, op.Identifier)
	}

	contextMount := SourceMount{
		Dest: "/dst",
		Spec: Source{
			Context: &SourceContext{
				Name: "."},
		},
	}

	imageMount := SourceMount{
		Dest: "/dst",
		Spec: Source{
			DockerImage: &SourceDockerImage{
				Ref: "localhost:0/some/image:latest",
				Cmd: &Command{
					Steps: []*BuildStep{
						{
							Command: "mkdir /nested/dir && echo 'some file contents' > /nested/dir/foo.txt",
						},
					},
				},
			},
		},
	}

	fileMount := SourceMount{
		Dest: "/filedest",
		Spec: Source{
			Inline: &SourceInline{
				File: &SourceInlineFile{
					Contents: "some file contents",
				},
			},
		},
	}

	t.Run("with cmd", func(t *testing.T) {
		src := Source{
			DockerImage: &SourceDockerImage{
				Ref: imgRef,
				Cmd: &Command{
					Dir: "/tmp",
					Steps: []*BuildStep{
						{Command: "echo hello 1", Env: map[string]string{"FOO": "bar1"}},
						{Command: "echo hello 2", Env: map[string]string{"FOO": "bar2"}},
					},
				},
			},
		}

		ctx := context.Background()
		ops := getSourceOp(ctx, t, src)

		imgBaseOp := ops[0].GetSource()
		if imgBaseOp.Identifier != xID {
			t.Errorf("expected identifier %q, got %q", xID, imgBaseOp.Identifier)
		}
		checkCmd(t, ops[1:], &src, [][]expectMount{noMountCheck, noMountCheck})

		t.Run("with file mount", func(t *testing.T) {
			src := src

			img := *src.DockerImage
			cmd := *img.Cmd
			cmd.Mounts = []SourceMount{fileMount}

			img.Cmd = &cmd
			src.DockerImage = &img

			ops := getSourceOp(ctx, t, src)
			fileMountCheck := []expectMount{{dest: "/filedest", selector: "/filedest", typ: pb.MountType_BIND}}
			checkCmd(t, ops[2:], &src, [][]expectMount{noMountCheck, fileMountCheck})
		})

		t.Run("with filters", func(t *testing.T) {
			t.Run("include and exclude", func(t *testing.T) {
				src := src
				src.Includes = []string{"foo", "bar"}
				src.Excludes = []string{"baz"}

				ops := getSourceOp(ctx, t, src)
				checkCmd(t, ops[1:len(ops)-1], &src, [][]expectMount{noMountCheck, noMountCheck})
				// When include/exclude are used, we are expecting a copy operation to be last.
				checkFilter(t, ops[len(ops)-1].GetFile(), &src)
			})
			t.Run("subpath", func(t *testing.T) {
				src := src
				src.Path = "subdir"

				ops := getSourceOp(ctx, t, src)

				img := ops[0].GetSource()
				if img.Identifier != xID {
					t.Errorf("expected identifier %q, got %q", xID, img.Identifier)
				}

				checkCmd(t, ops[1:], &src, [][]expectMount{{rootMount}, {rootMount}})
			})

			t.Run("mount beneath subpath", func(t *testing.T) {
				src := src
				src.Path = "subpath"

				img := *src.DockerImage
				cmd := *img.Cmd

				img.Cmd = &cmd
				src.DockerImage = &img

				img.Cmd.Mounts = []SourceMount{
					{
						Dest: src.Path,
						Spec: Source{
							Inline: &SourceInline{
								Dir: &SourceInlineDir{},
							},
						},
					},
				}

				_, err := src.AsState("test", SourceOpts{})
				assert.ErrorIs(t, err, errInvalidMountConfig)
			})

			t.Run("subpath with include-exclude", func(t *testing.T) {
				src := src
				src.Path = "subdir"
				src.Includes = []string{"foo", "bar"}
				src.Excludes = []string{"baz"}

				ops := getSourceOp(ctx, t, src)

				img := ops[0].GetSource()
				if img.Identifier != xID {
					t.Errorf("expected identifier %q, got %q", xID, img.Identifier)
				}
				ops = ops[1:]

				// When a subpath is used, we expect a mount to be applied.
				// There should be 2 mounts, one for the rootfs and one for our subdir
				checkCmd(t, ops[:len(ops)-1], &src, [][]expectMount{{rootMount, {dest: "subdir"}}, {rootMount, {dest: "subdir"}}})

				// last op is (should be) the include/exclude filter and not a cmd
				// When include/exclude are used, we are expecting a copy operation to be last.
				checkFilter(t, ops[len(ops)-1].GetFile(), &src)
			})

			t.Run("subpath within context mount", func(t *testing.T) {
				src := src
				contextMount := contextMount
				contextMount.Spec.Path = "subdir"

				// Add source to mounts
				img := *src.DockerImage
				cmd := *img.Cmd

				cmd.Mounts = []SourceMount{contextMount}
				img.Cmd = &cmd
				src.DockerImage = &img
				ops := getSourceOp(ctx, t, src)

				var contextOp *pb.Op

				// we must scan through the sources to find one with a matching id,
				// since the order of the source ops isn't always deterministic
				// (possible buildkit marshaling bug)
				if imageOp := findMatchingSource(ops, src); imageOp == nil {
					t.Errorf("could not find source with identifier %q", imgBaseOp.Identifier)
					return
				}

				if contextOp = findMatchingSource(ops, contextMount.Spec); contextOp == nil {
					t.Errorf("could not find source with identifier %q", contextMount.Spec.Path)
					return
				}

				checkContext(t, contextOp.GetSource(), &contextMount.Spec)
				// there should be no copy operation, since we have no includes and excludes,
				// so we can simply extract the dest path with a mount
				checkCmd(t, ops[2:], &src,
					[][]expectMount{{{dest: "/dst", selector: "subdir"}},
						{{dest: "/dst", selector: "subdir"}}})
			})

			t.Run("subpath within cmd mount", func(t *testing.T) {
				src := src
				imageMount := imageMount
				imageMount.Spec.Path = "/subdir"

				img := *src.DockerImage
				cmd := *img.Cmd

				cmd.Mounts = []SourceMount{imageMount}
				img.Cmd = &cmd
				src.DockerImage = &img
				src.DockerImage.Cmd.Mounts = []SourceMount{imageMount}

				ops := getSourceOp(ctx, t, src)

				var imgOp, subImg *pb.Op

				if imgOp = findMatchingSource(ops, src); imgOp == nil {
					t.Errorf("could not find source with identifier %q", src.DockerImage.Ref)
				}

				if subImg = findMatchingSource(ops, imageMount.Spec); subImg == nil {
					t.Errorf("could not find source with identifier %q", imageMount.Spec.DockerImage.Ref)
				}

				dMap := toDigestMap(ops)
				childOps := getChildren(subImg, ops, dMap)
				if len(childOps) != 1 {
					t.Fatalf("expecting single child op for %v\n", subImg.GetSource())
				}

				cmdOp := childOps[0]
				checkCmd(t, []*pb.Op{cmdOp}, &imageMount.Spec, [][]expectMount{noMountCheck, noMountCheck})

				nextCmd1 := getChildren(cmdOp, ops, dMap)
				nextCmd2 := getChildren(nextCmd1[0], ops, dMap)

				checkCmd(t, []*pb.Op{nextCmd1[0], nextCmd2[0]}, &src, [][]expectMount{{{dest: "/dst", selector: ""}}, noMountCheck})
			})
		})
	})
}

func getChildren(op *pb.Op, ops []*pb.Op, digests map[*pb.Op]digest.Digest) []*pb.Op {
	children := make([]*pb.Op, 0, len(ops))
	for _, maybeChild := range ops {
		for _, input := range maybeChild.Inputs {
			if input.Digest == digests[op] {
				children = append(children, maybeChild)
			}
		}
	}

	return children
}

func toDigestMap(ops []*pb.Op) map[*pb.Op]digest.Digest {
	hashes := make(map[*pb.Op]digest.Digest)
	for _, op := range ops {
		bytes, err := op.Marshal()
		if err != nil {
			panic(err)
		}
		hashes[op] = digest.FromBytes(bytes)
	}

	return hashes
}

func toContextRef(ctxRef string) string {
	return "local://" + ctxRef
}

func sourcesMatch(src Source, op *pb.SourceOp) bool {
	switch {
	case src.DockerImage != nil:
		return op.Identifier == toImageRef(src.DockerImage.Ref)
	case src.Context != nil:
		return op.Identifier == toContextRef(src.Context.Name)
	default:
		panic("unsupported source")
	}
}

func findMatchingSource(sOps []*pb.Op, src Source) *pb.Op {
	for _, s := range sOps {
		sOp := s.GetSource()
		if sOp == nil {
			continue
		}
		if sourcesMatch(src, sOp) {
			return s
		}
	}

	return nil
}

func TestSourceContext(t *testing.T) {
	ctx := context.Background()

	testWithFilters := func(t *testing.T, src Source) {
		t.Run("with filters", func(t *testing.T) {
			t.Run("subdir", func(t *testing.T) {
				src := src
				src.Path = "subdir"
				ops := getSourceOp(ctx, t, src)
				checkContext(t, ops[0].GetSource(), &src)
				// for context source, we expect to have a copy operation as the last op when subdir is used
				checkFilter(t, ops[1].GetFile(), &src)
			})

			t.Run("include and exclude", func(t *testing.T) {
				src := src
				src.Includes = []string{"foo", "bar"}
				src.Excludes = []string{"baz"}
				ops := getSourceOp(ctx, t, src)
				checkContext(t, ops[0].GetSource(), &src)
				// With include/exclude only, this should be handled with just one op.
				if len(ops) != 1 {
					t.Fatalf("expected 1 op, got %d\n%s", len(ops), ops)
				}
			})

			t.Run("subpath with include-exclude", func(t *testing.T) {
				src := src
				src.Path = "subdir"
				src.Includes = []string{"foo", "bar"}
				src.Excludes = []string{"baz"}
				ops := getSourceOp(ctx, t, src)
				checkContext(t, ops[0].GetSource(), &src)
				// for context source, we expect to have a copy operation as the last op when subdir is used

				// set includes, excludes to nil before checking against filter, as includes and excludes are
				// handled before filter operation for context sources
				src.Includes, src.Excludes = nil, nil
				checkFilter(t, ops[1].GetFile(), &src)
			})
		})
	}

	t.Run("default", func(t *testing.T) {
		src := Source{
			Context: &SourceContext{},
		}
		ops := getSourceOp(ctx, t, src)
		checkContext(t, ops[0].GetSource(), &src)

		testWithFilters(t, src)
	})

	t.Run("with custom name", func(t *testing.T) {
		src := Source{
			Context: &SourceContext{Name: "some-name"},
		}
		ops := getSourceOp(ctx, t, src)
		checkContext(t, ops[0].GetSource(), &src)
		testWithFilters(t, src)
	})
}

func TestSourceInlineFile(t *testing.T) {
	ctx := context.Background()

	for name, f := range testFiles() {
		f := f
		t.Run(name, func(t *testing.T) {
			src := Source{Inline: &SourceInline{File: f}}
			ops := getSourceOp(ctx, t, src)
			if len(ops) != 1 {
				t.Fatalf("expected 1 op, got %d:\n%s", len(ops), ops)
			}
			checkMkfile(t, ops[0].GetFile(), src.Inline.File, "/test")
		})
	}
}

func testFiles() map[string]*SourceInlineFile {
	empty := func() *SourceInlineFile {
		return &SourceInlineFile{}
	}

	modify := func(mods ...func(*SourceInlineFile)) *SourceInlineFile {
		src := empty()
		for _, mod := range mods {
			mod(src)
		}
		return src
	}

	withUID := func(uid int) func(*SourceInlineFile) {
		return func(s *SourceInlineFile) {
			s.UID = uid
		}
	}

	withGID := func(gid int) func(*SourceInlineFile) {
		return func(s *SourceInlineFile) {
			s.GID = gid
		}
	}

	withContents := func(contents string) func(*SourceInlineFile) {
		return func(s *SourceInlineFile) {
			s.Contents = contents
		}
	}

	return map[string]*SourceInlineFile{
		"empty file":                  modify(),
		"empty file with uid":         modify(withUID(1000)),
		"empty file with gid":         modify(withGID(1000)),
		"empty file with uid and gid": modify(withUID(1000), withGID(1000)),
		"with contents":               modify(withContents("hello world 1")),
		"with uid and contents":       modify(withUID(1000), withContents("hello world 2")),
		"with gid and contents":       modify(withGID(1000), withContents("hello world 3")),
		"with uid, gid, and contents": modify(withUID(1000), withGID(1000), withContents("hello world 4")),
	}
}

func TestSourceInlineDir(t *testing.T) {
	ctx := context.Background()

	empty := func() *SourceInlineDir {
		return &SourceInlineDir{}
	}

	modify := func(mods ...func(*SourceInlineDir)) *SourceInlineDir {
		src := empty()
		for _, mod := range mods {
			mod(src)
		}
		return src
	}

	withDirUID := func(uid int) func(*SourceInlineDir) {
		return func(s *SourceInlineDir) {
			s.UID = uid
		}
	}

	withDirGID := func(gid int) func(*SourceInlineDir) {
		return func(s *SourceInlineDir) {
			s.GID = gid
		}
	}

	testDirs := map[string]*SourceInlineDir{
		"default":          modify(),
		"with uid":         modify(withDirUID(1000)),
		"with gid":         modify(withDirGID(1000)),
		"with uid and gid": modify(withDirUID(1000), withDirGID(1001)),
	}

	for name, dir := range testDirs {
		dir := dir
		t.Run(name, func(t *testing.T) {
			src := Source{Inline: &SourceInline{Dir: dir}}
			ops := getSourceOp(ctx, t, src)
			checkMkdir(t, ops[0].GetFile(), src.Inline.Dir)

			t.Run("with files", func(t *testing.T) {
				src.Inline.Dir.Files = testFiles()
				ops := getSourceOp(ctx, t, src)
				checkMkdir(t, ops[0].GetFile(), src.Inline.Dir)

				if len(ops) != len(src.Inline.Dir.Files)+1 {
					t.Fatalf("expected %d ops, got %d\n%s", len(src.Inline.Dir.Files)+1, len(ops), ops)
				}

				sorted := SortMapKeys(src.Inline.Dir.Files)
				for i, name := range sorted {
					ops := getSourceOp(ctx, t, src)
					f := src.Inline.Dir.Files[name]
					checkMkfile(t, ops[i+1].GetFile(), f, name)
				}
			})
		})
	}
}

func checkMkdir(t *testing.T, op *pb.FileOp, src *SourceInlineDir) {
	if op == nil {
		t.Fatal("expected dir op")
	}

	if len(op.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(op.Actions))
	}

	mkdir := op.Actions[0].GetMkdir()
	if mkdir == nil {
		t.Fatalf("expected mkdir action: %v", op.Actions[0])
	}

	if mkdir.MakeParents {
		t.Error("expected make parents to be false")
	}

	if mkdir.GetOwner().User.GetByID() != uint32(src.UID) {
		t.Errorf("expected uid %d, got %d", src.UID, mkdir.GetOwner().User.GetByID())
	}

	if mkdir.GetOwner().Group.GetByID() != uint32(src.GID) {
		t.Errorf("expected gid %d, got %d", src.GID, mkdir.GetOwner().Group.GetByID())
	}

	xPerms := src.Permissions
	if xPerms == 0 {
		xPerms = defaultDirPerms
	}
	if os.FileMode(mkdir.Mode) != xPerms {
		t.Errorf("expected mode %O, got %O", xPerms, os.FileMode(mkdir.Mode))
	}
	if mkdir.Path != "/" {
		t.Errorf("expected path %q, got %q", "/", mkdir.Path)
	}
}

func checkMkfile(t *testing.T, op *pb.FileOp, src *SourceInlineFile, name string) {
	if op == nil {
		t.Fatal("expected file op")
	}

	if len(op.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(op.Actions))
	}

	mkfile := op.Actions[0].GetMkfile()
	if mkfile == nil {
		t.Fatalf("expected mkfile action: %v", op.Actions[0])
	}

	uid := mkfile.Owner.User.GetByID()
	if uid != uint32(src.UID) {
		t.Errorf("expected uid %d, got %d", src.UID, uid)
	}

	gid := mkfile.Owner.Group.GetByID()
	if gid != uint32(src.GID) {
		t.Errorf("expected gid %d, got %d", src.GID, gid)
	}

	mode := os.FileMode(mkfile.Mode).Perm()
	xMode := src.Permissions
	if xMode == 0 {
		xMode = defaultFilePerms
	}
	if mode != xMode {
		t.Errorf("expected mode %O, got %O", xMode, mode)
	}

	if string(mkfile.Data) != src.Contents {
		t.Errorf("expected data %q, got %q", src.Contents, mkfile.Data)
	}

	xPath := filepath.Join("/", name)
	if mkfile.Path != xPath {
		t.Errorf("expected path %q, got %q", xPath, mkfile.Path)
	}
}

func stubListener(t *testing.T) net.Addr {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = l.Close()
	})

	go func() {
		defer l.Close()
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}

			conn.Close()
		}
	}()

	return l.Addr()
}

// 1. Generates the LLB for a source using Source2LLBGetter (the function we are testing)
// 2. Marshals the LLB to a protobuf (since we don't have access to the data in LLB directly)
// 3. Unmarshals the protobuf to get the [pb.Op]s which is what buildkit would act on to get the actual source data during build.
func getSourceOp(ctx context.Context, t *testing.T, src Source) []*pb.Op {
	t.Helper()

	fillDefaults(&src)

	var sOpt SourceOpts
	if src.Build != nil {
		if src.Build.Source.Inline == nil || src.Build.Source.Inline.File == nil {
			t.Fatal("Cannot test from a Dockerfile without inline content")
		}
		sOpt.Forward = func(_ llb.State, build *SourceBuild) (llb.State, error) {
			// Note, we can't really test anything other than inline here because we don't have access to the actual buildkit client,
			// so we can't extract extract the dockerfile from the input state (nor do we have any input state)
			src := []byte(src.Build.Source.Inline.File.Contents)
			st, _, _, _, err := dockerfile2llb.Dockerfile2LLB(ctx, src, dockerfile2llb.ConvertOpt{
				MetaResolver: stubMetaResolver{},
			})
			return *st, err
		}
	}

	sOpt.GetContext = func(name string, opts ...llb.LocalOption) (*llb.State, error) {
		st := llb.Local(name, opts...)
		return &st, nil
	}

	st, err := src.AsState("test", sOpt)
	if err != nil {
		t.Fatal(err)
	}

	def, err := st.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}

	out := make([]*pb.Op, 0, len(def.Def)-1)

	// We'll drop the last op which is a "return" op, which doesn't matter for our tests.
	for _, dt := range def.Def[:len(def.Def)-1] {
		op := &pb.Op{}
		if err := op.Unmarshal(dt); err != nil {
			t.Fatal(err)
		}

		out = append(out, op)
	}

	return out
}

func checkGitOp(t *testing.T, ops []*pb.Op, src *Source) {
	op := ops[0].GetSource()

	var bkAddr string

	_, other, ok := strings.Cut(src.Git.URL, "@")
	if ok {
		// ssh
		// buildkit replaces the `:` between host and port with a `/` in the identifier
		bkAddr = "git://" + strings.Replace(other, ":", "/", 1)
	} else {
		// not ssh
		_, other, ok := strings.Cut(src.Git.URL, "://")
		if !ok {
			t.Fatal("invalid git URL")
		}
		bkAddr = "git://" + other
	}

	xID := bkAddr + "#" + src.Git.Commit
	if op.Identifier != xID {
		t.Errorf("expected identifier %q, got %q", xID, op.Identifier)
	}

	if op.Attrs["git.fullurl"] != src.Git.URL {
		t.Errorf("expected git.fullurl %q, got %q", src.Git.URL, op.Attrs["git.fullurl"])
	}
}

func checkFilter(t *testing.T, op *pb.FileOp, src *Source) {
	if op == nil {
		t.Fatal("expected file op")
	}

	cpAction := op.Actions[0].GetCopy()
	if cpAction == nil {
		t.Fatal("expected copy action")
	}

	if cpAction.Dest != "/" {
		t.Errorf("expected dest \"/\", got %q", cpAction.Dest)
	}

	p := src.Path
	if src.DockerImage != nil {
		// DockerImage handles subpaths itself
		p = "/"
	}
	if !filepath.IsAbs(p) {
		p = "/" + p
	}
	if cpAction.Src != p {
		t.Errorf("expected src %q, got %q", p, cpAction.Src)
	}
	if !cpAction.DirCopyContents {
		t.Error("expected dir copy contents")
	}

	if !reflect.DeepEqual(cpAction.IncludePatterns, src.Includes) {
		t.Fatalf("expected include patterns %v, got %v", src.Includes, cpAction.IncludePatterns)
	}

	if !reflect.DeepEqual(cpAction.ExcludePatterns, src.Excludes) {
		t.Fatalf("expected exclude patterns %v, got %v", src.Excludes, cpAction.ExcludePatterns)
	}
}

type expectMount struct {
	dest     string
	selector string
	typ      pb.MountType
}

func mountMatches(gotMount *pb.Mount, wantMount expectMount) bool {
	return wantMount.dest == gotMount.Dest && wantMount.selector == gotMount.Selector &&
		wantMount.typ == gotMount.MountType
}

func checkContainsMount(t *testing.T, mounts []*pb.Mount, expect expectMount) {
	for _, mnt := range mounts {
		if mountMatches(mnt, expect) {
			return
		}
	}

	t.Errorf("could not find mount with dest=%s selector=%s type=%q in mounts %v", expect.dest, expect.selector, expect.typ, mounts)
}

func checkCmd(t *testing.T, ops []*pb.Op, src *Source, expectMounts [][]expectMount) {
	t.Helper()
	if len(ops) != len(src.DockerImage.Cmd.Steps) {
		t.Fatalf("unexpected number of ops, expected %d, got %d\n\n%v", len(src.DockerImage.Cmd.Steps), len(ops), ops)
	}

	for i, step := range src.DockerImage.Cmd.Steps {
		exec := ops[i].GetExec()
		if exec == nil {
			t.Error("expected exec op")
			continue
		}

		xArgs := append([]string{"/bin/sh", "-c"}, step.Command)
		if !reflect.DeepEqual(exec.Meta.Args, xArgs) {
			t.Errorf("expected args %v, got %v", xArgs, exec.Meta.Args)
		}

		xEnv := append(envMapToSlice(src.DockerImage.Cmd.Env), envMapToSlice(step.Env)...)
		slices.Sort(xEnv)
		if !reflect.DeepEqual(exec.Meta.Env, xEnv) {
			t.Errorf("expected env %v, got %v", xEnv, exec.Meta.Env)
		}

		xCwd := src.DockerImage.Cmd.Dir
		if exec.Meta.Cwd != path.Join("/", xCwd) {
			t.Errorf("expected cwd %q, got %q", xCwd, exec.Meta.Cwd)
		}

		for _, expectMount := range expectMounts[i] {
			checkContainsMount(t, exec.Mounts, expectMount)
		}

		if exec.Mounts[0].Input == pb.Empty {
			t.Fatal("rootfs mount cannot be empty")
		}
	}
}

func checkContext(t *testing.T, op *pb.SourceOp, src *Source) {
	name := src.Context.Name
	if name == "" {
		name = "context"
	}
	xID := "local://" + name
	if op.Identifier != xID {
		t.Errorf("expected identifier %q, got %q", xID, op.Identifier)
	}

	if src.Includes != nil {
		includesJson, err := json.Marshal(src.Includes)
		if err != nil {
			t.Fatal(err)
		}
		localIncludes := op.Attrs["local.includepattern"]

		if string(includesJson) != localIncludes {
			t.Errorf("expected includes %q on local op, got %q", includesJson, localIncludes)
		}
	}

	if src.Excludes != nil {
		excludesJson, err := json.Marshal(src.Excludes)
		if err != nil {
			t.Fatal(err)
		}
		localExcludes := op.Attrs["local.excludepatterns"]

		if string(excludesJson) != localExcludes {
			t.Errorf("expected includes %q on local op, got %q", excludesJson, localExcludes)
		}
	}
}

func envMapToSlice(env map[string]string) []string {
	var out []string
	for k, v := range env {
		out = append(out, k+"="+v)
	}

	return out
}

type stubMetaResolver struct{}

func (stubMetaResolver) ResolveImageConfig(ctx context.Context, ref string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	// Craft a dummy image config
	// If we don't put at least 1 diffID, buildkit will treat this as `FROM scratch` (and actually literally convert it `llb.Scratch`)
	// This affects what ops that get marshaled.
	// Namely it removes our `docker-image` identifier op.
	img := DockerImageSpec{
		Image: v1.Image{
			RootFS: v1.RootFS{
				DiffIDs: []digest.Digest{digest.FromBytes(nil)},
			},
		},
	}

	dt, err := json.Marshal(img)
	if err != nil {
		return "", "", nil, err
	}
	return ref, "", dt, nil
}

func Test_pathHasPrefix(t *testing.T) {
	type testCase struct {
		path   string
		prefix string
		expect bool
	}
	cases := []testCase{
		{"/foo", "/foobar", false},
		{"/foo", "/foo", true},
		{"/foo/", "/foo", true},
		{"/foo/", "/foo/", true},
		{"//foo", "/foo", true},
		{"//foo/", "/foo", true},
		{"/foo/bar", "/foo", true},
		{"/foo/bar/", "/foo", true},
		{"/foo/bar/", "/foo/", true},
		{"/foo/bar", "/foo/", true},
		{"/foo/bar", "/bar", false},
		{"/foo/bar", "/foo/bar/baz", false},
		{"/foo/bar/baz", "/foo/bar", true},
		{"/foo//bar", "/foo", true},
		{"/foo//bar/", "/foo", true},
		{"/foo//bar/", "/foo/", true},
		{"/foo//bar/", "/foo//", true},
	}

	// Replace / char which is special for go tests with something less special.
	forTestName := func(s string) string {
		return strings.Replace(s, "/", "__", -1)
	}

	for _, tc := range cases {
		name := fmt.Sprintf("path=%s,prefix=%s", forTestName(tc.path), forTestName(tc.prefix))
		t.Run(name, func(t *testing.T) {
			hasPrefix := pathHasPrefix(tc.path, tc.prefix)
			assert.Equal(t, hasPrefix, tc.expect)
		})
	}
}
