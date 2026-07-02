package manager

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	accessapi "github.com/WuKongIM/WuKongIM/internalv2/access/api"
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
	// JoinNode submits a manager node join intent.
	JoinNode(ctx context.Context, req managementusecase.JoinNodeRequest) (managementusecase.JoinNodeResponse, error)
	// ActivateNode submits a manager node activation intent.
	ActivateNode(ctx context.Context, req managementusecase.ActivateNodeRequest) (managementusecase.ActivateNodeResponse, error)
	// MarkNodeLeaving submits a manager node leaving intent.
	MarkNodeLeaving(ctx context.Context, req managementusecase.MarkNodeLeavingRequest) (managementusecase.MarkNodeLeavingResponse, error)
	// MarkNodeRemoved marks a fully drained node removed.
	MarkNodeRemoved(ctx context.Context, req managementusecase.MarkNodeRemovedRequest) (managementusecase.MarkNodeRemovedResponse, error)
	// PromoteControllerVoter promotes an active data node into Controller voting membership.
	PromoteControllerVoter(ctx context.Context, req managementusecase.PromoteControllerVoterRequest) (managementusecase.PromoteControllerVoterResponse, error)
	// ListSlots returns manager-facing slot DTOs.
	ListSlots(ctx context.Context, opts managementusecase.ListSlotsOptions) ([]managementusecase.Slot, error)
	// ListSlotLogEntries returns one node-local Slot Raft log page.
	ListSlotLogEntries(ctx context.Context, req managementusecase.ListSlotLogEntriesRequest) (managementusecase.SlotLogEntriesResponse, error)
	// ListControllerLogEntries returns one node-local Controller Raft log page.
	ListControllerLogEntries(ctx context.Context, req managementusecase.ListControllerLogEntriesRequest) (managementusecase.ControllerLogEntriesResponse, error)
	// ListControllerTasks returns active Controller task rows.
	ListControllerTasks(ctx context.Context, req managementusecase.ListControllerTasksRequest) (managementusecase.ListControllerTasksResponse, error)
	// ControllerTask returns one active Controller task by ID.
	ControllerTask(ctx context.Context, taskID string) (managementusecase.ControllerTask, error)
	// ListControllerTaskAudits returns retained Controller task histories.
	ListControllerTaskAudits(ctx context.Context, req managementusecase.ControllerTaskAuditListRequest) (managementusecase.ControllerTaskAuditListResponse, error)
	// ControllerTaskAuditEvents returns retained events for one Controller task.
	ControllerTaskAuditEvents(ctx context.Context, taskID string) (managementusecase.ControllerTaskAuditEventsResponse, error)
	// ControllerRaftStatus returns one node-local Controller Raft status snapshot.
	ControllerRaftStatus(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftStatus, error)
	// CompactControllerRaftLog forces one node-local Controller Raft log compaction attempt.
	CompactControllerRaftLog(ctx context.Context, nodeID uint64) (managementusecase.ControllerRaftCompactionResult, error)
	// CompactControllerRaftLogs fans out Controller Raft log compaction to Controller voter nodes.
	CompactControllerRaftLogs(ctx context.Context) (managementusecase.ControllerRaftCompactionSummary, error)
	// CompactSlotRaftLog forces one node-local Slot Raft log compaction attempt.
	CompactSlotRaftLog(ctx context.Context, nodeID uint64, slotID uint32) (managementusecase.SlotRaftCompactionSummary, error)
	// RequestSlotLeaderTransfer submits a manager Slot leader transfer intent.
	RequestSlotLeaderTransfer(ctx context.Context, req managementusecase.SlotLeaderTransferRequest) (managementusecase.SlotLeaderTransferResponse, error)
	// PlanSlotLeaderTransfers previews a batch of Slot leader transfer tasks.
	PlanSlotLeaderTransfers(ctx context.Context, req managementusecase.SlotLeaderTransferBatchPlanRequest) (managementusecase.SlotLeaderTransferBatchPlanResponse, error)
	// ExecuteSlotLeaderTransferBatch submits a fenced batch of Slot leader transfer tasks.
	ExecuteSlotLeaderTransferBatch(ctx context.Context, req managementusecase.SlotLeaderTransferBatchExecuteRequest) (managementusecase.SlotLeaderTransferBatchExecuteResponse, error)
	// PlanNodeOnboarding previews bounded Slot replica move tasks for a target node.
	PlanNodeOnboarding(ctx context.Context, req managementusecase.NodeOnboardingPlanRequest) (managementusecase.NodeOnboardingPlanResponse, error)
	// StartNodeOnboarding creates bounded Slot replica move tasks for a target node.
	StartNodeOnboarding(ctx context.Context, req managementusecase.NodeOnboardingStartRequest) (managementusecase.NodeOnboardingStartResponse, error)
	// AdvanceNodeOnboarding creates another bounded set of Slot replica move tasks for a target node.
	AdvanceNodeOnboarding(ctx context.Context, req managementusecase.NodeOnboardingAdvanceRequest) (managementusecase.NodeOnboardingStartResponse, error)
	// NodeOnboardingStatus returns active onboarding tasks for a target node.
	NodeOnboardingStatus(ctx context.Context, req managementusecase.NodeOnboardingStatusRequest) (managementusecase.NodeOnboardingStatusResponse, error)
	// PlanNodeSlotMoveOut previews bounded Slot replica move tasks away from an active data node.
	PlanNodeSlotMoveOut(ctx context.Context, req managementusecase.NodeSlotMoveOutPlanRequest) (managementusecase.NodeSlotMoveOutPlanResponse, error)
	// AdvanceNodeSlotMoveOut creates bounded Slot replica move tasks away from an active data node.
	AdvanceNodeSlotMoveOut(ctx context.Context, req managementusecase.NodeSlotMoveOutAdvanceRequest) (managementusecase.NodeSlotMoveOutAdvanceResponse, error)
	// PlanNodeScaleIn previews bounded Slot replica move tasks away from a leaving node.
	PlanNodeScaleIn(ctx context.Context, req managementusecase.NodeScaleInPlanRequest) (managementusecase.NodeScaleInPlanResponse, error)
	// AdvanceNodeScaleIn creates bounded Slot replica move tasks away from a leaving node.
	AdvanceNodeScaleIn(ctx context.Context, req managementusecase.NodeScaleInAdvanceRequest) (managementusecase.NodeScaleInAdvanceResponse, error)
	// NodeScaleInStatus returns scale-in safety status for a leaving node.
	NodeScaleInStatus(ctx context.Context, req managementusecase.NodeScaleInStatusRequest) (managementusecase.NodeScaleInStatusResponse, error)
	// SetNodeDrainMode toggles gateway admission drain mode for a target node.
	SetNodeDrainMode(ctx context.Context, req managementusecase.SetNodeDrainModeRequest) (managementusecase.SetNodeDrainModeResponse, error)
	// DynamicNodeDiagnostics returns bounded diagnostics evidence for one node.
	DynamicNodeDiagnostics(ctx context.Context, req managementusecase.DynamicNodeDiagnosticsRequest) (managementusecase.DynamicNodeDiagnosticsResponse, error)
	// QueryDiagnostics returns a manager-facing diagnostics aggregate query result.
	QueryDiagnostics(ctx context.Context, req managementusecase.DiagnosticsQueryRequest) (managementusecase.DiagnosticsQueryResponse, error)
	// CreateDiagnosticsTrackingRule installs a temporary diagnostics tracking rule.
	CreateDiagnosticsTrackingRule(ctx context.Context, req managementusecase.DiagnosticsTrackingCreateRequest) (managementusecase.DiagnosticsTrackingMutationResponse, error)
	// ListDiagnosticsTrackingRules returns active temporary diagnostics tracking rules.
	ListDiagnosticsTrackingRules(ctx context.Context) (managementusecase.DiagnosticsTrackingListResponse, error)
	// DeleteDiagnosticsTrackingRule removes a temporary diagnostics tracking rule.
	DeleteDiagnosticsTrackingRule(ctx context.Context, ruleID string) (managementusecase.DiagnosticsTrackingDeleteResponse, error)
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
	// ListNodePlugins returns one node's local plugin inventory.
	ListNodePlugins(ctx context.Context, nodeID uint64) (managementusecase.NodePluginList, error)
	// GetNodePlugin returns one node-local plugin detail.
	GetNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error)
	// UpdateNodePluginConfig persists desired config for one node-local plugin.
	UpdateNodePluginConfig(ctx context.Context, nodeID uint64, pluginNo string, config json.RawMessage) (managementusecase.Plugin, error)
	// RestartNodePlugin restarts one node-local plugin process.
	RestartNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) (managementusecase.Plugin, error)
	// UninstallNodePlugin disables and removes one node-local plugin process.
	UninstallNodePlugin(ctx context.Context, nodeID uint64, pluginNo string) error
	// ListPluginBindings returns cluster-authoritative plugin-user bindings.
	ListPluginBindings(ctx context.Context, req managementusecase.PluginBindingListRequest) (managementusecase.PluginBindingListResponse, error)
	// BindPluginUser creates or updates a cluster-authoritative plugin-user binding.
	BindPluginUser(ctx context.Context, req managementusecase.PluginBindingMutationRequest) (managementusecase.PluginBindingMutationResponse, error)
	// UnbindPluginUser removes a cluster-authoritative plugin-user binding.
	UnbindPluginUser(ctx context.Context, req managementusecase.PluginBindingMutationRequest) error
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
	// ListDBInspectTables returns inspectable DB table metadata.
	ListDBInspectTables(ctx context.Context, nodeID uint64) (managementusecase.DBInspectQueryResponse, error)
	// DescribeDBInspectTable returns inspectable column metadata for one table.
	DescribeDBInspectTable(ctx context.Context, nodeID uint64, domain, table string) (managementusecase.DBInspectQueryResponse, error)
	// QueryDBInspect runs one bounded read-only DB inspect query.
	QueryDBInspect(ctx context.Context, req managementusecase.DBInspectQueryRequest) (managementusecase.DBInspectQueryResponse, error)
	// ApplicationLogSources returns ordinary application log sources for one selected node.
	ApplicationLogSources(ctx context.Context, req managementusecase.ApplicationLogSourcesRequest) (managementusecase.ApplicationLogSourcesResponse, error)
	// ApplicationLogEntries returns one page from one selected ordinary application log source.
	ApplicationLogEntries(ctx context.Context, req managementusecase.ApplicationLogEntriesRequest) (managementusecase.ApplicationLogEntriesResponse, error)
}

// Options configures the manager HTTP server.
type Options struct {
	// ListenAddr is the manager server listen address.
	ListenAddr string
	// Auth configures manager JWT login.
	Auth AuthConfig
	// Management provides manager read usecases.
	Management Management
	// RealtimeMonitor provides unified realtime monitor snapshots.
	RealtimeMonitor RealtimeMonitorProvider
	// Top provides local runtime pressure snapshots for read-only runtime views.
	Top accessapi.TopSnapshotProvider
	// Logger is the logger used by the manager server.
	Logger wklog.Logger
}

// Server serves the internalv2 manager HTTP API.
type Server struct {
	mu              sync.RWMutex
	engine          *gin.Engine
	httpServer      *http.Server
	listener        net.Listener
	listenAddr      string
	addr            string
	management      Management
	realtimeMonitor RealtimeMonitorProvider
	top             accessapi.TopSnapshotProvider
	auth            authState
	logger          wklog.Logger
	started         bool
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
		engine:          engine,
		listenAddr:      strings.TrimSpace(opts.ListenAddr),
		management:      opts.Management,
		realtimeMonitor: opts.RealtimeMonitor,
		top:             opts.Top,
		auth:            newAuthState(opts.Auth),
		logger:          opts.Logger,
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
	nodes.GET("/runtime/workqueues", s.handleRuntimeWorkqueues)
	nodes.GET("/realtime-monitor", s.handleRealtimeMonitor)

	nodeWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		nodeWrites.Use(s.requirePermission("cluster.node", "w"))
	}
	nodeWrites.POST("/nodes/join", s.handleJoinNode)
	nodeWrites.POST("/nodes/:node_id/activate", s.handleActivateNode)
	nodeWrites.POST("/nodes/:node_id/onboarding/plan", s.handleNodeOnboardingPlan)
	nodeWrites.POST("/nodes/:node_id/onboarding/start", s.handleNodeOnboardingStart)
	nodeWrites.POST("/nodes/:node_id/onboarding/advance", s.handleNodeOnboardingAdvance)
	nodeWrites.POST("/nodes/:node_id/slot-move-out/plan", s.handleNodeSlotMoveOutPlan)
	nodeWrites.POST("/nodes/:node_id/slot-move-out/advance", s.handleNodeSlotMoveOutAdvance)
	nodeWrites.POST("/nodes/:node_id/scale-in/plan", s.handleNodeScaleInPlan)
	nodeWrites.POST("/nodes/:node_id/scale-in/start", s.handleNodeScaleInStart)
	nodeWrites.POST("/nodes/:node_id/scale-in/drain", s.handleNodeScaleInDrain)
	nodeWrites.POST("/nodes/:node_id/scale-in/remove", s.handleNodeScaleInRemove)
	nodeWrites.POST("/nodes/:node_id/scale-in/advance", s.handleNodeScaleInAdvance)
	nodes.GET("/nodes/:node_id/onboarding/status", s.handleNodeOnboardingStatus)
	nodes.GET("/nodes/:node_id/scale-in/status", s.handleNodeScaleInStatus)
	nodes.GET("/nodes/:node_id/diagnostics", s.handleDynamicNodeDiagnostics)

	slots := s.engine.Group("/manager")
	if s.auth.enabled() {
		slots.Use(s.requirePermission("cluster.slot", "r"))
	}
	slots.GET("/slots", s.handleSlots)
	slots.GET("/slots/:slot_id/logs", s.handleSlotLogs)
	slots.POST("/slots/leader-transfer-plan", s.handleSlotLeaderTransferBatchPlan)

	slotWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		slotWrites.Use(s.requirePermission("cluster.slot", "w"))
	}
	slotWrites.POST("/nodes/:node_id/slots/:slot_id/compact", s.handleCompactSlotRaftLog)
	slotWrites.POST("/slots/leader-transfer-batch", s.handleSlotLeaderTransferBatchExecute)
	slotWrites.POST("/slots/:slot_id/leader-transfer", s.handleSlotLeaderTransfer)

	controllerReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		controllerReads.Use(s.requirePermission("cluster.controller", "r"))
	}
	controllerReads.GET("/controller/logs", s.handleControllerLogs)
	controllerReads.GET("/controller/tasks", s.handleControllerTasks)
	controllerReads.GET("/controller/tasks/:task_id", s.handleControllerTask)
	controllerReads.GET("/controller/task-audits", s.handleControllerTaskAudits)
	controllerReads.GET("/controller/task-audits/:task_id/events", s.handleControllerTaskAuditEvents)
	controllerReads.GET("/nodes/:node_id/controller-raft", s.handleControllerRaftStatus)

	controllerRaftWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		controllerRaftWrites.Use(s.requirePermission("cluster.controller", "w"))
	}
	controllerRaftWrites.POST("/nodes/:node_id/controller-raft/compact", s.handleCompactControllerRaftLog)
	controllerRaftWrites.POST("/nodes/:node_id/controller-voter/promote", s.handlePromoteControllerVoter)
	controllerRaftWrites.POST("/controller-raft/compact", s.handleCompactControllerRaftLogs)

	diagnostics := s.engine.Group("/manager")
	if s.auth.enabled() {
		diagnostics.Use(s.requirePermission("cluster.diagnostics", "r"))
	}
	diagnostics.GET("/diagnostics/trace/:trace_id", s.handleDiagnosticsTrace)
	diagnostics.GET("/diagnostics/message", s.handleDiagnosticsMessage)
	diagnostics.GET("/diagnostics/events", s.handleDiagnosticsEvents)
	diagnostics.GET("/diagnostics/tracking-rules", s.handleDiagnosticsTrackingRules)

	diagnosticsWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		diagnosticsWrites.Use(s.requirePermission("cluster.diagnostics", "w"))
	}
	diagnosticsWrites.POST("/diagnostics/tracking-rules", s.handleCreateDiagnosticsTrackingRule)
	diagnosticsWrites.DELETE("/diagnostics/tracking-rules/:rule_id", s.handleDeleteDiagnosticsTrackingRule)

	appLogs := s.engine.Group("/manager")
	if s.auth.enabled() {
		appLogs.Use(s.requirePermission("cluster.log", "r"))
	}
	appLogs.GET("/app-logs/sources", s.handleApplicationLogSources)
	appLogs.GET("/app-logs", s.handleApplicationLogEntries)
	appLogs.GET("/app-logs/stream", s.handleApplicationLogStream)

	dbInspect := s.engine.Group("/manager")
	if s.auth.enabled() {
		dbInspect.Use(s.requirePermission("cluster.db", "r"))
	}
	dbInspect.GET("/db/inspect/tables", s.handleDBInspectTables)
	dbInspect.GET("/db/inspect/tables/:domain/:table", s.handleDBInspectTable)
	dbInspect.POST("/db/inspect/query", s.handleDBInspectQuery)

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

	pluginReads := s.engine.Group("/manager")
	if s.auth.enabled() {
		pluginReads.Use(s.requirePermission("cluster.plugin", "r"))
	}
	pluginReads.GET("/nodes/:node_id/plugins", s.handleNodePlugins)
	pluginReads.GET("/nodes/:node_id/plugins/:plugin_no", s.handleNodePlugin)
	pluginReads.GET("/plugin-bindings", s.handlePluginBindings)

	pluginWrites := s.engine.Group("/manager")
	if s.auth.enabled() {
		pluginWrites.Use(s.requirePermission("cluster.plugin", "w"))
	}
	pluginWrites.POST("/plugin-bindings", s.handlePluginBindingCreate)
	pluginWrites.DELETE("/plugin-bindings", s.handlePluginBindingDelete)
	pluginWrites.PUT("/nodes/:node_id/plugins/:plugin_no/config", s.handleNodePluginConfigUpdate)
	pluginWrites.POST("/nodes/:node_id/plugins/:plugin_no/restart", s.handleNodePluginRestart)
	pluginWrites.DELETE("/nodes/:node_id/plugins/:plugin_no", s.handleNodePluginUninstall)

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
