package socketprovider

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"gotest.tools/v3/assert"
)

// echoServer implements a message based echo server
// It expects tos end and receive discreete json messages over a net.Conn
// Using discreete messages is helpful for testing purposes over a literal echo.
type echoServer struct{}

type echoRequest struct {
	Data string `json:"data"`
}

type echoResponse struct {
	Recvd string `json:"recvd"`
	Count int    `json:"count"`
}

func (es *echoServer) Serve(l net.Listener) error {
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go func() {
			dec := json.NewDecoder(conn)
			enc := json.NewEncoder(conn)

			var req echoRequest
			var resp echoResponse

			for i := 1; ; i++ {
				if err := dec.Decode(&req); err != nil {
					conn.Close()
					return
				}

				resp.Recvd = req.Data
				resp.Count = i
				if err := enc.Encode(&resp); err != nil {
					conn.Close()
					return
				}
			}
		}()
	}
}

func dialerFnToGRPCDialer(dialer func(ctx context.Context) (net.Conn, error)) grpc.DialOption {
	return grpc.WithContextDialer(func(ctx context.Context, addr string) (net.Conn, error) {
		return dialer(ctx)
	})
}

func TestProxyHandler(t *testing.T) {
	handlerListener := &PipeListener{}
	defer handlerListener.Close()

	echoListener := &PipeListener{}
	defer echoListener.Close()
	echo := &echoServer{}
	go echo.Serve(echoListener)

	handler, err := NewProxyHandler([]ProxyConfig{
		{ID: "test", Dialer: echoListener.Dialer},
	})
	assert.NilError(t, err)

	srv := grpc.NewServer()
	handler.Register(srv)

	// Start proxy handler service
	go srv.Serve(handlerListener)

	// passthrough:// is a special scheme that allows us to use the handlerListener's Dialer directly
	// otherwise grpc will try to resolve whatever we put in there.
	c, err := grpc.NewClient("passthrough://", dialerFnToGRPCDialer(handlerListener.Dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	assert.NilError(t, err)
	defer c.Close()

	client := sshforward.NewSSHClient(c)

	ctx := context.Background()

	_, err = client.CheckAgent(ctx, &sshforward.CheckAgentRequest{ID: "does-not-exist"})
	assert.ErrorContains(t, err, "no connection found")

	_, err = client.CheckAgent(ctx, &sshforward.CheckAgentRequest{ID: "test"})
	assert.NilError(t, err)

	ctx = metadata.AppendToOutgoingContext(ctx, sshforward.KeySSHID, "test")
	stream, err := client.ForwardAgent(ctx)
	assert.NilError(t, err)

	defer stream.CloseSend()

	req := echoRequest{Data: "hello, world!"}
	sw := &streamWriter{stream: stream}

	enc := json.NewEncoder(sw)
	err = enc.Encode(&req)
	assert.NilError(t, err)

	sr := &streamReader{stream: stream}
	var resp echoResponse
	dec := json.NewDecoder(sr)

	err = dec.Decode(&resp)
	assert.NilError(t, err)
	assert.Equal(t, resp.Recvd, req.Data)
	assert.Equal(t, resp.Count, 1)

	req.Data = "another message"
	err = enc.Encode(&req)
	assert.NilError(t, err)

	err = dec.Decode(&resp)
	assert.NilError(t, err)
	assert.Equal(t, resp.Recvd, req.Data)
	assert.Equal(t, resp.Count, 2)

	// Test larger message
	req.Data = strings.Repeat("x", 10000)
	err = enc.Encode(&req)
	assert.NilError(t, err)

	err = dec.Decode(&resp)
	assert.NilError(t, err)
	assert.Equal(t, resp.Recvd, req.Data)
	assert.Equal(t, resp.Count, 3)
}
