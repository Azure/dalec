package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path"
	"strconv"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/remotes"
	"github.com/containerd/containerd/v2/core/transfer/registry"
	"github.com/containerd/platforms"
	"github.com/cpuguy83/dockercfg"
	"github.com/distribution/reference"
	"github.com/goccy/go-yaml"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
)

type retagConfig struct {
	Source string `json:"source" yaml:"source"`
	Dest   string `json:"dest" yaml:"dest"`
}

type runConfig struct {
	RepoPrefix string
	SchemeHTTP bool
}

func boolFromEnv(name string) bool {
	v := os.Getenv(name)
	if v == "" {
		return false
	}
	vv, err := strconv.ParseBool(v)
	if err != nil {
		panic(fmt.Sprintf("invalid value for %s: %s", name, v))
	}
	return vv
}

func main() {
	var rcfg runConfig

	flag.StringVar(&rcfg.RepoPrefix, "retag-registry", os.Getenv("RETAG_REGISTRY"), "Prefix for the repository URLs")
	flag.BoolVar(&rcfg.SchemeHTTP, "http", boolFromEnv("RETAG_REGISTRY_USE_HTTP"), "Use HTTP instead of HTTPS for the destination registry")
	flag.Parse()

	config := flag.Arg(0)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := run(ctx, config, rcfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

type errgroupCollector struct {
	group sync.WaitGroup

	mu   sync.Mutex
	errs []error
}

func (ec *errgroupCollector) Do(f func() error) {
	ec.group.Add(1)

	go func() {
		defer ec.group.Done()

		if err := f(); err != nil {
			ec.mu.Lock()
			defer ec.mu.Unlock()
			ec.errs = append(ec.errs, err)
		}
	}()
}

func (ec *errgroupCollector) Wait() error {
	ec.group.Wait()

	ec.mu.Lock()
	defer ec.mu.Unlock()
	if len(ec.errs) == 0 {
		return nil
	}

	return fmt.Errorf("encountered %d errors: %w", len(ec.errs), errors.Join(ec.errs...))
}

func run(ctx context.Context, configPath string, rcfg runConfig) error {
	dt, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config file: %w", err)
	}

	var ls []retagConfig
	if err := yaml.Unmarshal(dt, &ls); err != nil {
		return fmt.Errorf("error unmarshalling config: %w", err)
	}

	var authFn credentialHelperFunc
	dcfg, err := dockercfg.LoadDefaultConfig()
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("error loading Docker config: %w", err)
		}
		authFn = func(h string) (string, string, error) {
			return dockercfg.GetCredentialsFromHelper("", h)
		}
	}

	if authFn == nil {
		authFn = dcfg.GetRegistryCredentials
	}

	var eg errgroupCollector
	for _, cfg := range ls {
		eg.Do(func() (retErr error) {
			defer func() {
				if retErr != nil {
					retErr = fmt.Errorf("error processing %s: %w", cfg.Source, retErr)
				}
			}()
			cfg.Dest = path.Join(rcfg.RepoPrefix, cfg.Dest)

			ref, err := reference.ParseAnyReference(cfg.Source)
			if err != nil {
				return fmt.Errorf("error parsing source image %s: %w", cfg.Source, err)
			}

			src, err := registry.NewOCIRegistry(ctx, ref.String(), registry.WithCredentials(authFn))
			if err != nil {
				return fmt.Errorf("error creating source registry for %s: %w", cfg.Source, err)
			}

			dstOpts := []registry.Opt{registry.WithCredentials(authFn)}
			if rcfg.SchemeHTTP {
				// If the destination is a local registry, we can skip TLS verification.
				dstOpts = append(dstOpts, registry.WithDefaultScheme("http"))
			}
			dst, err := registry.NewOCIRegistry(ctx, cfg.Dest, dstOpts...)
			if err != nil {
				return fmt.Errorf("error creating destination registry for %s: %w", cfg.Dest, err)
			}

			name, desc, err := src.Resolve(ctx)
			if err != nil {
				return fmt.Errorf("error resolving source image %s: %w", cfg.Source, err)
			}

			fetcher, err := src.Fetcher(ctx, name)
			if err != nil {
				return fmt.Errorf("error creating fetcher for destination %s: %w", cfg.Dest, err)
			}

			cp := contentProviderFunc(func(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
				r, err := fetcher.Fetch(ctx, desc)
				if err != nil {
					return nil, fmt.Errorf("error fetching descriptor %s from source %s: %w", desc.Digest, cfg.Dest, err)
				}
				return &contentReaderAt{
					desc:     desc,
					ReaderAt: &readerToReaderAt{reader: r, ref: src.Image(), size: desc.Size},
					Closer:   r,
				}, nil
			})

			pusher, err := dst.Pusher(ctx, desc)
			if err != nil {
				return fmt.Errorf("error creating pusher for destination %s: %w", cfg.Dest, err)
			}
			sem := semaphore.NewWeighted(5)
			return remotes.PushContent(ctx, pusher, desc, cp, sem, platforms.All, nil)
		})
	}

	return eg.Wait()
}

type credentialHelperFunc func(host string) (string, string, error)

func (f credentialHelperFunc) GetCredentials(_ context.Context, _, host string) (registry.Credentials, error) {
	host = dockercfg.ResolveRegistryHost(host)
	u, p, err := f(host)
	if err != nil {
		return registry.Credentials{}, fmt.Errorf("error getting credentials for host %s: %w", host, err)
	}

	return registry.Credentials{
		Host:     host,
		Username: u,
		Secret:   p,
	}, nil
}

type contentReaderAt struct {
	desc ocispec.Descriptor
	io.ReaderAt
	io.Closer
}

type readerToReaderAt struct {
	reader io.Reader
	ref    string
	pos    int64
	size   int64
}

func (r *readerToReaderAt) ReadAt(p []byte, off int64) (n int, retErr error) {
	defer func() {
		if retErr != nil && !errors.Is(retErr, io.EOF) {
			retErr = fmt.Errorf("error reading at offset %d for %s: %w", off, r.ref, retErr)
		}
	}()
	if ra, ok := r.reader.(io.ReaderAt); ok {
		n, err := ra.ReadAt(p, off)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return n, err
			}
			return n, fmt.Errorf("readerAt: %w", err)
		}
		return n, nil
	}

	if r.pos != off {
		if seeker, ok := r.reader.(io.Seeker); ok {
			// Seeking and then reading is not a faithful implementation of ReadAt,
			// but since we aren't planning to do parallel reads, this should be fine.
			if _, err := seeker.Seek(off, io.SeekStart); err != nil {
				return 0, fmt.Errorf("error seeking: %w", err)
			}
			r.pos = off
		} else {
			return 0, fmt.Errorf("reader does not support seeking: %s", r.ref)
		}
	}

	// Make sure we read at most the remaining bytes in the content OR at least the length of p.
	// This is part of the interface of [io.ReaderAt], which states that ReadAt should read len(p) bytes
	// or return a non-nil error.
	minRead := min(int(r.size-r.pos), len(p))
	n, err := io.ReadAtLeast(r.reader, p, int(minRead))
	if n > 0 {
		r.pos += int64(n)
	}
	return n, err
}

func (cra *contentReaderAt) Size() int64 {
	return cra.desc.Size
}

type contentProviderFunc func(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error)

func (f contentProviderFunc) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	return f(ctx, desc)
}
