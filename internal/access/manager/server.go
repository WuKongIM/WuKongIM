package manager

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	managementusecase "github.com/WuKongIM/WuKongIM/internal/usecase/management"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

// ErrListenAddrRequired reports that the manager listen address is missing.
var ErrListenAddrRequired = errors.New("access/manager: listen address required")

// Management exposes the manager usecases needed by HTTP handlers.
type Management interface {
	// ListNodes returns manager-facing node DTOs.
	ListNodes(ctx context.Context) ([]managementusecase.Node, error)
	// GetNode returns one manager-facing node detail DTO.
	GetNode(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error)
	// MarkNodeDraining marks a node as draining and returns the latest detail DTO.
	MarkNodeDraining(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error)
	// ResumeNode marks a node as alive and returns the latest detail DTO.
	ResumeNode(ctx context.Context, nodeID uint64) (managementusecase.NodeDetail, error)
	// ListSlots returns manager-facing slot DTOs.
	ListSlots(ctx context.Context) ([]managementusecase.Slot, error)
	// GetSlot returns one manager-facing slot detail DTO.
	GetSlot(ctx context.Context, slotID uint32) (managementusecase.SlotDetail, error)
	// TransferSlotLeader transfers one slot leader and returns the latest detail DTO.
	TransferSlotLeader(ctx context.Context, slotID uint32, targetNodeID uint64) (managementusecase.SlotDetail, error)
	// RecoverSlot runs one slot recover flow and returns the latest outcome DTO.
	RecoverSlot(ctx context.Context, slotID uint32, strategy managementusecase.SlotRecoverStrategy) (managementusecase.SlotRecoverResult, error)
	// RebalanceSlots starts one slot rebalance flow and returns the started plan DTO.
	RebalanceSlots(ctx context.Context) (managementusecase.SlotRebalanceResult, error)
	// ListTasks returns manager-facing reconcile task DTOs.
	ListTasks(ctx context.Context) ([]managementusecase.Task, error)
	// GetTask returns one manager-facing reconcile task detail DTO.
	GetTask(ctx context.Context, slotID uint32) (managementusecase.TaskDetail, error)
	// ListConnections returns manager-facing local connection DTOs.
	ListConnections(ctx context.Context) ([]managementusecase.Connection, error)
	// GetConnection returns one manager-facing local connection detail DTO.
	GetConnection(ctx context.Context, sessionID uint64) (managementusecase.ConnectionDetail, error)
	// ListChannelRuntimeMeta returns one manager-facing channel runtime metadata page.
	ListChannelRuntimeMeta(ctx context.Context, req managementusecase.ListChannelRuntimeMetaRequest) (managementusecase.ListChannelRuntimeMetaResponse, error)
	// GetChannelRuntimeMeta returns one manager-facing channel runtime metadata detail DTO.
	GetChannelRuntimeMeta(ctx context.Context, channelID string, channelType int64) (managementusecase.ChannelRuntimeMetaDetail, error)
	// ListMessages returns one manager-facing channel message page.
	ListMessages(ctx context.Context, req managementusecase.ListMessagesRequest) (managementusecase.ListMessagesResponse, error)
	// GetOverview returns the manager homepage overview DTO.
	GetOverview(ctx context.Context) (managementusecase.Overview, error)
}

// PermissionConfig binds a resource to allowed actions.
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

// AuthConfig configures manager JWT authentication and permissions.
type AuthConfig struct {
	// On enables JWT login and permission enforcement.
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

// Options configures the manager HTTP server.
type Options struct {
	// ListenAddr is the manager server listen address.
	ListenAddr string
	// Auth configures manager JWT login and permissions.
	Auth AuthConfig
	// Management provides the manager read usecases.
	Management Management
	// Logger is the logger used by the manager server.
	Logger wklog.Logger
}

// Server serves the manager HTTP API.
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
		listenAddr: opts.ListenAddr,
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

// Stop gracefully shuts the manager HTTP server down.
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
