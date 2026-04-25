package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"

	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

var ErrListenAddrRequired = errors.New("access/api: listen address required")

type MessageUsecase interface {
	Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error)
}

type UserUsecase interface {
	UpdateToken(ctx context.Context, cmd user.UpdateTokenCommand) error
}

type ConversationUsecase interface {
	Sync(ctx context.Context, query conversationusecase.SyncQuery) (conversationusecase.SyncResult, error)
}

type LegacyRouteAddresses struct {
	TCPAddr string
	WSAddr  string
	WSSAddr string
}

type Options struct {
	ListenAddr               string
	Messages                 MessageUsecase
	Users                    UserUsecase
	Conversations            ConversationUsecase
	ConversationDefaultLimit int
	ConversationMaxLimit     int
	MetricsHandler           http.Handler
	HealthDetailEnabled      bool
	HealthDetails            func() any
	Readyz                   func(context.Context) (bool, any)
	DebugEnabled             bool
	DebugConfig              func() any
	DebugCluster             func() any
	LegacyRouteExternal      LegacyRouteAddresses
	LegacyRouteIntranet      LegacyRouteAddresses
	Logger                   wklog.Logger
}

type Server struct {
	mu                       sync.RWMutex
	engine                   *gin.Engine
	httpServer               *http.Server
	listener                 net.Listener
	listenAddr               string
	addr                     string
	messages                 MessageUsecase
	users                    UserUsecase
	conversations            ConversationUsecase
	conversationDefaultLimit int
	conversationMaxLimit     int
	metricsHandler           http.Handler
	healthDetailEnabled      bool
	healthDetails            func() any
	readyz                   func(context.Context) (bool, any)
	debugEnabled             bool
	debugConfig              func() any
	debugCluster             func() any
	legacyRouteExternal      LegacyRouteAddresses
	legacyRouteIntranet      LegacyRouteAddresses
	logger                   wklog.Logger
	started                  bool
}

func New(opts Options) *Server {
	if gin.Mode() != gin.ReleaseMode {
		gin.SetMode(gin.ReleaseMode)
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	defaultLimit, maxLimit := normalizeConversationLimits(opts.ConversationDefaultLimit, opts.ConversationMaxLimit)
	engine := gin.New()
	engine.Use(openCORSMiddleware())
	srv := &Server{
		engine:                   engine,
		listenAddr:               opts.ListenAddr,
		messages:                 opts.Messages,
		users:                    opts.Users,
		conversations:            opts.Conversations,
		conversationDefaultLimit: defaultLimit,
		conversationMaxLimit:     maxLimit,
		metricsHandler:           opts.MetricsHandler,
		healthDetailEnabled:      opts.HealthDetailEnabled,
		healthDetails:            opts.HealthDetails,
		readyz:                   opts.Readyz,
		debugEnabled:             opts.DebugEnabled,
		debugConfig:              opts.DebugConfig,
		debugCluster:             opts.DebugCluster,
		legacyRouteExternal:      opts.LegacyRouteExternal,
		legacyRouteIntranet:      opts.LegacyRouteIntranet,
		logger:                   opts.Logger,
	}
	srv.registerRoutes()
	return srv
}

func (s *Server) Engine() *gin.Engine {
	if s == nil {
		return nil
	}
	return s.engine
}

func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

func (s *Server) Start() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return nil
	}
	listenAddr := s.listenAddr
	engine := s.engine
	s.mu.Unlock()

	if listenAddr == "" {
		return ErrListenAddrRequired
	}

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}

	httpServer := &http.Server{Handler: engine}

	s.mu.Lock()
	s.listener = ln
	s.httpServer = httpServer
	s.addr = ln.Addr().String()
	s.started = true
	s.mu.Unlock()

	go func() {
		_ = httpServer.Serve(ln)
	}()

	return nil
}

func (s *Server) Stop(ctx context.Context) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	httpServer := s.httpServer
	s.httpServer = nil
	s.listener = nil
	s.started = false
	s.mu.Unlock()

	if httpServer == nil {
		return nil
	}
	return httpServer.Shutdown(ctx)
}

func normalizeConversationLimits(defaultLimit, maxLimit int) (int, int) {
	if defaultLimit <= 0 {
		defaultLimit = 200
	}
	if maxLimit <= 0 {
		maxLimit = 500
	}
	if defaultLimit > maxLimit {
		defaultLimit = maxLimit
	}
	return defaultLimit, maxLimit
}
