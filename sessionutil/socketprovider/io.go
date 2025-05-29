package socketprovider

import (
	"github.com/moby/buildkit/session/sshforward"
	"google.golang.org/grpc"
)

type streamWriter struct {
	stream grpc.BidiStreamingClient[sshforward.BytesMessage, sshforward.BytesMessage]
}

func (sw *streamWriter) Write(p []byte) (n int, err error) {
	msg := &sshforward.BytesMessage{Data: p}
	if err := sw.stream.Send(msg); err != nil {
		return 0, err
	}

	return len(p), nil
}

type streamReader struct {
	stream grpc.BidiStreamingClient[sshforward.BytesMessage, sshforward.BytesMessage]
	buf    []byte
}

func (sr *streamReader) Read(p []byte) (int, error) {
	if len(sr.buf) > 0 {
		n := copy(p, sr.buf)
		sr.buf = sr.buf[n:]
		return n, nil
	}

	msg, err := sr.stream.Recv()
	if err != nil {
		return 0, err
	}

	n := copy(p, msg.Data)
	if n < len(msg.Data) {
		sr.buf = append(sr.buf, msg.Data[n:]...)
	}
	return n, nil
}
