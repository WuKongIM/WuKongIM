package pluginhost

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/WuKongIM/wkrpc"
	wkrpcproto "github.com/WuKongIM/wkrpc/proto"
)

// SocketServer exposes the node-local Unix socket used by PDK-compatible plugins.
type SocketServer interface {
	// Route registers a byte-oriented host RPC path; access adapters own business handlers.
	Route(path string, handler wkrpc.Handler)
	// Start starts the Unix socket server.
	Start() error
	// Stop stops the Unix socket server.
	Stop()
	// RequestWithContext sends a request to a connected plugin UID.
	RequestWithContext(ctx context.Context, uid, path string, body []byte) ([]byte, error)
	// Request sends a request to a connected plugin UID and satisfies RPCClient.
	Request(ctx context.Context, uid, path string, body []byte) ([]byte, error)
	// Send sends a byte message to a connected plugin UID and satisfies RPCClient.
	Send(uid string, msgType uint32, body []byte) error
}

type socketBackend interface {
	Route(path string, handler wkrpc.Handler)
	Start() error
	Stop()
	RequestWithContext(ctx context.Context, uid, path string, body []byte) (*wkrpcproto.Response, error)
	Send(uid string, msg *wkrpcproto.Message) error
}

const defaultSocketReadyTimeout = 2 * time.Second
const maxUnixSocketPathBytes = 100

// WKRPCSocketServer wraps a wkrpc Unix socket server without registering business routes.
type WKRPCSocketServer struct {
	socketPath   string
	backend      socketBackend
	readyTimeout time.Duration
	readyCheck   func(string, time.Duration) error

	mu      sync.Mutex
	started bool
}

// NewSocketServer creates a Unix socket server for plugin host RPC traffic.
func NewSocketServer(socketPath string) *WKRPCSocketServer {
	return newSocketServerWithBackend(socketPath, wkrpc.New("unix://"+socketPath))
}

func newSocketServerWithBackend(socketPath string, backend socketBackend) *WKRPCSocketServer {
	return &WKRPCSocketServer{
		socketPath:   socketPath,
		backend:      backend,
		readyTimeout: defaultSocketReadyTimeout,
		readyCheck:   waitUnixSocketReady,
	}
}

// Route registers a host RPC route on the underlying wkrpc server.
func (s *WKRPCSocketServer) Route(path string, handler wkrpc.Handler) {
	s.backend.Route(path, handler)
}

// Start creates the socket parent directory, removes stale sockets, and starts wkrpc.
func (s *WKRPCSocketServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}
	if err := validateUnixSocketPath(s.socketPath); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o755); err != nil {
		return fmt.Errorf("create plugin socket dir: %w", err)
	}
	if err := removeStaleSocket(s.socketPath); err != nil {
		return err
	}
	if err := s.backend.Start(); err != nil {
		return fmt.Errorf("start plugin socket %q: %w", s.socketPath, err)
	}
	if err := s.readyCheck(s.socketPath, s.readyTimeout); err != nil {
		s.backend.Stop()
		return err
	}
	s.started = true
	return nil
}

func validateUnixSocketPath(socketPath string) error {
	if len(socketPath) > maxUnixSocketPathBytes {
		return fmt.Errorf("plugin socket path is %d bytes, max %d bytes", len(socketPath), maxUnixSocketPathBytes)
	}
	return nil
}

func removeStaleSocket(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat plugin socket %q: %w", socketPath, err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("plugin socket path %q exists and is not a socket", socketPath)
	}
	if err := os.Remove(socketPath); err != nil {
		return fmt.Errorf("remove stale plugin socket %q: %w", socketPath, err)
	}
	return nil
}

func waitUnixSocketReady(socketPath string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = defaultSocketReadyTimeout
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		conn, err := net.DialTimeout("unix", socketPath, 10*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			return fmt.Errorf("wait plugin socket %q ready: %w", socketPath, lastErr)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Stop stops the underlying socket server once.
func (s *WKRPCSocketServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started {
		return
	}
	s.backend.Stop()
	s.started = false
}

// RequestWithContext sends a byte request and returns the response body.
func (s *WKRPCSocketServer) RequestWithContext(ctx context.Context, uid, path string, body []byte) ([]byte, error) {
	resp, err := s.backend.RequestWithContext(ctx, uid, path, body)
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("plugin socket returned nil response")
	}
	if resp.Status != wkrpcproto.StatusOK {
		return nil, fmt.Errorf("plugin socket request %q failed with status %d", path, resp.Status)
	}
	return append([]byte(nil), resp.Body...), nil
}

// Request sends a byte request and returns the response body.
func (s *WKRPCSocketServer) Request(ctx context.Context, uid, path string, body []byte) ([]byte, error) {
	return s.RequestWithContext(ctx, uid, path, body)
}

// Send sends a byte message to the plugin UID.
func (s *WKRPCSocketServer) Send(uid string, msgType uint32, body []byte) error {
	return s.backend.Send(uid, &wkrpcproto.Message{MsgType: msgType, Content: append([]byte(nil), body...), Timestamp: uint64(time.Now().UnixMilli())})
}
