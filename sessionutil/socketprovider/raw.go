package socketprovider

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/moby/buildkit/session/sshforward"
	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type SocketProxyConfig struct {
	ID   string
	Path string
}

type SocketProxyHandler struct {
	m map[string]string
}

func NewSocketProxyHandler(configs []SocketProxyConfig) (*SocketProxyHandler, error) {
	sp := &SocketProxyHandler{
		m: make(map[string]string, len(configs)),
	}

	for _, config := range configs {
		id := config.ID
		if id == "" {
			id = sshforward.DefaultID
		}

		if _, ok := sp.m[id]; ok {
			return nil, fmt.Errorf("duplicate socket proxy ID %s", id)
		}
		if config.Path == "" {
			return nil, fmt.Errorf("empty socket proxy path for ID %s", id)
		}
		sp.m[config.ID] = config.Path
	}

	return sp, nil
}

func (h *SocketProxyHandler) Register(srv *grpc.Server) {
	sshforward.RegisterSSHServer(srv, h)
}

func (h *SocketProxyHandler) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	id := sshforward.DefaultID
	if req.ID != "" {
		id = req.ID
	}
	if _, ok := h.m[id]; ok {
		return &sshforward.CheckAgentResponse{}, nil
	}

	return nil, fmt.Errorf("no connection found for ID %s", req.ID)
}

func (h *SocketProxyHandler) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	id := sshforward.DefaultID
	opts, _ := metadata.FromIncomingContext(stream.Context())
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}

	src, ok := h.m[id]
	if !ok {
		return errors.Errorf("unset ssh forward key %s", id)
	}

	conn, err := net.Dial("unix", src)
	if err != nil {
		return errors.Wrapf(err, "failed to connect to %s", src)
	}
	defer conn.Close()

	s1, s2 := net.Pipe()
	eg, ctx := errgroup.WithContext(stream.Context())

	eg.Go(func() error {
		_, err := io.Copy(conn, s1)
		return err
	})

	eg.Go(func() error {
		defer s1.Close()
		return sshforward.Copy(ctx, s2, stream, nil)
	})

	return eg.Wait()
}
