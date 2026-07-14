package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"strings"
	"sync"
	"time"

	obsdiagnostics "github.com/WuKongIM/WuKongIM/internal/observability/diagnostics"
	channelusecase "github.com/WuKongIM/WuKongIM/internal/usecase/channel"
	cmdsyncusecase "github.com/WuKongIM/WuKongIM/internal/usecase/cmdsync"
	conversationusecase "github.com/WuKongIM/WuKongIM/internal/usecase/conversation"
	messageusecase "github.com/WuKongIM/WuKongIM/internal/usecase/message"
	userusecase "github.com/WuKongIM/WuKongIM/internal/usecase/user"
	"github.com/WuKongIM/WuKongIM/pkg/bench/model"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/gin-gonic/gin"
)

const versionV1 = "bench/v1"

// ErrListenAddrRequired reports that the HTTP API listen address is empty.
var ErrListenAddrRequired = errors.New("internal/access/api: listen address required")

// GatewayAddresses are the externally reachable client gateway addresses exposed to wkbench.
type GatewayAddresses struct {
	// TCPAddr is the WKProto TCP gateway address used by wkbench workers.
	TCPAddr string `json:"tcp_addr"`
	// WSAddr is the WebSocket gateway address reserved for future benchmark workers.
	WSAddr string `json:"ws_addr"`
	// WSSAddr is the secure WebSocket gateway address reserved for future benchmark workers.
	WSSAddr string `json:"wss_addr"`
}

// ChannelRuntimeBenchController exposes benchmark-only channel runtime controls.
type ChannelRuntimeBenchController interface {
	Snapshot(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeSnapshot, error)
	Probe(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeProbeResult, error)
	Evict(context.Context, model.ChannelRuntimeQuery) (model.ChannelRuntimeEvictResult, error)
}

// PresenceBenchController exposes benchmark-only presence route diagnostics.
type PresenceBenchController interface {
	Snapshot(context.Context) (model.PresenceSnapshot, error)
}

// DiagnosticsReader queries node-local diagnostics events for debug API routes.
type DiagnosticsReader interface {
	QueryDiagnostics(ctx context.Context, query obsdiagnostics.Query) obsdiagnostics.QueryResult
}

// BenchData accepts benchmark setup mutations backed by the composition root.
type BenchData interface {
	UpsertChannels(context.Context, []BenchChannelMutation) (int, error)
	AddSubscribers(context.Context, []BenchSubscriberMutation) (int, error)
}

// LegacyRouteAddresses contains legacy client gateway addresses returned by /route APIs.
type LegacyRouteAddresses struct {
	// TCPAddr is the published WKProto TCP gateway address.
	TCPAddr string
	// WSAddr is the published WebSocket gateway address.
	WSAddr string
	// WSSAddr is the published secure WebSocket gateway address.
	WSSAddr string
}

// LegacyRouteNodeAddresses stores public and intranet route addresses for one cluster node.
type LegacyRouteNodeAddresses struct {
	// External is returned by legacy route APIs unless the request asks for intranet addresses.
	External LegacyRouteAddresses
	// Intranet is returned by legacy route APIs when intranet=1 is supplied.
	Intranet LegacyRouteAddresses
}

// MessageUsecase coordinates compatible message send and channel sync routes.
type MessageUsecase interface {
	Send(context.Context, messageusecase.SendCommand) (messageusecase.SendResult, error)
	AppendMessageEvent(context.Context, messageusecase.MessageEventAppend) (messageusecase.MessageEventAppendResult, error)
	SyncChannelMessages(context.Context, messageusecase.SyncChannelMessagesQuery) (messageusecase.SyncChannelMessagesResult, error)
}

// CMDSyncUsecase coordinates compatible durable command-message sync routes.
type CMDSyncUsecase interface {
	Sync(context.Context, cmdsyncusecase.SyncQuery) (cmdsyncusecase.SyncResult, error)
	SyncAck(context.Context, cmdsyncusecase.SyncAckCommand) error
}

// ConversationUsecase coordinates compatible conversation list and sync routes.
type ConversationUsecase interface {
	List(context.Context, conversationusecase.ListRequest) (conversationusecase.ListResult, error)
	Sync(context.Context, conversationusecase.SyncQuery) (conversationusecase.SyncResult, error)
	ClearUnread(context.Context, conversationusecase.ClearUnreadCommand) error
	SetUnread(context.Context, conversationusecase.SetUnreadCommand) error
	DeleteConversation(context.Context, conversationusecase.DeleteConversationCommand) error
}

// ConversationListObservation captures one /conversation/list request result.
type ConversationListObservation struct {
	// Result is a low-cardinality request result label.
	Result string
	// Duration is the end-to-end handler latency.
	Duration time.Duration
	// ReturnedItems is the number of conversation rows returned to the client.
	ReturnedItems int
	// SparseItems is the number of returned rows using sparse active ordering.
	SparseItems int
	// LastMessageLoads is the number of last-message loads attempted for returned rows.
	LastMessageLoads int
	// LastMessageErrors is the number of last-message load errors observed by the request.
	LastMessageErrors int
	// ActiveIndexStaleSkips is the number of stale active-index rows skipped by the request.
	ActiveIndexStaleSkips int
	// More reports whether the active page has another page after this response.
	More bool
}

// ConversationSyncObservation captures one /conversation/sync request result.
type ConversationSyncObservation struct {
	// Result is a low-cardinality request result label.
	Result string
	// Duration is the end-to-end handler latency.
	Duration time.Duration
	// OnlyUnread reports whether the request asked for unread conversations only.
	OnlyUnread bool
	// WithRecents reports whether the request asked for recent messages.
	WithRecents bool
	// ReturnedItems is the number of conversation rows returned to the client.
	ReturnedItems int
	// OverlayItems is the number of client-known overlay candidates before sync filtering.
	OverlayItems int
	// RecentLoadDuration records how long recent-message loading took when requested.
	RecentLoadDuration time.Duration
}

// ConversationListObserver receives performance observations for conversation list reads.
type ConversationListObserver interface {
	ObserveConversationList(ConversationListObservation)
}

// ConversationSyncObserver receives performance observations for conversation sync reads.
type ConversationSyncObserver interface {
	ObserveConversationSync(ConversationSyncObservation)
}

// ChannelUsecase coordinates compatible channel metadata and member mutations.
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

// UserUsecase coordinates compatible user token, device, online-status, and system UID routes.
type UserUsecase interface {
	UpdateToken(ctx context.Context, cmd userusecase.UpdateTokenCommand) error
	DeviceQuit(ctx context.Context, cmd userusecase.DeviceQuitCommand) error
	OnlineStatus(ctx context.Context, uids []string) ([]userusecase.OnlineStatus, error)
	AddSystemUIDs(ctx context.Context, uids []string) error
	RemoveSystemUIDs(ctx context.Context, uids []string) error
	ListSystemUIDs(ctx context.Context) ([]string, error)
	AddSystemUIDsToCache(uids []string) error
	RemoveSystemUIDsFromCache(uids []string) error
}

// BenchChannelMutation describes one benchmark channel metadata upsert.
type BenchChannelMutation struct {
	// ChannelID identifies the benchmark channel.
	ChannelID string
	// ChannelType is the WuKong channel type for ChannelID.
	ChannelType uint8
	// Large marks a large-group channel.
	Large bool
	// Ban blocks channel messaging when true.
	Ban bool
	// Disband marks a channel as disbanded.
	Disband bool
	// SendBan blocks sending while allowing receives.
	SendBan bool
	// AllowStranger permits stranger sends for compatible channel semantics.
	AllowStranger bool
}

// BenchSubscriberMutation describes one benchmark channel subscriber append.
type BenchSubscriberMutation struct {
	// ChannelID identifies the benchmark channel.
	ChannelID string
	// ChannelType is the WuKong channel type for ChannelID.
	ChannelType uint8
	// Subscribers are user IDs appended to the benchmark channel subscriber set.
	Subscribers []string
}

// Options configures the minimal internal HTTP API server.
type Options struct {
	// ListenAddr is the HTTP API listen address. An empty value makes Start fail.
	ListenAddr string
	// Readyz reports whether the node is ready for benchmark traffic.
	Readyz func(context.Context) (bool, any)
	// BenchEnabled exposes /bench/v1/* routes for controlled benchmark runs.
	BenchEnabled bool
	// BenchToken optionally requires an exact bearer capability on every /bench/v1/* route.
	BenchToken string
	// BenchMaxBatchSize limits top-level records accepted by one bench mutation request.
	BenchMaxBatchSize int
	// BenchMaxPayloadBytes limits bench mutation JSON request bodies in bytes.
	BenchMaxPayloadBytes int64
	// Gateway contains the published gateway addresses returned by /bench/v1/capacity-target.
	Gateway GatewayAddresses
	// BenchRuntime controls benchmark-only channel runtime diagnostics when configured.
	BenchRuntime ChannelRuntimeBenchController
	// BenchPresence controls benchmark-only presence route diagnostics when configured.
	BenchPresence PresenceBenchController
	// BenchData stores benchmark channel/subscriber setup when configured.
	BenchData BenchData
	// Top provides the node-local read-only operations snapshot for wkcli top.
	Top TopSnapshotProvider
	// Channels handles compatible channel metadata and member-list routes.
	Channels ChannelUsecase
	// Users handles compatible user token, device, online-status, and system UID routes.
	Users UserUsecase
	// Messages handles compatible message send and channel message sync routes.
	Messages MessageUsecase
	// CMDSync handles compatible durable command-message sync routes.
	CMDSync CMDSyncUsecase
	// Conversations handles compatible conversation list routes.
	Conversations ConversationUsecase
	// ConversationListObserver records conversation list read performance.
	ConversationListObserver ConversationListObserver
	// ConversationSyncObserver records conversation sync read performance.
	ConversationSyncObserver ConversationSyncObserver
	// LegacyRouteExternal is the default public gateway address set returned by /route APIs.
	LegacyRouteExternal LegacyRouteAddresses
	// LegacyRouteIntranet is the default intranet gateway address set returned by /route APIs.
	LegacyRouteIntranet LegacyRouteAddresses
	// LegacyRouteNodes maps node_id query parameters to node-specific legacy route addresses.
	LegacyRouteNodes map[uint64]LegacyRouteNodeAddresses
	// MetricsHandler serves Prometheus metrics when configured.
	MetricsHandler http.Handler
	// DebugAPIEnabled exposes local /debug endpoints when their handlers are configured.
	DebugAPIEnabled bool
	// DebugConfig returns a bounded configuration snapshot for /debug/config.
	DebugConfig func() any
	// DebugCluster returns a bounded cluster snapshot for /debug/cluster.
	DebugCluster func() any
	// Diagnostics reads the node-local diagnostics store for debug query endpoints.
	Diagnostics DiagnosticsReader
	// GoroutineSnapshot returns the current goroutine registry snapshot. Nil disables the endpoint.
	GoroutineSnapshot func() any
	// Logger records HTTP API failures that are not otherwise visible to callers.
	Logger wklog.Logger
}

// Server exposes health, readiness, and the minimum bench/v1 target surface for wukongim.
type Server struct {
	mu                       sync.RWMutex
	engine                   *gin.Engine
	httpServer               *http.Server
	listener                 net.Listener
	listenAddr               string
	addr                     string
	readyz                   func(context.Context) (bool, any)
	benchEnabled             bool
	benchToken               string
	benchMaxBatchSize        int
	benchMaxPayloadBytes     int64
	gateway                  GatewayAddresses
	benchRuntime             ChannelRuntimeBenchController
	benchPresence            PresenceBenchController
	benchData                BenchData
	top                      TopSnapshotProvider
	channels                 ChannelUsecase
	users                    UserUsecase
	messages                 MessageUsecase
	cmdSync                  CMDSyncUsecase
	conversations            ConversationUsecase
	conversationObserver     ConversationListObserver
	conversationSyncObserver ConversationSyncObserver
	legacyRouteExternal      LegacyRouteAddresses
	legacyRouteIntranet      LegacyRouteAddresses
	legacyRouteNodes         map[uint64]LegacyRouteNodeAddresses
	metricsHandler           http.Handler
	debugAPIEnabled          bool
	debugConfig              func() any
	debugCluster             func() any
	goroutineSnapshot        func() any
	diagnostics              DiagnosticsReader
	logger                   wklog.Logger
	counts                   map[string]int
	started                  bool
}

// New creates a minimal internal API server.
func New(opts Options) *Server {
	if gin.Mode() != gin.ReleaseMode {
		gin.SetMode(gin.ReleaseMode)
	}
	engine := gin.New()
	engine.Use(openCORSMiddleware())
	engine.HandleMethodNotAllowed = true
	s := &Server{
		engine:                   engine,
		listenAddr:               strings.TrimSpace(opts.ListenAddr),
		readyz:                   opts.Readyz,
		benchEnabled:             opts.BenchEnabled,
		benchToken:               strings.TrimSpace(opts.BenchToken),
		benchMaxBatchSize:        opts.BenchMaxBatchSize,
		benchMaxPayloadBytes:     opts.BenchMaxPayloadBytes,
		gateway:                  opts.Gateway,
		benchRuntime:             opts.BenchRuntime,
		benchPresence:            opts.BenchPresence,
		benchData:                opts.BenchData,
		top:                      opts.Top,
		channels:                 opts.Channels,
		users:                    opts.Users,
		messages:                 opts.Messages,
		cmdSync:                  opts.CMDSync,
		conversations:            opts.Conversations,
		conversationObserver:     opts.ConversationListObserver,
		conversationSyncObserver: opts.ConversationSyncObserver,
		legacyRouteExternal:      opts.LegacyRouteExternal,
		legacyRouteIntranet:      opts.LegacyRouteIntranet,
		legacyRouteNodes:         cloneLegacyRouteNodes(opts.LegacyRouteNodes),
		metricsHandler:           opts.MetricsHandler,
		debugAPIEnabled:          opts.DebugAPIEnabled,
		debugConfig:              opts.DebugConfig,
		debugCluster:             opts.DebugCluster,
		goroutineSnapshot:        opts.GoroutineSnapshot,
		diagnostics:              opts.Diagnostics,
		logger:                   opts.Logger,
		counts:                   map[string]int{},
	}
	if s.logger == nil {
		s.logger = wklog.NewNop()
	}
	s.registerRoutes()
	return s
}

func cloneLegacyRouteNodes(nodes map[uint64]LegacyRouteNodeAddresses) map[uint64]LegacyRouteNodeAddresses {
	if len(nodes) == 0 {
		return nil
	}
	out := make(map[uint64]LegacyRouteNodeAddresses, len(nodes))
	for nodeID, addrs := range nodes {
		out[nodeID] = addrs
	}
	return out
}

// Handler returns the HTTP handler for tests and in-process harnesses.
func (s *Server) Handler() http.Handler {
	if s == nil || s.engine == nil {
		return http.NotFoundHandler()
	}
	return s.engine
}

// Engine returns the underlying gin engine for tests and in-process harnesses.
func (s *Server) Engine() *gin.Engine {
	if s == nil {
		return nil
	}
	return s.engine
}

// Addr returns the bound listen address after Start succeeds.
func (s *Server) Addr() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.addr
}

// Start begins serving the HTTP API.
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
	handler := s.Handler()
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
			s.httpLogger().Error("api http serve failed",
				wklog.Event("internal.access.api.serve_failed"),
				wklog.String("addr", s.Addr()),
				wklog.Error(serveErr),
			)
		}
	}()
	return nil
}

func (s *Server) httpLogger() wklog.Logger {
	if s == nil || s.logger == nil {
		return wklog.NewNop()
	}
	return s.logger.Named("http")
}

// Stop gracefully shuts down the HTTP API.
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
	s.engine.GET("/healthz", s.handleHealthz)
	s.engine.GET("/readyz", s.handleReadyz)
	if s.metricsHandler != nil {
		s.engine.Any("/metrics", s.handleMetrics)
	}
	if s.debugAPIEnabled {
		s.registerPProfRoutes()
		if s.debugConfig != nil {
			s.engine.GET("/debug/config", s.handleDebugConfig)
		}
		if s.debugCluster != nil {
			s.engine.GET("/debug/cluster", s.handleDebugCluster)
		}
		if s.diagnostics != nil {
			s.registerDiagnosticsRoutes()
		}
	}
	s.engine.GET("/route", s.handleRoute)
	s.engine.POST("/route/batch", s.handleRouteBatch)
	s.registerChannelRoutes()
	s.registerUserRoutes()
	s.registerMessageRoutes()
	s.registerConversationRoutes()
	s.engine.GET("/top/v1/snapshot", s.handleTopSnapshot)
	if !s.benchEnabled {
		return
	}
	bench := s.engine.Group("/bench/v1")
	if s.benchToken != "" {
		bench.Use(s.requireBenchToken)
	}
	bench.GET("/capabilities", s.handleBenchCapabilities)
	bench.GET("/capacity-target", s.handleBenchCapacityTarget)
	bench.GET("/snapshot", s.handleBenchSnapshot)
	bench.GET("/presence/snapshot", s.handleBenchPresenceSnapshot)
	bench.GET("/channel-runtime/snapshot", s.handleBenchChannelRuntimeSnapshot)
	bench.POST("/channel-runtime/probe", s.handleBenchChannelRuntimeProbe)
	bench.POST("/channel-runtime/evict", s.handleBenchChannelRuntimeEvict)
	bench.POST("/users/tokens", s.handleBenchTokens)
	bench.POST("/channels", s.handleBenchChannels)
	bench.POST("/channels/subscribers", s.handleBenchSubscribers)
}

func (s *Server) requireBenchToken(c *gin.Context) {
	provided, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
	expectedDigest := sha256.Sum256([]byte(s.benchToken))
	providedDigest := sha256.Sum256([]byte(provided))
	if !ok || provided == "" || subtle.ConstantTimeCompare(expectedDigest[:], providedDigest[:]) != 1 {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "bench bearer token required"})
		return
	}
	c.Next()
}

func (s *Server) registerPProfRoutes() {
	s.engine.GET("/debug/goroutines", s.handleDebugGoroutines)
	s.engine.GET("/debug/goroutines/summary", s.handleDebugGoroutinesSummary)
	s.engine.Any("/debug/pprof", handlePProf)
	s.engine.Any("/debug/pprof/*name", handlePProf)
}

func (s *Server) handleMetrics(c *gin.Context) {
	if s == nil || s.metricsHandler == nil {
		c.Status(http.StatusNotFound)
		return
	}
	s.metricsHandler.ServeHTTP(c.Writer, c.Request)
}

func handlePProf(c *gin.Context) {
	r := c.Request
	name := strings.TrimPrefix(r.URL.Path, "/debug/pprof/")
	if name == r.URL.Path {
		name = ""
	}
	switch name {
	case "cmdline":
		pprof.Cmdline(c.Writer, r)
	case "profile":
		pprof.Profile(c.Writer, r)
	case "symbol":
		pprof.Symbol(c.Writer, r)
	case "trace":
		pprof.Trace(c.Writer, r)
	default:
		pprof.Index(c.Writer, r)
	}
}

func (s *Server) handleHealthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (s *Server) handleReadyz(c *gin.Context) {
	if s.readyz == nil {
		c.JSON(http.StatusOK, gin.H{"ready": true})
		return
	}
	ready, body := s.readyz(c.Request.Context())
	if ready {
		c.JSON(http.StatusOK, body)
		return
	}
	c.JSON(http.StatusServiceUnavailable, body)
}

type capabilitiesResponse struct {
	// Enabled confirms the target exposes the benchmark-only API surface.
	Enabled bool `json:"enabled"`
	// Version is the target bench API version.
	Version string `json:"version"`
	// Supports lists preparation features supported by this target.
	Supports capabilitiesSupports `json:"supports"`
	// Limits lists request limits visible to wkbench.
	Limits capabilitiesLimits `json:"limits"`
}

type capabilitiesSupports struct {
	UsersTokensBatch        bool `json:"users_tokens_batch"`
	ChannelsBatch           bool `json:"channels_batch"`
	ChannelSubscribersBatch bool `json:"channel_subscribers_batch"`
	Snapshot                bool `json:"snapshot"`
	// PresenceSnapshot indicates support for connection-route presence snapshots.
	PresenceSnapshot bool `json:"presence_snapshot"`
	// ChannelRuntimeSnapshot indicates support for local channel runtime snapshots.
	ChannelRuntimeSnapshot bool `json:"channel_runtime_snapshot"`
	// ChannelRuntimeProbe indicates support for bounded channel runtime probes.
	ChannelRuntimeProbe bool `json:"channel_runtime_probe"`
	// ChannelRuntimeEvict indicates support for bounded channel runtime eviction.
	ChannelRuntimeEvict bool `json:"channel_runtime_evict"`
	// ChannelRuntimeFaults indicates support for runtime fault injection controls.
	ChannelRuntimeFaults bool `json:"channel_runtime_faults"`
	// ChannelRuntimeActivate indicates support for server-side diagnostic activation.
	ChannelRuntimeActivate bool     `json:"channel_runtime_activate"`
	ChannelTypes           []string `json:"channel_types"`
}

type capabilitiesLimits struct {
	MaxBatchSize    int   `json:"max_batch_size"`
	MaxPayloadBytes int64 `json:"max_payload_bytes"`
}

func (s *Server) handleBenchCapabilities(c *gin.Context) {
	c.JSON(http.StatusOK, capabilitiesResponse{
		Enabled: true,
		Version: versionV1,
		Supports: capabilitiesSupports{
			UsersTokensBatch:        true,
			ChannelsBatch:           s.benchData != nil,
			ChannelSubscribersBatch: s.benchData != nil,
			Snapshot:                true,
			PresenceSnapshot:        s.benchPresence != nil,
			ChannelRuntimeSnapshot:  s.benchRuntime != nil,
			ChannelRuntimeProbe:     s.benchRuntime != nil,
			ChannelRuntimeEvict:     s.benchRuntime != nil,
			ChannelRuntimeFaults:    false,
			ChannelRuntimeActivate:  false,
			ChannelTypes:            []string{"group"},
		},
		Limits: capabilitiesLimits{
			MaxBatchSize:    s.benchMaxBatchSize,
			MaxPayloadBytes: s.benchMaxPayloadBytes,
		},
	})
}

type capacityTargetResponse struct {
	// Version is the benchmark API version that produced this target document.
	Version string `json:"version"`
	// Gateway contains gateway addresses published by this target node.
	Gateway GatewayAddresses `json:"gateway"`
}

func (s *Server) handleBenchCapacityTarget(c *gin.Context) {
	c.JSON(http.StatusOK, capacityTargetResponse{Version: versionV1, Gateway: s.gateway})
}

type snapshotResponse struct {
	Version string         `json:"version"`
	Counts  map[string]int `json:"counts,omitempty"`
}

func (s *Server) handleBenchSnapshot(c *gin.Context) {
	counts := s.snapshotCounts()
	resp := snapshotResponse{Version: versionV1}
	if len(counts) > 0 {
		resp.Counts = counts
	}
	c.JSON(http.StatusOK, resp)
}

func (s *Server) handleBenchPresenceSnapshot(c *gin.Context) {
	if s.benchPresence == nil {
		writeBenchError(c, http.StatusNotImplemented, "bench presence controller is not configured")
		return
	}
	resp, err := s.benchPresence.Snapshot(c.Request.Context())
	if err != nil {
		s.logBenchFailure(c, "internal.access.api.bench_presence_failed", "presence_snapshot", err)
		writeBenchError(c, http.StatusInternalServerError, err.Error())
		return
	}
	if resp.Version == "" {
		resp.Version = versionV1
	}
	c.JSON(http.StatusOK, resp)
}

type tokensRequest struct {
	RunID   string          `json:"run_id"`
	BatchID string          `json:"batch_id"`
	Upsert  bool            `json:"upsert,omitempty"`
	Users   []userTokenItem `json:"users,omitempty"`
	Items   []userTokenItem `json:"items,omitempty"`
}

type userTokenItem struct {
	UID         string `json:"uid"`
	Token       string `json:"token"`
	DeviceFlag  uint8  `json:"device_flag,omitempty"`
	DeviceLevel uint8  `json:"device_level,omitempty"`
}

func (r tokensRequest) tokenItems() []userTokenItem {
	if len(r.Users) > 0 {
		return r.Users
	}
	return r.Items
}

func (s *Server) handleBenchTokens(c *gin.Context) {
	var req tokensRequest
	if !s.bindBenchJSON(c, &req) {
		return
	}
	items := req.tokenItems()
	if err := s.validateMutation(req.RunID, req.BatchID, len(items)); err != nil {
		writeBenchError(c, http.StatusBadRequest, err.Error())
		return
	}
	s.addCount("accepted_users", len(items))
	c.JSON(http.StatusOK, mutationResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: len(items)})
}

type channelsRequest struct {
	RunID    string        `json:"run_id"`
	BatchID  string        `json:"batch_id"`
	Upsert   bool          `json:"upsert,omitempty"`
	Channels []channelItem `json:"channels,omitempty"`
	Items    []channelItem `json:"items,omitempty"`
}

type channelItem struct {
	ChannelID     string `json:"channel_id"`
	ChannelType   uint8  `json:"channel_type"`
	Large         bool   `json:"large,omitempty"`
	Ban           bool   `json:"ban,omitempty"`
	Disband       bool   `json:"disband,omitempty"`
	SendBan       bool   `json:"send_ban,omitempty"`
	AllowStranger bool   `json:"allow_stranger,omitempty"`
}

func (r channelsRequest) channelItems() []channelItem {
	if len(r.Channels) > 0 {
		return r.Channels
	}
	return r.Items
}

func (s *Server) handleBenchChannels(c *gin.Context) {
	if s.benchData == nil {
		writeBenchError(c, http.StatusNotImplemented, "bench channel data writer is not configured")
		return
	}
	var req channelsRequest
	if !s.bindBenchJSON(c, &req) {
		return
	}
	items := req.channelItems()
	if err := s.validateMutation(req.RunID, req.BatchID, len(items)); err != nil {
		writeBenchError(c, http.StatusBadRequest, err.Error())
		return
	}
	mutations := make([]BenchChannelMutation, 0, len(items))
	for _, item := range items {
		mutations = append(mutations, BenchChannelMutation{
			ChannelID:     item.ChannelID,
			ChannelType:   item.ChannelType,
			Large:         item.Large,
			Ban:           item.Ban,
			Disband:       item.Disband,
			SendBan:       item.SendBan,
			AllowStranger: item.AllowStranger,
		})
	}
	accepted, err := s.benchData.UpsertChannels(c.Request.Context(), mutations)
	if err != nil {
		s.logBenchFailure(c, "internal.access.api.bench_channels_failed", "channels_upsert", err,
			wklog.String("runID", req.RunID),
			wklog.String("batchID", req.BatchID),
			wklog.Int("channels", len(mutations)),
		)
		writeBenchError(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.addCount("accepted_channels", accepted)
	c.JSON(http.StatusOK, mutationResponse{RunID: req.RunID, BatchID: req.BatchID, Accepted: accepted})
}

type subscribersRequest struct {
	RunID   string           `json:"run_id"`
	BatchID string           `json:"batch_id"`
	Items   []subscriberItem `json:"items"`
}

type subscriberItem struct {
	ChannelID   string   `json:"channel_id"`
	ChannelType uint8    `json:"channel_type"`
	Reset       bool     `json:"reset,omitempty"`
	Subscribers []string `json:"subscribers"`
}

func (s *Server) handleBenchSubscribers(c *gin.Context) {
	if s.benchData == nil {
		writeBenchError(c, http.StatusNotImplemented, "bench subscriber data writer is not configured")
		return
	}
	var req subscribersRequest
	if !s.bindBenchJSON(c, &req) {
		return
	}
	if err := s.validateMutation(req.RunID, req.BatchID, len(req.Items)); err != nil {
		writeBenchError(c, http.StatusBadRequest, err.Error())
		return
	}
	acceptedSubscribers := 0
	mutations := make([]BenchSubscriberMutation, 0, len(req.Items))
	for _, item := range req.Items {
		if item.Reset {
			writeBenchError(c, http.StatusBadRequest, "bench/v1 subscribers reset=true is not supported")
			return
		}
		acceptedSubscribers += len(item.Subscribers)
		mutations = append(mutations, BenchSubscriberMutation{
			ChannelID:   item.ChannelID,
			ChannelType: item.ChannelType,
			Subscribers: append([]string(nil), item.Subscribers...),
		})
	}
	acceptedSubscribers, err := s.benchData.AddSubscribers(c.Request.Context(), mutations)
	if err != nil {
		s.logBenchFailure(c, "internal.access.api.bench_subscribers_failed", "subscribers_add", err,
			wklog.String("runID", req.RunID),
			wklog.String("batchID", req.BatchID),
			wklog.Int("items", len(mutations)),
		)
		writeBenchError(c, http.StatusInternalServerError, err.Error())
		return
	}
	s.addCount("accepted_subscriber_items", len(req.Items))
	s.addCount("accepted_subscribers", acceptedSubscribers)
	c.JSON(http.StatusOK, subscribersResponse{
		RunID:               req.RunID,
		BatchID:             req.BatchID,
		Accepted:            len(req.Items),
		AcceptedSubscribers: acceptedSubscribers,
	})
}

func (s *Server) logBenchFailure(c *gin.Context, event, op string, err error, fields ...wklog.Field) {
	if err == nil {
		return
	}
	path := ""
	method := ""
	if c != nil && c.Request != nil {
		r := c.Request
		method = r.Method
		if r.URL != nil {
			path = r.URL.Path
		}
	}
	all := []wklog.Field{
		wklog.Event(event),
		wklog.String("op", op),
		wklog.String("method", method),
		wklog.String("path", path),
	}
	all = append(all, fields...)
	all = append(all, wklog.Error(err))
	s.httpLogger().Error("bench api request failed", all...)
}

type mutationResponse struct {
	RunID    string `json:"run_id"`
	BatchID  string `json:"batch_id"`
	Accepted int    `json:"accepted"`
}

type subscribersResponse struct {
	RunID               string `json:"run_id"`
	BatchID             string `json:"batch_id"`
	Accepted            int    `json:"accepted"`
	AcceptedSubscribers int    `json:"accepted_subscribers"`
}

func (s *Server) bindBenchJSON(c *gin.Context, out any) bool {
	if s.benchMaxPayloadBytes > 0 {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, s.benchMaxPayloadBytes)
	}
	if err := c.ShouldBindJSON(out); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeBenchError(c, http.StatusRequestEntityTooLarge, fmt.Sprintf("payload too large: max %d bytes", maxBytesErr.Limit))
			return false
		}
		writeBenchError(c, http.StatusBadRequest, "invalid request")
		return false
	}
	return true
}

func (s *Server) validateMutation(runID, batchID string, n int) error {
	switch {
	case strings.TrimSpace(runID) == "":
		return fmt.Errorf("run_id is required")
	case strings.TrimSpace(batchID) == "":
		return fmt.Errorf("batch_id is required")
	case s.benchMaxBatchSize > 0 && n > s.benchMaxBatchSize:
		return fmt.Errorf("batch size %d exceeds max %d", n, s.benchMaxBatchSize)
	default:
		return nil
	}
}

func (s *Server) addCount(name string, n int) {
	if n <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts[name] += n
}

func (s *Server) snapshotCounts() map[string]int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.counts) == 0 {
		return nil
	}
	out := make(map[string]int, len(s.counts))
	for key, value := range s.counts {
		out[key] = value
	}
	return out
}

func writeBenchError(c *gin.Context, status int, msg string) {
	c.JSON(status, gin.H{"status": status, "msg": msg})
}
