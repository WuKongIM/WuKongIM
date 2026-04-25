package stdnet

import (
	"errors"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/WuKongIM/WuKongIM/internal/gateway/transport"
	"github.com/gorilla/websocket"
)

type WSListener struct {
	opts    transport.ListenerOptions
	handler transport.ConnHandler

	upgrader websocket.Upgrader

	mu      sync.Mutex
	server  *http.Server
	ln      net.Listener
	stopped bool
	conns   map[uint64]*wsConn

	nextConnID atomic.Uint64
	wg         sync.WaitGroup
}

type wsConn struct {
	id         uint64
	raw        *websocket.Conn
	localAddr  string
	remoteAddr string

	closeOnce sync.Once
	writeType atomic.Int32
}

func NewWSListener(opts transport.ListenerOptions, handler transport.ConnHandler) (*WSListener, error) {
	return &WSListener{
		opts:    opts,
		handler: handler,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(*http.Request) bool { return true },
		},
		conns: make(map[uint64]*wsConn),
	}, nil
}

func (l *WSListener) Start() error {
	l.mu.Lock()
	if l.server != nil {
		l.mu.Unlock()
		return nil
	}

	ln, err := net.Listen("tcp", l.opts.Address)
	if err != nil {
		l.mu.Unlock()
		return err
	}

	mux := http.NewServeMux()
	path := l.opts.Path
	if path == "" {
		path = "/"
	}
	mux.HandleFunc(path, l.handleUpgrade)

	server := &http.Server{
		Handler: mux,
	}

	l.server = server
	l.ln = ln
	l.stopped = false
	l.mu.Unlock()

	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			if l.opts.OnError != nil {
				l.opts.OnError(err)
			}
		}
	}()
	return nil
}

func (l *WSListener) Stop() error {
	l.mu.Lock()
	if l.stopped {
		l.mu.Unlock()
		return nil
	}
	l.stopped = true
	server := l.server
	ln := l.ln
	l.server = nil
	l.ln = nil
	conns := make([]*wsConn, 0, len(l.conns))
	for _, c := range l.conns {
		conns = append(conns, c)
	}
	l.mu.Unlock()

	var firstErr error
	if server != nil {
		if err := server.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if ln != nil {
		if err := ln.Close(); err != nil && firstErr == nil && !errors.Is(err, net.ErrClosed) {
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

func (l *WSListener) Addr() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.ln == nil || l.ln.Addr() == nil {
		return ""
	}
	return "http://" + l.ln.Addr().String()
}

func (l *WSListener) handleUpgrade(w http.ResponseWriter, r *http.Request) {
	conn, err := l.upgrader.Upgrade(w, r, nil)
	if err != nil {
		transport.LogConnectFailure(l.opts, 0, l.localAddr(), r.RemoteAddr, err)
		if l.opts.OnError != nil {
			l.opts.OnError(err)
		}
		return
	}

	raw := conn.UnderlyingConn()
	ws := &wsConn{
		id:         l.nextConnID.Add(1),
		raw:        conn,
		localAddr:  raw.LocalAddr().String(),
		remoteAddr: raw.RemoteAddr().String(),
	}
	l.trackConn(ws)

	if l.handler != nil {
		if err := l.handler.OnOpen(ws); err != nil {
			transport.LogConnectFailure(l.opts, ws.ID(), ws.LocalAddr(), ws.RemoteAddr(), err)
			l.handler.OnClose(ws, err)
			l.untrackConn(ws.ID())
			_ = ws.Close()
			return
		}
	}
	transport.LogConnectSuccess(l.opts, ws)

	l.wg.Add(1)
	go l.readLoop(ws)
}

func (l *WSListener) readLoop(c *wsConn) {
	defer l.wg.Done()

	var closeErr error
	for {
		messageType, payload, err := c.raw.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) || errors.Is(err, net.ErrClosed) {
				closeErr = nil
			} else {
				closeErr = err
			}
			break
		}
		if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
			continue
		}
		c.writeType.Store(int32(messageType))
		if l.handler != nil {
			if err := l.handler.OnData(c, payload); err != nil {
				closeErr = err
				break
			}
		}
	}

	l.untrackConn(c.ID())
	_ = c.Close()
	if l.handler != nil {
		l.handler.OnClose(c, closeErr)
	}
}

func (l *WSListener) trackConn(c *wsConn) {
	if l == nil || c == nil {
		return
	}

	l.mu.Lock()
	l.conns[c.ID()] = c
	l.mu.Unlock()
}

func (l *WSListener) untrackConn(id uint64) {
	if l == nil {
		return
	}

	l.mu.Lock()
	delete(l.conns, id)
	l.mu.Unlock()
}

func (l *WSListener) localAddr() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.ln == nil || l.ln.Addr() == nil {
		return ""
	}
	return l.ln.Addr().String()
}

func (c *wsConn) ID() uint64 {
	if c == nil {
		return 0
	}
	return c.id
}

func (c *wsConn) Write(data []byte) error {
	if c == nil || c.raw == nil {
		return nil
	}

	messageType := int(c.writeType.Load())
	if messageType != websocket.TextMessage && messageType != websocket.BinaryMessage {
		messageType = websocket.BinaryMessage
		if utf8.Valid(data) {
			messageType = websocket.TextMessage
		}
	}
	return c.raw.WriteMessage(messageType, data)
}

func (c *wsConn) Close() error {
	if c == nil || c.raw == nil {
		return nil
	}

	var err error
	c.closeOnce.Do(func() {
		err = c.raw.Close()
	})
	return err
}

func (c *wsConn) LocalAddr() string {
	if c == nil {
		return ""
	}
	return c.localAddr
}

func (c *wsConn) RemoteAddr() string {
	if c == nil {
		return ""
	}
	return c.remoteAddr
}

func (c *wsConn) SetWriteDeadline(deadline time.Time) error {
	if c == nil || c.raw == nil {
		return nil
	}
	return c.raw.SetWriteDeadline(deadline)
}
