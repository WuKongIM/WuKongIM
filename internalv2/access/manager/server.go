package manager

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internalv2/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

// ErrListenAddrRequired reports that the manager listen address is missing.
var ErrListenAddrRequired = errors.New("internalv2/access/manager: listen address required")

// PermissionConfig binds a manager resource to allowed actions.
type PermissionConfig struct {
	// Resource is the protected manager resource name; use "*" to grant all manager resources.
	Resource string
	// Actions contains the allowed action codes.
	Actions []string
}

// UserConfig describes one static manager login user.
type UserConfig struct {
	// Username is the static login identity.
	Username string
	// Password is the static login secret.
	Password string
	// Permissions lists the resource permissions granted to the user.
	Permissions []PermissionConfig
}

// AuthConfig configures manager JWT authentication.
type AuthConfig struct {
	// On enables JWT login for manager routes.
	On bool
	// JWTSecret is the HMAC signing secret for manager tokens.
	JWTSecret string
	// JWTIssuer is the issuer claim used in manager tokens.
	JWTIssuer string
	// JWTExpire is the token lifetime used for new manager tokens.
	JWTExpire time.Duration
	// Users contains the configured static manager users.
	Users []UserConfig
}

// Management exposes the manager read usecases needed by HTTP handlers.
type Management interface {
	// ListNodes returns manager-facing node DTOs.
	ListNodes(ctx context.Context) (managementusecase.NodeList, error)
	// ListSlots returns manager-facing slot DTOs.
	ListSlots(ctx context.Context, opts managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error)
	// ListSlotLogEntries returns one node-local Slot Raft log page.
	ListSlotLogEntries(ctx context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error)
	// ListControllerLogEntries returns one node-local Controller Raft log page.
	ListControllerLogEntries(ctx context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error)
	// ListBusinessChannels returns manager-facing channel metadata rows.
	ListBusinessChannels(ctx context.Context, req managementusecase.ListBusinessChannelsRequest) (managementusecase.ListBusinessChannelsResponse, error)
	// ListChannelRuntimeMeta returns manager-facing channel runtime metadata rows.
	ListChannelRuntimeMeta(ctx context.Context, req managementusecase.ListChannelRuntimeMetaRequest) (managementusecase.ListChannelRuntimeMetaResponse, error)
	// ListRecentConversations returns manager-facing recent conversations for one UID.
	ListRecentConversations(ctx context.Context, req managementusecase.RecentConversationsRequest) (managementusecase.RecentConversationsResponse, error)
	// ListMessages returns manager-facing channel messages.
	ListMessages(ctx context.Context, req managementusecase.ListMessagesRequest) (managementusecase.ListMessagesResponse, error)
	// AdvanceMessageRetention advances one channel's message retention boundary.
	AdvanceMessageRetention(ctx context.Context, req managementusecase.AdvanceMessageRetentionRequest) (managementusecase.AdvanceMessageRetentionResponse, error)
	// ListConnections returns manager-facing local connection DTOs.
	ListConnections(ctx context.Context, req managementusecase.ListConnectionsRequest) ([]managementusecase.Connection, error)
	// GetConnection returns one manager-facing local connection detail DTO.
	GetConnection(ctx context.Context, req managementusecase.GetConnectionRequest) (managementusecase.ConnectionDetail, error)
	// ListUsers returns manager-facing user metadata rows.
	ListUsers(ctx context.Context, req managementusecase.ListUsersRequest) (managementusecase.ListUsersResponse, error)
	// GetUser returns one manager-facing user detail.
	GetUser(ctx context.Context, uid string) (managementusecase.UserDetail, error)
	// KickUser forces one user's sessions offline.
	KickUser(ctx context.Context, req managementusecase.KickUserRequest) (managementusecase.KickUserResponse, error)
	// ResetUserToken resets one user's device token.
	ResetUserToken(ctx context.Context, req managementusecase.ResetUserTokenRequest) (managementusecase.ResetUserTokenResponse, error)
	// ListSystemUsers returns persisted system UID rows.
	ListSystemUsers(ctx context.Context) (managementusecase.ListSystemUsersResponse, error)
	// AddSystemUsers persists system UID rows.
	AddSystemUsers(ctx context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error)
	// RemoveSystemUsers removes persisted system UID rows.
	RemoveSystemUsers(ctx context.Context, req managementusecase.MutateSystemUsersRequest) (managementusecase.MutateSystemUsersResponse, error)
}

// Options configures the manager HTTP server.
type Options struct {
	// ListenAddr is the manager server listen address.
	ListenAddr string
	// Auth configures manager JWT login.
	Auth AuthConfig
	// Management provides manager read usecases.
	Management Management
	// Logger is the logger used by the manager server.
	Logger wklog.Logger
}

// Server serves the internalv2 manager HTTP API.
type Server struct {
	mu         sync.RWMutex
	engine     *gin.Engine
	httpServer *http.Server
	listener   net.Listener
	listenAddr string
	addr       string
	management Management
	auth       authState
	logger     wklog.Logger
	started    bool
}

// New constructs a manager HTTP server.
func New(opts Options) *Server {
	if gin.Mode() != gin.ReleaseMode {
		gin.SetMode(gin.ReleaseMode)
	}
	if opts.Logger == nil {
		opts.Logger = wklog.NewNop()
	}
	engine := gin.New()
	engine.Use(openCORSMiddleware())
	srv := &Server{
		engine:     engine,
		listenAddr: strings.TrimSpace(opts.ListenAddr),
		management: opts.Management,
		auth:       newAuthState(opts.Auth),
		logger:     opts.Logger,
	}
	srv.registerRoutes()
	return srv
}

// Engine returns the underlying gin engine.
func (s *Server) Engine() *gin.Engine {
	if s == nil {
		return nil
	}
	return s.engine
}

// Addr returns the resolved manager listen address after Start.
func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Start begins serving the manager HTTP API.
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
	handler := s.engine
	s.mu.Unlock()
	if listenAddr == "" {
		return ErrListenAddrRequired
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return err
	}
	httpServer := &http.Server{Handler: handler}
	s.mu.Lock()
	s.listener = ln
	s.httpServer = httpServer
	s.addr = ln.Addr().String()
	s.started = true
	s.mu.Unlock()
	go func() {
		if serveErr := httpServer.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			s.httpLogger().Error("manager http serve failed",
				wklog.Event("internalv2.access.manager.serve_failed"),
				wklog.String("addr", s.Addr()),
				wklog.Error(serveErr),
			)
		}
	}()
	return nil
}

// Stop gracefully shuts down the manager HTTP API.
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

func (s *Server) registerRoutes() {
	if s == nil || s.engine == nil {
		return
	}
	if s.auth.enabled() {
		s.engine.POST("/manager/login", s.handleLogin)
	}
	nodes := s.engine.Group("/manager")
	if s.auth.enabled() {
		nodes.Use(s.requirePermission("cluster.node", "r"))
	}
	nodes.GET("/nodes", s.handleNodes)

	slots := s.engine.Group("/manager")
	if s.auth.enabled() {
		slots.Use(s.requirePermission("cluster.slot", "r"))
	}
	slots.GET("/slots", s.handleSlots)
	slots.GET("/slots/:slot_id/logs", s.handleSlotLogs)

	controllerLogs := s.engine.Group("/manager")
	if s.auth.enabled() {
		controllerLogs.Use(s.requirePermission("cluster.controller", "r"))
	}
	controllerLogs.GET("/controller/logs", s.handleControllerLogs)

	channels := s.engine.Group("/manager")
	if s.auth.enabled() {
		channels.Use(s.requirePermission("cluster.channel", "r"))
	}
	channels.GET("/channel-runtime-meta", s.handleChannelRuntimeMeta)
	channels.GET("/channels", s.handleBusinessChannels)
	channels.GET("/conversations", s.handleConversations)
	channels.GET("/messages", s.handleMessages)

	connections := s.engine.Group("/manager")
	if s.auth.enabled() {
		connections.Use(s.requirePermission("cluster.connection", "r"))
	}
	connections.GET("/connections", s.handleConnections)
	connections.GET("/connections/:session_id", s.handleConnection)

	channelWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		channelWrites.Use(s.requirePermission("cluster.channel", "w"))
	}
	channelWrites.POST("/messages/retention", s.handleAdvanceMessageRetention)

	userReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		userReads.Use(s.requirePermission("cluster.user", "r"))
	}
	userReads.GET("/users", s.handleUsers)
	userReads.GET("/users/:uid", s.handleUser)
	userReads.GET("/system-users", s.handleSystemUsers)

	userWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		userWrites.Use(s.requirePermission("cluster.user", "w"))
	}
	userWrites.POST("/users/:uid/kick", s.handleUserKick)
	userWrites.POST("/users/:uid/token/reset", s.handleUserTokenReset)
	userWrites.POST("/system-users/add", s.handleSystemUsersAdd)
	userWrites.POST("/system-users/remove", s.handleSystemUsersRemove)
}

func (s *Server) httpLogger() wklog.Logger {
	if s == nil || s.logger == nil {
		return wklog.NewNop()
	}
	return s.logger.Named("http")
}
