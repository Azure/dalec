package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/azure/dalec/frontend/rpmbundle"
	"github.com/docker/docker/pkg/archive"
	"github.com/moby/buildkit/frontend/gateway/grpcclient"
	"github.com/moby/buildkit/util/appcontext"
	"github.com/moby/buildkit/util/bklog"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/grpclog"
)

const (
	Package = "github.com/azure/dalec/cmd/frontend-rpm-bundle"
)

func main() {
	bklog.L.Logger.SetOutput(os.Stderr)
	grpclog.SetLoggerV2(grpclog.NewLoggerV2WithVerbosity(bklog.L.WriterLevel(logrus.InfoLevel), bklog.L.WriterLevel(logrus.WarnLevel), bklog.L.WriterLevel(logrus.ErrorLevel), 1))

	if len(os.Args) > 1 {
		// Handle re-exec commands here
		// Useful for holding intermediate state without having to use an image or having to include a bunch of extra dependencies in the frontend image.
		if err := handleCmd(os.Args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := grpcclient.RunFromEnvironment(appcontext.Context(), rpmbundle.Build); err != nil {
		bklog.L.Errorf("fatal error: %+v", err)
		os.Exit(137)
	}
}

func handleCmd(args []string) error {
	switch args[0] {
	case "tar":
		return handleTar(args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func handleTar(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: %s tar <source> <destination>", os.Args[0])
	}

	src := args[0]
	dst := args[1]

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("error creating destination directory: %w", err)
	}

	f, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("error creating destination file: %w", err)
	}
	defer f.Close()

	// TODO: It would be great to not have to import from github.com/docker/docker here.
	// There's a lot of good and battle tested code in there, but its a lot...
	rdr, err := archive.Tar(src, archive.Gzip)
	if err != nil {
		return fmt.Errorf("error creating tar archive: %w", err)
	}
	defer f.Close()

	_, err = io.Copy(f, rdr)
	if err != nil {
		return fmt.Errorf("error copying tar archive: %w", err)
	}
	return nil
}
