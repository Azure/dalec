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

// Mux is a hack to be able to use buildkit's ssh forwarding with things that are not necessarily ssh agents.
// By default all requests are forwarded to an underlying ssh agent service, but you may register other handlers
// that will be used based on the request ID.
//
// The SSH forwarding API is called by the buildkit server (into the client)
// over the session API to deal with forwarding SSH sockets. Buildkit itself
// does care what the API implementation is as long as it implements the
// [sshforward.SSHServer] API.
// What is used to to provide that API is setup when creating the buildkit
// client.
// So you can, as an example, use [ProxyHandler] for that implementation OR you
// can use the implementation from buildkit, but you can't use both...
// unless you use Mux to handle routing requests from buildkit and send to the
// proper backend based on the socket ID.
type Mux struct {
	routes         map[string]sshforward.SSHServer
	ordered        []string
	defaultHandler sshforward.SSHServer
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
		m.defaultHandler = srv
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

	if m.defaultHandler != nil {
		return m.defaultHandler
	}

	return &defaultMuxHandler{ID: id}
}

func (m *Mux) ForwardAgent(stream sshforward.SSH_ForwardAgentServer) error {
	opts, _ := metadata.FromIncomingContext(stream.Context()) // if no metadata continue with empty object

	// `id` here would be the socket ID being requested.
	// This is the ID that the client uses to identify the socket it wants to forward.
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
