package plugin

import (
	"context"
	"errors"
	"time"

	"github.com/WuKongIM/WuKongIM/pkg/plugin/pluginproto"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/wkrpc"
)

// DefaultHostRPCMaxBodyBytes is the phase-1 host RPC request and response body limit.
const DefaultHostRPCMaxBodyBytes int64 = 10 << 20

// ErrUnimplementedStreamRPC is returned for stream host RPCs until phase 1 wires a stream runtime.
var ErrUnimplementedStreamRPC = errors.New("plugin stream rpc unimplemented in phase 1")

// ErrUsecaseRequired is returned when the host RPC adapter has no usecase target.
var ErrUsecaseRequired = errors.New("plugin host rpc usecase required")

// Options configures the plugin host RPC adapter.
type Options struct {
	// Routes receives all plugin host RPC route registrations.
	Routes RouteRegistrar
	// Usecase handles decoded plugin host RPC requests.
	Usecase Usecase
	// MaxBodyBytes limits decoded host RPC request and response bodies.
	MaxBodyBytes int64
	// Timeout bounds plugin-origin host RPC usecase calls.
	Timeout time.Duration
	// Logger records adapter-level diagnostics.
	Logger wklog.Logger
	// Now supplies time for tests and future adapter timestamps.
	Now func() time.Time
}

// RouteRegistrar is the narrow wkrpc route surface required by the adapter.
type RouteRegistrar interface {
	Route(path string, handler wkrpc.Handler)
}

// Usecase is the narrow plugin host RPC application contract.
type Usecase interface {
	StartPlugin(ctx context.Context, info *pluginproto.PluginInfo, callerUID string) (*pluginproto.StartupResp, error)
	ClosePlugin(ctx context.Context, pluginNo string, callerUID string) error
	SendMessage(ctx context.Context, req *pluginproto.SendReq, callerUID string) (*pluginproto.SendResp, error)
	ChannelMessages(ctx context.Context, req *pluginproto.ChannelMessageBatchReq, callerUID string) (*pluginproto.ChannelMessageBatchResp, error)
	HTTPForward(ctx context.Context, req *pluginproto.ForwardHttpReq, callerUID string) (*pluginproto.HttpResponse, error)
	ClusterConfig(ctx context.Context, callerUID string) (*pluginproto.ClusterConfig, error)
	ClusterChannelsBelongNode(ctx context.Context, req *pluginproto.ClusterChannelBelongNodeReq, callerUID string) (*pluginproto.ClusterChannelBelongNodeBatchResp, error)
	ConversationChannels(ctx context.Context, req *pluginproto.ConversationChannelReq, callerUID string) (*pluginproto.ConversationChannelResp, error)
}

// Server adapts plugin-origin wkrpc host RPCs to plugin usecases.
type Server struct {
	routes       RouteRegistrar
	usecase      Usecase
	maxBodyBytes int64
	timeout      time.Duration
	logger       wklog.Logger
	now          func() time.Time
}

// NewServer builds the plugin host RPC adapter and registers routes when provided.
func NewServer(opts Options) (*Server, error) {
	if opts.Timeout <= 0 {
		return nil, errors.New("plugin host rpc timeout must be positive")
	}
	if opts.Usecase == nil {
		return nil, ErrUsecaseRequired
	}
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 {
		maxBodyBytes = DefaultHostRPCMaxBodyBytes
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	s := &Server{
		routes:       opts.Routes,
		usecase:      opts.Usecase,
		maxBodyBytes: maxBodyBytes,
		timeout:      opts.Timeout,
		logger:       opts.Logger,
		now:          now,
	}
	if s.routes != nil {
		s.registerRoutes()
	}
	return s, nil
}

// MaxBodyBytes returns the configured request and response body limit.
func (s *Server) MaxBodyBytes() int64 { return s.maxBodyBytes }

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

var routePaths = []string{
	"/plugin/start",
	"/close",
	"/message/send",
	"/channel/messages",
	"/plugin/httpForward",
	"/cluster/config",
	"/cluster/channels/belongNode",
	"/conversation/channels",
	"/stream/open",
	"/stream/write",
	"/stream/close",
}

func (s *Server) handlePath(path string, c rpcContext) {
	switch path {
	case "/plugin/start":
		s.handlePluginStart(c)
	case "/close":
		s.handleClose(c)
	case "/message/send":
		s.handleSendMessage(c)
	case "/channel/messages":
		s.handleChannelMessages(c)
	case "/plugin/httpForward":
		s.handleHTTPForward(c)
	case "/cluster/config":
		s.handleClusterConfig(c)
	case "/cluster/channels/belongNode":
		s.handleClusterChannelsBelongNode(c)
	case "/conversation/channels":
		s.handleConversationChannels(c)
	case "/stream/open", "/stream/write", "/stream/close":
		s.handleUnimplementedStream(path, c)
	default:
		c.WriteErr(errors.New("plugin host rpc route not registered"))
	}
}
