package stdnet

import (
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
)

type TCPListener struct {
	opts    transport.ListenerOptions
	handler transport.ConnHandler

	mu      sync.Mutex
	ln      net.Listener
	stopped bool
	conns   map[uint64]*conn

	nextConnID atomic.Uint64
	wg         sync.WaitGroup
}

func NewTCPListener(opts transport.ListenerOptions, handler transport.ConnHandler) (*TCPListener, error) {
	return &TCPListener{
		opts:    opts,
		handler: handler,
		conns:   make(map[uint64]*conn),
	}, nil
}

func (l *TCPListener) Start() error {
	l.mu.Lock()
	if l.ln != nil {
		l.mu.Unlock()
		return nil
	}

	ln, err := net.Listen("tcp", l.opts.Address)
	if err != nil {
		l.mu.Unlock()
		return err
	}
	l.ln = ln
	l.stopped = false
	l.mu.Unlock()

	l.wg.Add(1)
	go l.acceptLoop()
	return nil
}

func (l *TCPListener) Stop() error {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return nil
	}
	l.stopped = true
	ln := l.ln
	l.ln = nil
	conns := make([]*conn, 0, len(l.conns))
	for _, c := range l.conns {
		conns = append(conns, c)
	}
	l.mu.Unlock()

	var firstErr error
	if ln != nil {
		if err := ln.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	for _, c := range conns {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}

	l.wg.Wait()
	return firstErr
}

func (l *TCPListener) Addr() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.ln == nil || l.ln.Addr() == nil {
		return ""
	}
	return l.ln.Addr().String()
}

func (l *TCPListener) acceptLoop() {
	defer l.wg.Done()

	for {
		l.mu.Lock()
		ln := l.ln
		stopped := l.stopped
		l.mu.Unlock()

		if stopped || ln == nil {
			return
		}

		rawConn, err := ln.Accept()
		if err != nil {
			if l.isStopping(err) {
				return
			}
			if l.opts.OnError != nil {
				l.opts.OnError(err)
			}
			continue
		}

		c := newConn(l.nextConnID.Add(1), rawConn)
		l.trackConn(c)

		if l.handler != nil {
			if err := l.handler.OnOpen(c); err != nil {
				transport.LogConnectFailure(l.opts, c.ID(), c.LocalAddr(), c.RemoteAddr(), err)
				l.handler.OnClose(c, err)
				l.untrackConn(c.ID())
				_ = c.Close()
				continue
			}
		}
		transport.LogConnectSuccess(l.opts, c)

		l.wg.Add(1)
		go l.readLoop(c)
	}
}

func (l *TCPListener) readLoop(c *conn) {
	defer l.wg.Done()

	buf := make([]byte, 4096)
	var closeErr error
	for {
		n, err := c.Read(buf)
		if n > 0 && l.handler != nil {
			payload := append([]byte(nil), buf[:n]...)
			if dataErr := l.handler.OnData(c, payload); dataErr != nil {
				closeErr = dataErr
				break
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				closeErr = nil
			} else {
				closeErr = err
			}
			break
		}
	}

	l.untrackConn(c.ID())
	_ = c.Close()
	if l.handler != nil {
		l.handler.OnClose(c, closeErr)
	}
}

func (l *TCPListener) trackConn(c *conn) {
	if l == nil || c == nil {
		return
	}

	l.mu.Lock()
	l.conns[c.ID()] = c
	l.mu.Unlock()
}

func (l *TCPListener) untrackConn(id uint64) {
	if l == nil {
		return
	}

	l.mu.Lock()
	delete(l.conns, id)
	l.mu.Unlock()
}

func (l *TCPListener) isStopping(err error) bool {
	if err == nil {
		return false
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stopped || errors.Is(err, net.ErrClosed)
}
