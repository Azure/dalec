package socketprovider

import (
	"context"
	"fmt"
	"net"

	"github.com/moby/buildkit/session/sshforward"
	"github.com/pkg/errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type ProxyConfig struct {
	// ID is the identifier for the proxy connection.
	// If empty, the default ID will be used.
	// This must be unique across all proxies in a single [ProxyHandler] instance.
	ID string
	// Dialer is the function that will be used to establish a connection to the
	// proxy target.
	Dialer DialFn
}

type DialFn func(ctx context.Context) (net.Conn, error)

// ProxyHandler implements the sshforward.SSHServer interface
// It can be used to create a raw proxy over buildkit's SSH forwarding mechanism.
//
// Create one with [NewProxyHandler]
type ProxyHandler struct {
	m map[string]DialFn
}

func UnixDialer(path string) DialFn {
	return func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
}

func NewProxyHandler(configs []ProxyConfig) (*ProxyHandler, error) {
	sp := &ProxyHandler{
		m: make(map[string]DialFn, len(configs)),
	}

	for _, config := range configs {
		id := config.ID
		if id == "" {
			id = sshforward.DefaultID
		}

		if _, ok := sp.m[id]; ok {
			return nil, fmt.Errorf("duplicate socket proxy ID %s", id)
		}
		if config.Dialer == nil {
			return nil, fmt.Errorf("empty socket proxy path for ID %s", id)
		}
		sp.m[id] = config.Dialer
	}

	return sp, nil
}

// Register registers the ProxyHandler with the given gRPC server.
//
// This is what allows the ProxyHandler to act as a [session.Attachable] service to be used with a buildkit client.
// In this case the ProxyHandler will be run as a GRPC service on the client which buildkit will connect to
// to establish the connection to the proxy target (using the [ProxyHandler.ForwardAgent] method).
func (h *ProxyHandler) Register(srv *grpc.Server) {
	sshforward.RegisterSSHServer(srv, h)
}

// CheckAgent checks if a connection exists for the given ID.
//
// Buildkit will call this method to verify if the agent is available before attempting to forward it.
func (h *ProxyHandler) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	id := sshforward.DefaultID
	if req.ID != "" {
		id = req.ID
	}
	if _, ok := h.m[id]; ok {
		return &sshforward.CheckAgentResponse{}, nil
	}

	return nil, fmt.Errorf("no connection found for ID %s", req.ID)
}

// ForwardAgent creates a bi-directional stream with the caller.
//
// Messages on the stream are used to encapsulate the raw data to be forwarded
// between the client and the target proxy connection.
//
// The ID of the connection is determined by the metadata in the context using the
// sshforward.KeySSHID key. If not set, the default ID will be used.
func (h *ProxyHandler) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	ctx := stream.Context()

	id := sshforward.DefaultID
	opts, _ := metadata.FromIncomingContext(ctx)
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}

	dial, ok := h.m[id]
	if !ok {
		return errors.Errorf("unset ssh forward key %s", id)
	}

	conn, err := dial(ctx)
	if err != nil {
		return errors.Wrapf(err, "failed to connect to %s", id)
	}
	defer conn.Close()

	return sshforward.Copy(ctx, conn, stream, nil)
}
