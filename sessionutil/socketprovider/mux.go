package socketprovider

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Mux is a hack to be able to use buildkit's ssh forwarding with things that are not neccessarily ssh agents.
// By default all requests are forwarded to an underlying ssh agent service, but you may register other handlers
// that will be used based on the request ID.
type Mux struct {
	routes         map[string]sshforward.SSHServer
	ordered        []string
	defaultHanlder sshforward.SSHServer
}

// WithMuxRoute registers a new route with the given prefix. The prefix is used to match incoming requests.
func WithMuxRoute(prefix string, srv sshforward.SSHServer) MuxOption {
	return func(m *Mux) {
		m.routes[prefix] = srv
		m.ordered = append(m.ordered, prefix)
	}
}

// WithDefaultHandler sets the default handler for all requests that do not match any registered route.
func WithDefaultHandler(srv sshforward.SSHServer) MuxOption {
	return func(m *Mux) {
		m.defaultHanlder = srv
	}
}

type MuxOption func(*Mux)

func NewMux(opts ...MuxOption) *Mux {
	var m Mux
	m.routes = make(map[string]sshforward.SSHServer)
	m.ordered = make([]string, 0)

	for _, opt := range opts {
		opt(&m)
	}
	slices.Sort(m.ordered)
	slices.Reverse(m.ordered)

	return &m
}

var _ session.Attachable = (*Mux)(nil)
var _ sshforward.SSHServer = (*Mux)(nil)

func (m *Mux) Register(srv *grpc.Server) {
	sshforward.RegisterSSHServer(srv, m)
}

func (m *Mux) CheckAgent(ctx context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	h := m.getHandler(req.ID)
	return h.CheckAgent(ctx, req)
}

func (m *Mux) getHandler(id string) sshforward.SSHServer {
	if srv, ok := m.routes[id]; ok {
		return srv
	}

	for _, prefix := range m.ordered {
		if strings.HasPrefix(id, prefix) {
			return m.routes[prefix]
		}
	}

	if m.defaultHanlder != nil {
		return m.defaultHanlder
	}

	return &defaultMuxHandler{ID: id}
}

func (m *Mux) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object

	var id string
	if v, ok := opts[sshforward.KeySSHID]; ok && len(v) > 0 && v[0] != "" {
		id = v[0]
	}
	h := m.getHandler(id)
	return h.ForwardAgent(stream)
}

type defaultMuxHandler struct {
	ID string
}

func (h *defaultMuxHandler) CheckAgent(_ context.Context, req *sshforward.CheckAgentRequest) (*sshforward.CheckAgentResponse, error) {
	return &sshforward.CheckAgentResponse{}, fmt.Errorf("no handler for key %s", h.ID)
}

func (h *defaultMuxHandler) ForwardAgent(sshforward.SSH_ForwardAgentServer) error {
	return fmt.Errorf("no handler for key %s", h.ID)
}
