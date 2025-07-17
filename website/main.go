package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"dagger.io/dagger"
	"github.com/pkg/errors"
)

var (
	//go:embed static src docs *.js *.ts *.json yarn.lock
	docsfs embed.FS
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	portFl := flag.Int("port", 3000, "port to run the website on")
	flag.Parse()

	go func() {
		<-ctx.Done()

		<-time.After(30 * time.Second)
		os.Exit(128 + int(syscall.SIGINT))
	}()

	client, err := dagger.Connect(ctx, dagger.WithLogOutput(os.Stderr))
	if err != nil {
		panic(err)
	}

	if err := website(ctx, client, *portFl); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func website(ctx context.Context, client *dagger.Client, port int) error {
	defer client.Close()

	docsDir, err := gofsToDagger(client)
	if err != nil {
		return errors.Wrap(err, "failed to create docs directory")
	}

	base := client.Container().From("docker.io/library/node:22-bookworm")

	err = base.
		WithDirectory("/website", docsDir).
		WithWorkdir("/website").
		WithMountedCache("/website/node_modules", client.CacheVolume("node_modules")).
		WithMountedCache("/root/.npm", client.CacheVolume("node-docusaurus-root")).
		WithExec([]string{"npm", "install"}).
		// Set the port in the container as well just so the port in the logs matches.
		WithExec([]string{"yarn", "start", "--host=0.0.0.0", "--port=" + strconv.Itoa(port)}).
		AsService().
		Up(ctx, dagger.ServiceUpOpts{
			Ports: []dagger.PortForward{
				{Backend: port, Frontend: port},
			},
		})

	if err != nil {
		return errors.Wrap(err, "failed to start website service")
	}

	return nil
}

func gofsToDagger(client *dagger.Client) (*dagger.Directory, error) {
	root := client.Directory()

	err := fs.WalkDir(docsfs, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}

		if entry.IsDir() {
			root = root.WithNewDirectory(path, dagger.DirectoryWithNewDirectoryOpts{Permissions: int(info.Mode().Perm())})
			return nil
		}

		dt, err := docsfs.ReadFile(path)
		if err != nil {
			return err
		}
		root = root.WithNewFile(path, string(dt), dagger.DirectoryWithNewFileOpts{Permissions: int(info.Mode().Perm())})
		return nil
	})
	return root, err
}
