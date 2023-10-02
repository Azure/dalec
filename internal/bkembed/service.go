package bkembed

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/snapshots"
	"github.com/containerd/containerd/snapshots/overlay"
	"github.com/moby/buildkit/control"
	"github.com/moby/buildkit/executor/oci"
	"github.com/moby/buildkit/frontend"
	"github.com/moby/buildkit/frontend/gateway"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/snapshot/containerd"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/bboltcachestorage"
	"github.com/moby/buildkit/util/entitlements"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/moby/buildkit/util/network/netproviders"
	"github.com/moby/buildkit/worker"
	"github.com/moby/buildkit/worker/base"
	"github.com/moby/buildkit/worker/runc"
	"go.etcd.io/bbolt"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthv1 "google.golang.org/grpc/health/grpc_health_v1"
)

const gatewayV0 = "gateway.v0"

type ServerConfig struct {
	Controller *control.Controller
	Health     *health.Server
}

func NewServer(ctx context.Context, controller *control.Controller) *grpc.Server {
	server := grpc.NewServer()

	controller.Register(server)
	healthv1.RegisterHealthServer(server, health.NewServer())
	return server
}

func NewController(ctx context.Context, root string, frontends map[string]frontend.Frontend) (*control.Controller, error) {
	if err := os.MkdirAll(root, 0o711); err != nil {
		return nil, err
	}

	cacheStorage, err := bboltcachestorage.NewStore(filepath.Join(root, "cache.db"))
	if err != nil {
		return nil, err
	}

	wc := &worker.Controller{}

	if frontends == nil {
		frontends = make(map[string]frontend.Frontend)
	}
	if _, ok := frontends[gatewayV0]; !ok {
		frontends[gatewayV0] = gateway.NewGatewayFrontend(wc)
	}

	sessionManager, err := session.NewManager()
	if err != nil {
		return nil, err
	}

	cs, err := local.NewStore(root)
	if err != nil {
		return nil, err
	}

	var timeout time.Duration
	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	} else {
		timeout = 30 * time.Second
	}

	db, err := bbolt.Open(filepath.Join(root, "metadata.db"), 0600, &bbolt.Options{
		Timeout: timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("error opening metadata db: %w", err)
	}

	mdb := metadata.NewDB(db, cs, nil)
	lm := metadata.NewLeaseManager(mdb)

	if dl, ok := ctx.Deadline(); ok {
		timeout = time.Until(dl)
	} else {
		timeout = 30 * time.Second
	}
	historyDB, err := bbolt.Open(filepath.Join(root, "history.db"), 0600, &bbolt.Options{
		Timeout: timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("error opening history db: %w", err)
	}

	snFactory := runc.SnapshotterFactory{
		Name: "overlay",
		New: func(root string) (snapshots.Snapshotter, error) {
			return overlay.NewSnapshotter(root)
		},
	}

	runcOpt, err := runc.NewWorkerOpt(root, snFactory, true, oci.ProcessSandbox, nil, nil, netproviders.Opt{
		Mode: "host",
	}, nil, "runc", "", false, nil, "", "")
	if err != nil {
		return nil, err
	}
	w, err := base.NewWorker(ctx, runcOpt)
	if err != nil {
		return nil, err
	}

	wc.Add(w) //nolint:errcheck

	cm := solver.NewCacheManager(ctx, "local", cacheStorage, worker.NewCacheResultStorage(wc))

	return control.NewController(control.Opt{
		SessionManager:   sessionManager,
		WorkerController: wc,
		Frontends:        frontends,
		HistoryDB:        historyDB,
		LeaseManager:     leaseutil.WithNamespace(lm, "bkembed"),
		ContentStore:     containerd.NewContentStore(mdb.ContentStore(), "bkembed"),
		CacheManager:     cm,
		Entitlements:     []string{string(entitlements.EntitlementNetworkHost)},
	})
}
