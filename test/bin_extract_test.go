package test

// import (
// 	"context"
// 	"fmt"
// 	"io"
// 	"io/fs"
// 	"os"
// 	"testing"

// 	"github.com/Azure/dalec"
// 	"github.com/Azure/dalec/frontend/pkg/bkfs"
// 	gwclient "github.com/moby/buildkit/frontend/gateway/client"
// 	"github.com/stretchr/testify/assert"
// )

// func TestBinExtract_Mariner2(t *testing.T) {
// 	t.Parallel()
// 	ctx := context.Background()
// 	testBinExtract(ctx, t, "mariner2/artifacts/bin")
// }

// func TestBinExtract_AzLinux3(t *testing.T) {
// 	t.Parallel()
// 	ctx := context.Background()
// 	testBinExtract(ctx, t, "azlinux3/artifacts/bin")
// }

// type expectFile struct {
// 	contents    string
// 	permissions os.FileMode
// }

// func assertRootContentsMatch(t *testing.T, fs fs.ReadDirFS, want map[string]expectFile) {
// 	entries, err := fs.ReadDir(".")
// 	if err != nil {
// 		t.Fatalf("unable to read directory: %s", err.Error())
// 	}
// 	assert.Equal(t, len(want), len(entries))

// 	for _, file := range entries {
// 		f, err := fs.Open(file.Name())
// 		assert.Nil(t, err)

// 		contents, err := io.ReadAll(f)
// 		assert.Nil(t, err, "unable to read file %s", file.Name())

// 		_, ok := want[file.Name()]
// 		if !ok {
// 			t.Fatalf("unexpected file %s", file.Name())
// 		}

// 		want := want[file.Name()]
// 		assert.Equal(t, want.contents, string(contents), "contents of %s do not match", file.Name())
// 		assert.Equal(t, want.permissions, file.Type().Perm(), "permissions of %s do not match", file.Name())
// 	}
// }

// func testBinExtract(ctx context.Context, t *testing.T, buildTarget string) {
// 	t.Run("test bin extract single bin", func(t *testing.T) {
// 		spec := &dalec.Spec{
// 			Name:        "test-bin",
// 			Version:     "v0.0.1",
// 			Revision:    "1",
// 			License:     "MIT",
// 			Website:     "https://github.com/azure/dalec",
// 			Vendor:      "Dalec",
// 			Packager:    "Dalec",
// 			Description: "A dalec spec with a single binary artifact",
// 			Sources: map[string]dalec.Source{
// 				"src": {
// 					Inline: &dalec.SourceInline{
// 						Dir: &dalec.SourceInlineDir{
// 							Files: map[string]*dalec.SourceInlineFile{
// 								"phony.sh": {
// 									Permissions: 0755,
// 									Contents:    "#!/bin/sh\necho 'phony'\n",
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},

// 			Artifacts: dalec.Artifacts{
// 				Binaries: map[string]dalec.ArtifactConfig{
// 					"src/phony.sh": {},
// 				},
// 			},
// 		}

// 		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
// 			sr := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec))
// 			sr.Evaluate = true
// 			res, err := client.Solve(ctx, sr)
// 			if err != nil {
// 				return nil, fmt.Errorf("unable to build package and extract binaries %w", err)
// 			}

// 			ref, err := res.SingleRef()
// 			if err != nil {
// 				return nil, err
// 			}

// 			fs := bkfs.FromRef(ctx, ref)

// 			want := map[string]expectFile{
// 				"phony.sh": {
// 					contents:    "#!/bin/sh\necho 'phony'\n",
// 					permissions: 0755,
// 				},
// 			}

// 			assertRootContentsMatch(t, fs, want)

// 			return res, nil
// 		})
// 	})

// 	t.Run("test bin extract nested bin", func(t *testing.T) {
// 		spec := &dalec.Spec{
// 			Name:        "test-bin",
// 			Version:     "v0.0.1",
// 			Revision:    "1",
// 			License:     "MIT",
// 			Website:     "https://github.com/azure/dalec",
// 			Vendor:      "Dalec",
// 			Packager:    "Dalec",
// 			Description: "A dalec spec with multiple binary artifacts",
// 			Sources: map[string]dalec.Source{
// 				"src": {
// 					Inline: &dalec.SourceInline{
// 						Dir: &dalec.SourceInlineDir{
// 							Files: map[string]*dalec.SourceInlineFile{
// 								"phony2.sh": {
// 									Permissions: 0755,
// 									Contents:    "#!/bin/sh\necho 'phony2'\n",
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},

// 			Artifacts: dalec.Artifacts{
// 				Binaries: map[string]dalec.ArtifactConfig{
// 					"src/phony2.sh": {
// 						SubPath: "nested",
// 						Name:    "unphony.sh",
// 					},
// 				},
// 			},
// 		}

// 		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
// 			sr := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec))
// 			sr.Evaluate = true
// 			res, err := client.Solve(ctx, sr)
// 			if err != nil {
// 				return nil, fmt.Errorf("unable to build package and extract binaries %w", err)
// 			}

// 			ref, err := res.SingleRef()
// 			if err != nil {
// 				return nil, err
// 			}

// 			fs := bkfs.FromRef(ctx, ref)

// 			want := map[string]expectFile{
// 				"unphony.sh": {
// 					contents:    "#!/bin/sh\necho 'phony2'\n",
// 					permissions: 0755,
// 				},
// 			}

// 			assertRootContentsMatch(t, fs, want)
// 			return res, nil
// 		})
// 	})

// 	t.Run("test bin extract multiple bin", func(t *testing.T) {
// 		spec := &dalec.Spec{
// 			Name:        "test-bin",
// 			Version:     "v0.0.1",
// 			Revision:    "1",
// 			License:     "MIT",
// 			Website:     "https://github.com/azure/dalec",
// 			Vendor:      "Dalec",
// 			Packager:    "Dalec",
// 			Description: "A dalec spec with multiple binary artifacts",
// 			Sources: map[string]dalec.Source{
// 				"src": {
// 					Inline: &dalec.SourceInline{
// 						Dir: &dalec.SourceInlineDir{
// 							Files: map[string]*dalec.SourceInlineFile{
// 								"phony1.sh": {
// 									Permissions: 0755,
// 									Contents:    "#!/bin/sh\necho 'phony1'\n",
// 								},

// 								"phony2.sh": {
// 									Permissions: 0755,
// 									Contents:    "#!/bin/sh\necho 'phony2'\n",
// 								},
// 							},
// 						},
// 					},
// 				},
// 			},

// 			Artifacts: dalec.Artifacts{
// 				Binaries: map[string]dalec.ArtifactConfig{
// 					"src/phony1.sh": {},
// 					"src/phony2.sh": {},
// 				},
// 			},
// 		}

// 		testEnv.RunTest(ctx, t, func(ctx context.Context, client gwclient.Client) (*gwclient.Result, error) {
// 			sr := newSolveRequest(withBuildTarget(buildTarget), withSpec(ctx, t, spec))
// 			sr.Evaluate = true
// 			res, err := client.Solve(ctx, sr)
// 			if err != nil {
// 				return nil, fmt.Errorf("unable to build package and extract binaries %w", err)
// 			}

// 			ref, err := res.SingleRef()
// 			if err != nil {
// 				return nil, err
// 			}

// 			fs := bkfs.FromRef(ctx, ref)

// 			want := map[string]expectFile{
// 				"phony1.sh": {
// 					contents:    "#!/bin/sh\necho 'phony1'\n",
// 					permissions: 0755,
// 				},
// 				"phony2.sh": {
// 					contents:    "#!/bin/sh\necho 'phony2'\n",
// 					permissions: 0755,
// 				},
// 			}

// 			assertRootContentsMatch(t, fs, want)
// 			return res, nil
// 		})
// 	})
// }
