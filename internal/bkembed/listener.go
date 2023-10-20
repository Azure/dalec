package bkembed

import (
	"context"
	"net"
	"sync"
)

type DialerFn func(context.Context) (net.Conn, error)

func PipeListener() (net.Listener, DialerFn) {
	l := &pipeListener{ch: make(chan net.Conn)}
	return l, l.Dial
}

type pipeListener struct {
	ch      chan net.Conn
	closers sync.Map

	mu     sync.Mutex
	closed bool
}

func (l *pipeListener) Accept() (net.Conn, error) {
	c, ok := <-l.ch
	if !ok {
		return nil, net.ErrClosed
	}

	go l.closers.Store(c, c)
	closer := func() {
		l.closers.Delete(c)
	}
	return pipeWrapCloser(c, closer), nil
}

func (l *pipeListener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return nil
	}

	close(l.ch)
	l.closers.Range(func(key, value interface{}) bool {
		c := key.(net.Conn) // nolint: forcetypeassert
		c.Close()
		return true
	})

	l.closed = true

	return nil
}

func (l *pipeListener) Addr() net.Addr {
	return &pipeAddr{}
}

type pipeAddr struct{}

func (a pipeAddr) Network() string {
	return "pipe"
}

func (a pipeAddr) String() string {
	return ""
}

func (l *pipeListener) Dial(_ context.Context) (net.Conn, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, net.ErrClosed
	}

	c1, c2 := net.Pipe()
	l.ch <- c1
	return c2, nil
}

func pipeWrapCloser(c net.Conn, f func()) net.Conn {
	return &pipeWrap{Conn: c, cb: f}
}

type pipeWrap struct {
	net.Conn
	cb func()
}

func (p *pipeWrap) Close() error {
	p.cb()
	return p.Conn.Close()
}
