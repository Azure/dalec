package bazeltest

import (
	"context"
	"sync/atomic"

	v2 "github.com/bazelbuild/remote-apis/build/bazel/remote/execution/v2"
	"github.com/bazelbuild/remote-apis/build/bazel/semver"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	_ v2.ActionCacheServer  = (*RemoteCache)(nil)
	_ v2.CapabilitiesServer = (*RemoteCache)(nil)
)

type RemoteCache struct {
	Called atomic.Bool
}

func RegisterRemoteCache(s grpc.ServiceRegistrar, srv *RemoteCache) {
	v2.RegisterActionCacheServer(s, srv)
	v2.RegisterCapabilitiesServer(s, srv)
}

func NewRemoteCache() *RemoteCache {
	return &RemoteCache{}
}

func (r *RemoteCache) GetActionResult(ctx context.Context, req *v2.GetActionResultRequest) (*v2.ActionResult, error) {
	// Implement the logic to retrieve the action result from the cache
	r.Called.Store(true)
	return nil, status.Errorf(codes.Unimplemented, "method GetActionResult not implemented")
}

func (r *RemoteCache) UpdateActionResult(ctx context.Context, req *v2.UpdateActionResultRequest) (*v2.ActionResult, error) {
	// Implement the logic to update the action result in the cache
	r.Called.Store(true)
	return nil, status.Errorf(codes.Unimplemented, "method UpdateActionResult not implemented")
}

func (r *RemoteCache) GetCapabilities(ctx context.Context, req *v2.GetCapabilitiesRequest) (*v2.ServerCapabilities, error) {
	// Implement the logic to retrieve server capabilities
	r.Called.Store(true)
	return &v2.ServerCapabilities{
		CacheCapabilities: &v2.CacheCapabilities{
			ActionCacheUpdateCapabilities: &v2.ActionCacheUpdateCapabilities{
				UpdateEnabled: true,
			},
			DigestFunctions: []v2.DigestFunction_Value{
				v2.DigestFunction_SHA256,
			},
		},
		LowApiVersion:  &semver.SemVer{Major: 2, Minor: 0, Patch: 0},
		HighApiVersion: &semver.SemVer{Major: 2, Minor: 0, Patch: 0},
	}, nil
}
