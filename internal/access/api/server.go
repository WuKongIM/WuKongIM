package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"

	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	"github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	"github.com/WuKongIM/WuKongIM/internal/usecase/message"
	testdatausecase "github.com/WuKongIM/WuKongIM/internal/usecase/testdata"
	"github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

var ErrListenAddrRequired = errors.New("access/api: listen address required")

type MessageUsecase interface {
	Send(ctx context.Context, cmd message.SendCommand) (message.SendResult, error)
	SyncChannelMessages(ctx context.Context, query message.SyncChannelMessagesQuery) (message.SyncChannelMessagesResult, error)
}

// CMDSyncUsecase serves legacy durable command-message sync APIs.
type CMDSyncUsecase interface {
	Sync(ctx context.Context, query cmdsync.SyncQuery) (cmdsync.SyncResult, error)
	SyncAck(ctx context.Context, cmd cmdsync.SyncAckCommand) error
}

type UserUsecase interface {
	UpdateToken(ctx context.Context, cmd user.UpdateTokenCommand) error
	DeviceQuit(ctx context.Context, cmd user.DeviceQuitCommand) error
	OnlineStatus(ctx context.Context, uids []string) ([]user.OnlineStatus, error)
	AddSystemUIDs(ctx context.Context, uids []string) error
	RemoveSystemUIDs(ctx context.Context, uids []string) error
	ListSystemUIDs(ctx context.Context) ([]string, error)
	AddSystemUIDsToCache(uids []string) error
	RemoveSystemUIDsFromCache(uids []string) error
}

type ChannelUsecase interface {
	Upsert(ctx context.Context, cmd channelusecase.UpsertCommand) error
	UpdateInfo(ctx context.Context, info channelusecase.Info) error
	Delete(ctx context.Context, key channelusecase.ChannelKey) error
	AddSubscribers(ctx context.Context, cmd channelusecase.SubscriberCommand) error
	RemoveSubscribers(ctx context.Context, cmd channelusecase.SubscriberCommand) error
	RemoveAllSubscribers(ctx context.Context, key channelusecase.ChannelKey) error
	SetTempSubscribers(ctx context.Context, cmd channelusecase.TempSubscriberCommand) error
	AddDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error
	SetDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error
	RemoveDenylist(ctx context.Context, cmd channelusecase.MemberCommand) error
	RemoveAllDenylist(ctx context.Context, key channelusecase.ChannelKey) error
	AddAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error
	SetAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error
	RemoveAllowlist(ctx context.Context, cmd channelusecase.MemberCommand) error
	RemoveAllAllowlist(ctx context.Context, key channelusecase.ChannelKey) error
	ListAllowlist(ctx context.Context, key channelusecase.ChannelKey) (channelusecase.MemberListResult, error)
}

// TestDataUsecase generates deterministic datasets for process-level e2e tests.
type TestDataUsecase interface {
	GenerateSlotSnapshotUsers(ctx context.Context, cmd testdatausecase.GenerateSlotSnapshotUsersCommand) (testdatausecase.GenerateSlotSnapshotUsersResult, error)
	GenerateControllerSnapshotJobs(ctx context.Context, cmd testdatausecase.GenerateControllerSnapshotJobsCommand) (testdatausecase.GenerateControllerSnapshotJobsResult, error)
}

// DiagnosticsReader queries node-local diagnostics events for debug API routes.
type DiagnosticsReader interface {
	QueryDiagnostics(ctx context.Context, query obsdiagnostics.Query) obsdiagnostics.QueryResult
}

type ConversationUsecase interface {
	Sync(ctx context.Context, query conversationusecase.SyncQuery) (conversationusecase.SyncResult, error)
	ClearUnread(ctx context.Context, cmd conversationusecase.ClearUnreadCommand) error
	SetUnread(ctx context.Context, cmd conversationusecase.SetUnreadCommand) error
	DeleteConversation(ctx context.Context, cmd conversationusecase.DeleteConversationCommand) error
}

type LegacyRouteAddresses struct {
	TCPAddr string
	WSAddr  string
	WSSAddr string
}

type Options struct {
	ListenAddr               string
	Messages                 MessageUsecase
	CMDSync                  CMDSyncUsecase
	Users                    UserUsecase
	Channels                 ChannelUsecase
	TestMode                 bool
	TestData                 TestDataUsecase
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
	DiagnosticsDebugEnabled  bool
	Diagnostics              DiagnosticsReader
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
	cmdSync                  CMDSyncUsecase
	users                    UserUsecase
	channels                 ChannelUsecase
	testMode                 bool
	testData                 TestDataUsecase
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
	diagnosticsDebugEnabled  bool
	diagnostics              DiagnosticsReader
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
		cmdSync:                  opts.CMDSync,
		users:                    opts.Users,
		channels:                 opts.Channels,
		testMode:                 opts.TestMode,
		testData:                 opts.TestData,
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
		diagnosticsDebugEnabled:  opts.DiagnosticsDebugEnabled,
		diagnostics:              opts.Diagnostics,
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
