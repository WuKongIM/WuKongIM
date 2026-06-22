package plugin

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/internal/usecase/plugin/pluginproto"
	"github.com/WuKongIM/wkrpc"
)

// DefaultHostRPCMaxBodyBytes is the phase-1 host RPC request and response body limit.
const DefaultHostRPCMaxBodyBytes int64 = 10 << 20

var (
	// ErrUsecaseRequired reports that the host RPC adapter has no usecase target.
	ErrUsecaseRequired = errors.New("plugin host rpc usecase required")
	// ErrTimeoutRequired reports that the host RPC adapter timeout is invalid.
	ErrTimeoutRequired = errors.New("plugin host rpc timeout must be positive")
)

// Options configures the v2 plugin host RPC adapter.
type Options struct {
	// Routes receives plugin host RPC route registrations.
	Routes RouteRegistrar
	// Usecase handles decoded plugin lifecycle requests.
	Usecase Usecase
	// MaxBodyBytes limits decoded host RPC request and response bodies.
	MaxBodyBytes int64
	// Timeout bounds plugin-origin host RPC usecase calls.
	Timeout time.Duration
}

// RouteRegistrar is the narrow wkrpc route surface required by the adapter.
type RouteRegistrar interface {
	Route(path string, handler wkrpc.Handler)
}

// Usecase is the narrow plugin lifecycle host RPC application contract.
type Usecase interface {
	StartPlugin(context.Context, *pluginproto.PluginInfo, string) (*pluginproto.StartupResp, error)
	ClosePlugin(context.Context, string, string) error
	SendMessage(context.Context, *pluginproto.SendReq, string) (*pluginproto.SendResp, error)
}

// Server adapts plugin-origin lifecycle wkrpc host RPCs to the v2 plugin usecase.
type Server struct {
	routes       RouteRegistrar
	usecase      Usecase
	maxBodyBytes int64
	timeout      time.Duration
	now          func() time.Time
}

// NewServer builds the v2 plugin host RPC adapter and registers routes when provided.
func NewServer(opts Options) (*Server, error) {
	if opts.Timeout <= 0 {
		return nil, ErrTimeoutRequired
	}
	if opts.Usecase == nil {
		return nil, ErrUsecaseRequired
	}
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultHostRPCMaxBodyBytes
	}
	s := &Server{
		routes:       opts.Routes,
		usecase:      opts.Usecase,
		maxBodyBytes: maxBodyBytes,
		timeout:      opts.Timeout,
		now:          time.Now,
	}
	if s.routes != nil {
		s.registerRoutes()
	}
	return s, nil
}

func (s *Server) registerRoutes() {
	for _, path := range routePaths {
		path := path
		s.routes.Route(path, s.routeHandler(path))
	}
}

func (s *Server) routeHandler(path string) wkrpc.Handler {
	handler := registeredRouteHandler{server: s, path: path}
	return wkrpc.Handler(handler.handleWKRPC)
}

type registeredRouteHandler struct {
	server *Server
	path   string
}

func (h registeredRouteHandler) handleWKRPC(c *wkrpc.Context) {
	h.handle(wkrpcContext{ctx: c})
}

func (h registeredRouteHandler) handle(c rpcContext) {
	h.server.handlePath(h.path, c)
}

var routePaths = []string{"/plugin/start", "/close", "/message/send"}

func (s *Server) handlePath(path string, c rpcContext) {
	switch path {
	case "/plugin/start":
		s.handlePluginStart(c)
	case "/close":
		s.handleClose(c)
	case "/message/send":
		s.handleSendMessage(c)
	default:
		c.WriteErr(errors.New("plugin host rpc route not registered"))
	}
}
